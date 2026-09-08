// Regenerate into a unique cache directory; a check never rewrites source files.
import assert from 'node:assert/strict';
import { createHash, randomUUID } from 'node:crypto';
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const root = fileURLToPath(new URL('../', import.meta.url));
const lock = JSON.parse(readFileSync(join(root, 'deploy/tools.lock.json'), 'utf8')).codegen.sqlc;
const platform = `${process.platform === 'win32' ? 'windows' : process.platform}_${process.arch === 'x64' ? 'amd64' : process.arch}`;
const build = lock.builds[platform];
assert.ok(build, 'sqlc_platform_not_locked');
const binary = join(root, '.cache/bin', build.executable);
assert.equal(createHash('sha256').update(readFileSync(binary)).digest('hex'), build.binary_sha256, 'sqlc_missing_or_unverified_run_install_sqlc');
const directory = join(root, '.cache/sqlc-check', randomUUID());
mkdirSync(directory, { recursive: true });
// sqlc.yaml is maintained as JSON (a YAML subset) so checks need no extra parser.
const config = JSON.parse(readFileSync(join(root, 'sqlc.yaml'), 'utf8'));
const comparisons = [];
for (const entry of config.sql) {
  entry.schema = (Array.isArray(entry.schema) ? entry.schema : [entry.schema]).map((path) => relative(directory, resolve(root, path)));
  entry.queries = (Array.isArray(entry.queries) ? entry.queries : [entry.queries]).map((path) => relative(directory, resolve(root, path)));
  const original = resolve(root, entry.gen.go.out);
  const generated = join(directory, `output-${comparisons.length}`);
  entry.gen.go.out = relative(directory, generated);
  comparisons.push({ original, generated });
}
const configPath = join(directory, 'sqlc.json');
writeFileSync(configPath, JSON.stringify(config));
const result = spawnSync(binary, ['generate', '-f', configPath], { cwd: root, encoding: 'utf8', windowsHide: true });
assert.ok(!result.error && result.status === 0, 'sqlc_generation_failed');
function files(directory) {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => entry.isDirectory() ? files(join(directory, entry.name)) : [join(directory, entry.name)]);
}
for (const { original, generated } of comparisons) {
  const tracked = files(original).map((path) => relative(original, path)).sort();
  const rebuilt = files(generated).map((path) => relative(generated, path)).sort();
  assert.deepEqual(rebuilt, tracked, 'sqlc_generated_file_set_drift');
  for (const file of rebuilt) assert.ok(readFileSync(join(generated, file), 'utf8').replaceAll('\r\n', '\n') === readFileSync(join(original, file), 'utf8').replaceAll('\r\n', '\n'), `sqlc_generated_drift: ${file}`);
}
console.log('PASS: sqlc generated queries match the locked generator.');
