// Install only the exact archive and executable recorded in tools.lock.json.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { chmodSync, existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const root = fileURLToPath(new URL('../', import.meta.url));
const lock = JSON.parse(readFileSync(join(root, 'deploy/tools.lock.json'), 'utf8')).codegen.sqlc;
const platform = `${process.platform === 'win32' ? 'windows' : process.platform}_${process.arch === 'x64' ? 'amd64' : process.arch}`;
const build = lock.builds[platform];
assert.ok(build, 'sqlc_platform_not_locked');
const digest = (data) => createHash('sha256').update(data).digest('hex');
const target = join(root, '.cache/bin');
const binary = join(target, build.executable);
mkdirSync(target, { recursive: true });
if (!existsSync(binary) || digest(readFileSync(binary)) !== build.binary_sha256) {
  const directory = join(root, '.cache/tools');
  mkdirSync(directory, { recursive: true });
  const archive = join(directory, build.archive);
  if (!existsSync(archive) || digest(readFileSync(archive)) !== build.archive_sha256) {
    assert.ok(build.url.startsWith(`https://github.com/sqlc-dev/sqlc/releases/download/v${lock.version}/`), 'sqlc_download_source_invalid');
    const response = await fetch(build.url, { signal: AbortSignal.timeout(120000) });
    assert.ok(response.ok, 'sqlc_download_failed');
    const chunks = []; let size = 0;
    for await (const chunk of response.body) {
      size += chunk.length;
      assert.ok(size <= 128 * 1024 * 1024, 'sqlc_archive_too_large');
      chunks.push(chunk);
    }
    const bytes = Buffer.concat(chunks);
    assert.equal(digest(bytes), build.archive_sha256, 'sqlc_archive_digest_mismatch');
    writeFileSync(archive, bytes);
  }
  // Extract one fixed archive member, never all archive paths.
  const extracted = spawnSync('tar', ['-xf', archive, '-C', target, build.executable], { encoding: 'utf8', windowsHide: true });
  assert.ok(!extracted.error && extracted.status === 0, 'sqlc_extract_failed');
  assert.equal(digest(readFileSync(binary)), build.binary_sha256, 'sqlc_binary_digest_mismatch');
  if (process.platform !== 'win32') chmodSync(binary, 0o755);
}
const version = spawnSync(binary, ['version'], { encoding: 'utf8', windowsHide: true });
assert.ok(!version.error && version.status === 0 && version.stdout.trim() === `v${lock.version}`, 'sqlc_version_mismatch');
console.log(`PASS: locked sqlc ${lock.version} (${platform}).`);
