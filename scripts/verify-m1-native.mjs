import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync, readdirSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = fileURLToPath(new URL('../', import.meta.url));
const cache = join(root, '.cache', 'm1-native-compiler');
const evidence = join(root, 'docs', 'evidence', 'T-029');
mkdirSync(cache, { recursive: true });
mkdirSync(evidence, { recursive: true });
const image = JSON.parse(readFileSync(join(root, 'deploy', 'tools.lock.json'), 'utf8')).images.runtime;
if (!image?.includes('@sha256:')) throw new Error('Pinned runtime image required');
const binary = join(cache, 'compiler.test');
const hash = (data) => createHash('sha256').update(data).digest('hex');
function sourceManifest() {
  const files = [];
  function walk(path) { for (const entry of readdirSync(path, { withFileTypes: true })) { const child = join(path, entry.name); if (entry.isDirectory()) walk(child); else files.push(child); } }
  for (const dir of ['internal/compiler', 'internal/adapter', 'internal/ir', 'internal/capability', 'schemas', 'compat', 'fixtures/compiler']) walk(join(root, dir));
  files.push(join(root, 'go.mod'), join(root, 'go.sum'), fileURLToPath(import.meta.url));
  return files.sort().map((path) => ({ path: relative(root, path).replaceAll('\\', '/'), sha256: hash(readFileSync(path)) }));
}
const sources = sourceManifest();
const compile = spawnSync('go', ['test', '-mod=readonly', '-c', '-o', binary, './internal/compiler'], {
  cwd: root, encoding: 'utf8', shell: false, env: { ...process.env, GOOS: 'linux', GOARCH: 'amd64', CGO_ENABLED: '0', GOCACHE: join(root, '.cache', 'go-build-m1-compiler') },
});
if (compile.status !== 0) throw new Error(compile.stderr || compile.stdout);
const result = spawnSync('docker', [
  'run', '--rm', '--network', 'none', '--read-only', '--cap-drop', 'ALL',
  '--security-opt', 'no-new-privileges', '--pids-limit', '256', '--memory', '512m',
  '--tmpfs', '/tmp:exec,mode=1777',
  '-v', `${cache.replaceAll('\\', '/') }:/suite/internal/compiler:ro`,
  '-v', `${join(root, 'fixtures').replaceAll('\\', '/') }:/suite/fixtures:ro`,
  '-v', `${join(root, '.cache', 'cores').replaceAll('\\', '/') }:/cores:ro`,
  '-v', `${evidence.replaceAll('\\', '/')}:/out`,
  '-w', '/suite/internal/compiler',
  '-e', 'PROXYLOOM_M1_NATIVE=1', '-e', 'PROXYLOOM_CORES_ROOT=/cores', '-e', 'PROXYLOOM_M1_REPORT=/out',
  image, '/bin/sh', '-c',
  'cp /suite/internal/compiler/compiler.test /tmp/compiler.test && chmod 0755 /tmp/compiler.test && /tmp/compiler.test -test.v -test.count=1 -test.timeout=5m -test.run=^TestM1Native',
], { cwd: root, encoding: 'utf8', shell: false, timeout: 360000, maxBuffer: 8 * 1024 * 1024 });
writeFileSync(join(evidence, 'native-compiler-output.txt'), `${result.stdout || ''}\n${result.stderr || ''}`);
process.stdout.write(result.stdout || '');
process.stderr.write(result.stderr || '');
if (result.status !== 0) throw new Error(`Native compiler matrix failed (${result.status})`);
if (JSON.stringify(sourceManifest()) !== JSON.stringify(sources)) throw new Error('Source changed during native run; rerun on the stable source state');
writeFileSync(join(evidence, 'native-compiler-source.json'), `${JSON.stringify({ image, network: 'none', platform: 'linux/amd64', binary_sha256: hash(readFileSync(binary)), sources }, null, 2)}\n`);
console.log('PASS: pinned linux/amd64 protocol, orchestration, selector and failure checks.');
