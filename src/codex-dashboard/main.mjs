import path from "node:path";
import { mkdir, rename, rm, writeFile } from "node:fs/promises";

import { createCodexAdapter, resolveCodexHome } from "./codex-adapter.mjs";
import { createDashboardServer } from "./dashboard-server.mjs";

const controlPath = readOption(process.argv.slice(2), "--control")
    || path.join(resolveCodexHome(), "dashboard", "control.json");
const adapter = createCodexAdapter();
const server = await createDashboardServer({
    getSnapshot: (filter) => adapter.getSnapshot(filter),
});

await writeControl(controlPath, {
    pid: process.pid,
    url: server.url,
    startedAt: new Date().toISOString(),
});

let stopping = false;
const stop = async () => {
    if (stopping) {
        return;
    }
    stopping = true;
    await server.close();
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

function readOption(args, name) {
    const index = args.indexOf(name);
    return index >= 0 ? args[index + 1] : null;
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
