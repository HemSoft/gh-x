import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmod, copyFile, mkdir, mkdtemp, readFile, readdir, rm, symlink, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';
import { repositoryRoot, toolsDirectory } from './tools.mjs';

test('the installer refuses dependency source inside the Go checkout', () => {
  const child = spawnSync(process.execPath, [fileURLToPath(new URL('./install.mjs', import.meta.url))], {
    encoding: 'utf8', env: { ...process.env, GH_X_DASHBOARD_QUALITY_TOOLS: repositoryRoot },
  });
  assert.equal(child.status, 1);
  assert.match(child.stderr, /Quality tools must be installed outside the source checkout/);
});

async function copyRunner(root, names) {
  const scripts = path.join(root, '.github/scripts/dashboard-quality');
  await mkdir(scripts, { recursive: true });
  for (const name of names) await copyFile(new URL(name, import.meta.url), path.join(scripts, name));
  return scripts;
}

test('a symlinked tools destination cannot install into the checkout', async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'dashboard-symlink-tools-'));
  context.after(() => rm(root, { recursive: true, force: true }));
  const checkout = path.join(root, 'checkout');
  const scripts = await copyRunner(checkout, ['install.mjs', 'tools.mjs']);
  const inside = path.join(checkout, 'dependencies');
  await mkdir(inside);
  const alias = path.join(root, 'alias');
  await symlink(inside, alias, process.platform === 'win32' ? 'junction' : 'dir');
  // The nonexistent child also proves resolution through an existing symlinked ancestor.
  const child = spawnSync(process.execPath, [path.join(scripts, 'install.mjs')], {
    encoding: 'utf8', env: { ...process.env, GH_X_DASHBOARD_QUALITY_TOOLS: path.join(alias, 'new-tools') },
  });
  assert.equal(child.status, 1);
  assert.match(child.stderr, /Quality tools must be installed outside the source checkout/);
  assert.deepEqual(await readdir(inside), []);
});

test('a failed npm install retains its exit status without a success message', async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'dashboard-install-failure-'));
  context.after(() => rm(root, { recursive: true, force: true }));
  const checkout = path.join(root, 'checkout');
  const scripts = await copyRunner(checkout, ['install.mjs', 'tools.mjs']);
  await writeFile(path.join(checkout, 'package.json'), '{}');
  await writeFile(path.join(checkout, 'package-lock.json'), '{}');
  const bin = path.join(root, 'bin');
  await mkdir(bin);
  const npm = path.join(bin, process.platform === 'win32' ? 'npm.cmd' : 'npm');
  await writeFile(npm, process.platform === 'win32' ? '@exit /b 23\r\n' : '#!/bin/sh\nexit 23\n');
  await chmod(npm, 0o755);
  const child = spawnSync(process.execPath, [path.join(scripts, 'install.mjs')], {
    encoding: 'utf8', env: { ...process.env, PATH: `${bin}${path.delimiter}${process.env.PATH}`, GH_X_DASHBOARD_QUALITY_TOOLS: path.join(root, 'tools') },
  });
  assert.equal(child.status, 23, child.stdout + child.stderr);
  assert.match(child.stderr, /Locked npm ci failed: exit 23/);
  assert.doesNotMatch(child.stdout, /Isolated dashboard quality tools:/);
});

test('missing direct tools report the installer command', async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'dashboard-no-tools-'));
  context.after(() => rm(root, { recursive: true, force: true }));
  const scripts = await copyRunner(root, ['run.mjs', 'tools.mjs', 'failure.mjs', 'raw.mjs']);
  await copyFile(new URL('../../../package.json', import.meta.url), path.join(root, 'package.json'));
  await writeFile(path.join(root, '.node-version'), process.versions.node);
  const output = path.join(root, 'reports');
  await mkdir(path.join(output, 'v8'), { recursive: true });
  await writeFile(path.join(output, 'v8', 'stale.json'), 'previous run coverage');
  const child = spawnSync(process.execPath, [path.join(scripts, 'run.mjs'), output], {
    encoding: 'utf8', env: { ...process.env, GH_X_DASHBOARD_QUALITY_TOOLS: root },
  });
  assert.equal(child.status, 1);
  assert.match(child.stderr, /c8 quality tool unavailable/);
  const failure = JSON.parse(await readFile(path.join(output, 'failure.json'), 'utf8'));
  assert.match(failure.error, /run node \.github\/scripts\/dashboard-quality\/install\.mjs/);
  await assert.rejects(readFile(path.join(output, 'v8', 'stale.json')), { code: 'ENOENT' });
});

