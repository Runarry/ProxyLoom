import { createHash } from 'node:crypto';
import { chmod, mkdir, rm, writeFile } from 'node:fs/promises';
import { createReadStream, createWriteStream } from 'node:fs';
import { Readable } from 'node:stream';
import { spawn } from 'node:child_process';
import { createGunzip } from 'node:zlib';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';
import { join } from 'node:path';

const root = fileURLToPath(new URL('../', import.meta.url));
const cache = join(root, '.cache', 'cores');
const namespace = '8c3f0e2a-4b91-41d6-a2c1-9f0e6b7d4a10';

const candidates = [
  {
    family: 'xray',
    version: '26.3.27',
    git_tag: 'v26.3.27',
    git_commit: 'd2758a023cd7f4174a5a5fa4ff66e487d4342ba0',
    os: 'linux',
    arch: 'amd64',
    asset_name: 'Xray-linux-64.zip',
    asset_sha256: '23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae',
    binary_name: 'xray',
    license: 'MPL-2.0',
    source: 'https://github.com/XTLS/Xray-core',
  },
  {
    family: 'xray',
    version: '26.3.27',
    git_tag: 'v26.3.27',
    git_commit: 'd2758a023cd7f4174a5a5fa4ff66e487d4342ba0',
    os: 'linux',
    arch: 'arm64',
    asset_name: 'Xray-linux-arm64-v8a.zip',
    asset_sha256: '4d30283ae614e3057f730f67cd088a42be6fdf91f8639d82cb69e48cde80413c',
    binary_name: 'xray',
    license: 'MPL-2.0',
    source: 'https://github.com/XTLS/Xray-core',
  },
  {
    family: 'sing-box',
    version: '1.14.0',
    git_tag: 'v1.14.0',
    git_commit: '0b8995879f29a9b98ee027bc17b75e101445b238',
    os: 'linux',
    arch: 'amd64',
    asset_name: 'sing-box-1.14.0-linux-amd64.tar.gz',
    asset_sha256: '2375de6999f4f56ab46b4fc5ddf26a6aba1d3e61a0f4e7ddec2f4690457d5f63',
    binary_name: 'sing-box',
    license: 'GPL-3.0-or-later',
    source: 'https://github.com/SagerNet/sing-box',
  },
  {
    family: 'sing-box',
    version: '1.14.0',
    git_tag: 'v1.14.0',
    git_commit: '0b8995879f29a9b98ee027bc17b75e101445b238',
    os: 'linux',
    arch: 'arm64',
    asset_name: 'sing-box-1.14.0-linux-arm64.tar.gz',
    asset_sha256: '04d9b40bc98dc55b6f509ce3292145c65478f65866bea64826ebb2f382385088',
    binary_name: 'sing-box',
    license: 'GPL-3.0-or-later',
    source: 'https://github.com/SagerNet/sing-box',
  },
  {
    family: 'mihomo',
    version: '1.19.30',
    git_tag: 'v1.19.30',
    git_commit: 'ac017cdd246ce8bd547653d927e7bf77d7ee73d5',
    os: 'linux',
    arch: 'amd64',
    asset_name: 'mihomo-linux-amd64-v1.19.30.gz',
    asset_sha256: 'cf06ce2c7d1421bdbda14ee4a5b6046672dc35ebf8eecd8e77504ec3c0ed9a84',
    binary_name: 'mihomo',
    license: 'GPL-3.0',
    source: 'https://github.com/MetaCubeX/mihomo',
  },
  {
    family: 'mihomo',
    version: '1.19.30',
    git_tag: 'v1.19.30',
    git_commit: 'ac017cdd246ce8bd547653d927e7bf77d7ee73d5',
    os: 'linux',
    arch: 'arm64',
    asset_name: 'mihomo-linux-arm64-v1.19.30.gz',
    asset_sha256: '58896873736d28628f66de3677c8654fa0f180662523148e136cff4f6e890069',
    binary_name: 'mihomo',
    license: 'GPL-3.0',
    source: 'https://github.com/MetaCubeX/mihomo',
  },
];

function sha256File(path) {
  return new Promise((resolve, reject) => {
    const hash = createHash('sha256');
    createReadStream(path)
      .on('error', reject)
      .on('data', (chunk) => hash.update(chunk))
      .on('end', () => resolve(hash.digest('hex')));
  });
}

