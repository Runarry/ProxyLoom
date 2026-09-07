import { createHash } from 'node:crypto';
import { createReadStream, createWriteStream, existsSync, readFileSync, writeFileSync } from 'node:fs';
import { mkdir, rm } from 'node:fs/promises';
import { spawn, spawnSync } from 'node:child_process';
import { createGunzip } from 'node:zlib';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const cache = join(root, '.cache', 'cores');
const generated = join(root, 'fixtures', 'compiler', 'golden');
const corruptDir = join(root, '.cache', 'compile-exec');
const lockText = readFileSync(join(root, 'compat', 'cores.lock.yaml'), 'utf8');
const tools = JSON.parse(readFileSync(join(root, 'deploy', 'tools.lock.json'), 'utf8'));
const image = tools.images.runtime;
if (!image || !image.includes('@sha256:')) {
  throw new Error('runtime image must be pinned by digest');
}

function fail(message) {
  throw new Error(`Compile exec: ${message}`);
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
  const work = join(dir, 'work-compile-exec');
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
    maxBuffer: 2 * 1024 * 1024,
  });
  if (result.error) throw result.error;
  return result;
}

function toPosix(path) {
  return path.replaceAll('\\', '/');
}

function familySpec(family) {
  if (family === 'xray') {
    return {
      valid: 'xray-native-chain-a-b.json',
      invalid: 'xray.invalid.json',
      check: '/work/core run -test -c /work/config.json',
      run: '/work/core run -c /work/config.json',
      config: 'config.json',
    };
  }
  if (family === 'sing-box') {
    return {
      valid: 'singbox-native-chain-a-b.json',
      invalid: 'singbox.invalid.json',
      check: '/work/core check -c /work/config.json',
      run: '/work/core run -c /work/config.json',
      config: 'config.json',
    };
  }
  if (family === 'mihomo') {
    return {
      valid: 'mihomo-native-chain-a-b.yaml',
      invalid: 'mihomo.invalid.yaml',
      check: '/work/core -t -d /work -f /work/config.yaml',
      run: '/work/core -d /work -f /work/config.yaml',
      config: 'config.yaml',
    };
  }
  fail(`unknown family ${family}`);
}

function corrupt(source, family) {
  const text = readFileSync(source, 'utf8');
  let next = text;
  if (family === 'xray') next = text.replaceAll('"protocol":"trojan"', '"protocol":"not-a-protocol"');
  else if (family === 'sing-box') next = text.replaceAll('"type":"trojan"', '"type":"not-a-protocol"');
  else next = text.replaceAll('type: trojan', 'type: not-a-protocol');
  if (next === text) fail(`could not corrupt ${family} generated config`);
  return next;
}

function script(binaryName, copyConfig, command, start) {
  const startBlock = start
    ? `
${command} &
pid=$!
sleep 1
if [ ! -d /proc/$pid ]; then echo START_FAILED; wait $pid; exit 1; fi
kill -TERM $pid 2>/dev/null || true
sleep 1
kill -KILL $pid 2>/dev/null || true
wait $pid 2>/dev/null || true
if [ -d /proc/$pid ]; then echo LEFTOVER; exit 1; fi
echo START_STOPPED
`
    : `
${command}
status=$?
echo CHECK_EXIT:$status
exit $status
`;
  return `set -eu
cp /cores-bin/${binaryName} /work/core
chmod 0755 /work/core
${copyConfig}
${startBlock}
`;
}

const handwritten = spawnSync(process.execPath, [join(root, 'scripts', 'verify-core-exec.mjs')], {
  cwd: root,
  encoding: 'utf8',
  shell: false,
  stdio: 'inherit',
});
if (handwritten.status !== 0) fail('handwritten verify-core-exec.mjs regression failed');

const builds = parseBuilds(lockText).filter((build) => build.os === 'linux' && build.arch === 'amd64');
if (builds.length !== 3) fail(`expected three linux/amd64 builds, got ${builds.length}`);

await mkdir(corruptDir, { recursive: true });
const report = [];
for (const build of builds) {
  await extractBinary(build);
  const spec = familySpec(build.family);
  const validPath = join(generated, spec.valid);
  if (!existsSync(validPath)) fail(`missing generated golden ${spec.valid}`);
  writeFileSync(join(corruptDir, spec.invalid), corrupt(validPath, build.family));
  const coreDir = dirname(join(cache, build.family, build.git_tag, build.arch, build.binary_name));
  const common = [
    'run', '--rm', '--network', 'none', '--read-only',
    '--tmpfs', '/work:uid=10002,mode=0700,exec',
    '--user', '10002:10002',
    '-v', `${toPosix(coreDir)}:/cores-bin:ro`,
    '-v', `${toPosix(generated)}:/generated:ro`,
    '-v', `${toPosix(corruptDir)}:/corrupt:ro`,
    image,
    '/bin/sh', '-c',
  ];
  const validCopy = `cp /generated/${spec.valid} /work/${spec.config}`;
  const invalidCopy = `cp /corrupt/${spec.invalid} /work/${spec.config}`;
  const checkValid = docker([...common, script(build.binary_name, validCopy, spec.check, false)]);
  if (checkValid.status !== 0) {
    fail(`${build.family} generated valid check failed: ${(checkValid.stderr || checkValid.stdout || '').slice(0, 400)}`);
  }
  const checkInvalid = docker([...common, script(build.binary_name, invalidCopy, spec.check, false)]);
  if (checkInvalid.status === 0) {
    fail(`${build.family} generated invalid check exited 0`);
  }
  const start = docker([...common, script(build.binary_name, validCopy, spec.run, true)]);
  if (start.status !== 0 || !(start.stdout || '').includes('START_STOPPED')) {
    fail(`${build.family} generated start/stop failed: ${(start.stderr || start.stdout || '').slice(0, 400)}`);
  }
  report.push({
    family: build.family,
    id: build.id,
    arch: build.arch,
    source: spec.valid,
    valid_check: 'pass',
    invalid_check: 'fail_closed',
    start_cancel: 'reaped',
  });
}

const encoded = JSON.stringify({ image, network: 'none', handwritten_regression: 'pass', results: report }, null, 2);
if (/secret|password|EXAMPLE_ONLY_/i.test(encoded)) fail('report contained secrets');
process.stdout.write(`${encoded}\n`);
process.stdout.write('PASS: compiler-generated linux/amd64 configs were accepted, corrupted copies were rejected, and loopback start was reaped.\n');
