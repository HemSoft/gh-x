import assert from "node:assert/strict";
import test from "node:test";
import { compare, parseGo } from "./compare.mjs";

const samples = (values) => ({ path: values.map((ns) => ({ ns })) });
const budgets = { path: { ns: 100 } };

test("isolated timing noise passes but sustained regression fails", () => {
    assert.equal(compare(samples([90, 90, 90, 90, 90, 900, 900]), budgets).passed, true);
    const result = compare(samples([90, 110, 110, 110, 110, 110, 110]), budgets);
    assert.equal(result.passed, false);
    assert.match(result.errors[0], /6\/7 samples/);
    assert.equal(compare(samples(Array(7).fill(100)), budgets).passed, true);
});

test("missing, extra, truncated and non-finite evidence fails closed", () => {
    for (const values of [[], [1], Array(8).fill(1), [1, 1, 1, 1, 1, 1, NaN], [1, 1, 1, 1, 1, 1, -1]]) {
        assert.equal(compare(samples(values), budgets).passed, false);
    }
    assert.equal(compare({}, budgets).passed, false);
    assert.equal(compare(samples(Array(7).fill(1)), {}).passed, false);
    assert.equal(compare(samples(Array(7).fill(1)), { path: { ns: Infinity } }).passed, false);
    assert.equal(compare(samples(Array(7).fill(1)), { path: {} }).passed, false);
});

test("baseline comparison retains ratios and rejects missing reference measurements", () => {
    const current = samples(Array(7).fill(80));
    const reference = samples(Array(7).fill(40));
    const result = compare(current, budgets, reference);
    assert.equal(result.passed, true);
    assert.equal(result.rows[0].baselineMedian, 40);
    assert.equal(result.rows[0].baselineRatio, 2);
    assert.equal(compare(current, budgets, {}).passed, false);
    assert.equal(compare(current, budgets, samples([40])).passed, false);
});

test("Go output parses fractional time, allocation metrics, CRLF and CPU suffixes", () => {
    const raw = "goos: linux\r\nBenchmarkCritical/PR/small-1 100 12.25 ns/op 24 B/op 2 allocs/op\r\nBenchmarkCritical/PR/small 200 13 ns/op 25 B/op 3 allocs/op\nPASS\n";
    assert.deepEqual(parseGo(raw), { "go/PR/small": [{ ns: 12.25, bytes: 24, allocs: 2 }, { ns: 13, bytes: 25, allocs: 3 }] });
});
