import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url));
const lock = JSON.parse(await readFile(join(root, 'deploy/sources.lock.json'), 'utf8'));
const directory = join(root, '.cache/release-sources'); await mkdir(directory, { recursive: true });
const hash = data => createHash('sha256').update(data).digest('hex');
for (const item of lock.sources) {
  assert.match(item.archive, /^[a-z-]+-[a-f0-9]{40}\.tar\.gz$/);
  assert.ok(item.url.startsWith('https://codeload.github.com/') && item.url.endsWith('/' + item.commit));
  const path = join(directory, item.archive); let bytes = await readFile(path).catch(() => null);
  if (!bytes) {
    const response = await fetch(item.url, { signal: AbortSignal.timeout(120000) }); assert.ok(response.ok);
    const chunks = []; let size = 0;
    for await (const chunk of response.body) { size += chunk.length; assert.ok(size <= 128 << 20); chunks.push(chunk); }
    bytes = Buffer.concat(chunks); assert.equal(hash(bytes), item.sha256); await writeFile(path, bytes, { flag: 'wx' });
  }
  assert.equal(hash(bytes), item.sha256); console.log('PASS: source-' + item.family);
}
