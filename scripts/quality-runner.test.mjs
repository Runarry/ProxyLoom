import assert from 'node:assert/strict';
import test from 'node:test';
import { randomBytes, randomUUID } from 'node:crypto';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { runQualitySteps } from './quality-runner.mjs';
import { collectQualityEvidence, stageEvidence } from './quality-evidence.mjs';

const sourceRoot = fileURLToPath(new URL('../', import.meta.url));
function temporary(t) {
  const root = mkdtempSync(join(tmpdir(), 'proxyloom-quality-test-'));
  t.after(() => {
    assert.equal(dirname(resolve(root)), resolve(tmpdir()));
    assert.ok(root.startsWith(join(tmpdir(), 'proxyloom-quality-test-')));
    rmSync(root, { recursive: true, force: true });
  });
  return root;
}
function write(root, path, value) {
  mkdirSync(dirname(join(root, path)), { recursive: true });
  writeFileSync(join(root, path), value);
}

test('an actually failing node test exits nonzero through the same runner used by CI', (t) => {
  const root = temporary(t);
  write(root, 'failure.test.mjs', "import test from 'node:test'; import assert from 'node:assert/strict'; test('intentional failure', () => assert.equal(1, 2));\n");
  write(root, 'entry.mjs', `import {runQualitySteps} from ${JSON.stringify(new URL('./quality-runner.mjs', import.meta.url).href)};
const report = runQualitySteps([{name:'failing-test',command:process.execPath,args:['--test','failure.test.mjs']},{name:'must-not-run',command:process.execPath,args:['-e',\"process.exit(0)\"]}], {root:process.cwd(),output:()=>{}});
console.log(JSON.stringify(report)); process.exitCode=report.result==='pass'?0:1;\n`);
  const result = spawnSync(process.execPath, ['entry.mjs'], { cwd: root, encoding: 'utf8', windowsHide: true });
  assert.equal(result.status, 1);
  const report = JSON.parse(result.stdout);
  assert.equal(report.steps[0].exit_code, 1);
  assert.equal(report.steps[1].result, 'not_run');
});

test('entry cannot retain a passing result if its final source inventory fails', (t) => {
  const root = temporary(t);
  for (const path of ['scripts/check-quality.mjs', 'scripts/quality-secrets.mjs']) {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    copyFileSync(join(sourceRoot, path), join(root, path));
  }
  write(root, 'deploy/tools.lock.json', JSON.stringify({ node: process.versions.node, pnpm: '10.33.2' }));
  write(root, 'scripts/quality-runner.mjs', `import {renameSync} from 'node:fs'; import {join,dirname,resolve} from 'node:path'; import {tmpdir} from 'node:os';
export function runQualitySteps(steps,{root}) {
 if(dirname(resolve(root))!==resolve(tmpdir()) || !root.includes('proxyloom-quality-test-')) throw new Error('temporary_root_required');
 renameSync(join(root,'.git'),join(root,'.git-held'));
 return {schema_version:1,result:'pass',steps:[{name:'synthetic-success',result:'pass'}]};
}\n`);
  assert.equal(spawnSync('git', ['init', '--quiet'], { cwd: root, windowsHide: true }).status, 0);
  const result = spawnSync(process.execPath, ['scripts/check-quality.mjs', '--profile', 'source'], { cwd: root, encoding: 'utf8', windowsHide: true });
  assert.equal(result.status, 1);
  assert.match(result.stdout, /FAIL: quality source-only/);
  assert.ok(!result.stdout.includes('PASS: quality'));
});

