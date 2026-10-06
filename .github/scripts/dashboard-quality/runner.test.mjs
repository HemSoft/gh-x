import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { copyFile, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { repositoryRoot } from './tools.mjs';

test('the installer refuses dependency source inside the Go checkout', () => {
  const child = spawnSync(process.execPath, [fileURLToPath(new URL('./install.mjs', import.meta.url))], {
    encoding: 'utf8', env: { ...process.env, GH_X_DASHBOARD_QUALITY_TOOLS: repositoryRoot },
  });
  assert.equal(child.status, 1);
  assert.match(child.stderr, /Quality tools must be installed outside the source checkout/);
});

test('missing secondary tooling fails before tests and preserves a failure report', async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'dashboard-missing-dependency-'));
  context.after(() => rm(root, { recursive: true, force: true }));
  const scriptDirectory = path.join(root, '.github/scripts/dashboard-quality');
  await mkdir(scriptDirectory, { recursive: true });
  await copyFile(new URL('./run.mjs', import.meta.url), path.join(scriptDirectory, 'run.mjs'));
  await copyFile(new URL('./tools.mjs', import.meta.url), path.join(scriptDirectory, 'tools.mjs'));
  await copyFile(new URL('./failure.mjs', import.meta.url), path.join(scriptDirectory, 'failure.mjs'));
  const declared = JSON.parse(await readFile(new URL('../../../package.json', import.meta.url), 'utf8'));
  await writeFile(path.join(root, 'package.json'), JSON.stringify(declared));
  await writeFile(path.join(root, '.node-version'), process.versions.node);
  for (const [name, version] of Object.entries(declared.devDependencies)) {
    const directory = path.join(root, 'node_modules', name);
    await mkdir(directory, { recursive: true });
    await writeFile(path.join(directory, 'package.json'), JSON.stringify({ name, version, dependencies: { 'missing-quality-secondary-fixture': '1.0.0' } }));
  }
  const output = path.join(root, 'reports');
  const child = spawnSync(process.execPath, [path.join(scriptDirectory, 'run.mjs'), output], {
    encoding: 'utf8', env: { ...process.env, GH_X_DASHBOARD_QUALITY_TOOLS: root },
  });
  assert.equal(child.status, 1, child.stdout + child.stderr);
  const failure = JSON.parse(await readFile(path.join(output, 'failure.json'), 'utf8'));
  assert.match(failure.error, /Quality dependencies incomplete/);
  assert.match(failure.error, /missing-quality-secondary-fixture/);
  await assert.rejects(readFile(path.join(output, 'quality.json')), { code: 'ENOENT' });
});

test('failed dashboard tests remain the primary error when coverage is missing', async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'dashboard-failed-test-'));
  context.after(() => rm(root, { recursive: true, force: true }));
  const scripts = path.join(root, '.github/scripts/dashboard-quality');
  await mkdir(scripts, { recursive: true });
  for (const name of ['run.mjs', 'tools.mjs', 'failure.mjs']) {
    await copyFile(new URL(name, import.meta.url), path.join(scripts, name));
  }
  await writeFile(path.join(scripts, 'measure.mjs'), "export const sourceRoots = ['src/codex-dashboard']; export const fixtureExclusions = [];\n");
  await mkdir(path.join(root, 'src/codex-dashboard'), { recursive: true });
  const declared = JSON.parse(await readFile(new URL('../../../package.json', import.meta.url), 'utf8'));
  await writeFile(path.join(root, 'package.json'), JSON.stringify(declared));
  await writeFile(path.join(root, '.node-version'), process.versions.node);
  for (const [name, version] of Object.entries(declared.devDependencies)) {
    const directory = path.join(root, 'node_modules', name);
    await mkdir(directory, { recursive: true });
    await writeFile(path.join(directory, 'package.json'), JSON.stringify({ name, version }));
  }
  const bin = path.join(root, 'node_modules/c8/bin');
  await mkdir(bin);
  await writeFile(path.join(bin, 'c8.js'), 'process.exit(23);\n');
  const output = path.join(root, 'reports');
  const child = spawnSync(process.execPath, [path.join(scripts, 'run.mjs'), output], {
    encoding: 'utf8', env: { ...process.env, GH_X_DASHBOARD_QUALITY_TOOLS: root },
  });
  assert.equal(child.status, 1, child.stdout + child.stderr);
  assert.match(child.stderr, /^Dashboard tests failed: exit 23/m);
  const failure = JSON.parse(await readFile(path.join(output, 'failure.json'), 'utf8'));
  assert.match(failure.error, /^Dashboard tests failed: exit 23/);
  assert.match(failure.error, /coverage-final\.json/);
});
