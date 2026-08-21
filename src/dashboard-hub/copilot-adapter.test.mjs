import assert from "node:assert/strict";
import test from "node:test";

import { createCopilotAdapter } from "./copilot-adapter.mjs";

test("reads the installed session store, overlays activity, and builds plan usage", async () => {
    const calls = [];
    const modules = {
        "session-store.mjs": {
            async readSessionStoreSnapshot(filter) {
                calls.push(["snapshot", filter]);
                return { available: true, sessions: [], billingPeriod: { aiCredits: 12 } };
            },
        },
        "session-activity.mjs": {
            createSessionActivityRegistry(options) {
                calls.push(["activity", options.sessionId]);
                return {
                    async overlay(snapshot) {
                        return { ...snapshot, overlaid: true };
                    },
                };
            },
        },
        "plan-usage.mjs": {
            buildPlanUsage(options) {
                calls.push(["plan", options]);
                return {
                    available: true,
                    usedCredits: options.accountQuota?.usedCredits || options.billingPeriod.aiCredits,
                };
            },
        },
        "account-quota-cache.mjs": {
            createAccountQuotaCache() {
                return {
                    async read() {
                        return {
                            available: true,
                            source: "assistant-usage-quota",
                            usedCredits: 171_000,
                            limitCredits: 350_000,
                            resetAt: "2026-09-01T00:00:00.000Z",
                        };
                    },
                };
            },
        },
    };
    const adapter = createCopilotAdapter({
        extensionDirectory: "C:\\fixture",
        accessImpl: async () => {},
        importModule: async (filePath) => modules[filePath.split("\\").at(-1)],
        now: () => new Date("2026-08-19T12:00:00.000Z"),
    });

    const snapshot = await adapter.getSnapshot({ recentWindowMs: 3_600_000 });

    assert.equal(snapshot.available, true);
    assert.equal(snapshot.overlaid, true);
    assert.equal(snapshot.plan.usedCredits, 171_000);
    assert.equal(calls[0][1].recentWindowMs, 3_600_000);
    assert.equal(calls[1][1], "dashboard-hub-read-only");
    assert.equal(calls[2][1].accountQuota.usedCredits, 171_000);
});

test("returns diagnostics when the installed extension cannot be loaded", async () => {
    const adapter = createCopilotAdapter({
        extensionDirectory: "C:\\missing",
        accessImpl: async () => {
            throw Object.assign(new Error("not found"), { code: "ENOENT" });
        },
    });

    const snapshot = await adapter.getSnapshot();

    assert.equal(snapshot.available, false);
    assert.equal(snapshot.diagnostics.code, "ENOENT");
    assert.deepEqual(snapshot.sessions, []);
});
