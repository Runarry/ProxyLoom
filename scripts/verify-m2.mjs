// M2 acceptance owns its database, native test container and browser workspace.
import assert from 'node:assert/strict';
import { randomBytes, randomUUID, createHash } from 'node:crypto';
import { spawn, spawnSync } from 'node:child_process';
import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { createServer } from 'node:net';
import { fileURLToPath } from 'node:url';
import { assertSecretFree } from './quality-secrets.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const mode = process.argv[2] ?? 'all';
assert.ok(['all', 'postgres', 'native', 'browser'].includes(mode), 'mode must be all, postgres, native or browser');
const id = randomUUID(), work = join(root, '.cache/m2', id), secretDir = join(work, 'secrets'), controlDir = join(work, 'control');
for (const directory of [work, secretDir, controlDir]) mkdirSync(directory, { recursive: true, mode: 0o700 });
const report = { schema_version: 1, run_id: id, mode, started_at: new Date().toISOString(), result: 'fail', steps: [], cleanup: [] };
const secrets = [];
const inventory = spawnSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '-z'], { cwd: root, encoding: 'utf8', windowsHide: true });
assert.equal(inventory.status, 0, 'source_inventory_failed');
const sources = [...new Set(inventory.stdout.split('\0').filter(Boolean))].filter(path => /^(api|compat|internal|migrations|schemas|fixtures|proxyloom-server|proxyloom-runner|proxyloom-web|scripts|deploy)\//.test(path) || ['go.mod', 'go.sum'].includes(path)).filter(path => existsSync(join(root, path))).sort();
const sourceManifest = () => sources.map(path => ({ path, sha256: createHash('sha256').update(readFileSync(join(root, path))).digest('hex') }));
const initialSources = sourceManifest();
writeFileSync(join(work, 'source-manifest.json'), JSON.stringify({ schema_version: 1, files: initialSources }, null, 2) + '\n');
report.source_manifest_sha256 = createHash('sha256').update(readFileSync(join(work, 'source-manifest.json'))).digest('hex');
report.core_lock_sha256 = createHash('sha256').update(readFileSync(join(root, 'compat/cores.lock.yaml'))).digest('hex');
const env = { ...process.env, GOPATH: join(root, '.cache/gopath'), GOMODCACHE: join(root, '.cache/gomod'), GOCACHE: join(root, '.cache/go-build') };
let database, nativeChild;
function safe(text) {
  for (const secret of secrets) if (secret) text = text.replaceAll(secret, '[REDACTED]');
  return text.replace(/sub_[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{43}/g, '[REDACTED_SUBSCRIPTION_TOKEN]');
}
async function command(name, executable, args, options = {}) {
  const result = await new Promise((resolveResult) => {
    const child = spawn(executable, args, { cwd: root, env, windowsHide: true, ...options });
    let output = '';
    child.stdout?.on('data', chunk => { output += chunk; }); child.stderr?.on('data', chunk => { output += chunk; });
    child.on('error', () => resolveResult({ code: -1, output: 'process_start_failed' }));
    child.on('close', code => resolveResult({ code, output }));
  });
  writeFileSync(join(work, `${name}.txt`), assertSecretFree(safe(result.output), { path: `m2/${name}.txt`, secrets }), { mode: 0o600 });
  report.steps.push({ name, result: result.code === 0 ? 'pass' : 'fail' });
  if (result.code !== 0) process.stdout.write(safe(result.output).slice(-5000));
  assert.equal(result.code, 0, `${name}_failed`);
  console.log(`PASS: ${name}`);
  return result.output;
}
function docker(args) {
  const result = spawnSync('docker', args, { cwd: root, encoding: 'utf8', windowsHide: true, timeout: 30000 });
  assert.ok(!result.error && result.status === 0, `docker_${args[0]}_failed`); return result.stdout.trim();
}
async function freePort() {
  const server = createServer(); await new Promise(resolveListen => server.listen(0, '127.0.0.1', resolveListen));
  const port = server.address().port; await new Promise(resolveClose => server.close(resolveClose)); return port;
}
try {
  const publicationEvidence = JSON.parse(readFileSync(join(root, 'internal/compiler/publication-evidence.json')));
  for (const entry of [publicationEvidence, publicationEvidence.vmess_evidence]) {
    assert.equal(createHash('sha256').update(readFileSync(join(root, entry.report))).digest('hex'), entry.report_sha256, 'publication_evidence_drift');
  }
  report.steps.push({ name: 'publication-evidence-integrity', result: 'pass' });
  const output = await command('database-start', process.execPath, ['scripts/acceptance-db.mjs', 'start']);
  database = JSON.parse(output.trim().split(/\r?\n/).at(-1));
  assert.match(database.id, /^[0-9a-f-]{36}$/);
  const network = `proxyloom-acceptance-${database.id}`;
  for (const [file, variable] of [['database_dsn', 'DATABASE'], ['migration_dsn', 'MIGRATION'], ['admin_dsn', 'ADMIN']]) {
    const path = join(database.secrets, file), original = readFileSync(path, 'utf8').trim(), dsn = new URL(original);
    secrets.push(original, dsn.password); env[`PROXYLOOM_TEST_${variable}_DSN_FILE`] = path;
    dsn.hostname = network; dsn.port = '5432'; secrets.push(dsn.toString());
    writeFileSync(join(secretDir, file), dsn.toString(), { mode: 0o444 });
  }
  if (['all', 'postgres'].includes(mode)) await command('postgres', 'go', ['test', '-mod=readonly', '-count=1', '-v', '-timeout=180s', '-run', '^TestPostgres(Publication|Subscription)', './internal/storage'], { env: { ...env, PROXYLOOM_REQUIRE_POSTGRES_TESTS: 'true' } });
  if (mode !== 'postgres') {
    await command('build-native-suite', 'go', ['test', '-mod=readonly', '-c', '-o', join(work, 'm2.test'), './internal/runnercontrol'], { env: { ...env, GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' } });
    chmodSync(join(work, 'm2.test'), 0o755);
    report.test_binary_sha256 = createHash('sha256').update(readFileSync(join(work, 'm2.test'))).digest('hex');
    const image = JSON.parse(readFileSync(join(root, 'deploy/tools.lock.json'), 'utf8')).images.runtime;
    const dockerArgs = (name, extra = []) => ['run', '--rm', '--name', name, '--label', `io.proxyloom.m2=${id}`, '--network', network, '--user', '10002:10002', '--read-only', '--cap-drop=ALL', '--security-opt', 'no-new-privileges=true', '--pids-limit', '128', '--memory', '512m', '--cpus', '2', '--tmpfs', '/tmp:rw,nosuid,nodev,noexec,size=128m,mode=1777', '--mount', `type=bind,source=${join(work, 'm2.test')},target=/suite/m2.test,readonly`, '--mount', `type=bind,source=${join(root, '.cache/cores')},target=/cores,readonly`, ...['database_dsn', 'migration_dsn', 'admin_dsn'].flatMap(file => ['--mount', `type=bind,source=${join(secretDir, file)},target=/run/secrets/${file},readonly`]), '-e', 'GOMAXPROCS=2', '-e', 'PROXYLOOM_REQUIRE_RUNNER_TESTS=true', '-e', 'PROXYLOOM_RUNNER_REAL_CORES=/cores', '-e', 'PROXYLOOM_TEST_DATABASE_DSN_FILE=/run/secrets/database_dsn', '-e', 'PROXYLOOM_TEST_MIGRATION_DSN_FILE=/run/secrets/migration_dsn', '-e', 'PROXYLOOM_TEST_ADMIN_DSN_FILE=/run/secrets/admin_dsn', ...extra, image, '/suite/m2.test', '-test.v', '-test.run=^TestPostgresM2RealPublication$', '-test.timeout=9m'];
    if (['all', 'native'].includes(mode)) await command('native-publication', 'docker', dockerArgs(`proxyloom-m2-native-${id}`));
    if (['all', 'browser'].includes(mode)) {
      const password = `M2-${randomBytes(24).toString('base64url')}!`; secrets.push(password);
      writeFileSync(join(secretDir, 'browser_password'), password, { mode: 0o444 });
      const port = await freePort(), baseURL = `http://127.0.0.1:${port}`, name = `proxyloom-m2-browser-${id}`;
      // Docker Desktop maps the host directory; Linux hosts grant only this
      // disposable coordination directory to the unprivileged test process.
      chmodSync(controlDir, 0o777);
      nativeChild = spawn('docker', dockerArgs(name, ['--publish', `127.0.0.1:${port}:18080`, '--mount', `type=bind,source=${join(root, 'proxyloom-web/dist')},target=/web,readonly`, '--mount', `type=bind,source=${controlDir},target=/control`, '--mount', `type=bind,source=${join(secretDir, 'browser_password')},target=/run/secrets/browser_password,readonly`, '-e', 'PROXYLOOM_M2_BROWSER_SERVER=true', '-e', `PROXYLOOM_M2_PUBLIC_URL=${baseURL}`, '-e', 'PROXYLOOM_M2_PASSWORD_FILE=/run/secrets/browser_password']), { cwd: root, env, windowsHide: true });
      let nativeOutput = '', nativeExit;
      nativeChild.stdout.on('data', c => { nativeOutput += c; }); nativeChild.stderr.on('data', c => { nativeOutput += c; });
      const finished = new Promise(resolveDone => { nativeChild.on('close', code => { nativeExit = code; resolveDone(code); }); });
      const deadline = Date.now() + 60000;
      while (!existsSync(join(controlDir, 'ready')) && nativeExit === undefined && Date.now() < deadline) await new Promise(resolveWait => setTimeout(resolveWait, 250));
      if (!existsSync(join(controlDir, 'ready'))) { process.stdout.write(safe(nativeOutput)); throw new Error('browser_native_server_not_ready'); }
      try {
        await command('browser', process.execPath, ['node_modules/@playwright/test/cli.js', 'test', 'tests/e2e/subscriptions.spec.ts', '--config', 'playwright.config.ts', '--output', join(work, 'browser-results')], { cwd: join(root, 'proxyloom-web'), env: { ...env, PROXYLOOM_E2E_BASE_URL: baseURL, PROXYLOOM_M2_DB_CONTAINER: network, PROXYLOOM_E2E_USERNAME: 'm2-admin', PROXYLOOM_E2E_PASSWORD: password, PROXYLOOM_E2E_BROWSER: process.env.PROXYLOOM_E2E_BROWSER ?? 'msedge', PROXYLOOM_M2_PROFILE: readFileSync(join(controlDir, 'ready'), 'utf8').trim() } });
      } finally {
        writeFileSync(join(controlDir, 'stop'), 'stop');
        const code = await finished; writeFileSync(join(work, 'browser-native.txt'), safe(nativeOutput)); nativeChild = undefined;
        report.steps.push({ name: 'browser-native-server', result: code === 0 ? 'pass' : 'fail' }); assert.equal(code, 0, 'browser_native_server_failed');
      }
    }
  }
  assert.deepEqual(sourceManifest(), initialSources, 'source_changed_during_acceptance');
  report.steps.push({ name: 'source-stability', result: 'pass' });
  report.result = 'pass';
} catch (error) { report.error = safe(String(error)); process.exitCode = 1; console.error(report.error); }
finally {
  for (const name of [`proxyloom-m2-native-${id}`, `proxyloom-m2-browser-${id}`]) {
    const found = spawnSync('docker', ['inspect', '--format', '{{index .Config.Labels "io.proxyloom.m2"}}', name], { encoding: 'utf8', windowsHide: true });
    if (found.status === 0 && found.stdout.trim() === id) { docker(['rm', '--force', name]); report.cleanup.push({ name, result: 'pass' }); }
  }
  if (database) { try { await command('database-stop', process.execPath, ['scripts/acceptance-db.mjs', 'stop', database.id]); report.cleanup.push({ name: 'database', result: 'pass' }); } catch { report.result = 'fail'; process.exitCode = 1; } }
  assert.equal(resolve(secretDir), resolve(root, '.cache/m2', id, 'secrets'));
  rmSync(secretDir, { recursive: true, force: true });
  report.ended_at = new Date().toISOString(); writeFileSync(join(work, 'report.json'), safe(JSON.stringify(report, null, 2)) + '\n');
  console.log(`M2 report: ${join(work, 'report.json')}`);
}
