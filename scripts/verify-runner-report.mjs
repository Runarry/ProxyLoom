import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

const sha = (data) => createHash('sha256').update(data).digest('hex');
export function runnerEvidence(root, suite, secrets = []) {
  const sourceHash = () => {
    const result = spawnSync('git', ['ls-files', '--cached', '--others', '--exclude-standard', '--',
      'internal', 'compat', 'api', 'migrations', 'proxyloom-runner', 'go.mod', 'go.sum', 'fixtures/runner/validate', 'deploy/tools.lock.json', 'scripts/verify-runner-*.mjs'], { cwd: root, encoding: 'utf8', shell: false });
    assert.equal(result.status, 0, 'Cannot identify verification source inputs');
    const files = [...new Set(result.stdout.trim().split(/\r?\n/).filter((path) => /\.(?:go|json|ya?ml|sql|mjs)$/.test(path) || /^go\.(mod|sum)$/.test(path)))].sort();
    return sha(JSON.stringify(files.map((path) => [path, sha(readFileSync(join(root, path)))])));
  };
  const before = sourceHash();
  let output = '';
  let binaryHash = null;
  return {
    binary(path) { binaryHash = sha(readFileSync(path)); },
    forward(stdout, stderr) {
      const text = (stdout ?? '') + (stderr ?? '');
      if (secrets.some((value) => value && text.includes(value))) throw new Error('Verification output contained a secret; output was withheld.');
      output += text;
      process.stdout.write(stdout ?? '');
      process.stderr.write(stderr ?? '');
    },
    finish(status) {
      const directory = join(root, '.cache', suite);
      mkdirSync(directory, { recursive: true });
      const report = { schema_version: 1, suite, status, generated_at: new Date().toISOString(), source_sha256_before: before, source_sha256_after: sourceHash(), test_binary_sha256: binaryHash };
      writeFileSync(join(directory, 'output.txt'), output);
      writeFileSync(join(directory, 'report.json'), JSON.stringify(report, null, 2) + '\n');
      console.log(JSON.stringify(report));
    },
  };
}
