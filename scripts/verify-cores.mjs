import { readFileSync } from 'node:fs';

const root = new URL('../', import.meta.url);
const lockText = readFileSync(new URL('compat/cores.lock.yaml', root), 'utf8');
const combinationsText = readFileSync(new URL('compat/p0-combinations.yaml', root), 'utf8');

function fail(message) {
  throw new Error(`Core lock: ${message}`);
}

if (/\blatest\b/i.test(lockText) || /\blatest\b/i.test(combinationsText)) fail('latest is forbidden');
if (lockText.includes('REQUIRED_AT_M0') || combinationsText.includes('REQUIRED_AT_M0')) fail('placeholder REQUIRED_AT_M0 is forbidden');
if (/^\s*state:\s*verified\s*$/m.test(lockText) || /^\s*state:\s*verified\s*$/m.test(combinationsText)) {
  fail('lock must not mark capabilities verified');
}

const families = [...lockText.matchAll(/^\s*family:\s*(\S+)\s*$/gm)].map((match) => match[1]);
const arches = [...lockText.matchAll(/^\s*arch:\s*(\S+)\s*$/gm)].map((match) => match[1]);
const assetHashes = [...lockText.matchAll(/^\s*asset_sha256:\s*([0-9a-f]{64})\s*$/gm)].map((match) => match[1]);
const binaryHashes = [...lockText.matchAll(/^\s*binary_sha256:\s*([0-9a-f]{64})\s*$/gm)].map((match) => match[1]);
const urls = [...lockText.matchAll(/^\s*asset_url:\s*(\S+)\s*$/gm)].map((match) => match[1]);
if (assetHashes.length !== 6 || binaryHashes.length !== 6) fail('exactly six asset and binary digests are required');
if (new Set(families).size !== 3 || !['xray', 'sing-box', 'mihomo'].every((name) => families.includes(name))) {
  fail('xray, sing-box and mihomo are all required');
}
if (arches.filter((arch) => arch === 'amd64').length !== 3 || arches.filter((arch) => arch === 'arm64').length !== 3) {
  fail('both linux architectures are required for each family');
}
if (urls.some((url) => !url.startsWith('https://github.com/') || url.includes('latest'))) {
  fail('asset_url must be a pinned GitHub release URL');
}
if ((combinationsText.match(/id:\s*P0-/g) || []).length < 10) fail('P0 combination list must include the scope.md candidates');
if (!/state:\s*unverified/.test(combinationsText)) fail('P0 combinations remain unverified');
if (!lockText.includes('adapter_version: 0.0.0-unverified')) fail('adapter_version must remain 0.0.0-unverified until T-024');
if (!lockText.includes('fixture_set: isolation-v1')) fail('fixture_set must be isolation-v1');

console.log('PASS: cores.lock.yaml pins six official linux builds with unverified P0 combinations and no latest/verified placeholders.');
