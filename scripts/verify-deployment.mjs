// A disposable Docker daemon owns all test services, networks and data. The host
// Docker socket and filesystem are never mounted into it. Only staged artifacts
// are mounted read-only. Privileged mode is required for the nested daemon.
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync, copyFileSync, chmodSync, linkSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertSecretFree } from './quality-secrets.mjs';

const root=fileURLToPath(new URL('../',import.meta.url));
const build=resolve(process.argv[2] ?? '');
const images=JSON.parse(readFileSync(join(build,'images.json')));
assert.equal(images.result,'pass');
const locked=JSON.parse(readFileSync(join(root,'deploy/tools.lock.json')));
const selected=images.images.filter(v=>v.architecture==='amd64'); assert.equal(selected.length,4);
const id=randomUUID(),work=join(root,'.cache/deployment',id),name=`proxyloom-deploy-${id}`;
mkdirSync(join(work,'deploy'),{recursive:true});
const report={schema_version:1,run_id:id,result:'fail',environment:'native_linux_amd64_in_nested_docker',started_at:new Date().toISOString(),images:selected,steps:[],cleanup:[]};
const extraArguments=process.argv.slice(3);
assert.ok(extraArguments.every(v=>!v.startsWith('--')||/^--capacity(?:=\d+)?$/.test(v)));
assert.ok(extraArguments.filter(v=>!v.startsWith('--')).length<=1);
const legacyArgument=extraArguments.find(v=>!v.startsWith('--'));
const legacyDirectory=legacyArgument?resolve(legacyArgument):null;
const capacityArgument=extraArguments.find(v=>v.startsWith('--capacity'));
const capacitySeconds=capacityArgument?Number(capacityArgument.split('=')[1]??1200):0;
assert.ok(!capacityArgument||(Number.isInteger(capacitySeconds)&&capacitySeconds>=10&&capacitySeconds<=1800));
if(capacitySeconds){writeFileSync(join(work,'capacity-seconds'),String(capacitySeconds));report.capacity_baseline=capacitySeconds>=1200;}
report.upgrade_validation=legacyDirectory?'M2_schema_15_to_M3_then_distinct_compatible_image_and_incompatible_rejection':'same_schema_commands_only';
for(const file of ['compose.yaml','proxyloom.sh','network-guard.sh','postgres-init.sh'])writeFileSync(join(work,'deploy',file),readFileSync(join(root,'deploy',file),'utf8').replaceAll('\r\n','\n'));
writeFileSync(join(work,'acceptance.sh'),readFileSync(join(root,'scripts/deployment-acceptance.sh'),'utf8').replaceAll('\r\n','\n'));
copyFileSync(join(build,'images-amd64.env'),join(work,'images-amd64.env'));
linkSync(join(build,'images-amd64.tar'),join(work,'images-amd64.tar'));
report.deployment_sha256=Object.fromEntries(['compose.yaml','proxyloom.sh','network-guard.sh','postgres-init.sh'].map(file=>[file,createHash('sha256').update(readFileSync(join(work,'deploy',file))).digest('hex')]));
async function command(step,exe,args,extra={}) {
 const result=await new Promise(done=>{const child=spawn(exe,args,{cwd:root,windowsHide:true,...extra});let out='',live='';child.stdout.on('data',v=>{out+=v;live+=v;let end;while((end=live.indexOf('\n'))>=0){const line=live.slice(0,end).trim();live=live.slice(end+1);if(/^CAPACITY_PROGRESS (management|subscription) elapsed_seconds=\d+ requests=\d+ errors=\d+$/.test(line)||/^PASS: capacity-[a-z-]+$/.test(line))console.log(line);}});child.stderr.on('data',v=>out+=v);child.on('error',()=>done({code:-1,out:'command_start_failed'}));child.on('close',code=>done({code,out}));});
 const out=assertSecretFree(result.out,{path:`deployment/${step}.txt`});writeFileSync(join(work,`${step}.txt`),out);report.steps.push({name:step,result:result.code===0?'pass':'fail'});
 if(result.code!==0)process.stdout.write(out.slice(-6000));assert.equal(result.code,0,`${step}_failed`);console.log(`PASS: ${step}`);return out;
}
try {
 await command('build-probe','go',['build','-mod=readonly','-trimpath','-buildvcs=false','-o',join(work,'deployment-probe'),'./scripts/deployment-probe'],{env:{...process.env,GOCACHE:join(root,'.cache/go-build'),GOPATH:join(root,'.cache/gopath'),GOMODCACHE:join(root,'.cache/gomod'),GOOS:'linux',GOARCH:'amd64',CGO_ENABLED:'0'}});chmodSync(join(work,'deployment-probe'),0o755);
 report.probe_sha256=createHash('sha256').update(readFileSync(join(work,'deployment-probe'))).digest('hex');
 report.probe_source_sha256=createHash('sha256').update(readFileSync(join(root,'scripts/deployment-probe/main.go'))).digest('hex');
 report.acceptance_script_sha256=createHash('sha256').update(readFileSync(join(work,'acceptance.sh'))).digest('hex');
 if(capacitySeconds){
  await command('build-capacity-probe','go',['build','-mod=readonly','-trimpath','-buildvcs=false','-o',join(work,'capacity-probe'),'./scripts/capacity-probe'],{env:{...process.env,GOCACHE:join(root,'.cache/go-build'),GOPATH:join(root,'.cache/gopath'),GOMODCACHE:join(root,'.cache/gomod'),GOOS:'linux',GOARCH:'amd64',CGO_ENABLED:'0'}});chmodSync(join(work,'capacity-probe'),0o755);
  report.capacity_probe_sha256=createHash('sha256').update(readFileSync(join(work,'capacity-probe'))).digest('hex');
  report.capacity_source_sha256=createHash('sha256').update(readFileSync(join(root,'scripts/capacity-probe/main.go'))).digest('hex');
 }
 if(legacyDirectory){
  const legacy=JSON.parse(readFileSync(join(legacyDirectory,'image.json'),'utf8'));
  assert.equal(legacy.commit,'246b08bed1d9490b1be4e0d946faced58f932ae6');assert.equal(legacy.architecture,'amd64');assert.match(legacy.tag,/^proxyloom-api:m2-246b08b-[0-9a-f-]+$/);
  const actual=JSON.parse(await command('inspect-legacy','docker',['image','inspect',legacy.tag]))[0];assert.equal(actual.Id,legacy.image_id);
  await command('save-legacy','docker',['image','save','--platform','linux/amd64','--output',join(work,'legacy.tar'),legacy.tag]);
  const saved=JSON.parse(await command('legacy-manifest','tar',['-xOf',join(work,'legacy.tar'),'manifest.json']))[0];
  const config=saved.Config.match(/(?:^|\/)([0-9a-f]{64})(?:\.json)?$/)?.[1];assert.ok(config);
  report.legacy={...legacy,config_digest:'sha256:'+config};
  writeFileSync(join(work,'legacy.env'),`LEGACY_API_IMAGE=${legacy.tag}\nLEGACY_API_MANIFEST_DIGEST=${legacy.image_id}\nLEGACY_API_CONFIG_DIGEST=sha256:${config}\n`);
 }
 await command('build-host','docker',['build','--file','deploy/Dockerfile.deployment-test','--tag',`proxyloom-deployment-test:${id}`,'.']);
 await command('start-host','docker',['run','-d','--privileged','--cgroupns=private','--name',name,'--label',`io.proxyloom.deployment=${id}`,'--memory','6g','--cpus','4','--pids-limit','1024','--mount',`type=bind,src=${work},dst=/input,readonly`,`proxyloom-deployment-test:${id}`,'dockerd','--host=unix:///var/run/docker.sock','--storage-driver=overlay2']);
 let ready=false;for(let attempt=0;attempt<60;attempt++){const result=spawnSync('docker',['exec',name,'docker','info','--format','{{.ServerVersion}}'],{encoding:'utf8',windowsHide:true,timeout:5000});if(result.status===0){ready=true;report.docker_version=result.stdout.trim();break;}await new Promise(r=>setTimeout(r,500));}assert.ok(ready,'nested_daemon_not_ready');
 await command('install-upgrade-restore','docker',['exec',name,'bash','/input/acceptance.sh']);
 if(capacitySeconds){await command('collect-capacity','docker',['cp',name+':/results/capacity.json',join(work,'capacity.json')]);const capacity=JSON.parse(readFileSync(join(work,'capacity.json'),'utf8'));assert.equal(capacity.result,'pass');assert.equal(capacity.baseline_run,capacitySeconds>=1200);}
 report.result='pass';
}catch(error){report.error=String(error);process.exitCode=1;
 if(capacitySeconds){try{await command('collect-capacity-failure','docker',['cp',name+':/results/capacity.json',join(work,'capacity.json')]);}catch{/* early failures may not have measurements */}}
 try{await command('service-logs','docker',['exec',name,'sh','-c','for project in proxyloom proxyloom-recovered; do for item in $(docker ps -aq --filter label=com.docker.compose.project=$project); do docker logs --tail 40 "$item"; done; done']);}catch{/* cleanup still runs */}
}
finally {
 if(capacitySeconds){spawnSync('docker',['cp',name+':/results/resources.jsonl',join(work,'resources.jsonl')],{encoding:'utf8',windowsHide:true,timeout:10000});}
 const owned=spawnSync('docker',['inspect','--format','{{index .Config.Labels "io.proxyloom.deployment"}}',name],{encoding:'utf8',windowsHide:true});
 if(owned.status===0&&owned.stdout.trim()===id){const removed=spawnSync('docker',['rm','--force','--volumes',name],{encoding:'utf8',windowsHide:true,timeout:60000});report.cleanup.push({name:'owned-docker-host-and-volumes',result:removed.status===0?'pass':'fail'});if(removed.status!==0){report.result='fail';process.exitCode=1;}}
 report.ended_at=new Date().toISOString();writeFileSync(join(work,'report.json'),JSON.stringify(report,null,2)+'\n');console.log(`Deployment report: ${join(work,'report.json')}`);
}
