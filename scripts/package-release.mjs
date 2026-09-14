// Assemble a local handoff. No registry push, release publication or deployment.
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { createReadStream } from 'node:fs';
import { copyFile, cp, mkdir, readdir, readFile, writeFile, stat } from 'node:fs/promises';
import { join, resolve, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
const imageDirectory=resolve(process.argv[2]??''), acceptanceDirectory=resolve(process.argv[3]??'');
const images=JSON.parse(await readFile(join(imageDirectory,'images.json'),'utf8'));
const acceptance=JSON.parse(await readFile(join(acceptanceDirectory,'report.json'),'utf8'));
assert.equal(images.result,'pass');assert.equal(acceptance.result,'pass');
assert.equal(acceptance.environment,'native_linux_amd64_in_nested_docker');
for(const image of images.images.filter(i=>i.architecture==='amd64'))assert.ok(acceptance.images.some(i=>i.service===image.service&&i.image_id===image.image_id),'acceptance_image_mismatch');
const buildDirectory=join(root,'.cache/release',images.source_build);
const source=JSON.parse(await readFile(join(buildDirectory,'source-manifest.json'),'utf8'));
const hash=data=>createHash('sha256').update(data).digest('hex');
for(const item of source.files.filter(i=>/^(internal|migrations|api|schemas|compat|proxyloom-server|proxyloom-runner|proxyloom-operations|proxyloom-web)\//.test(i.path)||['go.mod','go.sum'].includes(i.path)))assert.equal(hash(await readFile(join(root,item.path))),item.sha256,'application_source_changed_since_build');
for(const [path,expected] of Object.entries(acceptance.deployment_sha256))assert.equal(hash((await readFile(join(root,'deploy',path),'utf8')).replaceAll('\r\n','\n')),expected,'deployment_changed_since_acceptance');
const scanner=JSON.parse(await readFile(join(imageDirectory,'sbom/manifest.json'),'utf8'));
assert.equal(scanner.inventories.length,images.images.length+1);
for(const item of scanner.inventories)assert.equal(hash(await readFile(join(imageDirectory,'sbom',item.file))),item.sha256,'sbom_changed');
for(const image of images.images)assert.ok(scanner.inventories.some(i=>i.file===`${image.service}-${image.architecture}.cdx.json`&&i.image_config_digest===image.config_digest),'sbom_image_mismatch');
const vulnerabilities=JSON.parse(await readFile(join(imageDirectory,'vulnerabilities/manifest.json'),'utf8'));
assert.equal(vulnerabilities.scan_result,'pass');
assert.equal(vulnerabilities.inventories.length,scanner.inventories.length);
for(const item of vulnerabilities.inventories){assert.equal(hash(await readFile(join(imageDirectory,'vulnerabilities',item.file))),item.sha256);assert.ok(scanner.inventories.some(s=>s.sha256===item.sbom_sha256),'vulnerability_sbom_mismatch');}
const directory=join(root,'.cache/self-hosted',randomUUID());await mkdir(directory,{recursive:true});
async function digest(path){const result=createHash('sha256');for await(const chunk of createReadStream(path))result.update(chunk);return result.digest('hex');}
async function copy(from,to){await mkdir(join(to,'..'),{recursive:true});await copyFile(from,to);}
for(const archive of images.archives){assert.equal(await digest(join(imageDirectory,archive.file)),archive.sha256);await copy(join(imageDirectory,archive.file),join(directory,archive.file));await copy(join(imageDirectory,`images-${archive.architecture}.env`),join(directory,'deploy',`images-${archive.architecture}.env`));}
for(const name of ['compose.yaml','proxyloom.sh','network-guard.sh','postgres-init.sh','tools.lock.json','sources.lock.json','notices.lock.json']){await copy(join(root,'deploy',name),join(directory,'deploy',name));if(name.endsWith('.sh')||name.endsWith('.yaml'))await writeFile(join(directory,'deploy',name),(await readFile(join(root,'deploy',name),'utf8')).replaceAll('\r\n','\n'));}
await cp(join(root,'deploy/monitoring'),join(directory,'deploy/monitoring'),{recursive:true});
await cp(join(imageDirectory,'sbom'),join(directory,'sbom'),{recursive:true});
await cp(join(imageDirectory,'vulnerabilities'),join(directory,'vulnerabilities'),{recursive:true});
await copy(join(root,'docs/self-hosting.md'),join(directory,'README.md'));
await cp(join(root,'docs'),join(directory,'docs'),{recursive:true});
await copy(join(buildDirectory,'source-manifest.json'),join(directory,'evidence/build-source-manifest.json'));
await cp(acceptanceDirectory,join(directory,'evidence/deployment'),{recursive:true,filter:path=>!path.endsWith('.tar')&&!path.endsWith('deployment-probe')&&!path.endsWith('capacity-probe')&&!path.endsWith('deploy')});
const sourceLock=JSON.parse(await readFile(join(root,'deploy/sources.lock.json'),'utf8'));
for(const item of sourceLock.sources){const archive=join(root,'.cache/release-sources',item.archive);assert.equal(await digest(archive),item.sha256,'core_source_digest_mismatch');await copy(archive,join(directory,'sources/cores',item.archive));
 const listed=spawnSync('tar',['-tf',archive],{encoding:'utf8',windowsHide:true});assert.equal(listed.status,0);const name=listed.stdout.split(/\r?\n/).find(p=>/^[^/]+\/LICENSE$/.test(p));assert.ok(name);
 const license=spawnSync('tar',['-xOf',archive,name],{encoding:'utf8',windowsHide:true});assert.equal(license.status,0);await mkdir(join(directory,'licenses/cores'),{recursive:true});await writeFile(join(directory,'licenses/cores',item.family+'.txt'),license.stdout);
}
// Include exact project sources and the license notices of local Go/npm inputs.
const inventory=spawnSync('git',['ls-files','--cached','--others','--exclude-standard','-z'],{cwd:root,encoding:'utf8',windowsHide:true});assert.equal(inventory.status,0);
for(const path of [...new Set(inventory.stdout.split('\0').filter(Boolean))]){assert.ok(!path.split('/').includes('..'));if(path.startsWith('docs/evidence/'))continue;const from=join(root,path);if((await stat(from).catch(()=>null))?.isFile())await copy(from,join(directory,'sources/proxyloom',path));}
const modules=spawnSync('go',['list','-m','-f','{{.Path}}|{{.Version}}|{{.Dir}}','all'],{cwd:root,encoding:'utf8',windowsHide:true,env:{...process.env,GOCACHE:join(root,'.cache/go-build'),GOPATH:join(root,'.cache/gopath'),GOMODCACHE:join(root,'.cache/gomod')}});assert.equal(modules.status,0);
const licenses=[];
const goRoot=spawnSync('go',['env','GOROOT'],{encoding:'utf8',windowsHide:true});assert.equal(goRoot.status,0);await copy(join(goRoot.stdout.trim(),'LICENSE'),join(directory,'licenses/go-runtime/LICENSE'));
async function notices(kind,name,version,path){
 if(!path)return;
 const found=[];
 const noticeRoot=kind==='npm'&&name.startsWith('@rolldown/binding-')?join(root,'proxyloom-web/node_modules/.pnpm',`rolldown@${version}`,'node_modules/rolldown'):path;
 for(const file of await readdir(noticeRoot,{withFileTypes:true})){
  if(file.isFile()&&/(LICENSE|LICENCE|COPYING|NOTICE|COPYRIGHT)/i.test(file.name)){
   const target=join('licenses',kind,encodeURIComponent(name)+'@'+version,file.name);
   await copy(join(noticeRoot,file.name),join(directory,target));found.push(target.replaceAll('\\','/'));
  }
 }
 licenses.push({kind,name,version,files:found,...(noticeRoot!==path?{notice_origin:`rolldown@${version}`}:{})});
}
for(const line of modules.stdout.trim().split(/\r?\n/)){const [name,version,path]=line.split('|');if(version)await notices('go',name,version,path);}
const npmSeen=new Set(),npmRoot=join(root,'proxyloom-web/node_modules/.pnpm');
for(const entry of await readdir(npmRoot,{withFileTypes:true})){if(!entry.isDirectory())continue;const modulesDir=join(npmRoot,entry.name,'node_modules');for(const name of await readdir(modulesDir).catch(()=>[])){const paths=name.startsWith('@')?(await readdir(join(modulesDir,name))).map(child=>join(modulesDir,name,child)):[join(modulesDir,name)];for(const path of paths){let pkg;try{pkg=JSON.parse(await readFile(join(path,'package.json'),'utf8'));}catch{continue}const key=pkg.name+'@'+pkg.version;if(npmSeen.has(key))continue;npmSeen.add(key);await notices('npm',pkg.name,pkg.version,path);}}}
const noticeLock=JSON.parse(await readFile(join(root,'deploy/notices.lock.json'),'utf8'));
for(const item of licenses.filter(v=>v.files.length===0)){
 const upstream=noticeLock.notices.find(v=>v.kind===item.kind&&v.name===item.name&&v.version===item.version);
 assert.ok(upstream,'dependency_notice_missing_'+item.name);
 const from=join(root,'.cache/release-notices',upstream.file);assert.equal(await digest(from),upstream.sha256,'upstream_notice_changed');
 const target=join('licenses',item.kind,encodeURIComponent(item.name)+'@'+item.version,'UPSTREAM-NOTICE.txt');
 await copy(from,join(directory,target));item.files.push(target.replaceAll('\\','/'));item.upstream=upstream;
}
await writeFile(join(directory,'licenses/manifest.json'),JSON.stringify({schema_version:1,components:licenses,os_notices:'Debian package copyright notices are retained under /usr/share/doc in the supplied images.',remaining_review:licenses.filter(v=>v.upstream?.license_text_status==='declaration_only').map(v=>({name:v.name,version:v.version,reason:'Upstream declares MIT but omits the full copyright/license notice at the locked commit.'}))},null,2)+'\n');
await writeFile(join(directory,'delivery.json'),JSON.stringify({schema_version:1,created_at:new Date().toISOString(),scope:'local_self_hosted_handoff',gate:'G3_open',architectures:{amd64:{deployment:'pass',evidence:'evidence/deployment/report.json'},arm64:{build:'pass',native_runtime:'unverified',decision:'User explicitly retained the unverified status.'}},images,core_sources:sourceLock.sources,sbom:'sbom/manifest.json'},null,2)+'\n');
const sums=[];async function walk(path){for(const entry of (await readdir(path,{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name))){const full=join(path,entry.name);if(entry.isDirectory())await walk(full);else sums.push(`${await digest(full)}  ${relative(directory,full).replaceAll('\\','/')}`);}}await walk(directory);await writeFile(join(directory,'SHA256SUMS'),sums.join('\n')+'\n');
console.log(JSON.stringify({directory,result:'pass',gate:'G3_open',files:sums.length}));
