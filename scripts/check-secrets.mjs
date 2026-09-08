import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { scanRepository, validateExceptions } from './quality-secrets.mjs';

const root = fileURLToPath(new URL('../', import.meta.url));
try {
  if (process.argv.length !== 2) throw new Error('secret_scan_unknown_argument');
  const exceptions = validateExceptions(JSON.parse(readFileSync(resolve(root, 'fixtures/quality/secret-exceptions.json'), 'utf8')));
  const result = scanRepository(root, { exceptions });
  console.log(JSON.stringify(result, null, 2));
  process.exitCode = result.result === 'pass' ? 0 : 1;
} catch {
  // Do not let parser, filesystem or child-process errors echo input content.
  console.error('FAIL: secret scan could not safely finish. Check inventory, exception schema and file bounds.');
  process.exitCode = 1;
}
