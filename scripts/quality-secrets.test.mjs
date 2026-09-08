import assert from 'node:assert/strict';
import test from 'node:test';
import { randomBytes } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertSecretFree, scanRepository, scanText, sha256, validateExceptions } from './quality-secrets.mjs';

const sourceRoot = fileURLToPath(new URL('../', import.meta.url));
function temporary(t) {
  const directory = mkdtempSync(join(tmpdir(), 'proxyloom-secret-test-'));
  t.after(() => {
    assert.equal(dirname(resolve(directory)), resolve(tmpdir()));
    assert.ok(directory.startsWith(join(tmpdir(), 'proxyloom-secret-test-')));
    rmSync(directory, { recursive: true, force: true });
  });
  return directory;
}
function git(root, ...args) {
  const result = spawnSync('git', args, { cwd: root, encoding: 'utf8', windowsHide: true });
  assert.equal(result.status, 0, 'temporary git command failed');
}
const token = () => randomBytes(24).toString('base64url');
const subscription = () => ['sub', 'token', token()].join('_');
const credentialURI = () => ['postgres', '://reader:', token(), '@example.invalid/db'].join('');

test('detects private keys, subscriptions, credential URIs and registered values without echoing them', () => {
  const values = [
    ['-----BEGIN ', 'OPENSSH ', 'PRIVATE KEY-----'].join(''),
    subscription(), credentialURI(),
    ['sub', 'public'].join('_') + '.' + randomBytes(32).toString('base64url'),
    ['/s/', token(), '/custom-target'].join(''),
  ];
  for (const value of values) {
    const findings = scanText(value, { path: 'candidate.txt' });
    assert.ok(findings.length > 0);
    assert.ok(!JSON.stringify(findings).includes(value));
  }
  const generated = token();
  assert.equal(scanText(generated).length, 0, 'random values require exact registration');
  const findings = scanText(generated, { secrets: [generated], path: 'report.json' });
  assert.equal(findings[0].rule, 'registered-secret');
  assert.equal(findings[0].line_sha256, undefined);
  assert.throws(() => assertSecretFree(generated, { secrets: [generated] }), /secret_scan_rejected/);
  assert.throws(() => assertSecretFree(generated, { secrets: [generated] }), (error) => !error.message.includes(generated));
});

test('wide-character and binary files are inspected instead of skipped', () => {
  const value = credentialURI();
  const wide = Buffer.from(value, 'utf16le');
  for (const buffer of [wide, Buffer.from(wide).swap16(), Buffer.concat([Buffer.from([0, 255]), Buffer.from(value)])]) {
    assert.ok(scanText(buffer).some(({ rule }) => rule === 'credential-uri'));
  }
});

test('fixture exceptions are exact file, detector and whole line, never directories or registered secrets', () => {
  const value = credentialURI();
  const path = 'fixtures/quality/sample.txt';
  const exception = { path, rule: 'credential-uri', line_sha256: sha256(value), reason: 'Disposable synthetic unit-test credential, never a service credential.' };
  const exceptions = validateExceptions({ schema_version: 1, exceptions: [exception] });
  assert.equal(scanText(value, { path, exceptions }).length, 0);
  assert.equal(scanText(value, { path: 'fixtures/quality/other.txt', exceptions }).length, 1);
  assert.equal(scanText(`${value}?extra=true`, { path, exceptions }).length, 1);
  assert.equal(scanText(value, { path, exceptions, secrets: [value] })[0].rule, 'registered-secret');
  for (const path of ['fixtures/**', '../elsewhere.txt', '/absolute', 'fixtures/']) {
    assert.throws(() => validateExceptions({ schema_version: 1, exceptions: [{ ...exception, path }] }));
  }
  assert.throws(() => validateExceptions({ schema_version: 1, exceptions: [{ ...exception, rule: 'registered-secret' }] }));
});

test('repository scanner catches untracked candidates and staged secrets hidden by clean working files', (t) => {
  const root = temporary(t);
  git(root, 'init', '--quiet');
  writeFileSync(join(root, 'tracked.txt'), 'safe\n');
  writeFileSync(join(root, '.gitignore'), '.cache/\n');
  git(root, 'add', '.');
  mkdirSync(join(root, '.cache'));
  writeFileSync(join(root, '.cache/private.txt'), subscription());
  assert.equal(scanRepository(root).result, 'pass', 'ignored credentials are not commit candidates');
  writeFileSync(join(root, 'candidate.txt'), subscription());
  assert.equal(scanRepository(root).result, 'fail');
  writeFileSync(join(root, 'candidate.txt'), 'safe\n');
  writeFileSync(join(root, 'tracked.txt'), subscription());
  git(root, 'add', 'tracked.txt');
  writeFileSync(join(root, 'tracked.txt'), 'safe\n');
  const result = scanRepository(root);
  assert.equal(result.result, 'fail');
  assert.ok(result.findings.some(({ source }) => source === 'index'));
});

test('real scanner CLI exits nonzero on synthetic secrets in an independent repository', (t) => {
  const root = temporary(t);
  git(root, 'init', '--quiet');
  mkdirSync(join(root, 'scripts'));
  mkdirSync(join(root, 'fixtures/quality'), { recursive: true });
  for (const path of ['scripts/check-secrets.mjs', 'scripts/quality-secrets.mjs']) copyFileSync(join(sourceRoot, path), join(root, path));
  writeFileSync(join(root, 'fixtures/quality/secret-exceptions.json'), JSON.stringify({ schema_version: 1, exceptions: [] }));
  const value = credentialURI();
  writeFileSync(join(root, 'leak.txt'), value);
  const result = spawnSync(process.execPath, ['scripts/check-secrets.mjs'], { cwd: root, encoding: 'utf8', windowsHide: true });
  assert.equal(result.status, 1);
  assert.equal(JSON.parse(result.stdout).result, 'fail');
  assert.ok(!`${result.stdout}${result.stderr}`.includes(value));
});
