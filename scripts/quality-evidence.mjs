import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { assertSecretFree, confinedFile, sha256 } from './quality-secrets.mjs';

const runID = '[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}';
const uuid = new RegExp(`^${runID}$`);
const qualityFile = new RegExp(`^\\.cache/quality/${runID}/(?:report|source-manifest)\\.json$`);
const foundationFile = new RegExp(`^\\.cache/foundation/${runID}/(?:report\\.json|source-manifest\\.json|go-test\\.jsonl|go-stderr\\.txt)$`);
const credentialNames = ['db_bootstrap_password', 'db_runtime_password', 'db_migration_password', 'admin_dsn', 'database_dsn', 'migration_dsn'];

export function stageEvidence(root, entries, { destination = '.cache/quality-upload', secrets = [] } = {}) {
  if (!/^\.cache\/quality-upload(?:-[a-z0-9-]{1,64})?$/.test(destination)) throw new Error('quality_evidence_destination_invalid');
  if (!Array.isArray(entries) || !entries.length || entries.length > 64 || new Set(entries).size !== entries.length) throw new Error('quality_evidence_entry_count_invalid');
  let bytes = 0;
  const checked = entries.sort().map((path) => {
    if (!qualityFile.test(path) && !foundationFile.test(path)) throw new Error('quality_evidence_file_not_allowlisted');
    const content = readFileSync(confinedFile(root, path));
    bytes += content.length;
    if (bytes > 64 * 1024 * 1024) throw new Error('quality_evidence_exceeds_64_mib');
    // Exceptions from commit fixtures do not apply to captured execution output.
    assertSecretFree(content, { path, secrets });
    return { path, content, sha256: sha256(content) };
  });
  // All files are validated before the first evidence byte is copied. A stale
  // upload directory is an error, so old files cannot join a new CI artifact.
  mkdirSync(join(root, '.cache'), { recursive: true, mode: 0o700 });
  mkdirSync(join(root, destination), { mode: 0o700 });
  for (const entry of checked) {
    const output = join(root, destination, entry.path.slice('.cache/'.length));
    mkdirSync(dirname(output), { recursive: true, mode: 0o700 });
    writeFileSync(output, entry.content, { flag: 'wx', mode: 0o600 });
  }
  const manifest = { schema_version: 1, files: checked.map(({ path, content, sha256 }) => ({ path, bytes: content.length, sha256 })) };
  writeFileSync(join(root, destination, 'manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`, { flag: 'wx', mode: 0o600 });
  return manifest;
}

export function collectQualityEvidence(root, options = {}) {
  const qualityRoot = join(root, '.cache/quality');
  if (!existsSync(qualityRoot)) throw new Error('quality_evidence_no_run');
  const runs = readdirSync(qualityRoot).filter((id) => uuid.test(id));
  if (!runs.length || runs.length > 16) throw new Error('quality_evidence_run_count_invalid');
  const entries = [];
  const foundation = new Set();
  for (const id of runs) {
    const path = `.cache/quality/${id}/report.json`;
    const report = JSON.parse(readFileSync(confinedFile(root, path), 'utf8'));
    if (report.run_id !== id || !Array.isArray(report.foundation_runs) || report.foundation_runs.length > 4
      || !report.foundation_runs.every((value) => uuid.test(value))) throw new Error('quality_evidence_run_manifest_invalid');
    entries.push(path, `.cache/quality/${id}/source-manifest.json`);
    for (const value of report.foundation_runs) foundation.add(value);
  }
  const secrets = [...(options.secrets ?? [])];
  for (const id of foundation) {
    // These generated secret files are read solely to prevent exact-value leaks;
    // they can never match the upload allowlist and are never copied or logged.
    for (const name of credentialNames) {
      const path = `.cache/foundation/${id}/secrets/${name}`;
      if (existsSync(join(root, path))) secrets.push(readFileSync(confinedFile(root, path), 'utf8'));
    }
    for (const name of ['report.json', 'source-manifest.json', 'go-test.jsonl', 'go-stderr.txt']) {
      const path = `.cache/foundation/${id}/${name}`;
      if (existsSync(join(root, path))) entries.push(path);
    }
  }
  return stageEvidence(root, entries, { ...options, secrets });
}
