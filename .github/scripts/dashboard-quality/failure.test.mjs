import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdir, mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

test('a real diagnostic write failure preserves the original terminal failure', async context => {
  const output = await mkdtemp(path.join(os.tmpdir(), 'quality-report-failure-'));
  context.after(() => rm(output, { recursive: true, force: true }));
  await mkdir(path.join(output, 'failure.json'));
  const module = new URL('./failure.mjs', import.meta.url).href;
  const expression = `import { recordFailure } from ${JSON.stringify(module)}; await recordFailure(new Error('Primary dashboard test failure'), ${JSON.stringify(output)});`;
  const child = spawnSync(process.execPath, ['--input-type=module', '-e', expression], { encoding: 'utf8' });
  assert.equal(child.status, 1);
  const lines = child.stderr.trim().split('\n');
  assert.equal(lines[0], 'Primary dashboard test failure');
  assert.match(lines[1], /^Failure report unavailable: /);
  assert.equal(lines.filter(line => line === 'Primary dashboard test failure').length, 1);
});
