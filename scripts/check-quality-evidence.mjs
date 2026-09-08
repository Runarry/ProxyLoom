import { fileURLToPath } from 'node:url';
import { collectQualityEvidence } from './quality-evidence.mjs';

try {
  if (process.argv.length !== 2) throw new Error('quality_evidence_unknown_argument');
  const manifest = collectQualityEvidence(fileURLToPath(new URL('../', import.meta.url)));
  console.log(`PASS: ${manifest.files.length} bounded, secret-scanned evidence files staged in .cache/quality-upload.`);
} catch {
  console.error('FAIL: evidence was not staged; check the fixed file allowlist, run manifests, size bounds and secret scan.');
  process.exitCode = 1;
}
