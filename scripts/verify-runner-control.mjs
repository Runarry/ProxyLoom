import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { runnerEvidence } from './verify-runner-report.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const args = process.argv.slice(2);
assert.equal(args.length, 4, 'Usage: node scripts/verify-runner-control.mjs --network <isolated-acceptance-network> --dsn-dir <test-dsn-directory>');
assert.equal(args[0], '--network');
assert.equal(args[2], '--dsn-dir');
const network = args[1];
assert.match(network, /^proxyloom-acceptance-[a-z0-9-]+$/);
const source = resolve(args[3]);
assert.ok(!relative(join(root, '.cache', 'acceptance-db'), source).startsWith('..'), 'Only disposable acceptance credentials may be used');
const cache = join(root, '.cache', 'runner-control');
const runID = randomUUID();
const work = join(cache, runID);
const secrets = join(work, 'secrets');
mkdirSync(work, { recursive: true, mode: 0o700 });
mkdirSync(secrets, { mode: 0o700 });
chmodSync(work, 0o700);
chmodSync(secrets, 0o700);
const forbiddenOutput = [];
const evidence = runnerEvidence(root, 'runner-control', forbiddenOutput);
let status = 'failed';
const image = JSON.parse(readFileSync(join(root, 'deploy', 'tools.lock.json'), 'utf8')).images.runtime;
assert.match(image, /@sha256:[0-9a-f]{64}$/);
function run(command, argv, extra = {}) {
  const result = spawnSync(command, argv, { cwd: root, shell: false, encoding: 'utf8', timeout: 210000, maxBuffer: 2 * 1024 * 1024, ...extra });
  if (result.error) throw result.error;
  evidence.forward(result.stdout, result.stderr);
  assert.equal(result.status, 0, `${command} failed with status ${result.status}`);
}
try {
  for (const name of ['database_dsn', 'migration_dsn', 'admin_dsn']) {
    const original = readFileSync(join(source, name), 'utf8').trim();
    let dsn;
    try { dsn = new URL(original); } catch { throw new Error('Invalid disposable acceptance DSN'); }
    forbiddenOutput.push(original, dsn.password, decodeURIComponent(dsn.password));
    assert.ok(dsn.protocol === 'postgresql:' || dsn.protocol === 'postgres:');
    dsn.hostname = network;
    dsn.port = '5432';
    forbiddenOutput.push(dsn.toString());
    // Protected 0700 host parents retain confidentiality. Individual read-only
    // mounts expose only these files to container UID10002 on Linux hosts.
    writeFileSync(join(secrets, name), dsn.toString(), { mode: 0o444 });
  }
  run('go', ['test', '-mod=readonly', '-c', '-o', join(work, 'control.test'), './internal/runnercontrol'], {
    env: { ...process.env, GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0', GOCACHE: join(root, '.cache', 'go-build') },
  });
  chmodSync(join(work, 'control.test'), 0o755);
  evidence.binary(join(work, 'control.test'));
  run('docker', ['run', '--rm', '--name', `proxyloom-runner-control-${runID}`, '--network', network, '--user', '10002:10002', '--read-only', '--cap-drop=ALL', '--security-opt', 'no-new-privileges=true',
    '--pids-limit', '128', '--memory', '512m', '--cpus', '2', '--tmpfs', '/tmp:rw,nosuid,nodev,noexec,size=128m,mode=1777',
    '--mount', `type=bind,source=${join(work, 'control.test')},target=/suite/control.test,readonly`,
    ...['database_dsn', 'migration_dsn', 'admin_dsn'].flatMap((name) => ['--mount', `type=bind,source=${join(secrets, name)},target=/run/secrets/${name},readonly`]),
    '--mount', `type=bind,source=${join(root, '.cache', 'cores')},target=/cores,readonly`,
    '--mount', `type=bind,source=${join(root, 'fixtures', 'runner', 'validate')},target=/fixtures,readonly`,
    '-e', 'GOMAXPROCS=2', '-e', 'PROXYLOOM_REQUIRE_RUNNER_TESTS=true', '-e', 'PROXYLOOM_RUNNER_REAL_CORES=/cores', '-e', 'PROXYLOOM_RUNNER_FIXTURE_ROOT=/fixtures',
    '-e', 'PROXYLOOM_TEST_DATABASE_DSN_FILE=/run/secrets/database_dsn', '-e', 'PROXYLOOM_TEST_MIGRATION_DSN_FILE=/run/secrets/migration_dsn', '-e', 'PROXYLOOM_TEST_ADMIN_DSN_FILE=/run/secrets/admin_dsn',
    image, '/suite/control.test', '-test.v', '-test.timeout', '180s']);
  console.log('PASS: real mTLS, encrypted durable queue, three pinned positive/negative checkers, fencing and replay.');
  status = 'passed';
} finally {
  assert.equal(resolve(work), resolve(cache, runID));
  assert.match(runID, /^[a-f0-9-]{36}$/);
  assert.ok(!relative(cache, work).startsWith('..'));
  rmSync(work, { recursive: true, force: true });
  evidence.finish(status);
}
