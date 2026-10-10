import assert from "node:assert/strict";
import { test } from "node:test";
import { parseStatusAcquisition, compareStatusAcquisition } from "./status.mjs";

const samples = () => Array.from({ length: 7 }, () => ({ mode: "warm", ms: 20, gh: 0, keyring: 0 }));
const limits = { warm: { ms: 250, gh: 0, keyring: 0 } };

test("status parser requires actual acquisition output", () => {
    assert.throws(() => parseStatusAcquisition("PASS"), /Missing/);
    assert.deepEqual(parseStatusAcquisition(`STATUS_ACQUISITION=${JSON.stringify(samples())}\n`), samples());
});

test("status budget catches every warm subprocess and rejects missing evidence", () => {
    assert.equal(compareStatusAcquisition(samples(), limits).passed, true);
    const requests = samples(); requests[0].gh = 1;
    assert.equal(compareStatusAcquisition(requests, limits).passed, false);
    assert.throws(() => compareStatusAcquisition(samples().slice(1), limits), /seven/);
    const invalid = samples(); invalid[0].ms = Number.NaN;
    assert.throws(() => compareStatusAcquisition(invalid, limits), /invalid/);
});

test("status time budget uses the median", () => {
    const slow = samples(); for (const sample of slow) sample.ms = 251;
    assert.equal(compareStatusAcquisition(slow, limits).passed, false);
    slow[0].ms = 20; slow[1].ms = 20; slow[2].ms = 20; slow[3].ms = 20;
    assert.equal(compareStatusAcquisition(slow, limits).passed, true);
});
