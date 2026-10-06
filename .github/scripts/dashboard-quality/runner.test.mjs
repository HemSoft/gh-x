import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

test('missing secondary tooling fails before tests and preserves a failure report', async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'dashboard-missing-dependency-'));
  context.after(() => rm(root, { recursive: true, force: true }));
  const scriptDirectory = path.join(root, '.github/scripts/dashboard-quality');
  await mkdir(scriptDirectory, { recursive: true });
  await copyFile(new URL('./run.mjs', import.meta.url), path.join(scriptDirectory, 'run.mjs'));
  const declared = JSON.parse(await readFile(new URL('../../../package.json', import.meta.url), 'utf8'));
  await writeFile(path.join(root, 'package.json'), JSON.stringify(declared));
  await writeFile(path.join(root, '.node-version'), process.versions.node);
  for (const [name, version] of Object.entries(declared.devDependencies)) {
    const directory = path.join(root, 'node_modules', name);
    await mkdir(directory, { recursive: true });
    await writeFile(path.join(directory, 'package.json'), JSON.stringify({ name, version, dependencies: { 'missing-quality-secondary-fixture': '1.0.0' } }));
  }
  const output = path.join(root, 'reports');
  const child = spawnSync(process.execPath, [path.join(scriptDirectory, 'run.mjs'), output], { encoding: 'utf8' });
  assert.equal(child.status, 1, child.stdout + child.stderr);
  const failure = JSON.parse(await readFile(path.join(output, 'failure.json'), 'utf8'));
  assert.match(failure.error, /Quality dependencies incomplete/);
  assert.match(failure.error, /missing-quality-secondary-fixture/);
  await assert.rejects(readFile(path.join(output, 'quality.json')), { code: 'ENOENT' });
});
