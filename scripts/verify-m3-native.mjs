import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { runnerEvidence } from './verify-runner-report.mjs';

assert.ok(process.argv.slice(2).every(value=>value==='--arm64-sandbox'),'m3_native_unknown_argument');
const arm=process.argv.includes('--arm64-sandbox');
const root=fileURLToPath(new URL('../',import.meta.url));
const suite=arm?'m3-arm64-sandbox':'m3-native';
const work=join(root,'.cache',suite);mkdirSync(work,{recursive:true});
const lock=JSON.parse(readFileSync(join(root,'deploy/tools.lock.json'),'utf8'));
const evidence=runnerEvidence(root,suite);let status='failed';
function run(command,args,options={}) {const result=spawnSync(command,args,{cwd:root,encoding:'utf8',windowsHide:true,timeout:240000,maxBuffer:4*1024*1024,...options});evidence.forward(result.stdout,result.stderr);assert.ok(!result.error && result.status===0,`${command}_m3_native_failed`);return result.stdout;}
try {
 const binary=join(work,'suite.test');
 run('go',['test','-mod=readonly','-c','-o',binary,arm?'./internal/runner/exec':'./internal/runner'],{env:{...process.env,GOOS:'linux',GOARCH:arm?'arm64':'amd64',CGO_ENABLED:'0',GOCACHE:join(root,'.cache/go-build'),GOPATH:join(root,'.cache/gopath'),GOMODCACHE:join(root,'.cache/gomod')}});
 evidence.binary(binary);
 const pattern=arm?'^TestConfigSandboxEnforcesNetworkFilesystemProcessAndMemory$':'^TestNativeM3ThreeCoreNetwork$';
 const output=run('docker',['run','--rm','--platform',arm?'linux/arm64':'linux/amd64','--user','10002:10002','--read-only','--cap-drop=ALL','--security-opt','no-new-privileges=true','--pids-limit','128','--memory','1g','--cpus','2','--tmpfs','/tmp:rw,nosuid,nodev,noexec,size=192m,mode=1777','--mount',`type=bind,source=${work},target=/suite,readonly`,'--mount',`type=bind,source=${join(root,'.cache/cores')},target=/cores,readonly`,'-e','GOMAXPROCS=2','-e','PROXYLOOM_RUNNER_REAL_CORES=/cores',lock.images.runtime,'/suite/suite.test','-test.v','-test.run',pattern,'-test.timeout','180s']);
 assert.ok(output.includes('--- PASS: '+(arm?'TestConfigSandboxEnforcesNetworkFilesystemProcessAndMemory':'TestNativeM3ThreeCoreNetwork')),'m3_native_test_not_executed');
 status='passed';
} finally {evidence.finish(status);}
