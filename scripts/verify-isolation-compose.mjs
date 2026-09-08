import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { dirname, join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { isIP } from 'node:net';

const root = fileURLToPath(new URL('../', import.meta.url));
const certs = join(root, '.cache', 'isolation', 'certs');
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

function ipv4Range(cidr) {
  const [address, prefix, extra] = cidr.split('/');
  const family = isIP(address);
  if (extra !== undefined || !/^\d+$/.test(prefix || '') || !family || Number(prefix) > (family === 4 ? 32 : 128)) {
    fail(`invalid Docker subnet: ${cidr}`);
  }
  if (family === 6) return null;
  const value = address.split('.').reduce((n, octet) => n * 256 + Number(octet), 0);
  const size = 2 ** (32 - Number(prefix));
  const start = Math.floor(value / size) * size;
  return [start, start + size - 1];
}

export function subnetsOverlap(left, right) {
  const a = ipv4Range(left);
  const b = ipv4Range(right);
  return a !== null && b !== null && a[0] <= b[1] && b[0] <= a[1];
}

function prepare(bin) {
  if (!existsSync(join(certs, 'ca.pem')) || !existsSync(join(certs, 'a.pem')) || !existsSync(join(certs, 'b.pem'))) {
    const init = spawnSync('go', ['run', './proxyloom-fixtures', 'init-certs'], {
      cwd: root, encoding: 'utf8', shell: false,
    });
    if (init.error) throw init.error;
    if (init.status !== 0) fail(`init-certs failed: ${init.stderr || init.stdout}`);
  }
  compileLinuxTest('./internal/isolation', bin);
}

// Injection keeps the ownership and cleanup lifecycle testable without a Docker daemon.
export function runIsolationCompose({ execute = docker, prepareTest = prepare, outputRoot = join(root, '.cache', 't028'), write = (text) => process.stdout.write(text) } = {}) {
  const project = `proxyloom-isolation-t028-${randomUUID()}`;
  const outDir = join(outputRoot, project);
  const reportPath = join(outDir, 'compose-smoke-report.json');
  write(`Isolation compose project: ${project}\nReport path: ${reportPath}\n`);
  const docker = execute;
  const composeCmd = (args, timeoutMs = 180000) => docker(['compose', '-p', project, '-f', compose, ...args], timeoutMs);
  const checkedQuery = (args) => {
    const result = docker(args);
    if (result.error) throw result.error;
    if (result.status !== 0) fail(`Docker resource query failed (${args.slice(0, 2).join(' ')}); existing resources were not changed`);
    return (result.stdout || '').trim();
  };
  const info = docker(['version', '--format', '{{.Server.Os}}/{{.Server.Arch}}']);
  if (info.status !== 0 || !String(info.stdout).includes('linux/amd64')) {
    fail(`docker linux/amd64 required, got: ${(info.stdout || info.stderr || '').trim()}`);
  }

  for (const kind of ['container', 'network', 'volume']) {
    const args = [kind, 'ls', '-q', '--filter', `label=com.docker.compose.project=${project}`];
    if (kind === 'container') args.push('-a');
    if (checkedQuery(args)) fail(`compose project ${project} already has ${kind} resources; refusing to reuse it`);
  }

  const ids = checkedQuery(['network', 'ls', '-q']);
  for (const id of ids.split(/\s+/).filter(Boolean)) {
    const networks = JSON.parse(checkedQuery(['network', 'inspect', id]));
    if (!Array.isArray(networks) || networks.length === 0) fail('invalid Docker network inspection');
    for (const network of networks) {
      if (!network.IPAM || (network.IPAM.Config !== null && !Array.isArray(network.IPAM.Config))) fail('invalid Docker network IPAM inspection');
      for (const config of network.IPAM.Config || []) {
        if (config.Subnet && subnetsOverlap('172.30.253.0/24', config.Subnet)) {
          fail(`subnet 172.30.253.0/24 overlaps existing network ${network.Name} (${config.Subnet}); existing resources were not changed`);
        }
      }
    }
  }

  mkdirSync(outDir, { recursive: true });
  const bin = join(outDir, 'isolation-smoke.test');
  prepareTest(bin);

  const network = `${project}_isolation`;
  let failed = null;
  let ownsProject = false;
  let encoded;
  try {
    ownsProject = true;
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
    encoded = JSON.stringify({
      project,
      network,
      subnet: '172.30.253.0/24',
      phases: { startup: 'pass', bypass: 'pass', after_stop_a: 'pass' },
    }, null, 2);
    if (/(EXAMPLE_ONLY_|password|secret)/i.test(encoded + startup + bypass + after)) {
      fail('compose smoke output contained secrets');
    }
  } catch (err) {
    failed = err;
  } finally {
    if (ownsProject) {
      try {
        const down = composeCmd(['down', '--remove-orphans'], 180000);
        if (down.error) throw down.error;
        if (down.status !== 0) fail(`compose down failed for ${project}: ${down.stderr || down.stdout}`);
      } catch (err) {
        failed = failed ? new AggregateError([failed, err], `${failed.message}; cleanup failed: ${err.message}`) : err;
      }
    }
  }
  if (failed) throw failed;
  writeFileSync(reportPath, `${encoded}\n`);
  write(`${encoded}\n`);
  write('PASS: isolation compose smoke verified shared CA, source restriction, and stop-A fail-closed; cleanup succeeded.\n');
  return { project, outDir, reportPath };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  runIsolationCompose();
}
