import { randomUUID } from "node:crypto";

import { createCanvas, joinSession } from "@github/copilot-sdk/extension";

import { createIsolatedSnapshotReader } from "../../../src/codex-dashboard/isolated-snapshot-reader.mjs";
import {
    createCanvasControl,
    openCanvasInstance,
} from "../../../src/codex-dashboard/canvas-control.mjs";
import { createDashboardServer } from "../../../src/codex-dashboard/dashboard-server.mjs";

const CANVAS_ID = "codex-usage-dashboard";
const servers = new Map();
let closed = false;

const canvas = createCanvas({
    id: CANVAS_ID,
    displayName: "Codex Usage Dashboard",
    description: "Shows local Codex CLI sessions, tokens, context, and rate limits.",
    open: async ({ instanceId }) => {
        if (closed) throw new Error("Codex Canvas has stopped.");
        let opening = servers.get(instanceId);
        if (!opening) {
            opening = createDashboardServer({
                getSnapshot: createIsolatedSnapshotReader({
                    moduleUrl: new URL("../../../src/codex-dashboard/codex-adapter.mjs", import.meta.url).href,
                    factoryName: "createCodexAdapter",
                }),
            });
            // Track startup immediately so concurrent opens cannot orphan a server.
            servers.set(instanceId, opening);
            opening.catch(() => {
                if (servers.get(instanceId) === opening) servers.delete(instanceId);
            });
        }
        const server = await opening;
        if (closed) throw new Error("Codex Canvas has stopped.");
        return {
            title: "Codex usage",
            url: server.url,
        };
    },
    onClose: async ({ instanceId }) => {
        const opening = servers.get(instanceId);
        if (!opening) return;
        const server = await opening;
        await server.close();
        if (servers.get(instanceId) === opening) servers.delete(instanceId);
    },
});

const session = await joinSession({
    canvases: [
        canvas,
    ],
});

const openRegisteredCanvas = () => openCanvasInstance(session, {
    canvasId: CANVAS_ID,
    instanceId: randomUUID(),
});

const canvasControl = await createCanvasControl({
    workingDirectory: process.cwd(),
    openCanvas: openRegisteredCanvas,
});

if (process.env.CODEX_USAGE_DASHBOARD_AUTO_OPEN === "1") {
    try {
        await openRegisteredCanvas();
    } catch (error) {
        const message = `Codex dashboard auto-open failed: ${error?.message || "unknown error"}`;
        try {
            await session.log(message, { level: "error" });
        } catch (logError) {
            console.error(message, logError);
        }
    }
}

let closing;
const close = () => {
    if (!closing) {
        closed = true;
        closing = Promise.allSettled([
            canvasControl.close(),
            ...[...servers.values()].map(async opening => (await opening).close()),
        ]).then(() => servers.clear());
    }
    return closing;
};

session.on("session.shutdown", close);
for (const signal of ["SIGINT", "SIGTERM"]) {
    process.once(signal, async () => {
        await close();
        process.exit(0);
    });
}
