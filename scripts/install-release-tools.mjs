// Install only official archives and binary digests in the delivery tool lock.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { chmod, mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url));
const lock = JSON.parse(await readFile(join(root, 'deploy/tools.lock.json'), 'utf8'));
assert.ok(['win32', 'linux'].includes(process.platform) && process.arch === 'x64', 'scanner_host_not_locked');
const platform = process.platform === 'win32' ? 'windows_amd64' : 'linux_amd64';
const hash = data => createHash('sha256').update(data).digest('hex');
for (const [name, specification] of [['syft', lock.sbom], ['grype', lock.vulnerability_scanner]]) {
  const build = specification.builds[platform], filename = name + (process.platform === 'win32' ? '.exe' : '');
  const directory = join(root, '.cache/tools', name, specification.version), target = join(directory, 'unpacked');
  await mkdir(target, { recursive: true });
  const existing = await readFile(join(target, filename)).catch(() => null);
  if (!existing || hash(existing) !== build.binary_sha256) {
    const archive = join(directory, build.archive);
    let bytes = await readFile(archive).catch(() => null);
    if (!bytes || hash(bytes) !== build.sha256) {
      const url = `https://github.com/anchore/${name}/releases/download/v${specification.version}/${build.archive}`;
      const response = await fetch(url, { signal: AbortSignal.timeout(120000) });
      assert.ok(response.ok, `${name}_download_failed`);
      const chunks = []; let size = 0;
      for await (const chunk of response.body) { size += chunk.length; assert.ok(size <= 128 << 20, 'tool_archive_too_large'); chunks.push(chunk); }
      bytes = Buffer.concat(chunks);
      assert.equal(hash(bytes), build.sha256, 'tool_archive_digest_mismatch');
      await writeFile(archive, bytes);
    }
    const result = spawnSync('tar', ['-xf', archive, '-C', target, filename], { encoding: 'utf8', windowsHide: true });
    assert.equal(result.status, 0, 'tool_extract_failed');
    assert.equal(hash(await readFile(join(target, filename))), build.binary_sha256, 'tool_binary_digest_mismatch');
    if (process.platform !== 'win32') await chmod(join(target, filename), 0o755);
  }
  console.log(`PASS: locked-${name}-${specification.version}-${platform}`);
}