test('real API drift checker rejects a modified generated file in an isolated copy', (t) => {
  const root = temporary(t);
  for (const path of ['scripts/check-api.mjs', 'scripts/generate-api.mjs', 'api/openapi.yaml', 'proxyloom-web/src/api/schema.d.ts']) {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    copyFileSync(join(sourceRoot, path), join(root, path));
  }
  // Use the installed read-only dependency tree via an explicit environment path
  // in the copied generator; the repository generator is never modified.
  const generator = readFileSync(join(root, 'scripts/generate-api.mjs'), 'utf8');
  writeFileSync(join(root, 'scripts/generate-api.mjs'), generator.replace("resolve(root, 'proxyloom-web/node_modules/openapi-typescript/package.json')",
    `String.raw\`${join(sourceRoot, 'proxyloom-web/node_modules/openapi-typescript/package.json')}\``));
  const generated = join(root, 'proxyloom-web/src/api/schema.d.ts');
  writeFileSync(generated, `${readFileSync(generated, 'utf8')}\n// Intentional drift self-test.\n`);
  const before = readFileSync(generated, 'utf8');
  const result = spawnSync(process.execPath, ['scripts/check-api.mjs'], { cwd: root, encoding: 'utf8', windowsHide: true, timeout: 60000 });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /Generated API types drifted/);
  assert.equal(readFileSync(generated, 'utf8'), before, 'the check must not repair its input');
});

test('registered output secrets and workflow commands do not leak from subprocesses', (t) => {
  const root = temporary(t);
  const secret = randomBytes(32).toString('base64url');
  let output = '';
  const report = runQualitySteps([{ name: 'secret-output', command: process.execPath, args: ['-e', 'process.stdout.write(process.argv[1])', secret] }],
    { root, secrets: [secret], output: (text) => { output += text; } });
  assert.equal(report.result, 'fail');
  assert.equal(report.steps[0].error, 'secret-output-withheld');
  assert.ok(!output.includes(secret));
  assert.ok(!JSON.stringify(report).includes(secret));
  runQualitySteps([{ name: 'workflow-output', command: process.execPath, args: ['-e', 'console.log("::error::untrusted")'] }], { root, output: (text) => { output += text; } });
  assert.ok(!output.includes('::error::'));
});

test('evidence staging is all-or-nothing, bounded and limited to named files', (t) => {
  const root = temporary(t);
  const id = randomUUID();
  const report = `.cache/foundation/${id}/report.json`;
  const raw = `.cache/foundation/${id}/go-test.jsonl`;
  const secret = randomBytes(32).toString('base64url');
  write(root, report, '{"result":"fail"}\n');
  write(root, raw, secret);
  assert.throws(() => stageEvidence(root, [report, raw], { secrets: [secret] }), /secret_scan_rejected/);
  assert.equal(existsSync(join(root, '.cache/quality-upload')), false);
  const credential = `.cache/foundation/${id}/secrets/database_dsn`;
  write(root, credential, secret);
  assert.throws(() => stageEvidence(root, [credential]), /not_allowlisted/);
  assert.throws(() => stageEvidence(root, [report], { destination: '../outside' }), /destination_invalid/);
  write(root, raw, '{"Action":"fail"}\n');
  const manifest = stageEvidence(root, [report, raw], { secrets: [secret] });
  assert.equal(manifest.files.length, 2);
  assert.equal(existsSync(join(root, `.cache/quality-upload/foundation/${id}/secrets`)), false);
  assert.throws(() => stageEvidence(root, [report]), /EEXIST/);
});

test('collector loads registered runtime secrets before accepting even metadata-named evidence', (t) => {
  const root = temporary(t);
  const quality = randomUUID(), foundation = randomUUID();
  const secret = randomBytes(32).toString('hex');
  write(root, `.cache/quality/${quality}/report.json`, JSON.stringify({ run_id: quality, foundation_runs: [foundation] }));
  write(root, `.cache/quality/${quality}/source-manifest.json`, '{}');
  write(root, `.cache/foundation/${foundation}/secrets/db_runtime_password`, secret);
  write(root, `.cache/foundation/${foundation}/report.json`, JSON.stringify({ diagnostic: secret }));
  assert.throws(() => collectQualityEvidence(root), /secret_scan_rejected/);
  assert.equal(existsSync(join(root, '.cache/quality-upload')), false);
});
