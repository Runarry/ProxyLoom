// Exact upstream notices omitted from their published dependency archives.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url));
const lock = JSON.parse(await readFile(join(root, 'deploy/notices.lock.json'), 'utf8'));
const directory = join(root, '.cache/release-notices');
await mkdir(directory, { recursive: true });
for (const item of lock.notices) {
  assert.equal(item.file, encodeURIComponent(item.name) + '@' + item.version + '.txt');
  assert.match(item.commit, /^[0-9a-f]{40}$/);
  const url = new URL(item.url);
  assert.equal(url.origin, 'https://raw.githubusercontent.com');
  assert.ok(url.pathname.includes('/' + item.commit + '/'));
  const path = join(directory, item.file);
  let bytes = await readFile(path).catch(() => null);
  if (!bytes) {
    const response = await fetch(url, { signal: AbortSignal.timeout(30000) });
    assert.ok(response.ok, 'notice_download_failed');
    const chunks = []; let size = 0;
    for await (const chunk of response.body) {
      size += chunk.length; assert.ok(size <= 1 << 20, 'notice_too_large'); chunks.push(chunk);
    }
    bytes = Buffer.concat(chunks);
  }
  assert.equal(createHash('sha256').update(bytes).digest('hex'), item.sha256, 'notice_digest_mismatch');
  await writeFile(path, bytes);
}
console.log('PASS: locked upstream notices staged');
