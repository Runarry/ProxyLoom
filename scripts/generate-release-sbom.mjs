// Generate image and frontend build-input inventories using the pinned scanner.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { readFile, writeFile, mkdir, copyFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
const root=fileURLToPath(new URL('../',import.meta.url));
const directory=resolve(process.argv[2]??'');
const images=JSON.parse(await readFile(join(directory,'images.json'),'utf8'));assert.equal(images.result,'pass');
const lock=JSON.parse(await readFile(join(root,'deploy/tools.lock.json'),'utf8')).sbom;
const platform=process.platform==='win32'?'windows_amd64':'linux_amd64';
assert.equal(process.arch,'x64','scanner_host_architecture_not_locked');
const binary=resolve(process.argv[3]??join(root,'.cache/tools/syft',lock.version,'unpacked',process.platform==='win32'?'syft.exe':'syft'));
assert.equal(createHash('sha256').update(await readFile(binary)).digest('hex'),lock.builds[platform].binary_sha256,'scanner_binary_digest_mismatch');
const output=join(directory,'sbom'),inputs=join(directory,'sbom-inputs');await mkdir(output,{recursive:true});await mkdir(inputs,{recursive:true});
for(const file of ['package.json','pnpm-lock.yaml'])await copyFile(join(root,'proxyloom-web',file),join(inputs,file));
const inventory=[];
for(const item of [...images.images.map(image=>({name:image.service+'-'+image.architecture,source:'docker:'+image.tag,image})),{name:'frontend-build-inputs',source:'dir:'+inputs}]){
 const path=join(output,item.name+'.cdx.json');
 const result=await new Promise(done=>{const child=spawn(binary,['scan',item.source,'-o','cyclonedx-json='+path],{cwd:root,windowsHide:true,env:{...process.env,GOMAXPROCS:'2',SYFT_CHECK_FOR_APP_UPDATE:'false'}});let message='';child.stdout.on('data',c=>message+=c);child.stderr.on('data',c=>message+=c);child.on('error',()=>done({code:-1,message:'scanner_failed'}));child.on('close',code=>done({code,message}));});
 assert.equal(result.code,0,'scanner_failed_'+item.name);
 const content=await readFile(path),bom=JSON.parse(content);assert.equal(bom.bomFormat,'CycloneDX');assert.ok(bom.components?.length);
 inventory.push({file:item.name+'.cdx.json',sha256:createHash('sha256').update(content).digest('hex'),components:bom.components.length,...(item.image?{image_config_digest:item.image.config_digest,image_manifest_digest:item.image.image_id}:{scope:'frontend-build-inputs-including-development-dependencies'})});
 console.log('PASS: sbom-'+item.name);
}
await writeFile(join(output,'manifest.json'),JSON.stringify({schema_version:1,scanner:{name:'syft',version:lock.version,binary_sha256:lock.builds[platform].binary_sha256},inventories:inventory},null,2)+'\n');
