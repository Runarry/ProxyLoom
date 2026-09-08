import assert from 'node:assert/strict';
import { mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
export const generatedPath = resolve(root, 'proxyloom-web/src/api/schema.d.ts');

export async function generateAPI(outputPath = generatedPath) {
  const packagePath = await realpath(resolve(root, 'proxyloom-web/node_modules/openapi-typescript/package.json'));
  const packageJSON = JSON.parse(await readFile(packagePath, 'utf8'));
  assert.equal(packageJSON.version, '7.9.1', 'Install the frozen frontend lock before generation');
  const { default: openapiTS, astToString } = await import(pathToFileURL(resolve(dirname(packagePath), 'dist/index.mjs')));
  const { createConfig, makeDocumentFromString } = createRequire(packagePath)('@redocly/openapi-core');
  const source = await readFile(resolve(root, 'api/openapi.yaml'), 'utf8');
  // No user-selected file, remote ref, or Redocly project config is resolved.
  // Spec refs are restricted before handing the document to the generator.
  const document = makeDocumentFromString(source, resolve(root, 'api/openapi.yaml')).parsed;
  function localReferences(value) {
    if (!value || typeof value !== 'object') return;
    for (const [key, child] of Object.entries(value)) {
      if (['$ref', '$dynamicRef'].includes(key)) assert.ok(typeof child === 'string' && child.startsWith('#/'), 'OpenAPI refs must be local document fragments');
      if (key === '$id') throw new Error('OpenAPI schemas must not change the local resolution base');
      localReferences(child);
    }
  }
  localReferences(document);
  const redocly = await createConfig({ rules: { 'operation-operationId-unique': { severity: 'error' } } }, { extends: ['minimal'] });
  const ast = await openapiTS(document, {
    cwd: pathToFileURL(root),
    alphabetize: true,
    additionalProperties: false,
    emptyObjectsUnknown: false,
    redocly,
  });
  const text = '// Generated from api/openapi.yaml by openapi-typescript 7.9.1. Do not edit.\n' + astToString(ast);
  assert.ok(!/\|\s*unknown\b|\bunknown\s*\|/.test(text), 'An OpenAPI union lost its concrete TypeScript type');
  await mkdir(dirname(outputPath), { recursive: true });
  await writeFile(outputPath, text, 'utf8');
  return text;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  await generateAPI();
  console.log('Generated frontend API types from the local OpenAPI contract.');
}
