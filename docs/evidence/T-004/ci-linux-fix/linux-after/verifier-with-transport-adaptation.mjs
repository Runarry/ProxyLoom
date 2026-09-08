// T-004/005/007 integration: a uniquely owned, disposable PostgreSQL instance.
// No development secrets, existing database volumes, or external proxy nodes.
import assert from 'node:assert/strict';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { chmodSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { resolve, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync, spawn } from 'node:child_process';
import { summarizeGoFailure } from './foundation-report.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
assert.ok(process.argv.slice(2).every((argument) => argument === '--storage-only'), 'foundation_unknown_argument');
const storageOnly = process.argv.includes('--storage-only');
const lock = JSON.parse(readFileSync(join(root, 'deploy/tools.lock.json'), 'utf8'));
const runID = randomUUID();
const name = `proxyloom-foundation-${runID}`;
const label = 'io.proxyloom.foundation';
const directory = join(root, '.cache', 'foundation', runID);
const secrets = join(directory, 'secrets');
mkdirSync(secrets, { recursive: true, mode: 0o700 });
const sensitive = [];
const report = { schema_version: 1, run_id: runID, started_at: new Date().toISOString(), postgres_image: lock.images.postgres, checks: [], cleanup: [], result: 'fail' };
const owned = { network: false, container: false };
let pendingError;

function safe(content) {
  for (const value of sensitive) assert.ok(!content.includes(value), 'foundation_output_contains_secret');
  return content;
}
function docker(args, phase) {
  const result = spawnSync('docker', args, { cwd: root, encoding: 'utf8', windowsHide: true, timeout: 180000, maxBuffer: 8 * 1024 * 1024 });
  safe(`${result.stdout ?? ''}${result.stderr ?? ''}`);
  assert.ok(!result.error && result.status === 0, `foundation_${phase}_failed`);
  return result.stdout.trim();
}
function credential(file, value, containerReadable = false) {
  sensitive.push(value);
  const path = join(secrets, file);
  writeFileSync(path, value, { flag: 'wx', mode: 0o600 });
  // The host parent stays 0700. Only individual password files are mounted;
  // postgres must read them after dropping root. DSNs remain host-only 0600.
  if (containerReadable && process.platform !== 'win32') chmodSync(path, 0o444);
}
function pass(check) { report.checks.push({ check, result: 'pass' }); console.log(`PASS: ${check}`); }
function sourceManifest() {
  const entries = [];
  function walk(directory) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      if (['.git', '.cache', '.codex', '.agents', '.idea', '.vscode', 'node_modules', 'dist', 'bin', 'secrets', '.pnpm-store', 'coverage'].includes(entry.name) || entry.name.startsWith('.env')) continue;
      if (directory === root && entry.isDirectory() && !['api', 'compat', 'deploy', 'fixtures', 'internal', 'migrations', 'proxyloom-server', 'proxyloom-runner', 'proxyloom-fixtures', 'proxyloom-web', 'schemas', 'scripts', '.github', 'docs'].includes(entry.name)) continue;
      const path = join(directory, entry.name);
      const name = relative(root, path).replaceAll('\\', '/');
      if (name.startsWith('docs/') && !['docs/requirements_v1.0.md', 'docs/project_design_v1.0.md'].includes(name)) continue;
      if (entry.isDirectory()) walk(path);
      else if (entry.isFile() && name !== 'deploy/smoke-report.json' && !/\.(?:exe|log|test|tsbuildinfo)$/.test(name)) {
        entries.push({ path: name, sha256: createHash('sha256').update(readFileSync(path)).digest('hex') });
      }
    }
  }
  walk(root);
  return entries.sort((a, b) => a.path < b.path ? -1 : a.path > b.path ? 1 : 0);
}
function owner(kind) {
  const format = kind === 'container' ? `{{index .Config.Labels "${label}"}}` : `{{index .Labels "${label}"}}`;
  assert.equal(docker([kind, 'inspect', '--format', format, name], 'inspect_owner'), runID, 'foundation_resource_owner_mismatch');
}

