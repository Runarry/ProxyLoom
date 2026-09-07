import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const root = new URL('../', import.meta.url);
const plan = readFileSync(new URL('docs/PLAN.md', root), 'utf8');
const document = JSON.parse(readFileSync(new URL('docs/PLAN.tasks.json', root), 'utf8'));
assert.equal(document.schema_version, 1);
assert.equal(document.source, 'docs/PLAN.md');
const expected = [];
let workPackage;
for (const line of plan.split(/\r?\n/)) {
  const heading = line.match(/^## 6\.\d+ (WP-\d+) /);
  if (heading) workPackage = heading[1];
  const row = line.match(/^\| \*\*(T-\d{3}) (.+?)\*\*<br>(M\d) · (.+?)<br>(\d+)–(\d+) 人日 \| (.+?) \| (.+?) \| (.+?) \|$/);
  if (!row) continue;
  expected.push({
    id: row[1], work_package: workPackage, title: row[2], milestone: row[3], role: row[4],
    estimate_person_days: { min: Number(row[5]), max: Number(row[6]) },
    implementation: row[7], dependencies: row[8].match(/T-\d{3}/g) ?? [],
    acceptance: row[9].replaceAll('<br>', '\n'),
  });
}
assert.equal(expected.length, 60, 'PLAN must contain 60 task rows');
assert.equal(document.tasks.length, 60, 'JSON must contain 60 tasks');
const tasks = new Map(document.tasks.map((task) => [task.id, task]));
assert.equal(tasks.size, 60, 'Duplicate task IDs');
for (let n = 1; n <= 60; n++) assert.ok(tasks.has(`T-${String(n).padStart(3, '0')}`), `Missing task ${n}`);
for (const row of expected) {
  const task = tasks.get(row.id);
  for (const [key, value] of Object.entries(row)) assert.deepEqual(task[key], value, `${row.id}: ${key} differs from PLAN`);
  assert.ok(['not_started', 'ready', 'in_progress', 'in_review', 'done', 'blocked'].includes(task.status), `${row.id}: invalid status`);
  assert.equal(new Set(task.dependencies).size, task.dependencies.length, `${row.id}: duplicate dependency`);
  for (const dep of task.dependencies) assert.ok(tasks.has(dep), `${row.id}: unknown dependency ${dep}`);
}
const visiting = new Set();
const visited = new Set();
function visit(id) {
  assert.ok(!visiting.has(id), `Dependency cycle at ${id}`);
  if (visited.has(id)) return;
  visiting.add(id);
  for (const dep of tasks.get(id).dependencies) visit(dep);
  visiting.delete(id);
  visited.add(id);
}
for (const id of tasks.keys()) visit(id);
console.log('PASS: 60 unique sequential tasks match PLAN metadata and form an acyclic dependency graph.');
