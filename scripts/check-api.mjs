import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, rm } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { generateAPI, generatedPath } from './generate-api.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const cache = resolve(root, '.cache');
await mkdir(cache, { recursive: true });
const temporary = await mkdtemp(resolve(cache, 'api-drift-'));
try {
  const regenerated = await generateAPI(resolve(temporary, 'schema.d.ts'));
  assert.ok(await readFile(generatedPath, 'utf8') === regenerated,
    'Generated API types drifted. Run pnpm generate:api in proxyloom-web.');
  console.log('PASS: API types match local OpenAPI regeneration.');
} finally {
  // mkdtemp created this exact child in the project cache; never remove inputs.
  assert.equal(dirnameSafe(temporary), cache);
  await rm(temporary, { recursive: true, force: true });
}
function dirnameSafe(path) { return resolve(path, '..'); }
