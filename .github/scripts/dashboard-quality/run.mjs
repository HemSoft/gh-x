import { spawnSync } from 'node:child_process';
import { mkdir, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { toolsDirectory } from './tools.mjs';

const root = fileURLToPath(new URL('../../../', import.meta.url));
const toolRoot = await toolsDirectory(root);
const require = createRequire(path.join(toolRoot, 'package.json'));
const output = path.resolve(process.argv[2] ?? path.join(root, 'coverage/dashboard'));
await mkdir(output, { recursive: true });
for (const name of ['quality.json', 'quality.md', 'failure.json', 'coverage-final.json']) {
  await rm(path.join(output, name), { force: true });
}

async function verifyTools() {
  const nodeVersion = (await readFile(path.join(root, '.node-version'), 'utf8')).trim();
  if (process.versions.node !== nodeVersion) throw new Error(`Node ${process.versions.node}; expected ${nodeVersion}`);
  const declared = JSON.parse(await readFile(path.join(root, 'package.json'), 'utf8'));
  for (const tool of ['c8', 'eslint']) {
    const installed = JSON.parse(await readFile(require.resolve(`${tool}/package.json`), 'utf8'));
    if (installed.version !== declared.devDependencies[tool]) throw new Error(`${tool} ${installed.version}; expected ${declared.devDependencies[tool]}; run node .github/scripts/dashboard-quality/install.mjs`);
  }
  // npm's dependency graph catches missing secondary tools, not only the two CLIs.
  const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';
  const check = spawnSync(npm, ['ls', '--all', '--omit=optional'], { cwd: toolRoot, encoding: 'utf8', shell: process.platform === 'win32' });
  if (check.error || check.status !== 0) throw new Error(`Quality dependencies incomplete; run node .github/scripts/dashboard-quality/install.mjs: ${check.error?.message ?? check.stderr}`);
  return { node: nodeVersion, ...declared.devDependencies };
}

async function rawFunctions(directory) {
  const byFile = new Map();
  for (const name of await readdir(directory)) {
    if (!name.endsWith('.json')) continue;
    const data = JSON.parse(await readFile(path.join(directory, name), 'utf8'));
    for (const script of data.result ?? []) {
      if (!script.url.startsWith('file:')) continue;
      const file = fileURLToPath(script.url);
      const functions = byFile.get(file) ?? new Map();
      byFile.set(file, functions);
      for (const fn of script.functions) {
        const range = fn.ranges[0];
        const key = `${range.startOffset}:${range.endOffset}`;
        const merged = functions.get(key) ?? { ...range, count: 0 };
        merged.count += range.count;
        functions.set(key, merged);
      }
    }
  }
  return byFile;
}

async function run() {
  const tools = await verifyTools();
  const { sourceRoots, fixtureExclusions, measureModule, aggregate, policyFindings } = await import('./measure.mjs');
  const files = (await Promise.all(sourceRoots.map(async directory =>
    (await readdir(path.join(root, directory), { recursive: true })).filter(name => name.endsWith('.mjs') && !name.endsWith('.test.mjs'))
      .map(name => `${directory}/${name.split(path.sep).join('/')}`))))
    .flat().filter(file => !fixtureExclusions.includes(file)).sort();
  const args = [require.resolve('c8/bin/c8.js'), '--all', '--clean', '--extension', '.mjs'];
  for (const directory of sourceRoots) args.push('--src', directory, '--include', `${directory}/**/*.mjs`);
  args.push('--exclude', '**/*.test.mjs');
  for (const fixture of fixtureExclusions) args.push('--exclude', fixture);
  args.push('--reports-dir', output, '--temp-directory', path.join(output, 'v8'), '--reporter', 'json', '--reporter', 'text',
    process.execPath, '--test', ...sourceRoots.map(directory => `${directory}/*.test.mjs`));
  const tests = spawnSync(process.execPath, args, { cwd: root, stdio: 'inherit' });
  if (tests.error) throw tests.error;
  const coverage = JSON.parse(await readFile(path.join(output, 'coverage-final.json'), 'utf8'));
  const functions = await rawFunctions(path.join(output, 'v8'));
  const modules = await Promise.all(files.map(async file => {
    const absolute = path.join(root, file);
    return measureModule(file, await readFile(absolute, 'utf8'), coverage[absolute], [...(functions.get(absolute)?.values() ?? [])]);
  }));
  const policy = JSON.parse(await readFile(path.join(root, '.github/dashboard-quality-policy.json'), 'utf8'));
  const report = { tools, scope: { sourceRoots, fixtureExclusions, generatedExclusions: [] },
    measurement: 'c8 V8 statements/lines/branches; AST function inventory and raw V8 execution ranges; ESLint classic cyclomatic complexity',
    metrics: aggregate(modules), modules };
  const findings = policyFindings(report, policy);
  if (tests.status !== 0) findings.unshift(`Dashboard tests failed: exit ${tests.status}; retained measurements do not qualify failed tests`);
  report.findings = findings;
  await writeFile(path.join(output, 'quality.json'), JSON.stringify(report, null, 2) + '\n');
  const rows = modules.flatMap(module => module.functions).sort((a, b) => b.crap - a.crap);
  const summary = ['# Dashboard quality', '', `Tools: Node ${tools.node}; c8 ${tools.c8}; ESLint ${tools.eslint}.`, '',
    '| Metric | Coverage |', '| --- | --- |',
    ...Object.entries(report.metrics).map(([name, value]) => `| ${name} | ${value.percent.toFixed(4)}% (${value.covered}/${value.total}) |`), '',
    '| Function | CC | Coverage basis | Coverage | CRAP | Risk |', '| --- | --- | --- | --- | --- | --- |',
    ...rows.map(fn => `| ${fn.file}:${fn.location.start.line}:${fn.location.start.column + 1} ${fn.name} | ${fn.complexity} | ${fn.coverageBasis} | ${fn.coverage.toFixed(4)}% | ${fn.crap.toFixed(4)} | ${fn.risk} |`), '',
    ...findings.map(finding => `- ${finding}`)].join('\n');
  await writeFile(path.join(output, 'quality.md'), summary + '\n');
  console.log(`Dashboard report: ${output}`);
  if (findings.length) throw new Error(findings.join('\n'));
}

try {
  await run();
} catch (error) {
  await writeFile(path.join(output, 'failure.json'), JSON.stringify({ error: error.message }, null, 2) + '\n');
  console.error(error.message);
  process.exitCode = 1;
}
