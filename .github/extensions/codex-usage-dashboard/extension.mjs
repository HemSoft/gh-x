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

function closeInstance(instanceId, entry) {
    if (!entry.closing) {
        entry.closing = Promise.resolve().then(async () => {
            const server = await entry.opening;
            await server.close();
        }).finally(() => {
            if (servers.get(instanceId) === entry) servers.delete(instanceId);
        });
    }
    return entry.closing;
}

async function openServer(instanceId) {
    if (closed) throw new Error("Codex Canvas has stopped.");
    let entry = servers.get(instanceId);
    if (entry?.closing) {
        await entry.closing;
        return openServer(instanceId);
    }
    if (!entry) {
        entry = {
            opening: createDashboardServer({
                getSnapshot: createIsolatedSnapshotReader({
                    moduleUrl: new URL("../../../src/codex-dashboard/codex-adapter.mjs", import.meta.url).href,
                    factoryName: "createCodexAdapter",
                }),
            }),
            closing: null,
        };
        // Own startup immediately; concurrent opens share the same server.
        servers.set(instanceId, entry);
        entry.opening.catch(() => {
            if (servers.get(instanceId) === entry) servers.delete(instanceId);
        });
    }
    const server = await entry.opening;
    if (closed || entry.closing) throw new Error("Codex Canvas closed during startup.");
    return server;
}

const canvas = createCanvas({
    id: CANVAS_ID,
    displayName: "Codex Usage Dashboard",
    description: "Shows local Codex CLI sessions, tokens, context, and rate limits.",
    open: async ({ instanceId }) => ({
        title: "Codex usage",
        url: (await openServer(instanceId)).url,
    }),
    onClose: ({ instanceId }) => {
        const entry = servers.get(instanceId);
        return entry ? closeInstance(instanceId, entry) : Promise.resolve();
    },
});

const session = await joinSession({ canvases: [canvas] });
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
            ...[...servers.entries()].map(([instanceId, entry]) => closeInstance(instanceId, entry)),
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
