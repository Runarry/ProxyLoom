// Build local self-hosted images; never push or mark a platform verified.
import assert from 'node:assert/strict';
import { randomUUID, createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const architectures = (process.argv[2] ?? 'amd64,arm64').split(',');
assert.ok(architectures.length && new Set(architectures).size === architectures.length && architectures.every(v => ['amd64', 'arm64'].includes(v)));
const id = randomUUID(), directory = join(root, '.cache/release', id);
await mkdir(directory, { recursive: true });
const report = { schema_version: 1, run_id: id, result: 'fail', created_at: new Date().toISOString(), images: [], architectures: {}, directory };
const inventory = spawnSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '-z'], { cwd: root, encoding: 'utf8', windowsHide: true });
assert.equal(inventory.status, 0);
const sources = [...new Set(inventory.stdout.split('\0').filter(Boolean))].filter(path => /^(api|compat|internal|migrations|schemas|fixtures|proxyloom-server|proxyloom-runner|proxyloom-operations|proxyloom-web|scripts|deploy)\//.test(path) || ['go.mod', 'go.sum'].includes(path)).filter(path => existsSync(join(root, path))).sort();
const sourceManifest = () => sources.map(path => ({ path, sha256: createHash('sha256').update(readFileSync(join(root, path))).digest('hex') }));
const initialSources = sourceManifest();
await writeFile(join(directory, 'source-manifest.json'), JSON.stringify({ schema_version: 1, files: initialSources }, null, 2) + '\n');
report.source_manifest_sha256 = createHash('sha256').update(await readFile(join(directory, 'source-manifest.json'))).digest('hex');
async function run(name, args, executable = 'docker') {
  const result = await new Promise(resolveRun => {
    const child = spawn(executable, args, { cwd: root, windowsHide: true, env: { ...process.env, SYFT_CHECK_FOR_APP_UPDATE: 'false' } });
    let output = ''; child.stdout.on('data', data => { output += data; }); child.stderr.on('data', data => { output += data; });
    child.on('error', () => resolveRun({ code: -1, output: 'command_start_failed' }));
    child.on('close', code => resolveRun({ code, output }));
  });
  await writeFile(join(directory, `${name}.txt`), result.output);
  if (result.code !== 0) process.stdout.write(result.output.slice(-7000));
  assert.equal(result.code, 0, `${name}_failed`);
  console.log(`PASS: ${name}`);
  return result.output;
}
try {
  const stage = process.argv[3] ? { context: resolve(process.argv[3]) } : JSON.parse((await run('stage-cores', ['scripts/stage-release-cores.mjs'], process.execPath)).trim());
  assert.ok(stage.context);
  for (const arch of architectures) {
    for (const [service, dockerfile] of [['api', 'api'], ['runner', 'runner-release'], ['operations', 'operations']]) {
      const tag = `proxyloom-${service}:m3-${id}-${arch}`;
      await run(`build-${service}-${arch}`, ['buildx', 'build', '--load', '--platform', `linux/${arch}`, '--provenance=false', '--progress=plain',
        '--file', `deploy/Dockerfile.${dockerfile}`, '--tag', tag, ...(service === 'runner' ? ['--build-context', `lockedcores=${stage.context}`] : []), '.']);
      const inspected = JSON.parse(await run(`inspect-${service}-${arch}`, ['image', 'inspect', tag]));
      assert.equal(inspected[0].Architecture, arch);
      assert.match(inspected[0].Id, /^sha256:[a-f0-9]{64}$/);
      report.images.push({ service, architecture: arch, tag, image_id: inspected[0].Id, os: inspected[0].Os, runtime_verified: false });
    }
    report.architectures[arch] = { built: true, native_runtime: 'unverified' };
  }
  report.core_lock_sha256 = createHash('sha256').update(await readFile(join(root, 'compat/cores.lock.yaml'))).digest('hex');
  assert.deepEqual(sourceManifest(), initialSources, 'source_changed_during_build');
  report.result = 'pass';
} catch (error) { report.error = String(error); process.exitCode = 1; }
finally {
  await writeFile(join(directory, 'images.json'), JSON.stringify(report, null, 2) + '\n');
  console.log(JSON.stringify(report));
}
