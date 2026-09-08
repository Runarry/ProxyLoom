import { spawnSync } from 'node:child_process';
import { existsSync, readFileSync, realpathSync } from 'node:fs';
import { createRequire } from 'node:module';
import { resolve } from 'node:path';
import { confinedFile, safeLabel, sha256 } from './quality-secrets.mjs';

const contractPath = (path) => path === 'api/openapi.yaml' || /^schemas\/.+\.schema\.json$/.test(path);
const migrationPath = (path) => /^migrations\/.+\.sql$/.test(path);
const declarationPath = (path) => /^compat\/contract-changes\/T-\d{3}(?:-[A-Za-z0-9-]+)?\.json$/.test(path);
const portablePath = (path) => typeof path === 'string' && /^[A-Za-z0-9_.\-/]+$/.test(path)
  && !path.split('/').some((part) => !part || part === '.' || part === '..');
export const normalizedSource = (source) => source.replaceAll('\r\n', '\n');

function git(root, args, allowedFailure = false) {
  const result = spawnSync('git', args, { cwd: root, encoding: 'utf8', windowsHide: true, maxBuffer: 32 * 1024 * 1024 });
  if (result.error || (result.status !== 0 && !allowedFailure)) throw new Error('contract_git_read_failed');
  return result.status === 0 ? result.stdout : null;
}

function canonical(value) {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value && typeof value === 'object') return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonical(value[key])}`).join(',')}}`;
  return JSON.stringify(value);
}
const digest = (value) => value === undefined ? null : sha256(canonical(value));
const object = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
const pointerKey = (key) => key.replaceAll('~', '~0').replaceAll('/', '~1');

// Conservative review gate: arrays and constraints are exact, not a claim that
// every OpenAPI/JSON Schema dialect has a decidable compatibility relation.
export function contractDiff(before, after, pointer = '') {
  if (canonical(before) === canonical(after)) return [];
  if (object(before) && object(after)) {
    return [...new Set([...Object.keys(before), ...Object.keys(after)])].sort()
      .flatMap((key) => contractDiff(before[key], after[key], `${pointer}/${pointerKey(key)}`));
  }
  return [{ pointer, before_sha256: digest(before), after_sha256: digest(after) }];
}

export function parseContract(root, path, source) {
  if (source === null) return undefined;
  if (path.endsWith('.json')) return JSON.parse(source);
  // Reuse the exact frontend dependency graph, never fetch a parser or a ref.
  const packagePath = realpathSync(resolve(root, 'proxyloom-web/node_modules/openapi-typescript/package.json'));
  if (JSON.parse(readFileSync(packagePath, 'utf8')).version !== '7.9.1') throw new Error('contract_parser_requires_frozen_frontend_install');
  const { makeDocumentFromString } = createRequire(packagePath)('@redocly/openapi-core');
  return makeDocumentFromString(source, resolve(root, path)).parsed;
}

export function describeContractChange(root, path, before, after) {
  const sourceHash = (source) => source === null ? null : sha256(normalizedSource(source));
  const changes = contractDiff(parseContract(root, path, before), parseContract(root, path, after));
  return { path, before_sha256: sourceHash(before), after_sha256: sourceHash(after), changes };
}

