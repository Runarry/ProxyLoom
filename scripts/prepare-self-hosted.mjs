// Reproducible local/CI handoff pipeline. It never pushes images or deploys to a
// user host; runtime checks own disposable Docker containers and databases.
import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawn } from 'node:child_process';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url));
assert.equal(process.argv.length, 2, 'handoff_pipeline_takes_no_arguments');
const directory = join(root, '.cache/handoff', randomUUID()); await mkdir(directory, { recursive: true });
const report = { schema_version: 1, result: 'fail', started_at: new Date().toISOString(), steps: [], arm64_runtime: 'unverified', gate: 'G3_open' };
async function run(script, args = [], extraEnv = {}) {
  const result = await new Promise(done => {
    const child = spawn(process.execPath, ['scripts/' + script + '.mjs', ...args], { cwd: root, windowsHide: true, env: { ...process.env, ...extraEnv } });
    let out = ''; child.stdout.on('data', v => { out += v; process.stdout.write(v); }); child.stderr.on('data', v => process.stderr.write(v));
    child.on('error', () => done({ code: -1, out })); child.on('close', code => done({ code, out }));
  });
  report.steps.push({ name: script, result: result.code === 0 ? 'pass' : 'fail' }); assert.equal(result.code, 0, script + '_failed');
  return result.out;
}
function lastJSON(text) { return JSON.parse(text.trim().split(/\r?\n/).findLast(line => line.startsWith('{'))); }
try {
  const cores = lastJSON(await run('stage-release-cores'));
  await run('check-quality');
  await run('verify-m1-native', [], { PROXYLOOM_NATIVE_EVIDENCE_DIR: '.cache/handoff-native' });
  await run('verify-live-chain');
  await run('verify-runner-sandbox', ['--full']);
  await run('verify-m2', ['all']);
  await run('verify-m3', ['all']);
  const build = lastJSON(await run('build-release', ['amd64,arm64', cores.context]));
  const images = lastJSON(await run('export-release-images', [build.directory, 'amd64,arm64']));
  const deployment = await run('verify-deployment', [images.directory, '--capacity']);
  const deploymentReport = deployment.match(/Deployment report: (.+[\\/]report\.json)/)?.[1]; assert.ok(deploymentReport);
  await run('install-release-tools');
  await run('stage-release-sources');
  await run('stage-release-notices');
  await run('generate-release-sbom', [images.directory]);
  await run('scan-release-vulnerabilities', [images.directory]);
  const delivery = lastJSON(await run('package-release', [images.directory, join(deploymentReport, '..')]));
  report.delivery_directory = delivery.directory; report.result = 'pass';
} catch (error) { report.error = String(error); process.exitCode = 1; }
finally {
  report.ended_at = new Date().toISOString(); await writeFile(join(directory, 'report.json'), JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify({ report: join(directory, 'report.json'), ...report }));
}
