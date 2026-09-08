import { randomUUID } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { runQualitySteps } from './quality-runner.mjs';
import { assertSecretFree, confinedFile, sha256 } from './quality-secrets.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const foundationRuns = () => existsSync(join(root, '.cache/foundation'))
  ? readdirSync(join(root, '.cache/foundation')).filter((name) => uuid.test(name)) : [];
function sourceManifest() {
  const inventory = spawnSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '-z'], {
    cwd: root, encoding: 'utf8', windowsHide: true, maxBuffer: 8 * 1024 * 1024,
  });
  if (inventory.error || inventory.status !== 0) throw new Error('quality_source_inventory_failed');
  return [...new Set(inventory.stdout.split('\0').filter(Boolean))].filter((path) =>
    /^(?:api|compat|deploy|fixtures|internal|migrations|proxyloom-server|proxyloom-runner|proxyloom-fixtures|proxyloom-web|schemas|scripts|\.github)\//.test(path)
    || ['README.md', 'go.mod', 'go.sum', 'sqlc.yaml', '.gitignore', '.gitattributes',
      'docs/PLAN.md', 'docs/PLAN.tasks.json', 'docs/requirements_v1.0.md', 'docs/project_design_v1.0.md'].includes(path))
    .filter((path) => existsSync(join(root, path))).sort()
    .map((path) => ({ path, sha256: sha256(readFileSync(confinedFile(root, path))) }));
}
let report;
let directory;
let priorFoundation;
let sources;
try {
  const options = { profile: 'full', base: process.env.QUALITY_BASE_REF ?? 'HEAD' };
  const args = process.argv.slice(2);
  for (let index = 0; index < args.length; index += 2) {
    if (!['--base', '--profile'].includes(args[index]) || !args[index + 1]) throw new Error('quality_arguments_invalid');
    options[args[index].slice(2)] = args[index + 1];
  }
  if (!['source', 'full'].includes(options.profile)) throw new Error('quality_profile_invalid');
  const lock = JSON.parse(readFileSync(join(root, 'deploy/tools.lock.json'), 'utf8'));
  const runID = randomUUID();
  directory = join(root, '.cache/quality', runID);
  mkdirSync(directory, { recursive: true, mode: 0o700 });
  priorFoundation = new Set(foundationRuns());
  const nodeStep = (name, args) => ({ name, command: process.execPath, args });
  const pnpmStep = (name, args) => process.platform === 'win32'
    ? { name, command: process.env.ComSpec ?? 'cmd.exe', args: ['/d', '/s', '/c', `pnpm ${args.join(' ')}`], cwd: join(root, 'proxyloom-web') }
    : { name, command: 'pnpm', args, cwd: join(root, 'proxyloom-web') };
  const tests = readdirSync(join(root, 'scripts')).filter((name) => name.endsWith('.test.mjs')).sort().map((name) => `scripts/${name}`);
  const steps = [
    nodeStep('secret-scan', ['scripts/check-secrets.mjs']),
    { ...nodeStep('node-version', ['--version']), expected_stdout: `v${lock.node}` },
    nodeStep('tool-locks', ['scripts/check-locks.mjs']),
    { ...pnpmStep('pnpm-version', ['--version']), expected_stdout: lock.pnpm },
    { ...pnpmStep('frontend-frozen-install', ['install', '--frozen-lockfile']), env: { CI: 'true' } },
    nodeStep('sqlc-locked-install', ['scripts/install-sqlc.mjs']),
    nodeStep('quality-and-script-selftests', ['--test', ...tests]),
    nodeStep('engineering', ['scripts/check.mjs']),
    pnpmStep('frontend-types-and-build', ['build']),
    nodeStep('api-generated-drift', ['scripts/check-api.mjs']),
    nodeStep('contract-base-diff', ['scripts/check-contracts.mjs', '--base', options.base]),
  ];
  if (options.profile === 'full') steps.push(nodeStep('postgres-foundation-and-identity', ['scripts/verify-foundation.mjs']));
  report = { schema_version: 1, run_id: runID, profile: options.profile, started_at: new Date().toISOString(), result: 'fail',
    scope: options.profile === 'full' ? 'source-and-postgres' : 'source-only-postgres-not-run' };
  sources = sourceManifest();
  writeFileSync(join(directory, 'source-manifest.json'), `${JSON.stringify({ schema_version: 1, files: sources }, null, 2)}\n`, { flag: 'wx', mode: 0o600 });
  Object.assign(report, runQualitySteps(steps, { root }));
  const stable = JSON.stringify(sourceManifest()) === JSON.stringify(sources);
  report.steps.push({ name: 'source-stability', result: stable ? 'pass' : 'fail' });
  if (!stable) { report.result = 'fail'; report.error = 'source-changed-during-quality-run'; }
} catch {
  report ??= { schema_version: 1, result: 'fail', steps: [] };
  report.result = 'fail';
  report.error = 'quality-entry-could-not-finish';
  console.error('FAIL: quality entry could not finish; check prerequisites and arguments.');
} finally {
  if (directory) {
    report.foundation_runs = foundationRuns().filter((id) => !priorFoundation?.has(id));
    writeFileSync(join(directory, 'report.json'), assertSecretFree(`${JSON.stringify(report, null, 2)}\n`, { path: 'quality/report.json' }), { flag: 'wx', mode: 0o600 });
    console.log(`Quality evidence: .cache/quality/${report.run_id}/report.json`);
  }
  console.log(`${report?.result === 'pass' ? 'PASS' : 'FAIL'}: quality ${report?.scope ?? 'entry'}. No task or capability status was changed.`);
  process.exitCode = report?.result === 'pass' ? 0 : 1;
}
