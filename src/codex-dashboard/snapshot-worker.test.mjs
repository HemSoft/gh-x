import assert from "node:assert/strict";
import test from "node:test";

import { createSnapshotWorker, SnapshotBusyError } from "./snapshot-worker.mjs";
import { createDashboardServer } from "./dashboard-server.mjs";
import { createDashboardHub } from "../dashboard-hub/hub-server.mjs";

const flush = () => new Promise(setImmediate);

for (const mode of ["standalone", "hub"]) {
    test(`${mode} stops abandoned reads before serving the current filter`, { timeout: 5_000 }, async (t) => {
        const reads = Array.from({ length: 2 }, () => Promise.withResolvers());
        const entered = Array.from({ length: 2 }, () => Promise.withResolvers());
        let started = 0;
        let stopped = 0;
        const getSnapshot = (filter, { signal } = {}) => {
            const index = started++;
            if (index >= 2) return { available: true, filter };
            entered[index].resolve();
            signal?.addEventListener("abort", () => {
                stopped++;
                reads[index].reject(signal.reason);
            }, { once: true });
            return reads[index].promise;
        };
        const server = mode === "standalone"
            ? await createDashboardServer({ token: "cancelled-read-fixture", getSnapshot })
            : await createDashboardHub({
                codexAdapter: { getSnapshot },
                copilotAdapter: { extensionDirectory: "fixture-only", getSnapshot: async () => ({ available: true }) },
                readFileImpl: async () => "<html><head></head><body>fixture-only</body></html>", port: 0,
            });
        const controllers = Array.from({ length: 2 }, () => new AbortController());
        t.after(async () => {
            controllers.forEach(controller => controller.abort());
            reads.forEach(read => read.resolve({ available: true }));
            await flush();
            await server.close();
        });
        const api = mode === "standalone" ? `${server.url}api/usage` : `${server.url}codex/api/usage`;
        const pending = controllers.map((controller, i) => fetch(`${api}?recentWindowMs=${i ? 86400000 : 3600000}`, {
            signal: controller.signal,
        }).catch(error => error));
        await Promise.all(entered.map(item => item.promise));
        controllers.forEach(controller => controller.abort());
        await Promise.all(pending);
        // Health round trip lets the server observe transport closure without sleeps.
        await fetch(`${server.url}api/health`);
        const recovered = await fetch(`${api}?recentWindowMs=21600000`);
        assert.equal(recovered.status, 200, "abandoned reads must not leave the new filter stuck at 503");
        assert.deepEqual((await recovered.json()).filter, { recentWindowMs: 21_600_000 });
        assert.equal(stopped, 2, "both underlying reads must stop, not merely release accounting slots");
    });
}

test("snapshot workers bound unfinished reads and release capacity only on settlement", async (t) => {
    const reads = Array.from({ length: 3 }, () => Promise.withResolvers());
    t.after(() => reads.forEach(read => read.resolve("cleanup")));
    const filters = [];
    const worker = createSnapshotWorker(filter => {
        filters.push(filter);
        return reads[filters.length - 1].promise;
    });
    const first = worker({ recentWindowMs: 3_600_000 });
    const second = worker({ recentWindowMs: 86_400_000 });
    const overflow = worker({ recentWindowMs: 21_600_000 }).catch(error => error);
    await flush();
    assert.equal(filters.length, 2);
    const error = await overflow;
    assert.ok(error instanceof SnapshotBusyError);
    assert.equal(error.code, "dashboard_snapshot_busy");
    reads[0].resolve("first");
    assert.equal(await first, "first");
    const recovered = worker({ recentWindowMs: 21_600_000 });
    assert.deepEqual(filters[2], { recentWindowMs: 21_600_000 });
    reads[2].resolve("recovered");
    assert.equal(await recovered, "recovered");
    reads[1].resolve("second");
    assert.equal(await second, "second");
});

test("snapshot workers release capacity after synchronous and asynchronous failure", async () => {
    for (const asyncFailure of [false, true]) {
        const worker = createSnapshotWorker(() => {
            if (asyncFailure) return Promise.reject(new Error("fixture failure"));
            throw new Error("fixture failure");
        });
        for (let attempt = 0; attempt < 4; attempt++) {
            await assert.rejects(async () => worker({}), /fixture failure/);
        }
    }
});

test("independent snapshot workers do not share capacity", async (t) => {
    const pending = Promise.withResolvers();
    t.after(() => pending.resolve({ available: true }));
    const busy = createSnapshotWorker(() => pending.promise);
    busy({});
    busy({});
    const healthy = createSnapshotWorker(filter => ({ available: true, filter }));
    assert.deepEqual(await healthy({ recentWindowMs: 3_600_000 }), {
        available: true, filter: { recentWindowMs: 3_600_000 },
    });
});

for (const mode of ["standalone", "hub"]) {
    test(`${mode} bounds abandoned server work and recovers without mixing filters`, async (t) => {
        const reads = Array.from({ length: 2 }, () => Promise.withResolvers());
        const entered = Array.from({ length: 2 }, () => Promise.withResolvers());
        const filters = [];
        const getSnapshot = filter => {
            filters.push(filter);
            const index = filters.length - 1;
            if (index < 2) {
                entered[index].resolve();
                return reads[index].promise;
            }
            return { available: true, filter };
        };
        const server = mode === "standalone"
            ? await createDashboardServer({ token: "bounded-work-fixture", getSnapshot })
            : await createDashboardHub({
                codexAdapter: { getSnapshot },
                copilotAdapter: {
                    extensionDirectory: "fixture-only",
                    getSnapshot: async () => ({ available: true, provider: "copilot" }),
                },
                readFileImpl: async () => "<html><head></head><body>fixture-only</body></html>",
                port: 0,
            });
        const controllers = Array.from({ length: 2 }, () => new AbortController());
        t.after(async () => {
            controllers.forEach(controller => controller.abort());
            reads.forEach(read => read.resolve({ available: true }));
            await flush();
            await server.close();
        });
        const api = mode === "standalone" ? `${server.url}api/usage` : `${server.url}dashboards/codex/api/usage`;
        const first = fetch(`${api}?recentWindowMs=3600000`, { signal: controllers[0].signal }).catch(error => error);
        await entered[0].promise;
        const second = fetch(`${api}?recentWindowMs=86400000`, { signal: controllers[1].signal }).catch(error => error);
        await entered[1].promise;
        controllers.forEach(controller => controller.abort());
        assert.equal((await first).name, "AbortError");
        assert.equal((await second).name, "AbortError");
        for (let retry = 0; retry < 3; retry++) {
            const busy = await fetch(`${api}?recentWindowMs=21600000`);
            const snapshot = await busy.json();
            assert.equal(busy.status, 503);
            assert.equal(snapshot.available, false);
            assert.equal(snapshot.diagnostics.code, "dashboard_snapshot_busy");
            assert.equal(filters.length, 2);
        }
        assert.equal((await fetch(`${server.url}api/health`)).status, 200);
        if (mode === "hub") {
            const copilot = await fetch(`${server.url}copilot/api/usage`);
            assert.equal(copilot.status, 200);
            assert.equal((await copilot.json()).provider, "copilot");
        }
        reads[0].resolve({ available: true });
        await flush();
        const recovered = await fetch(`${api}?recentWindowMs=21600000`);
        assert.equal(recovered.status, 200);
        assert.deepEqual((await recovered.json()).filter, { recentWindowMs: 21_600_000 });
        assert.equal(filters.length, 3);
    });
}
