import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { DatabaseSync } from "node:sqlite";
import { createCodexAdapter } from "../src/codex-dashboard/codex-adapter.mjs";
import { sampleCount } from "./compare.mjs";

const now = new Date("2026-09-01T12:00:00.000Z");
const workloads = { small: { sessions: 4, turns: 10 }, large: { sessions: 80, turns: 100 } };

async function fixture(home, size) {
    const database = new DatabaseSync(path.join(home, "state_5.sqlite"));
    try {
        database.exec(`CREATE TABLE threads (
            id TEXT PRIMARY KEY, rollout_path TEXT, created_at_ms INTEGER,
            updated_at_ms INTEGER, cwd TEXT, title TEXT, tokens_used INTEGER,
            model TEXT, git_branch TEXT, archived INTEGER)`);
        const insert = database.prepare("INSERT INTO threads VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)");
        let bytes = 0;
        for (let session = 0; session < size.sessions; session++) {
            const timestamp = new Date(now.getTime() - 60_000 - session).toISOString();
            const records = [{ timestamp, type: "event_msg", payload: { type: "task_started" } }];
            for (let turn = 0; turn < size.turns; turn++) {
                records.push(
                    { timestamp, type: "event_msg", payload: { type: "user_message", message: `Synthetic prompt ${turn}: café 界 ${"text ".repeat(20)}` } },
                    { timestamp, type: "response_item", payload: { type: "function_call", name: "shell_command", arguments: "{}" } },
                    { timestamp, type: "event_msg", payload: { type: "token_count", info: {
                        total_token_usage: { input_tokens: 1000, cached_input_tokens: 200, output_tokens: 100, total_tokens: 1100 },
                        last_token_usage: { input_tokens: 500, output_tokens: 50, total_tokens: 550 }, model_context_window: 100_000,
                    } } },
                );
            }
            const name = `rollout-${session}.jsonl`;
            const content = records.map((record) => JSON.stringify(record)).join("\n") + "\n";
            bytes += Buffer.byteLength(content);
            await writeFile(path.join(home, name), content);
            insert.run(`session-${session}`, name, now.getTime() - 120_000, now.getTime() - 60_000 - session,
                path.join(home, "synthetic-project"), "Synthetic session", 1100, "fixture-model", "fixture-branch");
        }
        return { ...size, records: size.sessions * (1 + size.turns * 3), bytes };
    } finally {
        database.close();
    }
}

function validate(snapshot, size) {
    assert.equal(snapshot.available, true, snapshot.diagnostics.message);
    assert.equal(snapshot.sessions.length, size.sessions);
    assert.equal(snapshot.totals.requests, size.sessions);
    assert.equal(snapshot.totals.toolCalls, size.sessions * size.turns);
    assert.equal(snapshot.totals.totalTokens, size.sessions * 1100);
}

async function sample(home, size) {
    global.gc();
    const before = process.memoryUsage().heapUsed;
    const adapter = createCodexAdapter({ codexHome: home, now: () => now });
    let start = performance.now();
    let snapshot = await adapter.getSnapshot();
    const coldMs = performance.now() - start;
    global.gc();
    const coldRetained = Math.max(0, process.memoryUsage().heapUsed - before);
    validate(snapshot, size);
    start = performance.now();
    snapshot = await adapter.getSnapshot();
    const warmMs = performance.now() - start;
    global.gc();
    const warmRetained = Math.max(0, process.memoryUsage().heapUsed - before);
    validate(snapshot, size);
    // Keep the adapter/cache live through memory measurement, not only its result.
    assert.equal(typeof adapter.getSnapshot, "function");
    return {
        cold: { ms: coldMs, retainedBytes: coldRetained },
        warm: { ms: warmMs, retainedBytes: warmRetained },
    };
}

const sizeName = process.argv[2];
assert.ok(Object.hasOwn(workloads, sizeName), "expected small or large workload");
assert.equal(typeof global.gc, "function", "run with --expose-gc");
const home = await mkdtemp(path.join(os.tmpdir(), "gh-x-perf-codex-"));
try {
    const size = await fixture(home, workloads[sizeName]);
    // Two complete pairs warm JIT/module/OS file caches. Each measured cold
    // snapshot still gets a new adapter with an empty application rollout cache.
    for (let i = 0; i < 2; i++) await sample(home, size);
    const samples = { [`node/cold/${sizeName}`]: [], [`node/warm/${sizeName}`]: [] };
    for (let i = 0; i < sampleCount; i++) {
        const result = await sample(home, size);
        samples[`node/cold/${sizeName}`].push(result.cold);
        samples[`node/warm/${sizeName}`].push(result.warm);
    }
    process.stdout.write(JSON.stringify({ workload: size, samples }) + "\n");
} finally {
    // home is the exact directory this process obtained from mkdtemp.
    await rm(home, { recursive: true, force: true });
}
