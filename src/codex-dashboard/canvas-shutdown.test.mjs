import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { cp, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as delay } from "node:timers/promises";
import test from "node:test";

// Run the real Canvas caller and server/readers with SDK/data doubles and a
// controlled startup fault. No signed-in Canvas session is needed.
test("Canvas closes an unsettled read and leaves a sibling instance healthy", { timeout: 15_000 }, async t => {
    const directory = await mkdtemp(path.join(os.tmpdir(), "codex-canvas-shutdown-"));
    let child;
    let childClosed;
    t.after(async () => {
        if (child && child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
        await childClosed?.catch(() => {});
        await rm(directory, { recursive: true, force: true, maxRetries: 3, retryDelay: 25 });
    });
    const source = fileURLToPath(new URL(".", import.meta.url));
    const target = path.join(directory, "src", "codex-dashboard");
    await mkdir(target, { recursive: true });
    for (const file of ["canvas-control.mjs", "dashboard-server.mjs", "snapshot-worker.mjs", "isolated-snapshot-reader.mjs", "snapshot-reader-process.mjs", "snapshot-reader-thread.mjs", "dashboard.html", "dashboard-ui.mjs", "dashboard-entry.mjs", "codex-icon.svg"]) {
        await cp(path.join(source, file), path.join(target, file));
    }
    await cp(path.join(target, "dashboard-server.mjs"), path.join(target, "dashboard-server-real.mjs"));
    await writeFile(path.join(target, "dashboard-server.mjs"), `
        import { createDashboardServer as createRealServer } from "./dashboard-server-real.mjs";
        export function createDashboardServer(options) {
            if (globalThis.fixtureFailStartup) {
                globalThis.fixtureFailStartup = false;
                return new Promise((_resolve, reject) => { globalThis.fixtureRejectStartup = reject; });
            }
            return createRealServer(options);
        }
    `);
    await writeFile(path.join(target, "codex-adapter.mjs"), `
        import { writeFileSync, renameSync } from "node:fs";
        export const resolveCodexHome = () => process.env.CODEX_HOME;
        export const createCodexAdapter = () => ({ async getSnapshot(filter) {
            if (filter.recentWindowMs === 3600000) {
                const marker = process.env.CODEX_HOME + "/reader.json";
                writeFileSync(marker + ".tmp", JSON.stringify({ pid: process.pid }));
                renameSync(marker + ".tmp", marker);
                await new Promise(() => {});
            }
            return { available: true, pid: process.pid };
        }});
    `);
    const extensionDirectory = path.join(directory, ".github", "extensions", "codex-usage-dashboard");
    await mkdir(extensionDirectory, { recursive: true });
    await cp(new URL("../../.github/extensions/codex-usage-dashboard/extension.mjs", import.meta.url), path.join(extensionDirectory, "extension.mjs"));
    const sdk = path.join(directory, "node_modules", "@github", "copilot-sdk");
    await mkdir(sdk, { recursive: true });
    await writeFile(path.join(sdk, "package.json"), JSON.stringify({ type: "module", exports: { "./extension": "./extension.mjs" } }));
    await writeFile(path.join(sdk, "extension.mjs"), `
        export const createCanvas = config => { globalThis.fixtureCanvas = config; return config; };
        export const joinSession = async () => ({ on: (_event, handler) => { globalThis.fixtureShutdown = handler; } });
    `);
    await writeFile(path.join(directory, "driver.mjs"), `
        import assert from "node:assert/strict";
        import { readFile, rm } from "node:fs/promises";
        import { setTimeout as delay } from "node:timers/promises";
        await import("./.github/extensions/codex-usage-dashboard/extension.mjs");
        const canvas = globalThis.fixtureCanvas;
        try {
        const [first, sameInstance] = await Promise.all([
            canvas.open({ instanceId: "first" }), canvas.open({ instanceId: "first" }),
        ]);
        assert.equal(first.url, sameInstance.url, "Concurrent opens must share one owned server");
        const second = await canvas.open({ instanceId: "second" });
        const sibling = await (await fetch(second.url + "api/usage")).json();
        await rm(process.env.CODEX_HOME + "/reader.json", { force: true });
        const pending = fetch(first.url + "api/usage?recentWindowMs=3600000").catch(error => error);
        let marker;
        const deadline = Date.now() + 3000;
        while (!marker && Date.now() < deadline) {
            try { marker = JSON.parse(await readFile(process.env.CODEX_HOME + "/reader.json", "utf8")); }
            catch (error) { if (error.code !== "ENOENT") throw error; await delay(10); }
        }
        assert.ok(marker, "Canvas read did not start");
        if (process.env.CANVAS_FIXTURE_FAILURE) {
            console.log("owned-readers:" + JSON.stringify([marker.pid, sibling.pid]));
            if (process.env.CANVAS_FIXTURE_FAILURE === "abrupt") process.exit(42);
            throw new Error("expected fixture assertion failure");
        }
        globalThis.fixtureFailStartup = true;
        const failedStartup = canvas.open({ instanceId: "failed-startup" });
        const failedClose = canvas.onClose({ instanceId: "failed-startup" });
        const recoverStartup = canvas.open({ instanceId: "failed-startup" });
        failedStartup.catch(() => {}); failedClose.catch(() => {}); recoverStartup.catch(() => {});
        globalThis.fixtureRejectStartup(Object.assign(new Error("fixture asset read failed"), { code: "ENOENT" }));
        await assert.rejects(failedStartup, /asset read failed/);
        await failedClose;
        const afterFailure = await recoverStartup;
        assert.equal((await (await fetch(afterFailure.url + "api/usage")).json()).available, true);
        const closeFirst = canvas.onClose({ instanceId: "first" });
        const reopening = canvas.open({ instanceId: "first" });
        const closed = await Promise.race([
            closeFirst.then(() => true), delay(1000).then(() => false),
        ]);
        assert.equal(closed, true, "Canvas shutdown must not wait forever for its abandoned read");
        await pending;
        const reopened = await reopening;
        assert.notEqual(reopened.url, first.url, "Reopening must not return the closing server");
        const reopenedSnapshot = await (await fetch(reopened.url + "api/usage")).json();
        assert.equal(reopenedSnapshot.available, true);
        const startup = canvas.open({ instanceId: "startup" });
        const closeStartup = canvas.onClose({ instanceId: "startup" });
        await assert.rejects(startup, /closed/);
        await closeStartup;
        assert.throws(() => process.kill(marker.pid, 0), error => error.code === "ESRCH");
        const recoveredSibling = await (await fetch(second.url + "api/usage")).json();
        assert.equal(recoveredSibling.available, true);
        assert.equal(recoveredSibling.pid, sibling.pid);
        await globalThis.fixtureShutdown();
        assert.throws(() => process.kill(sibling.pid, 0), error => error.code === "ESRCH");
        assert.throws(() => process.kill(reopenedSnapshot.pid, 0), error => error.code === "ESRCH");
        console.log("canvas shutdown and sibling isolation verified");
        } finally { await globalThis.fixtureShutdown(); }
    `);
    child = spawn(process.execPath, [path.join(directory, "driver.mjs")], {
        cwd: directory, env: { ...process.env, CODEX_HOME: directory, CODEX_USAGE_DASHBOARD_AUTO_OPEN: "0" },
        stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    child.stdout.on("data", chunk => { output += chunk; });
    child.stderr.on("data", chunk => { output += chunk; });
    childClosed = once(child, "close");
    childClosed.catch(() => {});
    const [code] = await childClosed;
    assert.equal(code, 0, output);
    assert.match(output, /canvas shutdown and sibling isolation verified/);
    for (const failure of ["assertion", "abrupt"]) {
        output = "";
        child = spawn(process.execPath, [path.join(directory, "driver.mjs")], {
            cwd: directory, env: { ...process.env, CODEX_HOME: directory, CODEX_USAGE_DASHBOARD_AUTO_OPEN: "0", CANVAS_FIXTURE_FAILURE: failure },
            stdio: ["ignore", "pipe", "pipe"],
        });
        child.stdout.on("data", chunk => { output += chunk; });
        child.stderr.on("data", chunk => { output += chunk; });
        childClosed = once(child, "close");
        childClosed.catch(() => {});
        const [failedCode] = await childClosed;
        assert.equal(failedCode, failure === "abrupt" ? 42 : 1, output);
        const marker = output.match(/owned-readers:(\[[0-9,]+\])/);
        assert.ok(marker, output);
        for (const pid of JSON.parse(marker[1])) {
            const deadline = Date.now() + 3_000;
            let alive = true;
            do {
                try { process.kill(pid, 0); await delay(10); }
                catch (error) { if (error.code !== "ESRCH") throw error; alive = false; }
            } while (alive && Date.now() < deadline);
            assert.equal(alive, false, `fixture reader ${pid} survived ${failure} exit`);
        }
    }
});
