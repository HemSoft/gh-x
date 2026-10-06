import assert from 'node:assert/strict';
import test from 'node:test';
import { inventoryFunctions, measureModule, aggregate, policyFindings } from './measure.mjs';

const file = 'src/codex-dashboard/fixture.mjs';
const source = 'function same(value = 1) { if (value) return 1; return 0; }\nfunction outer() { return () => true; }';

function fixture() {
  const functions = inventoryFunctions(source, file);
  const coverage = { statementMap: {}, s: {}, fnMap: {}, f: {}, branchMap: {}, b: {} };
  const raw = functions.map(fn => ({ startOffset: fn.range[0], endOffset: fn.range[1], count: 1 }));
  functions.forEach((fn, index) => {
    coverage.statementMap[index] = fn.location;
    coverage.s[index] = 1;
    coverage.branchMap[index] = { locations: [fn.location] };
    coverage.b[index] = [1];
  });
  return { coverage, raw, functions };
}

function measured() {
  const { coverage, raw } = fixture();
  const module = measureModule(file, source, coverage, raw);
  return { modules: [module], metrics: aggregate([module]) };
}

function baseline(report) {
  return { minimum: Object.fromEntries(Object.entries(report.metrics).map(([name, value]) => [name, value.percent])),
    modules: Object.fromEntries(report.modules.map(module => [module.file, {
      minimum: Object.fromEntries(Object.entries(module.metrics).map(([name, value]) => [name, value.percent])),
      maximumCrap: Math.max(...module.functions.map(fn => fn.crap)),
    }])) };
}

test('classic complexity includes defaults and nested functions remain separate', () => {
  const functions = inventoryFunctions(source, file);
  assert.deepEqual(functions.map(fn => fn.complexity), [3, 1, 1]);
  assert.deepEqual(functions.map(fn => fn.location.start.line), [1, 2, 2]);
});

test('raw V8 ranges include anonymous functions omitted from Istanbul fnMap', () => {
  const { coverage, raw } = fixture();
  const module = measureModule(file, source, coverage, raw);
  assert.equal(module.metrics.functions.percent, 100);
  assert.equal(module.functions[1].branchTotal, 1);
  assert.equal(module.functions[2].branchTotal, 1);
  assert.deepEqual(module.functions.map(fn => fn.crap), [3, 1, 1]);
});

test('missing reports and entirely unexecuted modules cannot receive passing scores', () => {
  assert.throws(() => measureModule(file, source, null, []), /Missing production module/);
  const { coverage } = fixture();
  coverage.s = { 0: 0, 1: 0, 2: 0 };
  coverage.b = { 0: [0], 1: [0], 2: [0] };
  const module = measureModule(file, source, coverage, []);
  assert.equal(module.metrics.functions.percent, 0);
  assert.equal(module.metrics.lines.percent, 0);
  assert.deepEqual(module.functions.map(fn => fn.crap), [12, 2, 2]);
});

test('negative fixtures reject missing or new modules, covered behavior loss and worst CRAP growth', () => {
  const report = measured();
  const policy = baseline(report);
  assert.deepEqual(policyFindings(report, policy), []);
  assert.match(policyFindings({ ...report, modules: [] }, policy).join('\n'), /Missing maintained production module/);
  const added = structuredClone(report);
  added.modules.push({ ...added.modules[0], file: 'src/codex-dashboard/new-untested.mjs' });
  assert.match(policyFindings(added, policy).join('\n'), /Unreviewed production module/);
  const lost = fixture();
  lost.coverage.s[0] = 0;
  const module = measureModule(file, source, lost.coverage, lost.raw);
  assert.match(policyFindings({ modules: [module], metrics: aggregate([module]) }, policy).join('\n'), /lines coverage/);
  const risk = structuredClone(report);
  risk.modules[0].functions[0].crap += 1;
  assert.match(policyFindings(risk, policy).join('\n'), /worst CRAP/);
  risk.modules[0].functions[0].crap = 31;
  assert.match(policyFindings(risk, policy).join('\n'), /CRAP 31.* > 30; simplify/);
});

test('same function names in separate modules keep independent risk evidence', () => {
  const { coverage, raw } = fixture();
  const covered = measureModule(file, source, coverage, raw);
  const uncovered = measureModule('src/dashboard-hub/fixture.mjs', source, coverage, []);
  assert.equal(covered.functions[0].crap, 3);
  assert.equal(uncovered.functions[0].crap, 12);
  assert.equal(uncovered.functions[0].coverage, 0);
});
