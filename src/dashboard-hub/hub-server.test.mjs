import assert from "node:assert/strict";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import { createDashboardHub } from "./hub-server.mjs";

test("serves both dashboards through local and Tailscale-mounted paths", async (context) => {
    const fixture = await mkdtemp(path.join(os.tmpdir(), "dashboard-hub-"));
    await Promise.all([
        writeFile(path.join(fixture, "dashboard.html"), "<html><head><meta name=api content=\"__DASHBOARD_API_URL__\"></head><body><table><tr><td>x</td></tr></table></body></html>"),
        writeFile(path.join(fixture, "dashboard-ui.mjs"), "export const ready = true;"),
    ]);
    context.after(() => rm(fixture, { recursive: true, force: true }));
    const codexAdapter = {
        getSnapshot: async (filter) => ({ provider: "codex", filter }),
    };
    const copilotAdapter = {
        extensionDirectory: fixture,
        getSnapshot: async (filter) => ({ provider: "copilot", filter }),
    };
    const hub = await createDashboardHub({ codexAdapter, copilotAdapter, port: 0 });
    context.after(() => hub.close());

    const landing = await fetch(hub.url);
    assert.equal(landing.status, 200);
    assert.match(await landing.text(), /CLI dashboards/);

    const mountedLanding = await fetch(`${hub.url}dashboards/`);
    assert.equal(mountedLanding.status, 200);

    const redirect = await fetch(`${hub.url}dashboards`, { redirect: "manual" });
    assert.equal(redirect.status, 308);
    assert.equal(redirect.headers.get("location"), "/dashboards/");

    const codex = await fetch(`${hub.url}dashboards/codex/api/usage?recentWindowMs=3600000`);
    assert.deepEqual(await codex.json(), {
        provider: "codex",
        filter: { recentWindowMs: 3_600_000 },
    });

    const copilot = await fetch(`${hub.url}copilot/api/usage`);
    assert.deepEqual(await copilot.json(), { provider: "copilot", filter: {} });

    const mobilePage = await fetch(`${hub.url}copilot/`);
    assert.match(await mobilePage.text(), /max-width: 720px/);

    const health = await fetch(`${hub.url}api/health`);
    assert.deepEqual(await health.json(), { ready: true, service: "cli-dashboard-hub" });
});

test("rejects invalid dashboard filters", async (context) => {
    const fixture = await mkdtemp(path.join(os.tmpdir(), "dashboard-hub-filter-"));
    await Promise.all([
        writeFile(path.join(fixture, "dashboard.html"), "<html><head></head><body>__DASHBOARD_API_URL__</body></html>"),
        writeFile(path.join(fixture, "dashboard-ui.mjs"), ""),
    ]);
    context.after(() => rm(fixture, { recursive: true, force: true }));
    const adapter = { getSnapshot: async () => ({ available: true }) };
    const hub = await createDashboardHub({
        codexAdapter: adapter,
        copilotAdapter: { ...adapter, extensionDirectory: fixture },
        port: 0,
    });
    context.after(() => hub.close());

    const response = await fetch(`${hub.url}codex/api/usage?recentWindowMs=123`);
    assert.equal(response.status, 400);
    assert.deepEqual(await response.json(), { error: "Unsupported recent session window." });
});