try {
  const platform = docker(['version', '--format', '{{.Server.Os}}/{{.Server.Arch}}'], 'engine');
  assert.ok(platform.startsWith('linux/'), 'foundation_requires_linux_docker');
  report.postgres_platform = platform;
  report.test_platform = `${process.platform}/${process.arch}`;
  for (const kind of ['container', 'network']) {
    const args = [kind, 'ls', '--quiet', '--filter', `name=${name}`];
    if (kind === 'container') args.push('--all');
    assert.equal(docker(args, 'inventory'), '', 'foundation_resource_already_exists');
  }
  const bootstrapPassword = randomBytes(32).toString('hex');
  const runtimePassword = randomBytes(32).toString('hex');
  const migrationPassword = randomBytes(32).toString('hex');
  credential('db_bootstrap_password', bootstrapPassword, true);
  credential('db_runtime_password', runtimePassword, true);
  credential('db_migration_password', migrationPassword, true);
  // Resolve the exact digest locally before fetching. A dedicated bridge permits
  // Windows host tests to use the loopback-only published port; Docker Desktop
  // does not allocate such a port on an internal-only network.
  const cached = spawnSync('docker', ['image', 'inspect', lock.images.postgres], { encoding: 'utf8', windowsHide: true });
  if (cached.error || cached.status !== 0) docker(['pull', lock.images.postgres], 'pull_postgres');
  docker(['network', 'create', '--label', `${label}=${runID}`, name], 'create_network');
  owned.network = true;
  docker(['network', 'connect', name, process.env.REPRO_CLIENT], 'connect_repro_client');
  docker(['create', '--name', name, '--label', `${label}=${runID}`, '--network', name,
    '--publish', '127.0.0.1::5432', '--memory', '512m', '--cpus', '1', '--pids-limit', '128',
    '--tmpfs', '/var/lib/postgresql/data:rw,size=268435456',
    ...['db_bootstrap_password', 'db_runtime_password', 'db_migration_password'].flatMap((file) =>
      ['--mount', `type=bind,source=${join(secrets, file)},target=/run/secrets/${file},readonly`]),
    '--mount', `type=bind,source=${join(root, 'deploy', 'postgres-init.sh')},target=/docker-entrypoint-initdb.d/10-proxyloom.sh,readonly`,
    '--env', 'POSTGRES_DB=proxyloom', '--env', 'POSTGRES_USER=proxyloom_bootstrap',
    '--env', 'POSTGRES_PASSWORD_FILE=/run/secrets/db_bootstrap_password',
    '--env', 'POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256',
    '--env', 'PGDATA=/var/lib/postgresql/data', lock.images.postgres], 'create_postgres');
  owned.container = true;
  docker(['start', name], 'start_postgres');
  const deadline = Date.now() + 60000;
  let ready = false;
  while (Date.now() < deadline) {
    const result = spawnSync('docker', ['exec', name, 'pg_isready', '-h', '127.0.0.1', '-U', 'proxyloom_bootstrap', '-d', 'proxyloom'], { encoding: 'utf8', windowsHide: true, timeout: 5000 });
    if (!result.error && result.status === 0) {
      // pg_isready also succeeds during init; the runtime role proves init completed.
      const roles = spawnSync('docker', ['exec', name, 'psql', '-U', 'proxyloom_bootstrap', '-d', 'proxyloom', '-Atc', "SELECT count(*) FROM pg_roles WHERE rolname IN ('proxyloom','proxyloom_migrator')"], { encoding: 'utf8', windowsHide: true, timeout: 5000 });
      if (!roles.error && roles.status === 0 && roles.stdout.trim() === '2') { ready = true; break; }
    }
    await new Promise((resolveWait) => setTimeout(resolveWait, 500));
  }
  assert.ok(ready, 'foundation_postgres_readiness_timeout');
  const bindings = JSON.parse(docker(['inspect', '--format', '{{json .NetworkSettings.Ports}}', name], 'port'));
  const port = bindings['5432/tcp']?.[0]?.HostPort;
  assert.equal(bindings['5432/tcp']?.[0]?.HostIp, '127.0.0.1', 'foundation_postgres_must_be_loopback');
  assert.ok(/^\d+$/.test(port ?? ''), 'foundation_postgres_port_invalid');
  credential('admin_dsn', `postgres://proxyloom_bootstrap:${bootstrapPassword}@${name}:5432/proxyloom?sslmode=disable`);
  credential('database_dsn', `postgres://proxyloom:${runtimePassword}@${name}:5432/proxyloom?sslmode=disable`);
  credential('migration_dsn', `postgres://proxyloom_migrator:${migrationPassword}@${name}:5432/proxyloom?sslmode=disable`);
  pass('isolated_locked_postgres_with_separate_roles');
  const env = { ...process.env, GOPATH: join(root, '.cache/gopath'), GOMODCACHE: join(root, '.cache/gomod'), GOCACHE: join(root, '.cache/go-build'),
    PROXYLOOM_TEST_DATABASE_DSN_FILE: join(secrets, 'database_dsn'),
    PROXYLOOM_TEST_MIGRATION_DSN_FILE: join(secrets, 'migration_dsn'),
    PROXYLOOM_TEST_ADMIN_DSN_FILE: join(secrets, 'admin_dsn'), PROXYLOOM_REQUIRE_POSTGRES_TESTS: 'true' };
  const packages = storageOnly ? ['./internal/catalog', './internal/storage', './internal/secretbox', './internal/config'] : ['./internal/catalog', './internal/storage', './internal/secretbox', './internal/apicontract', './api'];
  const args = ['test', '-mod=readonly', '-json', '-count=1', '-timeout=180s', ...packages];
  report.mode = storageOnly ? 'storage' : 'full';
  report.command = `go ${args.join(' ')}`;
  const manifest = JSON.stringify({ schema_version: 1, files: sourceManifest() }, null, 2) + '\n';
  writeFileSync(join(directory, 'source-manifest.json'), manifest);
  report.source_manifest_sha256 = createHash('sha256').update(manifest).digest('hex');
  report.go_version = spawnSync('go', ['version'], { cwd: root, env, encoding: 'utf8', windowsHide: true }).stdout?.trim() ?? 'unavailable';
  const output = await new Promise((resolveOutput, reject) => {
    const child = spawn('go', args, { cwd: root, env, windowsHide: true, stdio: ['ignore', 'pipe', 'pipe'] });
    let stdout = '', stderr = '';
    child.stdout.setEncoding('utf8'); child.stderr.setEncoding('utf8');
    child.stdout.on('data', (chunk) => { stdout += chunk; });
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.on('error', () => reject(new Error('foundation_go_start_failed')));
    child.on('close', (code) => resolveOutput({ stdout, stderr, code }));
  });
  safe(output.stdout); safe(output.stderr);
  writeFileSync(join(directory, 'go-test.jsonl'), output.stdout, { mode: 0o600 });
  writeFileSync(join(directory, 'go-stderr.txt'), output.stderr, { mode: 0o600 });
  const events = output.stdout.split(/\r?\n/).filter(Boolean).map((line) => JSON.parse(line));
  const integration = events.filter((event) => /^TestPostgres/.test(event.Test ?? '') && !event.Test.includes('/') && ['pass', 'fail', 'skip'].includes(event.Action));
  report.postgres_tests = integration.map(({ Test, Action }) => ({ name: Test, result: Action }));
  report.postgres_skipped_tests = events.filter((event) => /^TestPostgres/.test(event.Test ?? '') && event.Action === 'skip').map(({ Test }) => Test);
  report.failed_tests = events.filter((event) => event.Action === 'fail').map(({ Package, Test }) => ({ package: Package, test: Test ?? null }));
  if (output.code !== 0) {
    for (const line of summarizeGoFailure({ events, stderr: output.stderr, secrets: sensitive })) console.error(line);
  }
  assert.ok(manifest === JSON.stringify({ schema_version: 1, files: sourceManifest() }, null, 2) + '\n', 'foundation_source_changed_during_tests');
  assert.equal(output.code, 0, 'foundation_go_tests_failed_see_local_evidence');
  assert.ok(integration.length > 0 && integration.every((event) => event.Action === 'pass') && report.postgres_skipped_tests.length === 0, 'foundation_postgres_tests_missing_or_skipped');
  pass('postgres_and_foundation_tests_executed');
} catch (error) {
  pendingError = error;
  report.error = safe(error.message);
} finally {
  if (owned.network) docker(['network', 'disconnect', name, process.env.REPRO_CLIENT], 'disconnect_repro_client');
  for (const kind of ['container', 'network']) {
    if (!owned[kind]) continue;
    try {
      owner(kind);
      docker(kind === 'container' ? ['container', 'rm', '--force', '--volumes', name] : ['network', 'rm', name], `cleanup_${kind}`);
      report.cleanup.push({ kind, result: 'pass' });
    } catch (error) {
      report.cleanup.push({ kind, result: 'fail' });
      pendingError ??= error;
    }
  }
  report.ended_at = new Date().toISOString();
  report.result = pendingError ? 'fail' : 'pass';
  report.source_commit = spawnSync('git', ['rev-parse', 'HEAD'], { cwd: root, encoding: 'utf8', windowsHide: true }).stdout?.trim() ?? 'unavailable';
  writeFileSync(join(directory, 'report.json'), safe(`${JSON.stringify(report, null, 2)}\n`));
  console.log(`Foundation report: ${resolve(directory, 'report.json')}`);
}
if (pendingError) throw pendingError;
console.log('PASS: foundation integration and owned Docker resource cleanup.');
