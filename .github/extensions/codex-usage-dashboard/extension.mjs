import { randomUUID } from "node:crypto";

import { createCanvas, joinSession } from "@github/copilot-sdk/extension";

import { createCodexAdapter } from "../../../src/codex-dashboard/codex-adapter.mjs";
import {
    createCanvasControl,
    openCanvasInstance,
} from "../../../src/codex-dashboard/canvas-control.mjs";
import { createDashboardServer } from "../../../src/codex-dashboard/dashboard-server.mjs";

const CANVAS_ID = "codex-usage-dashboard";
const adapter = createCodexAdapter();
const servers = new Map();

const canvas = createCanvas({
    id: CANVAS_ID,
    displayName: "Codex Usage Dashboard",
    description: "Shows local Codex CLI sessions, tokens, context, and rate limits.",
    open: async ({ instanceId }) => {
        let server = servers.get(instanceId);
        if (!server) {
            server = await createDashboardServer({
                getSnapshot: (filter) => adapter.getSnapshot(filter),
            });
            servers.set(instanceId, server);
        }
        return {
            title: "Codex usage",
            url: server.url,
        };
    },
    onClose: async ({ instanceId }) => {
        const server = servers.get(instanceId);
        if (!server) {
            return;
        }
        servers.delete(instanceId);
        await server.close();
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

let closed = false;
const close = async () => {
    if (closed) {
        return;
    }
    closed = true;
    await Promise.allSettled([
        canvasControl.close(),
        ...[...servers.values()].map((server) => server.close()),
    ]);
    servers.clear();
};

session.on("session.shutdown", close);
for (const signal of ["SIGINT", "SIGTERM"]) {
    process.once(signal, async () => {
        await close();
        process.exit(0);
    });
}
