import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { readdirSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
function run(command, args, capture = false) {
  console.log(`> ${command} ${args.join(' ')}`);
  const result = spawnSync(command, args, { cwd: root, shell: false, encoding: 'utf8', stdio: capture ? 'pipe' : 'inherit' });
  if (result.error) throw result.error;
  assert.equal(result.status, 0, result.stderr || `${command} failed (${result.status})`);
  return result.stdout;
}
run(process.execPath, ['scripts/check-locks.mjs']);
run(process.execPath, ['scripts/check-plan.mjs']);
run(process.execPath, ['scripts/check-boundaries.mjs']);
function goFiles(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    if (entry.name.startsWith('.') || ['node_modules', 'vendor', 'dist'].includes(entry.name)) return [];
    const path = join(directory, entry.name);
    return entry.isDirectory() ? goFiles(path) : entry.isFile() && path.endsWith('.go') ? [path] : [];
  });
}
const files = goFiles(root);
assert.ok(files.length, 'No Go sources found');
for (let i = 0; i < files.length; i += 50) {
  const unformatted = run('gofmt', ['-l', ...files.slice(i, i + 50)], true).trim();
  assert.equal(unformatted, '', `Run gofmt on:\n${unformatted}`);
}
run('go', ['mod', 'verify']);
run('go', ['mod', 'tidy', '-diff']);
run('go', ['vet', '-mod=readonly', './...']);
run('go', ['test', '-mod=readonly', '-race', './...']);
console.log('PASS: engineering checks. Frontend checks run separately in CI.');
