import { readFileSync, readdirSync } from 'node:fs';
import { isDeepStrictEqual } from 'node:util';

const root = new URL('../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
function requireMatch(condition, message) {
  // Report only the field/file name, never source text or environment values.
  if (!condition) throw new Error(`Tool lock drift: ${message}`);
}
const lock = JSON.parse(read('deploy/tools.lock.json'));
requireMatch(lock.schema_version === 1, 'unsupported schema_version');
for (const name of ['go', 'node', 'pnpm']) {
  requireMatch(/^\d+\.\d+\.\d+$/.test(lock[name]), `${name} must have an exact version`);
}

const goMod = read('go.mod');
requireMatch(goMod.match(/^go\s+(\S+)\s*$/m)?.[1] === lock.go, 'go.mod Go version');
requireMatch(!/^\s*(replace|toolchain)\b/m.test(goMod), 'go.mod override requires explicit lock support');
const direct = {};
let inRequire = false;
for (const line of goMod.split(/\r?\n/)) {
  if (/^require\s*\(\s*$/.test(line)) { inRequire = true; continue; }
  if (inRequire && /^\s*\)\s*$/.test(line)) { inRequire = false; continue; }
  const entry = (inRequire ? line : line.replace(/^require\s+/, '')).match(/^\s*(\S+)\s+(v\S+)(?:\s+\/\/\s*(.*))?\s*$/);
  if (!entry || (!inRequire && !line.startsWith('require ')) || entry[3]?.trim() === 'indirect') continue;
  requireMatch(!Object.hasOwn(direct, entry[1]), 'duplicate Go dependency');
  direct[entry[1]] = entry[2];
}
requireMatch(isDeepStrictEqual(direct, lock.go_direct_dependencies), 'go.mod direct dependency set or versions');

const frontend = JSON.parse(read('proxyloom-web/package.json'));
requireMatch(frontend.engines?.node === lock.node, 'frontend engines.node');
requireMatch(frontend.packageManager === `pnpm@${lock.pnpm}`, 'frontend packageManager');
requireMatch(!Object.keys(frontend.optionalDependencies ?? {}).length && !Object.keys(frontend.peerDependencies ?? {}).length,
  'frontend optional/peer dependencies require explicit lock support');
const frontendDependencies = { ...frontend.dependencies, ...frontend.devDependencies };
requireMatch(Object.keys(frontendDependencies).length === Object.keys(frontend.dependencies ?? {}).length + Object.keys(frontend.devDependencies ?? {}).length,
  'frontend duplicate direct dependency');
requireMatch(isDeepStrictEqual(frontendDependencies, lock.frontend_direct_dependencies), 'frontend direct dependency set or versions');

const images = new Set(Object.values(lock.images));
for (const name of ['go_builder', 'node_builder', 'postgres', 'runtime']) {
  requireMatch(typeof lock.images[name] === 'string' && /^[^\s@]+@sha256:[0-9a-f]{64}$/.test(lock.images[name]), `images.${name} requires SHA-256 digest`);
}
requireMatch(lock.images.go_builder.startsWith(`golang:${lock.go}-`), 'go_builder tag/Go version');
requireMatch(lock.images.node_builder.startsWith(`node:${lock.node}-`), 'node_builder tag/Node version');
const dockerfiles = readdirSync(new URL('deploy/', root)).filter((name) => name.startsWith('Dockerfile.'));
for (const required of ['Dockerfile.api', 'Dockerfile.runner', 'Dockerfile.check']) requireMatch(dockerfiles.includes(required), `missing ${required}`);
for (const name of dockerfiles) {
  const source = read(`deploy/${name}`);
  const fromLines = source.split(/\r?\n/).filter((line) => /^\s*FROM\b/i.test(line));
  requireMatch(fromLines.length > 0, `${name} has no FROM`);
  for (const line of fromLines) {
    const image = line.match(/^\s*FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+\S+)?\s*$/i)?.[1];
    requireMatch(images.has(image) && /@sha256:[0-9a-f]{64}$/.test(image), `${name} FROM differs from locked images`);
  }
  for (const match of source.matchAll(/\bpnpm@([^\s;&]+)/g)) requireMatch(match[1] === lock.pnpm, `${name} pnpm version`);
}

// This check intentionally accepts the repository's simple YAML layout only;
// variables, aliases or alternative layouts must receive explicit lock support.
const compose = read('deploy/compose.dev.yaml');
const postgresBlocks = [...compose.matchAll(/^  postgres:\s*\r?\n((?:(?: {4}[^\r\n]*|[ \t]*)\r?\n)*)/gm)];
requireMatch(postgresBlocks.length === 1, 'Compose postgres service layout');
const postgresImages = [...postgresBlocks[0][1].matchAll(/^    image:\s*(\S+)\s*$/gm)];
requireMatch(postgresImages.length === 1 && postgresImages[0][1] === lock.images.postgres, 'Compose postgres image');

const workflow = read('.github/workflows/check.yml');
for (const [field, value] of [['go-version', lock.go], ['node-version', lock.node]]) {
  const declarations = [...workflow.matchAll(new RegExp(`^\\s+${field}:\\s*['\"]?([^'\"\\s]+)['\"]?\\s*$`, 'gm'))];
  requireMatch(declarations.length > 0 && declarations.every((match) => match[1] === value), `CI ${field}`);
}
const pnpmVersions = [...workflow.matchAll(/\bpnpm@([^\s;&]+)/g)];
requireMatch(pnpmVersions.length > 0 && pnpmVersions.every((match) => match[1] === lock.pnpm), 'CI pnpm version');
console.log('PASS: tool versions, direct dependencies, Docker base digests, Compose PostgreSQL and CI versions match tools.lock.json.');
