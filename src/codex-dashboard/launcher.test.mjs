import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import {
    mkdtemp,
    readFile,
    rm,
    writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import { fileURLToPath } from "node:url";

const launcherPath = new URL("../../run-codex.ps1", import.meta.url);
const execFileAsync = promisify(execFile);

test("run-codex launches the local dashboard in a standalone browser window", async () => {
    const launcher = await readFile(launcherPath, "utf8");

    assert.match(launcher, /src\\codex-dashboard\\main\.mjs/);
    assert.match(launcher, /dashboard\\control\.json/);
    assert.match(launcher, /--app=\$dashboardUrl/);
    assert.doesNotMatch(launcher, /CopilotCommand/);
    assert.doesNotMatch(launcher, /canvas-controls/);
    assert.doesNotMatch(launcher, /CODEX_USAGE_DASHBOARD_AUTO_OPEN/);
});

test("run-codex starts and reuses the standalone dashboard server", {
    skip: process.platform !== "win32",
}, async () => {
    const directory = await mkdtemp(path.join(os.tmpdir(), "run-codex-launcher-"));
    const codexHome = path.join(directory, "codex-home");
    const fakeBrowser = path.join(directory, "fake-browser.cmd");
    const outputPath = path.join(directory, "browser-arguments.txt");
    const controlPath = path.join(codexHome, "dashboard", "control.json");
    let serverPid;

    await writeFile(fakeBrowser, [
        "@echo off",
        "> \"%CODEX_LAUNCHER_TEST_OUTPUT%\" echo %*",
        "",
    ].join("\r\n"), "utf8");

    const runLauncher = () => execFileAsync("pwsh", [
        "-NoProfile",
        "-File",
        fileURLToPath(launcherPath),
        "-BrowserCommand",
        fakeBrowser,
    ], {
        cwd: directory,
        env: {
            ...process.env,
            CODEX_HOME: codexHome,
            CODEX_LAUNCHER_TEST_OUTPUT: outputPath,
        },
    });

    try {
        await runLauncher();
        const firstControl = JSON.parse(await readFile(controlPath, "utf8"));
        serverPid = firstControl.pid;
        const firstArguments = await waitForFile(outputPath);
        assert.match(firstArguments, /^--app=http:\/\/127\.0\.0\.1:\d+\/[^/]+\/$/);

        await rm(outputPath, { force: true });
        await runLauncher();
        const secondControl = JSON.parse(await readFile(controlPath, "utf8"));
        const secondArguments = await waitForFile(outputPath);
        assert.equal(secondControl.pid, firstControl.pid);
        assert.equal(secondArguments, firstArguments);
    } finally {
        if (serverPid) {
            try {
                process.kill(serverPid);
            } catch {
                // The test-owned server already exited.
            }
        }
        await rm(directory, { recursive: true, force: true });
    }
});

async function waitForFile(filePath) {
    const deadline = Date.now() + 5_000;
    while (Date.now() < deadline) {
        try {
            return (await readFile(filePath, "utf8")).trim();
        } catch (error) {
            if (error.code !== "ENOENT") {
                throw error;
            }
            await new Promise((resolve) => setTimeout(resolve, 50));
        }
    }
    throw new Error(`Timed out waiting for ${filePath}`);
}
