import { spawnSync } from 'node:child_process';
import { assertSecretFree, safeLabel } from './quality-secrets.mjs';

// Always preserve failure. No log regex, skipped test, or earlier success can
// convert a nonzero subprocess exit into a passing quality report.
export function runQualitySteps(steps, { root, secrets = [], output = (text) => process.stdout.write(text) } = {}) {
  const report = { schema_version: 1, result: 'fail', steps: [] };
  let failed = false;
  for (const step of steps) {
    if (!/^[a-z0-9-]+$/.test(step.name)) throw new Error('quality_step_name_invalid');
    if (failed) {
      report.steps.push({ name: step.name, result: 'not_run' });
      continue;
    }
    output(`> ${step.name}\n`);
    const started = Date.now();
    const environment = { ...process.env, ...(step.env ?? {}) };
    // A nested Node test runner must execute as an independent process. Node's
    // internal inherited test context otherwise suppresses its normal CLI mode.
    delete environment.NODE_TEST_CONTEXT;
    const child = spawnSync(step.command, step.args, { cwd: step.cwd ?? root, windowsHide: true, shell: false,
      env: environment, encoding: 'utf8', timeout: step.timeout ?? 600000, maxBuffer: 16 * 1024 * 1024 });
    const entry = { name: step.name, result: 'fail', exit_code: child.status, duration_ms: Date.now() - started };
    let safe = true;
    for (const [channel, text] of [['stdout', child.stdout ?? ''], ['stderr', child.stderr ?? '']]) {
      try {
        assertSecretFree(text, { path: `${step.name}/${channel}`, secrets });
      } catch {
        safe = false;
        entry.error = 'secret-output-withheld';
      }
    }
    if (safe) {
      // Keep live diagnostics bounded and neutralize terminal/workflow commands.
      const clean = (value) => value.replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, '')
        .replace(/[\x00-\x08\x0b-\x1f\x7f-\x9f\u202a-\u202e\u2066-\u2069]/g, '')
        .replaceAll('::', ': :').slice(-65536);
      output(clean(`${child.stdout ?? ''}${child.stderr ?? ''}`));
      if (child.error) entry.error = 'subprocess-unavailable-timeout-or-output-limit';
      else if (step.expected_stdout !== undefined && child.stdout?.trim() !== step.expected_stdout) entry.error = 'locked-tool-version-mismatch';
      else if (child.status === 0) entry.result = 'pass';
    } else output(`FAIL: ${safeLabel(step.name)} output was withheld by the secret scanner.\n`);
    report.steps.push(entry);
    failed = entry.result !== 'pass';
  }
  report.result = !failed && report.steps.length && report.steps.every(({ result }) => result === 'pass') ? 'pass' : 'fail';
  return report;
}
