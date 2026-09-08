// Explicitly owned disposable database for independent acceptance processes.
// It never reuses development volumes or prints credentials.
import assert from 'node:assert/strict';
import { randomBytes, randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const root = fileURLToPath(new URL('../', import.meta.url));
const [mode, argument] = process.argv.slice(2);
assert.ok(mode === 'start' && argument === undefined || mode === 'stop' && /^[0-9a-f-]{36}$/.test(argument), 'acceptance_db_arguments');
const id = mode === 'start' ? randomUUID() : argument;
const name = `proxyloom-acceptance-${id}`;
const label = 'io.proxyloom.acceptance';
const directory = join(root, '.cache/acceptance-db', id);
const secrets = join(directory, 'secrets');
const owned = { container: false, network: false };
const sensitive = [];
function docker(args) {
  const r = spawnSync('docker', args, { cwd: root, encoding: 'utf8', windowsHide: true, timeout: 180000, maxBuffer: 1024 * 1024 });
  assert.ok(!r.error && r.status === 0, 'acceptance_db_docker_failed');
  for (const value of sensitive) assert.ok(!r.stdout.includes(value), 'acceptance_db_output_sensitive');
  return r.stdout.trim();
}
function cleanup() {
  for (const kind of ['container', 'network']) {
    if (!owned[kind]) continue;
    const format = kind === 'container' ? `{{index .Config.Labels "${label}"}}` : `{{index .Labels "${label}"}}`;
    assert.equal(docker([kind, 'inspect', '--format', format, name]), id, 'acceptance_db_ownership');
    docker(kind === 'container' ? ['container', 'rm', '--force', '--volumes', name] : ['network', 'rm', name]);
    owned[kind] = false;
  }
}
if (mode === 'stop') {
  const state = JSON.parse(readFileSync(join(directory, 'state.json'), 'utf8'));
  assert.equal(state.id, id); assert.equal(state.name, name);
  owned.container = state.owned.container; owned.network = state.owned.network;
  cleanup();
  writeFileSync(join(directory, 'state.json'), JSON.stringify({ id, name, owned, stopped_at: new Date().toISOString() }, null, 2));
  console.log('PASS: owned acceptance database and network removed.');
} else {
  mkdirSync(secrets, { recursive: true, mode: 0o700 });
  const passwords = {};
  function secret(file, value) { sensitive.push(value); writeFileSync(join(secrets, file), value, { flag: 'wx', mode: 0o444 }); }
  try {
    for (const role of ['bootstrap', 'runtime', 'migration']) {
      passwords[role] = randomBytes(32).toString('hex'); secret(`db_${role}_password`, passwords[role]);
    }
    const lock = JSON.parse(readFileSync(join(root, 'deploy/tools.lock.json'), 'utf8'));
    const cached = spawnSync('docker', ['image', 'inspect', lock.images.postgres], { encoding: 'utf8', windowsHide: true });
    if (cached.error || cached.status !== 0) docker(['pull', lock.images.postgres]);
    docker(['network', 'create', '--label', `${label}=${id}`, name]); owned.network = true;
    docker(['create', '--name', name, '--label', `${label}=${id}`, '--network', name,
      '--publish', '127.0.0.1::5432', '--memory', '512m', '--cpus', '1', '--pids-limit', '128',
      '--tmpfs', '/var/lib/postgresql/data:rw,size=536870912',
      ...['bootstrap', 'runtime', 'migration'].flatMap(role => ['--mount', `type=bind,source=${join(secrets, `db_${role}_password`)},target=/run/secrets/db_${role}_password,readonly`]),
      '--mount', `type=bind,source=${join(root, 'deploy/postgres-init.sh')},target=/docker-entrypoint-initdb.d/10-proxyloom.sh,readonly`,
      '--env', 'POSTGRES_DB=proxyloom', '--env', 'POSTGRES_USER=proxyloom_bootstrap',
      '--env', 'POSTGRES_PASSWORD_FILE=/run/secrets/db_bootstrap_password', '--env', 'POSTGRES_INITDB_ARGS=--auth-host=scram-sha-256',
      '--env', 'PGDATA=/var/lib/postgresql/data', lock.images.postgres]); owned.container = true;
    docker(['start', name]);
    let ready = false;
    for (let n = 0; n < 120 && !ready; n++) {
      const r = spawnSync('docker', ['exec', name, 'psql', '-U', 'proxyloom_bootstrap', '-d', 'proxyloom', '-tAc', "SELECT count(*) FROM pg_roles WHERE rolname IN ('proxyloom','proxyloom_migrator')"], { encoding: 'utf8', windowsHide: true, timeout: 3000 });
      ready = !r.error && r.status === 0 && r.stdout.trim() === '2';
      if (!ready) await new Promise(resolve => setTimeout(resolve, 500));
    }
    assert.ok(ready, 'acceptance_db_readiness');
    const bindings = JSON.parse(docker(['inspect', '--format', '{{json .NetworkSettings.Ports}}', name]));
    const { HostIp, HostPort } = bindings['5432/tcp'][0]; assert.equal(HostIp, '127.0.0.1'); assert.match(HostPort, /^\d+$/);
    for (const [file, role, user] of [['admin_dsn','bootstrap','proxyloom_bootstrap'],['database_dsn','runtime','proxyloom'],['migration_dsn','migration','proxyloom_migrator']]) {
      const dsn = new URL('postgres://127.0.0.1/proxyloom?sslmode=disable');
      dsn.username = user; dsn.password = passwords[role]; dsn.port = HostPort;
      secret(file, dsn.toString());
    }
    writeFileSync(join(directory, 'state.json'), JSON.stringify({ id, name, owned, created_at: new Date().toISOString() }, null, 2));
    console.log(JSON.stringify({ id, directory, secrets, cleanup: `node scripts/acceptance-db.mjs stop ${id}` }));
  } catch (err) { cleanup(); throw err; }
}
