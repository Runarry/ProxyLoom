import assert from 'node:assert/strict';
import test from 'node:test';
import { spawnSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { checkContracts, describeContractChange } from './quality-contracts.mjs';

const sourceRoot = fileURLToPath(new URL('../', import.meta.url));
const schemaPath = 'schemas/example.schema.json';
const original = { type: 'object', required: ['name'], properties: { name: { type: 'string', maxLength: 64 } }, additionalProperties: false };
function write(root, path, value) {
  mkdirSync(dirname(join(root, path)), { recursive: true });
  writeFileSync(join(root, path), typeof value === 'string' ? value : `${JSON.stringify(value, null, 2)}\n`);
}
function fixture(t, { api = false, migration = false } = {}) {
  const root = mkdtempSync(join(tmpdir(), 'proxyloom-contract-test-'));
  t.after(() => {
    assert.equal(dirname(resolve(root)), resolve(tmpdir()));
    assert.ok(root.startsWith(join(tmpdir(), 'proxyloom-contract-test-')));
    // node_modules is a directory junction/symlink, not a copied dependency tree.
    rmSync(root, { recursive: true, force: true });
  });
  write(root, '.gitignore', 'node_modules/\n');
  write(root, schemaPath, original);
  write(root, 'docs/change.md', 'Before contract.\n');
  write(root, 'fixtures/ir/example.json', { name: 'before' });
  if (migration) write(root, 'migrations/000001_initial.sql', 'CREATE TABLE example (id integer);\n');
  if (api) {
    mkdirSync(join(root, 'proxyloom-web'), { recursive: true });
    symlinkSync(join(sourceRoot, 'proxyloom-web/node_modules'), join(root, 'proxyloom-web/node_modules'), process.platform === 'win32' ? 'junction' : 'dir');
    write(root, 'api/openapi.yaml', { openapi: '3.1.0', info: { title: 'Example', version: '1.0.0' }, paths: {}, components: { schemas: { Example: original } } });
    write(root, 'proxyloom-web/src/api/schema.d.ts', 'export type Example = string;\n');
  }
  for (const args of [['init', '--quiet'], ['add', '.'], ['-c', 'user.name=Quality fixture', '-c', 'user.email=quality@example.invalid', 'commit', '--quiet', '-m', 'Synthetic baseline']]) {
    assert.equal(spawnSync('git', args, { cwd: root, encoding: 'utf8', windowsHide: true }).status, 0);
  }
  return root;
}
function mutate(root, path = schemaPath) {
  const before = readFileSync(join(root, path), 'utf8');
  const document = JSON.parse(before);
  if (path === schemaPath) document.properties.name.maxLength = 12;
  else delete document.components.schemas.Example.properties.name;
  write(root, path, document);
  return describeContractChange(root, path, before, readFileSync(join(root, path), 'utf8'));
}
function declare(root, change, { companions = true, generated = [] } = {}) {
  if (companions) {
    write(root, 'docs/change.md', 'After contract: narrowed name accepts at most twelve characters.\n');
    write(root, 'fixtures/ir/example.json', { name: 'after' });
  }
  write(root, 'compat/contract-changes/T-008.json', {
    schema_version: 1, task: 'T-008', summary: 'Synthetic bounded compatibility gate self-test declaration.',
    documents: ['docs/change.md'], fixtures: ['fixtures/ir/example.json'], generated, contracts: [change],
  });
}

test('unknown breaking Schema mutation fails and one exact reviewed change passes', (t) => {
  const root = fixture(t);
  const change = mutate(root);
  assert.equal(checkContracts(root).result, 'fail');
  declare(root, change);
  assert.equal(checkContracts(root).result, 'pass');
  const document = JSON.parse(readFileSync(join(root, schemaPath), 'utf8'));
  document.properties.name.maxLength = 10;
  write(root, schemaPath, document);
  assert.equal(checkContracts(root).result, 'fail', 'a declared pointer cannot authorize a different value');
});

test('contract declarations require documentation and fixtures changed in the same diff', (t) => {
  const root = fixture(t);
  declare(root, mutate(root), { companions: false });
  const result = checkContracts(root);
  assert.equal(result.result, 'fail');
  assert.ok(result.failures.some(({ rule }) => rule === 'documents-must-change-with-contract'));
  assert.ok(result.failures.some(({ rule }) => rule === 'fixtures-must-change-with-contract'));
});

test('OpenAPI removal is blocked; declared API changes also require generated TypeScript changes', (t) => {
  const root = fixture(t, { api: true });
  const change = mutate(root, 'api/openapi.yaml');
  assert.equal(checkContracts(root).result, 'fail');
  declare(root, change, { generated: ['proxyloom-web/src/api/schema.d.ts'] });
  assert.ok(checkContracts(root).failures.some(({ rule }) => rule === 'api-generated-types-must-change'));
  write(root, 'proxyloom-web/src/api/schema.d.ts', 'export type Example = never;\n');
  assert.equal(checkContracts(root).result, 'pass');
});

test('source digests catch precision-losing JSON numeric changes and immutable migration edits', (t) => {
  const root = fixture(t, { migration: true });
  const before = readFileSync(join(root, schemaPath), 'utf8');
  mutate(root);
  write(root, schemaPath, readFileSync(join(root, schemaPath), 'utf8').replace('12', '9223372036854775807'));
  declare(root, describeContractChange(root, schemaPath, before, readFileSync(join(root, schemaPath), 'utf8')));
  assert.equal(checkContracts(root).result, 'pass');
  assert.equal(JSON.parse('9223372036854775807'), JSON.parse('9223372036854775806'));
  write(root, schemaPath, readFileSync(join(root, schemaPath), 'utf8').replace('9223372036854775807', '9223372036854775806'));
  assert.equal(checkContracts(root).result, 'fail');
  write(root, 'migrations/000001_initial.sql', 'DROP TABLE example;\n');
  assert.ok(checkContracts(root).failures.some(({ rule }) => rule === 'existing-migration-is-immutable'));
});

test('missing PR base fails closed, and checks never rewrite task status or capabilities', (t) => {
  const root = fixture(t);
  write(root, 'docs/PLAN.tasks.json', { tasks: [{ id: 'T-008', status: 'in_progress' }] });
  write(root, 'compat/p0-combinations.yaml', 'status: unsupported\n');
  const before = ['docs/PLAN.tasks.json', 'compat/p0-combinations.yaml'].map((path) => readFileSync(join(root, path), 'utf8'));
  assert.throws(() => checkContracts(root, { base: 'refs/heads/absent' }));
  assert.equal(checkContracts(root).result, 'pass');
  assert.deepEqual(['docs/PLAN.tasks.json', 'compat/p0-combinations.yaml'].map((path) => readFileSync(join(root, path), 'utf8')), before);
});
