import assert from 'node:assert/strict';
import test from 'node:test';
import { summarizeGoFailure } from './foundation-report.mjs';

const failure = { Action: 'fail', Package: 'proxyloom/internal/storage', Test: 'TestPostgresRoles' };
const output = (Output, extra = {}) => ({ ...failure, Action: 'output', Output, ...extra });

test('summarizes failed tests and source diagnostics, omitting successful test output', () => {
  const lines = summarizeGoFailure({ events: [
    output('=== RUN   TestPostgresRoles\n'),
    output('    roles_test.go:42: expected permission denied, got nil\n'),
    output('success-only payload', { Test: 'TestSuccess' }),
    failure,
    { Action: 'fail', Package: failure.Package },
  ] });
  assert.match(lines.join('\n'), /FAIL proxyloom\/internal\/storage \/ TestPostgresRoles/);
  assert.match(lines.join('\n'), /roles_test.go:42: expected permission denied, got nil/);
  assert.ok(!lines.join('\n').includes('success-only'));
  assert.ok(!lines.join('\n').includes('=== RUN'));
});

test('retains compile stderr even when Go emitted no test failure event', () => {
  assert.deepEqual(summarizeGoFailure({ stderr: '# proxyloom/internal/storage\nroles.go:12: undefined: role\n' }),
    ['# proxyloom/internal/storage', 'roles.go:12: undefined: role']);
});

test('redacts registered secrets and DSNs plus common credential forms', () => {
  const secrets = ['generated-password-123', 'postgres://user:generated-password-123@localhost/db'];
  const text = `${secrets.join(' ')} https://other:credential@example.com/x?token=abc Bearer abc.def token=xyz password='two words' api_key=hello`;
  const summary = summarizeGoFailure({ events: [output(text), failure], secrets }).join('\n');
  for (const value of [...secrets, 'credential', 'abc.def', 'xyz', 'two words', 'hello']) assert.ok(!summary.includes(value));
  assert.match(summary, /REDACTED/);
});

test('neutralizes workflow commands, terminal controls and line injections', () => {
  const summary = summarizeGoFailure({ events: [output('\x1b[31m\x00::error::injected\n::add-mask::value\r\u202ehidden'), failure] }).join('\n');
  assert.ok(!summary.includes('::'));
  assert.doesNotMatch(summary, /[\x00-\x09\x0b-\x1f\x7f\u202e]/);
  assert.match(summary, /: :error: :injected/);
});

test('bounds UTF-8 bytes and total lines', () => {
  for (const diagnostic of ['界'.repeat(10000), 'short line\n'.repeat(1000)]) {
    const lines = summarizeGoFailure({ events: [failure, output(diagnostic)] });
    assert.ok(lines.length <= 50);
    assert.ok(Buffer.byteLength(lines.join('\n')) <= 8192);
    assert.ok(!lines.join('\n').includes('\ufffd'));
  }
});

test('successful runs and empty input produce no failure summary', () => {
  assert.deepEqual(summarizeGoFailure({}), []);
  assert.deepEqual(summarizeGoFailure({ events: [output('roles_test.go:42: ordinary test log'), { ...failure, Action: 'pass' }] }), []);
});
