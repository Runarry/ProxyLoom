import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const outDir = join(root, '.cache', 't028');
const certs = join(root, '.cache', 'isolation', 'certs');
const project = 'proxyloom-isolation-t028';
const compose = join(root, 'deploy', 'compose.isolation.yaml');
const tools = JSON.parse(readFileSync(join(root, 'deploy', 'tools.lock.json'), 'utf8'));
const image = tools.images.runtime;
if (!image || !image.includes('@sha256:')) {
  throw new Error('runtime image must be pinned by digest');
}

function fail(message) {
  throw new Error(`Isolation compose: ${message}`);
}

function docker(args, timeoutMs = 180000) {
  const result = spawnSync('docker', args, {
    cwd: root,
    encoding: 'utf8',
    shell: false,
    timeout: timeoutMs,
    maxBuffer: 4 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  return result;
}

function composeCmd(args, timeoutMs = 180000) {
  return docker(['compose', '-p', project, '-f', compose, ...args], timeoutMs);
}

function toPosix(path) {
  return path.replaceAll('\\', '/');
}

function compileLinuxTest(pkg, dest) {
  mkdirSync(dirname(dest), { recursive: true });
  const result = spawnSync('go', ['test', '-mod=readonly', '-c', '-o', dest, pkg], {
    cwd: root,
    encoding: 'utf8',
    shell: false,
    env: { ...process.env, GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0' },
  });
  if (result.status !== 0) {
    fail(`go test -c ${pkg} failed: ${result.stderr || result.stdout}`);
  }
}

const info = docker(['version', '--format', '{{.Server.Os}}/{{.Server.Arch}}']);
if (info.status !== 0 || !String(info.stdout).includes('linux/amd64')) {
  fail(`docker linux/amd64 required, got: ${(info.stdout || info.stderr || '').trim()}`);
}

const existing = composeCmd(['ps', '-q']);
if ((existing.stdout || '').trim()) {
  fail(`compose project ${project} is already running; refusing to reuse it`);
}

const ids = docker(['network', 'ls', '-q']);
for (const id of (ids.stdout || '').trim().split(/\s+/).filter(Boolean)) {
  const inspect = docker(['network', 'inspect', id, '--format', '{{.Name}} {{range .IPAM.Config}}{{.Subnet}}{{end}}']);
  const line = (inspect.stdout || '').trim();
  if (line.includes('172.30.253.') && !line.startsWith(`${project}_`)) {
    fail(`subnet 172.30.253.0/24 already used by ${line}`);
  }
}

if (!existsSync(join(certs, 'ca.pem')) || !existsSync(join(certs, 'a.pem')) || !existsSync(join(certs, 'b.pem'))) {
  const init = spawnSync('go', ['run', './proxyloom-fixtures', 'init-certs'], {
    cwd: root, encoding: 'utf8', shell: false,
  });
  if (init.status !== 0) fail(`init-certs failed: ${init.stderr || init.stdout}`);
}

mkdirSync(outDir, { recursive: true });
const bin = join(outDir, 'isolation-smoke.test');
compileLinuxTest('./internal/isolation', bin);

const network = `${project}_isolation`;
let failed = null;
try {
  const up = composeCmd(['up', '--build', '-d'], 360000);
  if (up.status !== 0) fail(`compose up failed: ${up.stderr || up.stdout}`);
  const runPhase = (phase) => {
    const script = `set -eu
cp /work/isolation-smoke.test /tmp/isolation-smoke.test
chmod 0755 /tmp/isolation-smoke.test
/tmp/isolation-smoke.test -test.v -test.count=1 -test.timeout 2m -test.run TestComposeContainerSmoke
`;
    const result = docker([
      'run', '--rm', '--network', network,
      '--tmpfs', '/tmp:exec,mode=1777',
      '-v', `${toPosix(bin)}:/work/isolation-smoke.test:ro`,
      '-v', `${toPosix(certs)}:/certs:ro`,
      '-e', 'PROXYLOOM_COMPOSE_SMOKE=1',
      '-e', `PROXYLOOM_COMPOSE_SMOKE_PHASE=${phase}`,
      '-e', 'PROXYLOOM_ISOLATION_CA=/certs/ca.pem',
      image,
      '/bin/sh', '-c', script,
    ], 180000);
    writeFileSync(join(outDir, `compose-smoke-${phase}.txt`), `${result.stdout || ''}\n${result.stderr || ''}`);
    if (result.status !== 0) {
      fail(`${phase} failed: ${(result.stderr || result.stdout || '').slice(-2000)}`);
    }
    return result.stdout || '';
  };
  const startup = runPhase('startup');
  const bypass = runPhase('bypass');
  const stopA = composeCmd(['stop', 'proxy-a']);
  if (stopA.status !== 0) fail(`stop proxy-a failed: ${stopA.stderr || stopA.stdout}`);
  const after = runPhase('after-stop-a');
  const encoded = JSON.stringify({
    project,
    network,
    subnet: '172.30.253.0/24',
    phases: { startup: 'pass', bypass: 'pass', after_stop_a: 'pass' },
  }, null, 2);
  if (/(EXAMPLE_ONLY_|password|secret)/i.test(encoded + startup + bypass + after)) {
    fail('compose smoke output contained secrets');
  }
  writeFileSync(join(outDir, 'compose-smoke-report.json'), `${encoded}\n`);
  process.stdout.write(`${encoded}\n`);
  process.stdout.write('PASS: isolation compose smoke verified shared CA, source restriction, and stop-A fail-closed.\n');
} catch (err) {
  failed = err;
} finally {
  const down = composeCmd(['down', '--remove-orphans'], 180000);
  if (down.status !== 0 && !failed) {
    failed = new Error(`compose down failed: ${down.stderr || down.stdout}`);
  }
}
if (failed) throw failed;
