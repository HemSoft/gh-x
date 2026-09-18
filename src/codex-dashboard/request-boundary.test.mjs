import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { request } from "node:http";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";

import { createDashboardServer } from "./dashboard-server.mjs";
import { createDashboardHub } from "../dashboard-hub/hub-server.mjs";

const malformedTargets = ["http://[", "http://%", "//["];

function rawRequest(baseUrl, target) {
    const url = new URL(baseUrl);
    return new Promise((resolve, reject) => {
        const outgoing = request({
            hostname: url.hostname,
            port: url.port,
            path: target,
            agent: false,
            timeout: 2_000,
        }, (response) => {
            let body = "";
            response.setEncoding("utf8");
            response.on("data", (chunk) => { body += chunk; });
            response.on("error", reject);
            response.on("end", () => resolve({ status: response.statusCode, body }));
        });
        outgoing.on("timeout", () => outgoing.destroy(new Error("Request timed out")));
        outgoing.on("error", reject);
        outgoing.end();
    });
}

async function assertRequestIsolation(server, healthUrl) {
    for (const target of malformedTargets) {
        const response = await rawRequest(server.url, target);
        assert.equal(response.status, 400, target);
        assert.deepEqual(JSON.parse(response.body), { error: "Invalid request target." });
        const health = await fetch(healthUrl, { signal: AbortSignal.timeout(2_000) });
        assert.equal(health.status, 200);
        assert.equal((await health.json()).ready, true);
    }
}

test("standalone rejects malformed targets before token routing and stays healthy", async (context) => {
    const server = await createDashboardServer({
        token: "request-fixture",
        getSnapshot: async () => assert.fail("Malformed requests must not read session data"),
    });
    context.after(() => server.close());
    await assertRequestIsolation(server, `${server.url}api/health`);
    assert.equal((await rawRequest(server.url, "/api/health")).status, 403);
});

test("hub rejects malformed targets and preserves local and mounted health routes", async (context) => {
    const adapter = { getSnapshot: async () => assert.fail("Malformed requests must not read session data") };
    const server = await createDashboardHub({
        codexAdapter: adapter,
        copilotAdapter: { ...adapter, extensionDirectory: "unused-fixture" },
        port: 0,
        readFileImpl: async () => "<html><head></head></html>",
    });
    context.after(() => server.close());
    await assertRequestIsolation(server, `${server.url}api/health`);
    await assertRequestIsolation(server, `${server.url}dashboards/api/health`);
});

test("standalone launcher survives malformed requests without unhandled errors", { timeout: 15_000 }, async (context) => {
    const directory = await mkdtemp(path.join(os.tmpdir(), "dashboard-request-boundary-"));
    const controlPath = path.join(directory, "control.json");
    const child = spawn(process.execPath, [
        fileURLToPath(new URL("./main.mjs", import.meta.url)), "--control", controlPath,
    ], {
        env: { ...process.env, CODEX_HOME: directory },
        stdio: ["ignore", "ignore", "pipe"],
    });
    let stderr = "";
    child.stderr.setEncoding("utf8");
    child.stderr.on("data", (chunk) => { stderr += chunk; });
    const closed = once(child, "close");
    // Observe spawn errors immediately, even if setup fails before teardown.
    closed.catch(() => {});
    context.after(async () => {
        if (child.exitCode === null && child.signalCode === null) child.kill();
        await closed;
        await rm(directory, { recursive: true, force: true });
    });
    const control = await waitForControl(controlPath, child);
    await assertRequestIsolation(control, `${control.url}api/health`);
    assert.equal(child.exitCode, null, stderr);
    assert.equal(child.signalCode, null, stderr);
    assert.equal(stderr, "");
});

async function waitForControl(controlPath, child) {
    const deadline = Date.now() + 5_000;
    while (Date.now() < deadline) {
        assert.equal(child.exitCode, null, "Launcher exited before publishing its control file");
        try {
            return JSON.parse(await readFile(controlPath, "utf8"));
        } catch (error) {
            if (error.code !== "ENOENT") throw error;
        }
        await delay(25);
    }
    throw new Error("Launcher did not publish its control file");
}
