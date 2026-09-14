// Compare local SBOMs against one downloaded public vulnerability database.
// Findings are retained for disposition; a successful scan is not a clean bill.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../', import.meta.url)), directory = resolve(process.argv[2] ?? '');
const lock = JSON.parse(await readFile(join(root, 'deploy/tools.lock.json'), 'utf8')).vulnerability_scanner;
assert.ok(['win32', 'linux'].includes(process.platform) && process.arch === 'x64', 'scanner_host_not_locked');
const platform = process.platform === 'win32' ? 'windows_amd64' : 'linux_amd64';
const binary = resolve(process.argv[3] ?? join(root, '.cache/tools/grype', lock.version, 'unpacked', process.platform === 'win32' ? 'grype.exe' : 'grype'));
const hash = data => createHash('sha256').update(data).digest('hex');
assert.equal(hash(await readFile(binary)), lock.builds[platform].binary_sha256);
const sbom = JSON.parse(await readFile(join(directory, 'sbom/manifest.json'), 'utf8'));
const output = join(directory, 'vulnerabilities'); await mkdir(output, { recursive: true });
const env = { ...process.env, GOMAXPROCS: '2', GRYPE_CHECK_FOR_APP_UPDATE: 'false', GRYPE_DB_CACHE_DIR: join(root, '.cache/tools/grype/database'), GRYPE_DB_AUTO_UPDATE: 'false' };
async function run(args) {
  const result = await new Promise(done => {
    const child = spawn(binary, args, { cwd: root, env, windowsHide: true }); let out = '', errors = '';
    child.stdout.on('data', v => out += v); child.stderr.on('data', v => errors += v);
    child.on('error', () => done({ code: -1, out, errors: 'scanner_start_failed' })); child.on('close', code => done({ code, out, errors }));
  });
  assert.equal(result.code, 0, 'vulnerability_scanner_failed'); return result.out;
}
await run(['db', 'update']);
const database = JSON.parse(await run(['db', 'status', '-o', 'json']));
const inventories = [];
for (const item of sbom.inventories) {
  const path = join(directory, 'sbom', item.file);
  assert.equal(hash(await readFile(path)), item.sha256, 'sbom_changed');
  const report = await run(['sbom:' + path, '-o', 'json']), parsed = JSON.parse(report);
  const file = item.file.replace('.cdx.json', '.grype.json');
  await writeFile(join(output, file), report);
  const severities = {}, fixable = {};
  for (const match of parsed.matches ?? []) {
    const severity = match.vulnerability.severity;
    severities[severity] = (severities[severity] ?? 0) + 1;
    if (match.vulnerability.fix?.state === 'fixed') fixable[severity] = (fixable[severity] ?? 0) + 1;
  }
  inventories.push({ file, sha256: hash(report), sbom_sha256: item.sha256, findings: parsed.matches?.length ?? 0, severities, fixable });
  console.log(`PASS: scan-${file}`);
}
await writeFile(join(output, 'manifest.json'), JSON.stringify({ schema_version: 1, scan_result: 'pass', disposition: 'review_required', created_at: new Date().toISOString(), scanner: { name: 'grype', version: lock.version, binary_sha256: lock.builds[platform].binary_sha256 }, database, inventories }, null, 2) + '\n');
