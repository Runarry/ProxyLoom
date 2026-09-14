// Preserve portable manifest/config identities across Docker storage backends.
import assert from 'node:assert/strict';
import { randomUUID, createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
const source=resolve(process.argv[2]??'');
const input=JSON.parse(await readFile(join(source,'images.json'),'utf8'));assert.equal(input.result,'pass');
const architectures=(process.argv[3]??'amd64,arm64').split(',');assert.ok(architectures.every(a=>['amd64','arm64'].includes(a)));
const locked=JSON.parse(await readFile(join(root,'deploy/tools.lock.json'),'utf8'));
const id=randomUUID(),directory=join(root,'.cache/release-images',id);await mkdir(directory,{recursive:true});
const report={schema_version:1,run_id:id,result:'fail',directory,source_build:input.run_id,source_manifest_sha256:input.source_manifest_sha256,images:[],archives:[]};
async function run(name,exe,args){const result=await new Promise(done=>{const child=spawn(exe,args,{cwd:root,windowsHide:true});let out='';child.stdout.on('data',b=>out+=b);child.stderr.on('data',b=>out+=b);child.on('error',()=>done({code:-1,out:'command_failed'}));child.on('close',code=>done({code,out}));});await writeFile(join(directory,name+'.txt'),result.out);assert.equal(result.code,0,name+'_failed');return result.out;}
try{
 for(const arch of architectures){
  const selected=input.images.filter(i=>i.architecture===arch);assert.equal(selected.length,3);
  for(const item of selected){const got=JSON.parse(await run('inspect-'+item.service+'-'+arch,'docker',['image','inspect',item.tag]))[0];assert.equal(got.Id,item.image_id);}
  await run('pull-postgres-index-'+arch,'docker',['pull','--platform','linux/'+arch,locked.images.postgres]);
  const pg=JSON.parse(await run('inspect-postgres-'+arch,'docker',['image','inspect','--platform','linux/'+arch,locked.images.postgres]))[0];
  const pgTag=`proxyloom-postgres:m3-${id}-${arch}`;
  // Materialize the selected manifest as a local reference; containerd stores
  // do not resolve an untagged child manifest by bare ID under an index.
  const pgManifest=pg.Descriptor?.digest;
  if(pgManifest){await run('pull-postgres-'+arch,'docker',['pull','--platform','linux/'+arch,'postgres@'+pgManifest]);}
  await run('tag-postgres-'+arch,'docker',['image','tag',pgManifest?'postgres@'+pgManifest:locked.images.postgres,pgTag]);
  selected.push({service:'postgres',architecture:arch,tag:pgTag,image_id:pg.Id,os:'linux',runtime_verified:false,source:locked.images.postgres});
  const archive=join(directory,`images-${arch}.tar`);
  await run('save-'+arch,'docker',['image','save','--platform','linux/'+arch,'--output',archive,...selected.map(i=>i.tag)]);
  const manifest=JSON.parse(await run('manifest-'+arch,'tar',['-xOf',archive,'manifest.json']));
  const environment=[];
  for(const item of selected){const saved=manifest.find(m=>m.RepoTags?.includes(item.tag));assert.ok(saved);const config=saved.Config.match(/(?:^|\/)([0-9a-f]{64})(?:\.json)?$/)?.[1];assert.ok(config);
   item.config_digest='sha256:'+config;
   environment.push(`PROXYLOOM_${item.service.toUpperCase()}_IMAGE=${item.tag}`,`PROXYLOOM_${item.service.toUpperCase()}_MANIFEST_DIGEST=${item.image_id}`,`PROXYLOOM_${item.service.toUpperCase()}_CONFIG_DIGEST=${item.config_digest}`);
   report.images.push(item);
  }
  await writeFile(join(directory,`images-${arch}.env`),environment.join('\n')+'\n');
  const hash=createHash('sha256');for await(const chunk of createReadStream(archive))hash.update(chunk);
  report.archives.push({architecture:arch,file:`images-${arch}.tar`,sha256:hash.digest('hex')});
  console.log('PASS: exported-'+arch);
 }
 report.result='pass';
}catch(e){report.error=String(e);process.exitCode=1;}
finally{await writeFile(join(directory,'images.json'),JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify(report));}