function uuidv5(name) {
  const ns = Buffer.from(namespace.replaceAll('-', ''), 'hex');
  const hash = createHash('sha1').update(ns).update(name).digest();
  hash[6] = (hash[6] & 0x0f) | 0x50;
  hash[8] = (hash[8] & 0x3f) | 0x80;
  const hex = hash.subarray(0, 16).toString('hex');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20, 32)}`;
}

function assetURL(candidate) {
  const repo = candidate.source.replace('https://github.com/', '');
  return `https://github.com/${repo}/releases/download/${candidate.git_tag}/${candidate.asset_name}`;
}

async function download(url, dest) {
  const response = await fetch(url, {
    headers: { 'User-Agent': 'ProxyLoom-pin-cores/0.1.0-m0-skeleton', Accept: 'application/octet-stream' },
    redirect: 'follow',
  });
  if (!response.ok) throw new Error(`download failed ${response.status} for ${candidateLabel(url)}`);
  await pipeline(Readable.fromWeb(response.body), createWriteStream(dest));
}

function candidateLabel(url) {
  return url.replace(/https:\/\/github.com\/[^/]+\/[^/]+/, 'github-release');
}

function runTar(args, cwd) {
  return new Promise((resolve, reject) => {
    const child = spawn('tar', args, { cwd, shell: false, stdio: 'ignore' });
    child.on('error', reject);
    child.on('exit', (code) => (code === 0 ? resolve() : reject(new Error(`tar failed (${code})`))));
  });
}

async function extractBinary(candidate, assetPath, work) {
  const extractDir = join(work, 'extract');
  await mkdir(extractDir, { recursive: true });
  if (candidate.asset_name.endsWith('.zip')) {
    await runTar(['-xf', assetPath], extractDir);
  } else if (candidate.asset_name.endsWith('.tar.gz')) {
    await runTar(['-xzf', assetPath], extractDir);
  } else if (candidate.asset_name.endsWith('.gz')) {
    const dest = join(extractDir, candidate.binary_name);
    await pipeline(createReadStream(assetPath), createGunzip(), createWriteStream(dest));
  } else {
    throw new Error(`unsupported asset ${candidate.asset_name}`);
  }
  const found = await findFile(extractDir, candidate.binary_name);
  if (!found) throw new Error(`binary ${candidate.binary_name} missing from ${candidate.asset_name}`);
  return found;
}

async function findFile(directory, name) {
  const { readdir } = await import('node:fs/promises');
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      const nested = await findFile(path, name);
      if (nested) return nested;
    } else if (entry.name === name || entry.name === `${name}.exe`) {
      return path;
    }
  }
  return null;
}

async function pinOne(candidate) {
  const url = assetURL(candidate);
  if (url.includes('latest')) throw new Error('refusing floating latest URL');
  const dir = join(cache, candidate.family, candidate.git_tag, candidate.arch);
  await mkdir(dir, { recursive: true });
  const assetPath = join(dir, candidate.asset_name);
  const work = join(dir, 'work');
  await rm(work, { recursive: true, force: true });
  await mkdir(work, { recursive: true });
  process.stderr.write(`download ${candidate.family} ${candidate.arch} ${candidate.asset_name}\n`);
  await download(url, assetPath);
  const assetHash = await sha256File(assetPath);
  if (assetHash !== candidate.asset_sha256) {
    throw new Error(`asset digest mismatch for ${candidate.family} ${candidate.arch}`);
  }
  const binaryPath = await extractBinary(candidate, assetPath, work);
  const binaryHash = await sha256File(binaryPath);
  const destBinary = join(dir, candidate.binary_name);
  await pipeline(createReadStream(binaryPath), createWriteStream(destBinary));
  try {
    await chmod(destBinary, 0o755);
  } catch {
    // Windows cannot set POSIX execute bits; Docker Linux copies chmod later.
  }
  await rm(work, { recursive: true, force: true });
  const id = uuidv5(`${candidate.family}|${candidate.git_tag}|${candidate.os}|${candidate.arch}|${binaryHash}`);
  return {
    ...candidate,
    id,
    asset_url: url,
    binary_sha256: binaryHash,
  };
}

const results = [];
for (const candidate of candidates) {
  results.push(await pinOne(candidate));
}
await mkdir(cache, { recursive: true });
await writeFile(join(cache, 'pin-report.json'), `${JSON.stringify(results, null, 2)}\n`);
process.stdout.write(`${JSON.stringify(results, null, 2)}\n`);
