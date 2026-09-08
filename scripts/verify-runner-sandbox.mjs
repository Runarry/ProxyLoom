import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { runnerEvidence } from './verify-runner-report.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const work = join(root, '.cache', 'runner-sandbox');
mkdirSync(work, { recursive: true });
const lock = JSON.parse(readFileSync(join(root, 'deploy', 'tools.lock.json'), 'utf8'));
assert.match(lock.images.runtime, /@sha256:[0-9a-f]{64}$/);
const evidence = runnerEvidence(root, 'runner-sandbox');
let status = 'failed';
function run(command, args, extra = {}) {
  const result = spawnSync(command, args, { cwd: root, shell: false, encoding: 'utf8', timeout: 180000, maxBuffer: 2 * 1024 * 1024, ...extra });
  if (result.error) throw result.error;
  evidence.forward(result.stdout, result.stderr);
  assert.equal(result.status, 0, `${command} failed with status ${result.status}`);
}
try {
run('go', ['test', '-mod=readonly', '-c', '-o', join(work, 'exec.test'), './internal/runner/exec'], {
  env: { ...process.env, GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0', GOCACHE: join(root, '.cache', 'go-build') },
});
evidence.binary(join(work, 'exec.test'));
// The supervisor deliberately has an ordinary Docker network. Tests prove that
// its child cannot create TCP, UDP, IPv6 or Unix sockets, independently of Docker.
run('docker', ['run', '--rm', '--user', '10002:10002', '--read-only', '--cap-drop=ALL', '--security-opt', 'no-new-privileges=true',
  '--pids-limit', '128', '--memory', '512m', '--cpus', '2', '--tmpfs', '/tmp:rw,nosuid,nodev,noexec,size=96m,mode=1777',
  '--mount', `type=bind,source=${work},target=/suite,readonly`,
  '--mount', `type=bind,source=${join(root, '.cache', 'cores')},target=/cores,readonly`,
  '--mount', `type=bind,source=${join(root, 'fixtures', 'runner', 'validate')},target=/fixtures,readonly`,
  '-e', 'GOMAXPROCS=2', '-e', 'PROXYLOOM_RUNNER_REAL_CORES=/cores', '-e', 'PROXYLOOM_RUNNER_FIXTURE_ROOT=/fixtures',
  lock.images.runtime, '/suite/exec.test', '-test.v', '-test.run', 'TestConfigSandbox|TestRealConfigSandbox|TestSealedExecutable', '-test.timeout', '90s']);
console.log('PASS: enforced Linux checker sandbox and all three locked core checks.');
status = 'passed';
} finally { evidence.finish(status); }
