import assert from "node:assert/strict";
import test from "node:test";

import { createDashboardServer } from "./dashboard-server.mjs";

test("serves the protected Codex dashboard and validated snapshots", async () => {
    const requests = [];
    const server = await createDashboardServer({
        token: "known-token",
        getSnapshot: async (filter) => {
            requests.push(filter);
            return { available: true, sessions: [] };
        },
    });

    const root = new URL(server.url);
    const html = await fetch(server.url);
    assert.equal(html.status, 200);
    const htmlText = await html.text();
    assert.match(htmlText, /\/known-token\/api\/usage/);
    assert.match(htmlText, /codex-icon\.svg/);
    assert.match(htmlText, /rate-projected-value/);
    assert.match(htmlText, /row-update-glow/);
    const icon = await fetch(`${server.url}codex-icon.svg`);
    assert.equal(icon.status, 200);
    assert.match(icon.headers.get("content-type"), /image\/svg\+xml/);

    const configured = await fetch(`${server.url}api/usage?recentWindowMs=604800000`);
    assert.equal(configured.status, 200);
    assert.deepEqual(requests[0], { recentWindowMs: 604_800_000 });

    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const todayResponse = await fetch(
        `${server.url}api/usage?recentSince=${encodeURIComponent(today.toISOString())}`,
    );
    assert.equal(todayResponse.status, 200);
    assert.deepEqual(requests[1], { recentSince: today.toISOString() });

    assert.equal((await fetch(`${server.url}api/usage?recentWindowMs=123`)).status, 400);
    assert.equal((await fetch(`${root.origin}/api/usage`)).status, 403);
    assert.equal((await fetch(`${server.url}api/health`)).status, 200);

    await server.close();
    await assert.rejects(fetch(server.url));
});

test("returns an explicit unavailable snapshot when refresh fails", async () => {
    const server = await createDashboardServer({
        token: "failing-token",
        getSnapshot: async () => {
            throw new Error("fixture failure");
        },
    });

    const response = await fetch(`${server.url}api/usage`);
    assert.equal(response.status, 500);
    const snapshot = await response.json();
    assert.equal(snapshot.available, false);
    assert.equal(snapshot.diagnostics.code, "dashboard_snapshot_failed");
    assert.match(snapshot.diagnostics.message, /fixture failure/);
    await server.close();
});
