import path from "node:path";
import { mkdir, rename, rm, writeFile } from "node:fs/promises";

import { createCodexAdapter, resolveCodexHome } from "../codex-dashboard/codex-adapter.mjs";
import { createCopilotAdapter } from "./copilot-adapter.mjs";
import { createDashboardHub } from "./hub-server.mjs";

const args = process.argv.slice(2);
const port = Number(readOption(args, "--port") || 4765);
const controlPath = readOption(args, "--control")
    || path.join(resolveCodexHome(), "dashboard-hub", "control.json");
const hub = await createDashboardHub({
    codexAdapter: createCodexAdapter(),
    copilotAdapter: createCopilotAdapter(),
    port,
});

await writeControl(controlPath, {
    pid: process.pid,
    url: hub.url,
    startedAt: new Date().toISOString(),
});

let stopping = false;
const stop = async () => {
    if (stopping) return;
    stopping = true;
    await hub.close();
    await rm(controlPath, { force: true });
    process.exitCode = 0;
};

process.once("SIGINT", stop);
process.once("SIGTERM", stop);
process.once("uncaughtException", async (error) => {
    console.error(error);
    await stop();
    process.exitCode = 1;
});
process.once("unhandledRejection", async (error) => {
    console.error(error);
    await stop();
    process.exitCode = 1;
});

function readOption(values, name) {
    const index = values.indexOf(name);
    return index >= 0 ? values[index + 1] : null;
}

async function writeControl(filePath, control) {
    await mkdir(path.dirname(filePath), { recursive: true });
    const temporaryPath = `${filePath}.${process.pid}.tmp`;
    await writeFile(temporaryPath, `${JSON.stringify(control, null, 2)}\n`, {
        encoding: "utf8",
        mode: 0o600,
    });
    await rename(temporaryPath, filePath);
}
