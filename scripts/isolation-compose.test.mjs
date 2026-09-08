import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtempSync, existsSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { runIsolationCompose, subnetsOverlap } from './verify-isolation-compose.mjs';

function harness(t, override = () => undefined) {
  const outputRoot = mkdtempSync(join(tmpdir(), 'isolation-compose-'));
  t.after(() => rmSync(outputRoot, { recursive: true, force: true }));
  const calls = [];
  let output = '';
  const options = {
    outputRoot,
    prepareTest: () => {},
    write: (value) => { output += value; },
    execute: (args) => {
      calls.push(args);
      return override(args, calls) ?? { status: 0, stdout: args[0] === 'version' ? 'linux/amd64' : '', stderr: '' };
    },
  };
  return { options, calls, output: () => output };
}

function isCompose(args, operation) {
  return args[0] === 'compose' && args[5] === operation;
}

for (const resource of ['stopped container', 'orphan container', 'network', 'volume']) {
  test(`existing ${resource} refuses all mutations`, (t) => {
    const kind = resource.includes('container') ? 'container' : resource;
    const h = harness(t, (args) => {
      if (args[0] === kind && args.includes('--filter')) {
        if (kind === 'container') assert.ok(args.includes('-a'), 'inventory must include stopped/orphan containers');
        assert.match(args[args.indexOf('--filter') + 1], /^label=com\.docker\.compose\.project=proxyloom-isolation-t028-/);
        return { status: 0, stdout: 'existing-resource' };
      }
    });
    assert.throws(() => runIsolationCompose(h.options), /already has/);
    assert.ok(h.calls.every((args) => args[0] !== 'compose' && args[0] !== 'run'));
    assert.ok(!h.output().includes('PASS'));
  });
}

for (const query of ['container', 'network', 'volume', 'all networks', 'inspect']) {
  test(`${query} query failure fails closed`, (t) => {
    const h = harness(t, (args) => {
      const matches = query === 'all networks' ? args[0] === 'network' && args[1] === 'ls' && !args.includes('--filter')
        : query === 'inspect' ? args[1] === 'inspect'
          : args[0] === query && args.includes('--filter');
      if (matches) return { status: 1, stdout: '', stderr: 'unavailable' };
      if (args[0] === 'network' && !args.includes('--filter')) return { status: 0, stdout: 'network-id' };
    });
    assert.throws(() => runIsolationCompose(h.options), /query failed/);
    assert.ok(h.calls.every((args) => args[0] !== 'compose' && args[0] !== 'run'));
  });
}

for (const subnet of ['172.30.0.0/16', '172.30.252.0/23', '172.30.253.128/25', '172.30.253.0/24']) {
  test(`occupied overlapping ${subnet} does not change existing resources`, (t) => {
    const h = harness(t, (args) => {
      if (args[0] === 'network' && args[1] === 'ls' && !args.includes('--filter')) return { status: 0, stdout: 'network-id' };
      if (args[1] === 'inspect') return { status: 0, stdout: JSON.stringify([{ Name: 'existing', IPAM: { Config: [{ Subnet: subnet }] } }]) };
    });
    assert.throws(() => runIsolationCompose(h.options), /overlaps existing network/);
    assert.ok(h.calls.every((args) => args[0] !== 'compose' && args[0] !== 'run'));
  });
}

test('CIDR overlap handles boundaries, host bits and IPv6', () => {
  assert.equal(subnetsOverlap('172.30.253.0/24', '172.30.252.0/24'), false);
  assert.equal(subnetsOverlap('172.30.253.0/24', '172.30.254.0/24'), false);
  assert.equal(subnetsOverlap('172.30.253.0/24', '172.30.253.1/32'), true);
  assert.equal(subnetsOverlap('172.30.253.0/24', '0.0.0.0/0'), true);
  assert.equal(subnetsOverlap('172.30.253.0/24', 'fd00::/64'), false);
  assert.throws(() => subnetsOverlap('172.30.253.0/24', 'invalid'), /invalid Docker subnet/);
});

for (const throws of [false, true]) {
  test(`partial up failure (${throws ? 'exception' : 'exit code'}) cleans only this run`, (t) => {
    const h = harness(t, (args) => {
      if (isCompose(args, 'up')) {
        if (throws) throw new Error('partial startup');
        return { status: 1, stderr: 'partial startup' };
      }
    });
    assert.throws(() => runIsolationCompose(h.options), /partial startup/);
    const up = h.calls.find((args) => isCompose(args, 'up'));
    const downs = h.calls.filter((args) => isCompose(args, 'down'));
    assert.equal(downs.length, 1);
    assert.equal(downs[0][2], up[2]);
    assert.ok(!h.output().includes('PASS'));
    assert.equal(existsSync(join(h.options.outputRoot, up[2], 'compose-smoke-report.json')), false);
  });
}

test('cleanup failure cannot publish a pass report or message', (t) => {
  const h = harness(t, (args) => isCompose(args, 'down') ? { status: 1, stderr: 'cleanup error' } : undefined);
  assert.throws(() => runIsolationCompose(h.options), /compose down failed/);
  const project = h.calls.find((args) => isCompose(args, 'up'))[2];
  assert.equal(existsSync(join(h.options.outputRoot, project, 'compose-smoke-report.json')), false);
  assert.ok(!h.output().includes('PASS'));
});

test('phase and cleanup exceptions preserve both failures without a pass', (t) => {
  const h = harness(t, (args) => {
    if (args[0] === 'run') throw new Error('phase error');
    if (isCompose(args, 'down')) throw new Error('cleanup error');
  });
  assert.throws(() => runIsolationCompose(h.options), (error) => {
    assert.ok(error instanceof AggregateError);
    assert.match(error.message, /phase error.*cleanup error/);
    return true;
  });
  assert.equal(h.calls.filter((args) => isCompose(args, 'down')).length, 1);
  assert.ok(!h.output().includes('PASS'));
});

test('successful runs isolate project and output, publish only after cleanup', (t) => {
  const h = harness(t, (args) => {
    if (isCompose(args, 'down')) {
      assert.equal(existsSync(join(h.options.outputRoot, args[2], 'compose-smoke-report.json')), false);
    }
  });
  const a = runIsolationCompose(h.options);
  const b = runIsolationCompose(h.options);
  assert.notEqual(a.project, b.project);
  assert.notEqual(a.outDir, b.outDir);
  for (const result of [a, b]) {
    assert.equal(JSON.parse(readFileSync(result.reportPath, 'utf8')).project, result.project);
    assert.ok(h.output().includes(result.reportPath));
    assert.ok(existsSync(join(result.outDir, 'compose-smoke-startup.txt')));
  }
  assert.equal(h.calls.filter((args) => isCompose(args, 'down')).length, 2);
});
