import { randomBytes } from "node:crypto";
import {
    mkdir,
    rename,
    unlink,
    writeFile,
} from "node:fs/promises";
import { createServer } from "node:http";
import path from "node:path";

import { resolveCodexHome } from "./codex-adapter.mjs";

export const DEFAULT_CANVAS_CONTROL_DIRECTORY = path.join(
    resolveCodexHome(),
    "dashboard",
    "canvas-controls",
);

export async function openCanvasInstance(session, {
    canvasId,
    instanceId,
} = {}) {
    const openCanvas = session?.rpc?.canvas?.open;
    if (!session?.capabilities?.ui?.canvases || typeof openCanvas !== "function") {
        throw new Error("Canvas rendering is unavailable in this Copilot CLI session.");
    }
    await openCanvas.call(session.rpc.canvas, {
        canvasId,
        instanceId,
    });
}

export async function createCanvasControl({
    openCanvas,
    workingDirectory,
    directory = DEFAULT_CANVAS_CONTROL_DIRECTORY,
    token = randomBytes(24).toString("base64url"),
    pid = process.pid,
    now = () => new Date(),
    createServerImpl = createServer,
} = {}) {
    if (typeof openCanvas !== "function") {
        throw new TypeError("openCanvas is required.");
    }
    if (typeof workingDirectory !== "string" || !workingDirectory.trim()) {
        throw new TypeError("workingDirectory is required.");
    }

    const route = `/${token}/open`;
    const server = createServerImpl(async (request, response) => {
        const requestUrl = new URL(request.url || "/", "http://127.0.0.1");
        if (requestUrl.pathname !== route) {
            sendJson(response, 403, { error: "Unauthorized." });
            return;
        }
        if (request.method !== "POST") {
            sendJson(response, 405, { error: "Method not allowed." });
            return;
        }
        try {
            await openCanvas();
            response.writeHead(204, {
                "Cache-Control": "no-store",
                "Referrer-Policy": "no-referrer",
            });
            response.end();
        } catch (error) {
            sendJson(response, 500, {
                error: error?.message || "Unable to open Canvas.",
            });
        }
    });

    await new Promise((resolve, reject) => {
        server.once("error", reject);
        server.listen(0, "127.0.0.1", resolve);
    });

    const address = server.address();
    const port = typeof address === "object" && address ? address.port : 0;
    const endpoint = `http://127.0.0.1:${port}${route}`;
    const markerPath = path.join(directory, `${pid}-${token.slice(0, 12)}.json`);
    const temporaryPath = `${markerPath}.tmp`;
    await mkdir(directory, { recursive: true });
    await writeFile(temporaryPath, JSON.stringify({
        pid,
        workingDirectory: path.resolve(workingDirectory),
        endpoint,
        updatedAt: now().toISOString(),
    }), "utf8");
    await rename(temporaryPath, markerPath);

    let closed = false;
    return {
        endpoint,
        markerPath,
        async close() {
            if (closed) {
                return;
            }
            closed = true;
            await Promise.allSettled([
                removeFile(markerPath),
                new Promise((resolve, reject) => {
                    server.close((error) => error ? reject(error) : resolve());
                }),
            ]);
        },
    };
}

function sendJson(response, statusCode, body) {
    response.writeHead(statusCode, {
        "Cache-Control": "no-store",
        "Content-Type": "application/json; charset=utf-8",
        "Referrer-Policy": "no-referrer",
        "X-Content-Type-Options": "nosniff",
    });
    response.end(JSON.stringify(body));
}

async function removeFile(filePath) {
    try {
        await unlink(filePath);
    } catch (error) {
        if (error?.code !== "ENOENT") {
            throw error;
        }
    }
}
