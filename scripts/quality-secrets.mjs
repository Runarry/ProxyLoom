import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, realpathSync } from 'node:fs';
import { isAbsolute, relative, resolve, sep } from 'node:path';
import { spawnSync } from 'node:child_process';

// Rules are intentionally explicit. Unknown generated credentials MUST also be
// registered by exact value; no heuristic scanner can recognize every secret.
const rules = [
  ['private-key', /-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----/g],
  ['subscription-token', /\bsub[_-]?token(?:[_-]|["']?\s*[:=]\s*["']?)[A-Za-z0-9._~+/-]{12,}/gi],
  ['canonical-subscription-token', /\bsub_[A-Za-z0-9_-]{1,64}\.[A-Za-z0-9_-]{43}(?![A-Za-z0-9_-])/g],
  ['subscription-url', /\/s\/[A-Za-z0-9._~-]{24,}\/[A-Za-z0-9._~-]+/g],
  ['credential-uri', /\b[a-z][a-z0-9+.-]*:\/\/[^\s/"'<>`]+@[^\s"'<>`]*/gi],
  ['encoded-proxy-uri', /\b(?:vmess|ss):\/\/[A-Za-z0-9+/=_-]{20,}/gi],
  ['authorization', /\b(?:Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{20,}/gi],
  ['session-cookie', /\bproxyloom_session=[A-Za-z0-9_-]{32,}/g],
  ['secret-literal', /\b(?:password|passwd|secret|api[_-]?key|(?:access|refresh|csrf|session)[_-]?token)["']?\s*[:=]\s*["'][A-Za-z0-9_+/.=-]{16,}["']/gi],
];

export const sha256 = (value) => createHash('sha256').update(value).digest('hex');
export const safeLabel = (value) => String(value).replace(/[^A-Za-z0-9_./:@ -]/g, '?').replaceAll('::', ': :').slice(0, 240);

export function validateExceptions(document) {
  if (document?.schema_version !== 1 || !Array.isArray(document.exceptions)) throw new Error('secret_exception_schema_invalid');
  const seen = new Set();
  for (const entry of document.exceptions) {
    // Only an exact file, detector and complete line digest can be excepted.
    // Registered exact values are never suppressible, even in synthetic tests.
    if (!entry || typeof entry.path !== 'string' || !/^[A-Za-z0-9_.\-/]+$/.test(entry.path)
      || entry.path.split('/').some((part) => !part || part === '.' || part === '..')
      || !rules.some(([rule]) => rule === entry.rule) || !/^[a-f0-9]{64}$/.test(entry.line_sha256)
      || typeof entry.reason !== 'string' || entry.reason.length < 20
      || Object.keys(entry).some((key) => !['path', 'rule', 'line_sha256', 'reason'].includes(key))) {
      throw new Error('secret_exception_must_be_exact_and_explained');
    }
    const key = `${entry.path}:${entry.rule}:${entry.line_sha256}`;
    if (seen.has(key)) throw new Error('secret_exception_duplicate');
    seen.add(key);
  }
  return document.exceptions;
}

export function scanText(input, { path = '(memory)', secrets = [], exceptions = [] } = {}) {
  const buffer = Buffer.isBuffer(input) ? input : Buffer.from(String(input));
  const encodings = [buffer.toString('utf8')];
  // Decode UTF-16 only when null bytes suggest a wide-character text file.
  if (buffer.includes(0)) {
    const even = Buffer.from(buffer.subarray(0, buffer.length - buffer.length % 2));
    encodings.push(even.toString('utf16le'));
    even.swap16();
    encodings.push(even.toString('utf16le'));
  }
  const findings = [];
  const recorded = new Set();
  function append(rule, line, digest) {
    const key = `${rule}:${line}:${digest}`;
    if (recorded.has(key)) return;
    recorded.add(key);
    findings.push({ path: safeLabel(path), line, rule, line_sha256: digest });
  }
  for (const text of encodings) {
    for (const secret of new Set(secrets)) {
      if (typeof secret !== 'string' || !secret) throw new Error('registered_secret_must_be_nonempty');
      const index = text.indexOf(secret);
      // No secret digest is published: even a digest can identify a short secret.
      if (index !== -1) append('registered-secret', text.slice(0, index).split('\n').length, undefined);
    }
    const lines = text.split(/\r?\n/);
    for (let index = 0; index < lines.length; index++) {
      for (const [rule, pattern] of rules) {
        pattern.lastIndex = 0;
        if (!pattern.test(lines[index])) continue;
        const digest = sha256(lines[index]);
        if (!exceptions.some((entry) => entry.path === path && entry.rule === rule && entry.line_sha256 === digest)) {
          append(rule, index + 1, digest);
        }
      }
    }
  }
  return findings;
}

export function assertSecretFree(input, options = {}) {
  const findings = scanText(input, options);
  if (findings.length) throw new Error(`secret_scan_rejected: ${findings.map(({ path, line, rule }) => `${path}:${line}:${rule}`).join(', ').slice(0, 1800)}`);
  return input;
}

function git(root, args) {
  const result = spawnSync('git', args, { cwd: root, encoding: 'utf8', windowsHide: true, maxBuffer: 32 * 1024 * 1024 });
  if (result.error || result.status !== 0) throw new Error('secret_scan_git_inventory_failed');
  return result.stdout;
}

export function confinedFile(root, name) {
  const path = resolve(root, name);
  const local = relative(resolve(root), path);
  if (!local || isAbsolute(local) || local === '..' || local.startsWith(`..${sep}`)) throw new Error('quality_path_outside_root');
  const stat = lstatSync(path);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('quality_requires_regular_file');
  const real = relative(realpathSync(root), realpathSync(path));
  if (isAbsolute(real) || real === '..' || real.startsWith(`..${sep}`)) throw new Error('quality_symlink_outside_root');
  if (stat.size > 16 * 1024 * 1024) throw new Error('quality_file_exceeds_16_mib');
  return path;
}

export function scanRepository(root, { exceptions = [], secrets = [] } = {}) {
  const candidates = [...new Set(git(root, ['ls-files', '-z', '--cached', '--others', '--exclude-standard']).split('\0').filter(Boolean))].sort();
  const findings = [];
  let checked = 0;
  for (const path of candidates) {
    let file;
    try { file = confinedFile(root, path); } catch (error) { if (error.code === 'ENOENT') continue; throw error; }
    findings.push(...scanText(readFileSync(file), { path, exceptions, secrets }));
    checked++;
  }
  // A clean working file cannot hide a secret still staged for the next commit.
  const staged = git(root, ['ls-files', '--stage', '-z']).split('\0').filter(Boolean);
  const entries = [];
  for (const entry of staged) {
    const match = entry.match(/^(\d+) ([a-f0-9]+) ([0-3])\t([\s\S]+)$/);
    if (!match || match[3] !== '0' || !['100644', '100755'].includes(match[1])) throw new Error('secret_scan_index_entry_unsupported');
    entries.push({ hash: match[2], path: match[4] });
  }
  const batch = spawnSync('git', ['cat-file', '--batch'], {
    cwd: root, input: entries.map(({ hash }) => `${hash}\n`).join(''), windowsHide: true, maxBuffer: 128 * 1024 * 1024,
  });
  if (batch.error || batch.status !== 0) throw new Error('secret_scan_index_read_failed');
  let offset = 0;
  for (const entry of entries) {
    const end = batch.stdout.indexOf(10, offset);
    const header = batch.stdout.subarray(offset, end).toString('ascii').match(/^([a-f0-9]+) blob (\d+)$/);
    if (!header || header[1] !== entry.hash || Number(header[2]) > 16 * 1024 * 1024) throw new Error('secret_scan_index_blob_invalid_or_too_large');
    offset = end + 1;
    const size = Number(header[2]);
    const content = batch.stdout.subarray(offset, offset + size);
    if (content.length !== size || batch.stdout[offset + size] !== 10) throw new Error('secret_scan_index_blob_truncated');
    findings.push(...scanText(content, { path: entry.path, exceptions, secrets }).map((finding) => ({ ...finding, source: 'index' })));
    offset += size + 1;
  }
  return { schema_version: 1, result: findings.length ? 'fail' : 'pass', candidate_files: checked, index_blobs: staged.length, findings };
}
