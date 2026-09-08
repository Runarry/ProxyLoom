import { fileURLToPath } from 'node:url';
import { checkContracts } from './quality-contracts.mjs';

try {
  const args = process.argv.slice(2);
  if (args.length && (args.length !== 2 || args[0] !== '--base')) throw new Error('contract_unknown_argument');
  const result = checkContracts(fileURLToPath(new URL('../', import.meta.url)), { base: args[1] ?? process.env.QUALITY_BASE_REF ?? 'HEAD' });
  console.log(JSON.stringify(result, null, 2));
  process.exitCode = result.result === 'pass' ? 0 : 1;
} catch {
  console.error('FAIL: contract check could not finish. Check the base ref, frozen frontend install and declaration schema.');
  process.exitCode = 1;
}
