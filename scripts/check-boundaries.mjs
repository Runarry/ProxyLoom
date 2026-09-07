import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const result = spawnSync('go', ['list', '-mod=readonly', '-deps', '-f', '{{.ImportPath}}|{{join .Imports ","}}', './...'], {
  cwd: fileURLToPath(new URL('../', import.meta.url)), encoding: 'utf8', shell: false,
});
if (result.error) throw result.error;
assert.equal(result.status, 0, result.stderr || 'go list failed');
const packages = new Map(result.stdout.trim().split(/\r?\n/).map((line) => {
  const [name, imports] = line.split('|');
  return [name, imports ? imports.split(',') : []];
}));
const moduleResult = spawnSync('go', ['list', '-mod=readonly', '-m', '-f', '{{.Path}}'], {
  cwd: fileURLToPath(new URL('../', import.meta.url)), encoding: 'utf8', shell: false,
});
if (moduleResult.error) throw moduleResult.error;
assert.equal(moduleResult.status, 0, moduleResult.stderr || 'go list module failed');
const modulePath = moduleResult.stdout.trim();
const local = (name) => name.startsWith(`${modulePath}/`);
const ir = [...packages.keys()].filter((name) => local(name) && /\/internal\/ir(?:\/|$)/.test(name));
const runner = [...packages.keys()].filter((name) => name === `${modulePath}/proxyloom-runner` || name.startsWith(`${modulePath}/proxyloom-runner/`));
const server = [...packages.keys()].filter((name) => name === `${modulePath}/proxyloom-server` || name.startsWith(`${modulePath}/proxyloom-server/`));
assert.ok(ir.length, 'No IR packages found');
assert.ok(runner.length, 'No Runner packages found');
assert.ok(server.length, 'No API packages found');
const database = (name) => /^(database\/sql(?:\/|$)|github\.com\/jackc\/|github\.com\/lib\/pq(?:\/|$)|gorm\.io\/)/.test(name);
const forbiddenLocal = (name) => local(name) && /\/(?:db|database|storage|identity|server|proxyloom-server)(?:\/|$)/.test(name);
// Follow project imports, but do not attribute a schema library's optional HTTP loader to the IR.
function checkIR(name, seen = new Set()) {
  if (seen.has(name)) return;
  seen.add(name);
  for (const dep of packages.get(name) ?? []) {
    assert.ok(!/^net\/http(?:\/|$)/.test(dep) && dep !== 'os/exec' && !database(dep) && !forbiddenLocal(dep) && !/\/internal\/isolation(?:\/|$)/.test(dep), `IR boundary: ${name} imports ${dep}`);
    if (local(dep)) checkIR(dep, seen);
  }
}
function checkRunner(name, seen = new Set()) {
  if (seen.has(name)) return;
  seen.add(name);
  for (const dep of packages.get(name) ?? []) {
    assert.ok(!database(dep) && !forbiddenLocal(dep), `Runner boundary: ${name} imports ${dep}`);
    assert.ok(!/\/internal\/isolation(?:\/|$)/.test(dep), `Runner boundary: ${name} imports isolation fixture ${dep}`);
    checkRunner(dep, seen);
  }
}
function checkServer(name, seen = new Set()) {
  if (seen.has(name)) return;
  seen.add(name);
  for (const dep of packages.get(name) ?? []) {
    assert.ok(!/\/internal\/isolation(?:\/|$)/.test(dep), `API boundary: ${name} imports isolation fixture ${dep}`);
    if (local(dep)) checkServer(dep, seen);
  }
}
for (const name of ir) checkIR(name);
for (const name of runner) checkRunner(name);
for (const name of server) checkServer(name);
console.log('PASS: IR project dependencies exclude HTTP/database/exec; Runner dependency graph excludes database/identity/server; API and Runner exclude isolation fixtures.');
