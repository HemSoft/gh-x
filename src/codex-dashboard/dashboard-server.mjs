import { randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { fileURLToPath } from "node:url";

const dashboardPath = fileURLToPath(new URL("./dashboard.html", import.meta.url));
const dashboardUiPath = fileURLToPath(new URL("./dashboard-ui.mjs", import.meta.url));
const dashboardEntryPath = fileURLToPath(new URL("./dashboard-entry.mjs", import.meta.url));
const codexIconPath = fileURLToPath(new URL("./codex-icon.svg", import.meta.url));
const RECENT_WINDOWS = new Set([
    60 * 60 * 1_000,
    6 * 60 * 60 * 1_000,
    12 * 60 * 60 * 1_000,
    24 * 60 * 60 * 1_000,
    3 * 24 * 60 * 60 * 1_000,
    7 * 24 * 60 * 60 * 1_000,
    30 * 24 * 60 * 60 * 1_000,
]);

export async function createDashboardServer({
    getSnapshot,
    token = randomBytes(24).toString("base64url"),
    readFileImpl = readFile,
    createServerImpl = createServer,
} = {}) {
    if (typeof getSnapshot !== "function") {
        throw new TypeError("getSnapshot is required.");
    }

    const basePath = `/${token}`;
    const apiPath = `${basePath}/api/usage`;
    const [htmlTemplate, dashboardUi, dashboardEntry, codexIcon] = await Promise.all([
        readFileImpl(dashboardPath, "utf8"),
        readFileImpl(dashboardUiPath, "utf8"),
        readFileImpl(dashboardEntryPath, "utf8"),
        readFileImpl(codexIconPath, "utf8"),
    ]);
    const html = htmlTemplate.replace("__CODEX_API_URL__", escapeHtmlAttribute(apiPath));
    const server = createServerImpl(async (request, response) => {
        const requestUrl = new URL(request.url || "/", "http://127.0.0.1");
        if (!requestUrl.pathname.startsWith(`${basePath}/`) && requestUrl.pathname !== basePath) {
            sendJson(response, 403, { error: "Unauthorized." });
            return;
        }
        if (request.method === "GET" && requestUrl.pathname === basePath) {
            redirect(response, `${basePath}/`);
            return;
        }
        if (request.method === "GET" && requestUrl.pathname === `${basePath}/`) {
            send(response, 200, "text/html; charset=utf-8", html);
            return;
        }
        if (request.method === "GET" && requestUrl.pathname === `${basePath}/dashboard-ui.mjs`) {
            send(response, 200, "text/javascript; charset=utf-8", dashboardUi);
            return;
        }
        if (request.method === "GET" && requestUrl.pathname === `${basePath}/dashboard-entry.mjs`) {
            send(response, 200, "text/javascript; charset=utf-8", dashboardEntry);
            return;
        }
        if (request.method === "GET" && requestUrl.pathname === `${basePath}/codex-icon.svg`) {
            send(response, 200, "image/svg+xml; charset=utf-8", codexIcon);
            return;
        }
        if (request.method === "GET" && requestUrl.pathname === `${basePath}/api/health`) {
            sendJson(response, 200, { ready: true });
            return;
        }
        if (request.method === "GET" && requestUrl.pathname === apiPath) {
            try {
                const filter = parseFilter(requestUrl.searchParams);
                if (filter.error) {
                    sendJson(response, 400, { error: filter.error });
                    return;
                }
                sendJson(response, 200, await getSnapshot(filter.value));
            } catch (error) {
                sendJson(response, 500, {
                    available: false,
                    source: "codex-local",
                    lastRefreshAt: new Date().toISOString(),
                    diagnostics: {
                        code: "dashboard_snapshot_failed",
                        message: error?.message || "Unable to refresh Codex usage.",
                    },
                });
            }
            return;
        }
        sendJson(response, 404, { error: "Not found." });
    });

    await new Promise((resolve, reject) => {
        server.once("error", reject);
        server.listen(0, "127.0.0.1", resolve);
    });
    const address = server.address();
    const port = typeof address === "object" && address ? address.port : 0;
    let closed = false;
    return {
        token,
        url: `http://127.0.0.1:${port}${basePath}/`,
        async close() {
            if (closed) {
                return;
            }
            closed = true;
            await new Promise((resolve, reject) => {
                server.close((error) => error ? reject(error) : resolve());
            });
        },
    };
}

function parseFilter(searchParams, now = Date.now()) {
    const windowValue = searchParams.get("recentWindowMs");
    const sinceValue = searchParams.get("recentSince");
    if (windowValue !== null && sinceValue !== null) {
        return { error: "Choose either a recent window or cutoff." };
    }
    if (windowValue !== null) {
        const recentWindowMs = Number(windowValue);
        return RECENT_WINDOWS.has(recentWindowMs)
            ? { value: { recentWindowMs } }
            : { error: "Unsupported recent session window." };
    }
    if (sinceValue !== null) {
        const date = new Date(sinceValue);
        const timestamp = date.getTime();
        const maximumAgeMs = 26 * 60 * 60 * 1_000;
        if (
            !Number.isFinite(timestamp)
            || date.toISOString() !== sinceValue
            || timestamp > now + 60_000
            || timestamp < now - maximumAgeMs
        ) {
            return { error: "Invalid recent session cutoff." };
        }
        return { value: { recentSince: sinceValue } };
    }
    return { value: {} };
}

function redirect(response, location) {
    response.writeHead(308, {
        Location: location,
        "Cache-Control": "no-store",
        "Referrer-Policy": "no-referrer",
    });
    response.end();
}

function sendJson(response, statusCode, body) {
    send(response, statusCode, "application/json; charset=utf-8", JSON.stringify(body));
}

function send(response, statusCode, contentType, body) {
    response.writeHead(statusCode, {
        "Cache-Control": "no-store",
        "Content-Security-Policy": "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-ancestors 'none'",
        "Content-Type": contentType,
        "Referrer-Policy": "no-referrer",
        "X-Content-Type-Options": "nosniff",
    });
    response.end(body);
}

function escapeHtmlAttribute(value) {
    return String(value)
        .replaceAll("&", "&amp;")
        .replaceAll('"', "&quot;")
        .replaceAll("<", "&lt;")
        .replaceAll(">", "&gt;");
}