test('missing secondary tooling fails before tests and preserves a failure report', async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'dashboard-missing-dependency-'));
  context.after(() => rm(root, { recursive: true, force: true }));
  const scriptDirectory = path.join(root, '.github/scripts/dashboard-quality');
  await mkdir(scriptDirectory, { recursive: true });
  await copyFile(new URL('./run.mjs', import.meta.url), path.join(scriptDirectory, 'run.mjs'));
  await copyFile(new URL('./tools.mjs', import.meta.url), path.join(scriptDirectory, 'tools.mjs'));
  await copyFile(new URL('./failure.mjs', import.meta.url), path.join(scriptDirectory, 'failure.mjs'));
  await copyFile(new URL('./raw.mjs', import.meta.url), path.join(scriptDirectory, 'raw.mjs'));
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
  for (const name of ['run.mjs', 'tools.mjs', 'failure.mjs', 'raw.mjs']) {
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


test('nested production modules execute their nested behavior tests', async context => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'dashboard-nested-test-'));
  context.after(() => rm(root, { recursive: true, force: true }));
  const scripts = await copyRunner(root, ['run.mjs', 'tools.mjs', 'failure.mjs', 'raw.mjs', 'measure.mjs']);
  for (const name of ['package.json', 'package-lock.json', '.node-version']) {
    await copyFile(new URL(`../../../${name}`, import.meta.url), path.join(root, name));
  }
  const nested = path.join(root, 'src/codex-dashboard/nested');
  await mkdir(nested, { recursive: true });
  await mkdir(path.join(root, 'src/dashboard-hub'), { recursive: true });
  await writeFile(path.join(root, 'src/codex-dashboard/smoke.test.mjs'), "import test from 'node:test'; test('root', () => {});\n");
  await writeFile(path.join(root, 'src/dashboard-hub/smoke.test.mjs'), "import test from 'node:test'; test('hub', () => {});\n");
  await writeFile(path.join(nested, 'behavior.mjs'), 'export function twice(value) { return value * 2; }\n');
  await writeFile(path.join(nested, 'behavior.test.mjs'), "import assert from 'node:assert/strict'; import test from 'node:test'; import {twice} from './behavior.mjs'; test('nested behavior', () => assert.equal(twice(2), 4));\n");
  const metrics = { lines: 100, statements: 100, branches: 100, functions: 100 };
  await writeFile(path.join(root, '.github/dashboard-quality-policy.json'), JSON.stringify({
    maximumActionableCrap: 30, minimum: metrics,
    modules: { 'src/codex-dashboard/nested/behavior.mjs': { minimum: metrics, maximumCrap: 1 } },
  }));
  const output = path.join(root, 'reports');
  const environment = { ...process.env, GH_X_DASHBOARD_QUALITY_TOOLS: await toolsDirectory() };
  // This fixture starts an independent test runner, not a recursive node:test child.
  delete environment.NODE_TEST_CONTEXT;
  const child = spawnSync(process.execPath, [path.join(scripts, 'run.mjs'), output], {
    encoding: 'utf8', env: environment,
  });
  assert.equal(child.status, 0, child.stdout + child.stderr);
  assert.match(child.stdout, /nested behavior/);
  const report = JSON.parse(await readFile(path.join(output, 'quality.json'), 'utf8'));
  assert.equal(report.modules.length, 1);
  assert.equal(report.modules[0].functions[0].hits, 1);
  assert.equal(report.metrics.functions.percent, 100);
  assert.deepEqual(report.findings, []);
});
