// Stage only the already locked release binaries for image build contexts.
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { createReadStream, createWriteStream } from 'node:fs';
import { chmod, copyFile, mkdir, readFile, readdir, stat, writeFile } from 'node:fs/promises';
import { createGunzip } from 'node:zlib';
import { pipeline } from 'node:stream/promises';
import { spawn } from 'node:child_process';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const text = await readFile(join(root, 'compat/cores.lock.yaml'), 'utf8');
const builds = text.split(/\r?\n  - id: /).slice(1).map(block => {
  const value = name => block.match(new RegExp(`(?:^|\\n)\\s*${name}:\\s*([^\\r\\n]+)`))?.[1].trim();
  return { id: block.match(/^[0-9a-f-]{36}/)?.[0], family: value('family'), arch: value('arch'), tag: value('git_tag'), binary: value('binary_name'), binary_sha256: value('binary_sha256'), asset: value('asset_name'), asset_url: value('asset_url'), asset_sha256: value('asset_sha256') };
});
assert.equal(builds.length, 6);
const directory = join(root, '.cache/release', randomUUID());
const context = join(directory, 'cores');
await mkdir(context, { recursive: true });
async function digest(path) { const hash = createHash('sha256'); for await (const chunk of createReadStream(path)) hash.update(chunk); return hash.digest('hex'); }
async function findBinary(path, name) {
  for (const entry of await readdir(path, { withFileTypes: true })) {
    if (entry.isFile() && entry.name === name) return join(path, entry.name);
    if (entry.isDirectory()) { const found = await findBinary(join(path, entry.name), name); if (found) return found; }
  }
}
for (const build of builds) {
  assert.ok(['xray', 'sing-box', 'mihomo'].includes(build.family));
  assert.ok(['amd64', 'arm64'].includes(build.arch));
  for (const name of [build.tag, build.binary, build.asset]) assert.match(name, /^[A-Za-z0-9._-]+$/);
  for (const hash of [build.binary_sha256, build.asset_sha256]) assert.match(hash, /^[0-9a-f]{64}$/);
  const cache = join(root, '.cache/cores', build.family, build.tag, build.arch);
  let binary = join(cache, build.binary);
  if (!(await stat(binary).catch(() => null))?.isFile()) {
    const asset = join(cache, build.asset);
    if (!(await stat(asset).catch(() => null))?.isFile()) {
      const repositories = { xray: 'XTLS/Xray-core', 'sing-box': 'SagerNet/sing-box', mihomo: 'MetaCubeX/mihomo' };
      assert.equal(build.asset_url, `https://github.com/${repositories[build.family]}/releases/download/${build.tag}/${build.asset}`);
      await mkdir(cache, { recursive: true });
      const response = await fetch(build.asset_url, { signal: AbortSignal.timeout(120000) });
      assert.ok(response.ok, 'locked_core_download_failed');
      const chunks = []; let size = 0;
      for await (const chunk of response.body) { size += chunk.length; assert.ok(size <= 256 << 20, 'core_archive_too_large'); chunks.push(chunk); }
      const bytes = Buffer.concat(chunks);
      assert.equal(createHash('sha256').update(bytes).digest('hex'), build.asset_sha256, 'locked_core_archive_digest_mismatch');
      await writeFile(asset, bytes, { flag: 'wx' });
    }
    assert.equal(await digest(asset), build.asset_sha256, 'locked_core_archive_digest_mismatch');
    const unpack = join(directory, 'unpack', build.id); await mkdir(unpack, { recursive: true });
    if (build.asset.endsWith('.gz') && !build.asset.endsWith('.tar.gz')) await pipeline(createReadStream(asset), createGunzip(), createWriteStream(join(unpack, build.binary), { flags: 'wx' }));
    else await new Promise((resolve, reject) => { const child = spawn('tar', ['-xf', asset, '-C', unpack], { shell: false, windowsHide: true, stdio: 'ignore' }); child.on('error', reject); child.on('exit', code => code === 0 ? resolve() : reject(new Error('locked_core_extract_failed'))); });
    binary = await findBinary(unpack, build.binary); assert.ok(binary, 'locked_core_binary_missing');
  }
  assert.equal(await digest(binary), build.binary_sha256, 'locked_core_binary_digest_mismatch');
  if (binary !== join(cache, build.binary)) { await copyFile(binary, join(cache, build.binary)); await chmod(join(cache, build.binary), 0o555); }
  const destination = join(context, build.arch, build.family, build.tag, build.arch, build.binary);
  await mkdir(join(destination, '..'), { recursive: true });
  await copyFile(binary, destination); await chmod(destination, 0o555);
}
await writeFile(join(directory, 'core-manifest.json'), JSON.stringify({ schema_version: 1, builds, verification: 'hashes_only', arm64_runtime: 'unverified' }, null, 2) + '\n');
console.log(JSON.stringify({ directory, context, builds: builds.length }));
