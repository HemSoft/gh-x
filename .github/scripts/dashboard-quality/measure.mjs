import { Linter } from 'eslint';
import { builtinRules } from 'eslint/use-at-your-own-risk';

export const sourceRoots = ['src/codex-dashboard', 'src/dashboard-hub'];
export const fixtureExclusions = ['src/codex-dashboard/test-process-liveness.mjs'];

const complexityRule = builtinRules.get('complexity');
const percent = (covered, total) => total ? covered * 100 / total : 100;
const comparePosition = (a, b) => a.line - b.line || a.column - b.column;
const contains = (outer, inner) => comparePosition(outer.start, inner.start) <= 0 &&
  comparePosition(outer.end, inner.end) >= 0;

export function inventoryFunctions(source, file) {
  const functions = [];
  const rule = {
    meta: complexityRule.meta,
    create(context) {
      return complexityRule.create({
        sourceCode: context.sourceCode,
        options: [0],
        report({ node, data }) {
          const method = node.parent?.type === 'MethodDefinition' ||
            (node.parent?.type === 'Property' && node.parent.method);
          const location = method ? node.parent.loc : node.loc;
          functions.push({
            file, name: data.name, location, complexity: data.complexity,
            range: method ? node.parent.range : node.range,
            bodyStart: (node.body ?? node).range[0],
          });
        },
      });
    },
  };
  const messages = new Linter().verify(source, [{
    files: ['**/*.mjs'],
    languageOptions: { ecmaVersion: 'latest', sourceType: 'module' },
    plugins: { measurement: { rules: { complexity: rule } } },
    rules: { 'measurement/complexity': 'error' },
  }], { filename: file });
  if (messages.length) throw new Error(`${file}: ${messages.map(m => m.message).join('; ')}`);
  return functions.sort((a, b) => comparePosition(a.location.start, b.location.start));
}

function owner(functions, location) {
  return functions.filter(fn => contains(fn.location, location))
    .sort((a, b) => comparePosition(b.location.start, a.location.start))[0];
}

function functionHit(fn, rawFunctions) {
  const matches = rawFunctions.filter(entry => entry.endOffset === fn.range[1] &&
    entry.startOffset >= fn.range[0] && entry.startOffset <= fn.bodyStart);
  if (matches.length > 1) throw new Error(`Ambiguous function coverage: ${fn.file}:${fn.location.start.line}`);
  return matches.length ? matches[0].count : 0;
}

function metricsFor(coverage, functions) {
  const lines = new Map();
  for (const [id, entry] of Object.entries(coverage.statementMap)) {
    lines.set(entry.start.line, Math.max(lines.get(entry.start.line) ?? 0, coverage.s[id]));
  }
  const values = {
    lines: [...lines.values()], statements: Object.values(coverage.s),
    branches: Object.values(coverage.b).flat(), functions: functions.map(fn => fn.hits),
  };
  return Object.fromEntries(Object.entries(values).map(([metric, hits]) => {
    const covered = hits.filter(hit => hit > 0).length;
    return [metric, { covered, total: hits.length, percent: percent(covered, hits.length) }];
  }));
}

export function measureModule(file, source, coverage, rawFunctions) {
  if (!coverage) throw new Error(`Missing production module coverage: ${file}`);
  const counts = [...Object.values(coverage.s), ...Object.values(coverage.b).flat(), ...rawFunctions.map(fn => fn.count)];
  if (counts.some(count => !Number.isFinite(count) || count < 0)) throw new Error(`Invalid coverage counts: ${file}`);
  const functions = inventoryFunctions(source, file);
  const branches = new Map(functions.map(fn => [fn, []]));
  for (const [id, entry] of Object.entries(coverage.branchMap)) {
    entry.locations.forEach((location, index) => {
      const fn = owner(functions, location);
      if (fn) branches.get(fn).push(coverage.b[id][index]);
    });
  }
  for (const fn of functions) {
    fn.hits = functionHit(fn, rawFunctions);
    const hits = branches.get(fn);
    fn.coverageBasis = hits.length ? 'V8 block branches; nested functions excluded' : 'function execution (no owned V8 branches)';
    fn.coverage = fn.hits > 0 ? (hits.length ? percent(hits.filter(hit => hit > 0).length, hits.length) : 100) : 0;
    fn.branchCovered = hits.filter(hit => hit > 0).length;
    fn.branchTotal = hits.length;
    fn.crap = fn.complexity ** 2 * (1 - fn.coverage / 100) ** 3 + fn.complexity;
    fn.risk = fn.crap > 30 ? 'actionable' : fn.crap >= 15 ? 'review' : 'low';
  }
  return { file, metrics: metricsFor(coverage, functions), functions };
}

export function aggregate(modules) {
  return Object.fromEntries(['lines', 'statements', 'branches', 'functions'].map(metric => {
    const covered = modules.reduce((sum, module) => sum + module.metrics[metric].covered, 0);
    const total = modules.reduce((sum, module) => sum + module.metrics[metric].total, 0);
    return [metric, { covered, total, percent: percent(covered, total) }];
  }));
}

export function policyFindings(report, policy) {
  if (policy.maximumActionableCrap !== undefined && policy.maximumActionableCrap !== 30) throw new Error('The actionable CRAP threshold must remain 30');
  const findings = [];
  const actualFiles = report.modules.map(module => module.file).sort();
  const expectedFiles = Object.keys(policy.modules).sort();
  for (const file of new Set([...actualFiles, ...expectedFiles])) {
    if (!actualFiles.includes(file)) findings.push(`Missing maintained production module: ${file}`);
    if (!expectedFiles.includes(file)) findings.push(`Unreviewed production module: ${file}; add tests and review its baseline`);
  }
  for (const [metric, floor] of Object.entries(policy.minimum)) {
    if (report.metrics[metric].percent + 1e-9 < floor) findings.push(`${metric} coverage ${report.metrics[metric].percent.toFixed(4)}% < ${floor}%`);
  }
  for (const module of report.modules) {
    const baseline = policy.modules[module.file];
    if (!baseline) continue;
    for (const [metric, floor] of Object.entries(baseline.minimum)) {
      if (module.metrics[metric].percent + 1e-9 < floor) findings.push(`${module.file}: ${metric} coverage ${module.metrics[metric].percent.toFixed(4)}% < ${floor}%`);
    }
    const worst = Math.max(0, ...module.functions.map(fn => fn.crap));
    if (worst > baseline.maximumCrap + 1e-9) findings.push(`${module.file}: worst CRAP ${worst.toFixed(4)} > ${baseline.maximumCrap}`);
  }
  for (const fn of report.modules.flatMap(module => module.functions)) {
    if (fn.crap > 30) findings.push(`${fn.file}:${fn.location.start.line}:${fn.location.start.column + 1} ${fn.name}: CRAP ${fn.crap.toFixed(4)} > 30; simplify or add branch tests`);
  }
  return findings;
}
