import assert from 'node:assert/strict';
import test from 'node:test';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const script = fileURLToPath(new URL('../deploy/postgres-init.sh', import.meta.url));
const posix = { skip: process.platform === 'win32' ? 'Requires a POSIX shell; exercised on Linux CI' : false };

function runInit(t, mode) {
  const directory = mkdtempSync(join(tmpdir(), 'proxyloom-postgres-init-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const marker = join(directory, 'psql-called');
  writeFileSync(join(directory, 'cat'), `#!/bin/sh
case "$1" in
  /run/secrets/db_runtime_password) role=runtime ;;
  /run/secrets/db_migration_password) role=migration ;;
  *) exit 90 ;;
esac
case "$TEST_MODE" in
  unreadable-"$role") exit 1 ;;
  empty-"$role") exit 0 ;;
esac
printf '%s' "synthetic-$role-value"
`, { mode: 0o700 });
  writeFileSync(join(directory, 'psql'), `#!/bin/sh
printf '%s\\n' "$PROXYLOOM_DB_RUNTIME_PASSWORD" "$PROXYLOOM_DB_MIGRATION_PASSWORD" > "$TEST_MARKER"
`, { mode: 0o700 });
  const result = spawnSync('/bin/sh', [script], {
    env: { PATH: directory, POSTGRES_USER: 'synthetic-user', POSTGRES_DB: 'synthetic-db', TEST_MODE: mode, TEST_MARKER: marker },
    encoding: 'utf8', timeout: 5000,
  });
  assert.ifError(result.error);
  assert.doesNotMatch(result.stdout + result.stderr, /synthetic-(?:runtime|migration)-value/, 'credentials must not be printed');
  return { result, marker };
}

for (const mode of ['unreadable-runtime', 'unreadable-migration', 'empty-runtime', 'empty-migration']) {
  test(`${mode} fails before invoking psql`, posix, (t) => {
    const { result, marker } = runInit(t, mode);
    assert.notEqual(result.status, 0, 'initialization must reject missing credentials');
    assert.equal(existsSync(marker), false, 'psql must not run with missing credentials');
  });
}

test('valid credentials are exported to psql', posix, (t) => {
  const { result, marker } = runInit(t, 'valid');
  assert.equal(result.status, 0, result.stderr);
  assert.equal(readFileSync(marker, 'utf8'), 'synthetic-runtime-value\nsynthetic-migration-value\n');
});
