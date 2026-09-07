import { createHash } from 'node:crypto';
import { createReadStream, createWriteStream, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { mkdir, rm } from 'node:fs/promises';
import { spawn, spawnSync } from 'node:child_process';
import { createGunzip } from 'node:zlib';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const cache = join(root, '.cache', 'cores');
const outDir = join(root, '.cache', 't028');
const lockText = readFileSync(join(root, 'compat', 'cores.lock.yaml'), 'utf8');
const tools = JSON.parse(readFileSync(join(root, 'deploy', 'tools.lock.json'), 'utf8'));
const image = tools.images.go_builder;
if (!image || !image.includes('@sha256:')) {
  throw new Error('go builder image must be pinned by digest');
}

function fail(message) {
  throw new Error(`Live chain: ${message}`);
}

function parseBuilds(text) {
  const blocks = text.split(/\n  - id: /).slice(1);
  return blocks.map((block) => {
    const field = (name) => block.match(new RegExp(`(?:^|\\n)\\s*${name}:\\s*(\\S+)`))?.[1];
    return {
      id: block.match(/^[0-9a-f-]+/)[0],
      family: field('family'),
      git_tag: field('git_tag'),
      os: field('os'),
      arch: field('arch'),
      asset_name: field('asset_name'),
      binary_name: field('binary_name'),
      binary_sha256: field('binary_sha256'),
    };
  });
}

function sha256File(path) {
  return new Promise((resolve, reject) => {
    const hash = createHash('sha256');
    createReadStream(path)
      .on('error', reject)
      .on('data', (chunk) => hash.update(chunk))
      .on('end', () => resolve(hash.digest('hex')));
  });
}

async function findFile(directory, name) {
  const { readdir } = await import('node:fs/promises');
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      const nested = await findFile(path, name);
      if (nested) return nested;
    } else if (entry.name === name) {
      return path;
    }
  }
  return null;
}

function runTar(args, cwd) {
  return new Promise((resolve, reject) => {
    const child = spawn('tar', args, { cwd, shell: false, stdio: 'ignore' });
    child.on('error', reject);
    child.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`tar failed (${code})`))));
  });
}

async function extractBinary(build) {
  const dir = join(cache, build.family, build.git_tag, build.arch);
  const dest = join(dir, build.binary_name);
  if (existsSync(dest) && (await sha256File(dest)) === build.binary_sha256) {
    return dest;
  }
  const assetPath = join(dir, build.asset_name);
  if (!existsSync(assetPath)) {
    fail(`missing ${build.family} ${build.arch} asset; run node scripts/pin-cores.mjs`);
  }
  const work = join(dir, 'work-live-chain');
  await rm(work, { recursive: true, force: true });
  await mkdir(work, { recursive: true });
  if (build.asset_name.endsWith('.zip')) {
    await runTar(['-xf', assetPath], work);
  } else if (build.asset_name.endsWith('.tar.gz')) {
    await runTar(['-xzf', assetPath], work);
  } else if (build.asset_name.endsWith('.gz')) {
    await pipeline(createReadStream(assetPath), createGunzip(), createWriteStream(join(work, build.binary_name)));
  } else {
    fail(`unsupported asset ${build.asset_name}`);
  }
  const found = await findFile(work, build.binary_name);
  if (!found) fail(`binary missing from ${build.asset_name}`);
  const hash = await sha256File(found);
  if (hash !== build.binary_sha256) fail(`extracted digest mismatch for ${build.family} ${build.arch}`);
  await pipeline(createReadStream(found), createWriteStream(dest));
  await rm(work, { recursive: true, force: true });
  return dest;
}

function docker(args, timeoutMs = 120000) {
  const result = spawnSync('docker', args, {
    cwd: root,
    encoding: 'utf8',
    shell: false,
    timeout: timeoutMs,
    maxBuffer: 8 * 1024 * 1024,
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

const info = docker(['version', '--format', '{{.Server.Os}}/{{.Server.Arch}}']);
if (info.status !== 0 || !String(info.stdout).includes('linux/amd64')) {
  fail(`docker linux/amd64 required, got: ${(info.stdout || info.stderr || '').trim()}`);
}

const builds = parseBuilds(lockText).filter((build) => build.os === 'linux' && build.arch === 'amd64');
if (builds.length !== 3) fail(`expected three linux/amd64 builds, got ${builds.length}`);
for (const build of builds) {
  await extractBinary(build);
}

const bin = join(outDir, 'chainverify.test');
compileLinuxTest('./internal/chainverify', bin);
await mkdir(outDir, { recursive: true });

const script = `set -eu
cp /work/chainverify.test /tmp/chainverify.test
chmod 0755 /tmp/chainverify.test
/tmp/chainverify.test -test.v -test.count=1 -test.timeout 15m
`;
const result = docker([
  'run', '--rm', '--network', 'none',
  '--tmpfs', '/tmp:exec,mode=1777',
  '-v', `${toPosix(cache)}:/cores:ro`,
  '-v', `${toPosix(bin)}:/work/chainverify.test:ro`,
  '-v', `${toPosix(outDir)}:/out`,
  '-e', 'PROXYLOOM_LIVE_CHAIN=1',
  '-e', 'PROXYLOOM_LIVE_CHAIN_INSTALL_CA=1',
  '-e', 'PROXYLOOM_CORES_ROOT=/cores',
  '-e', 'PROXYLOOM_LIVE_REPORT=/out/live-chain-report.json',
  '-e', 'TMPDIR=/tmp',
  image,
  '/bin/sh', '-c', script,
], 960000);

writeFileSync(join(outDir, 'live-chain-stdout.txt'), `${result.stdout || ''}\n${result.stderr || ''}`);
if (result.status !== 0) {
  fail(`live-chain tests failed (${result.status}): ${(result.stderr || result.stdout || '').slice(-2000)}`);
}
if (!existsSync(join(outDir, 'live-chain-report.json'))) {
  fail('live-chain report was not written');
}
const report = readFileSync(join(outDir, 'live-chain-report.json'), 'utf8');
if (/(EXAMPLE_ONLY_|password|secret)/i.test(report)) fail('live-chain report contained secrets');
process.stdout.write(result.stdout || '');
process.stdout.write('PASS: three-core live chain matrix ran in an isolated linux/amd64 container with system test-CA trust.\n');