export function checkContracts(root, { base = 'HEAD' } = {}) {
  if (typeof base !== 'string' || !/^[A-Za-z0-9_./~^+-]{1,200}$/.test(base) || base.startsWith('-')) throw new Error('contract_base_ref_invalid');
  const baseCommit = git(root, ['rev-parse', '--verify', '--end-of-options', `${base}^{commit}`]).trim();
  const basePaths = git(root, ['ls-tree', '-r', '--name-only', '-z', baseCommit]).split('\0').filter(Boolean);
  const currentPaths = git(root, ['ls-files', '--cached', '--others', '--exclude-standard', '-z']).split('\0').filter(Boolean)
    .filter((path) => existsSync(resolve(root, path)));
  const baseSet = new Set(basePaths);
  const currentSet = new Set(currentPaths);
  const old = (path) => baseSet.has(path) ? git(root, ['show', `${baseCommit}:${path}`]) : null;
  const current = (path) => currentSet.has(path) ? readFileSync(confinedFile(root, path), 'utf8') : null;
  const changed = (path) => normalizedSource(old(path) ?? '') !== normalizedSource(current(path) ?? '');
  const failures = [];
  const changes = [];
  const declarations = [];
  for (const path of [...currentSet].filter(declarationPath).sort()) {
    const declaration = JSON.parse(current(path));
    if (declaration.schema_version !== 1 || !/^T-\d{3}$/.test(declaration.task)
      || typeof declaration.summary !== 'string' || declaration.summary.length < 20
      || !Array.isArray(declaration.contracts) || !declaration.contracts.length
      || !['documents', 'fixtures', 'generated'].every((key) => Array.isArray(declaration[key]) && declaration[key].every(portablePath))) {
      throw new Error('contract_declaration_invalid');
    }
    if (Object.keys(declaration).some((key) => !['schema_version', 'task', 'summary', 'documents', 'fixtures', 'generated', 'contracts'].includes(key))) throw new Error('contract_declaration_unknown_field');
    declarations.push({ path, ...declaration });
  }
  for (const path of [...new Set([...basePaths, ...currentPaths])].filter((path) => contractPath(path) || migrationPath(path)).sort()) {
    if (!changed(path)) continue;
    if (migrationPath(path) && baseSet.has(path)) {
      failures.push({ path, rule: 'existing-migration-is-immutable' });
      continue;
    }
    const change = contractPath(path) ? describeContractChange(root, path, old(path), current(path)) : {
      path, before_sha256: null, after_sha256: sha256(normalizedSource(current(path))), changes: [],
    };
    changes.push(change);
    const matching = declarations.filter((declaration) => declaration.contracts.some((entry) => canonical(entry) === canonical(change)));
    if (matching.length !== 1) {
      failures.push({ path, rule: 'contract-change-needs-one-exact-declaration' });
      continue;
    }
    const declaration = matching[0];
    // Existing declarations remain history. The declaration for this base diff
    // and every cited companion must change in the same PR/working tree.
    if (!changed(declaration.path)) failures.push({ path, rule: 'declaration-must-change-with-contract' });
    const required = [
      ['documents', (name) => /^docs\/(?!evidence\/).+\.md$/.test(name)],
      ['fixtures', (name) => /^fixtures\/(?:api|ir)\/.+\.(?:json|yaml)$/.test(name)],
    ];
    for (const [group, allowed] of required) {
      if (!declaration[group].length || !declaration[group].every((name) => allowed(name) && currentSet.has(name) && changed(name))) {
        failures.push({ path, rule: `${group}-must-change-with-contract` });
      }
    }
    if (path === 'api/openapi.yaml' && (!declaration.generated.includes('proxyloom-web/src/api/schema.d.ts')
      || !currentSet.has('proxyloom-web/src/api/schema.d.ts') || !changed('proxyloom-web/src/api/schema.d.ts'))) {
      failures.push({ path, rule: 'api-generated-types-must-change' });
    }
    if (migrationPath(path) && (!declaration.generated.some((name) => /^internal\/storage\/generated\/.+\.go$/.test(name) && changed(name)))) {
      failures.push({ path, rule: 'migration-generated-queries-must-change' });
    }
    for (const name of declaration.generated) {
      if (!currentSet.has(name) || !changed(name)) failures.push({ path, rule: 'declared-generated-file-must-change' });
    }
  }
  // An entry for a changed declaration cannot authorize unrelated future edits.
  for (const declaration of declarations.filter((entry) => changed(entry.path))) {
    for (const entry of declaration.contracts) {
      if (!changes.some((change) => canonical(change) === canonical(entry))) failures.push({ path: declaration.path, rule: 'stale-or-unmatched-declaration-entry' });
    }
  }
  return { schema_version: 1, base_commit: baseCommit, result: failures.length ? 'fail' : 'pass', changes,
    failures: failures.map((failure) => ({ ...failure, path: safeLabel(failure.path) })) };
}
