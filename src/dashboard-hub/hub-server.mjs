import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const moduleDirectory = path.dirname(fileURLToPath(import.meta.url));
const codexDirectory = path.resolve(moduleDirectory, "..", "codex-dashboard");
const recentWindows = new Set([
    60 * 60 * 1_000,
    6 * 60 * 60 * 1_000,
    12 * 60 * 60 * 1_000,
    24 * 60 * 60 * 1_000,
    3 * 24 * 60 * 60 * 1_000,
    7 * 24 * 60 * 60 * 1_000,
    30 * 24 * 60 * 60 * 1_000,
]);

const mobileTableCss = `
<style>
@media (max-width: 720px) {
    main { padding-left: 12px !important; padding-right: 12px !important; }
    .card { padding-left: 14px !important; padding-right: 14px !important; }
    table { font-size: .67rem !important; }
    th, td { padding-left: 4px !important; padding-right: 4px !important; }
}
</style>`;

const codexMobileCss = `
<style>
@media (max-width: 720px) {
    th:nth-child(3), td:nth-child(3), th:nth-child(5), td:nth-child(5),
    th:nth-child(6), td:nth-child(6), th:nth-child(7), td:nth-child(7),
    th:nth-child(8), td:nth-child(8), th:nth-child(9), td:nth-child(9),
    th:nth-child(11), td:nth-child(11) { display: none !important; }
    th:nth-child(1), td:nth-child(1) { width: 15% !important; }
    th:nth-child(2), td:nth-child(2) { width: 18% !important; }
    th:nth-child(4), td:nth-child(4) { width: 36% !important; }
    th:nth-child(10), td:nth-child(10) { width: 15% !important; }
    th:nth-child(12), td:nth-child(12) { width: 16% !important; }
}
</style>`;

const copilotMobileCss = `
<style>
@media (max-width: 720px) {
    .controls { grid-template-columns: repeat(2, minmax(0, 1fr)) !important; }
    .control-group, .select-wrap, .theme-controls, select { min-width: 0 !important; width: 100%; }
    .controls > .control-group:nth-child(3) { grid-column: 1 / -1; }
    .zoom-controls { justify-content: center; }
    th:nth-child(3), td:nth-child(3), th:nth-child(5), td:nth-child(5),
    th:nth-child(6), td:nth-child(6), th:nth-child(7), td:nth-child(7),
    th:nth-child(8), td:nth-child(8), th:nth-child(9), td:nth-child(9),
    th:nth-child(11), td:nth-child(11) { display: none !important; }
    th:nth-child(1), td:nth-child(1) { width: 15% !important; }
    th:nth-child(2), td:nth-child(2) { width: 18% !important; }
    th:nth-child(4), td:nth-child(4) { width: 36% !important; }
    th:nth-child(10), td:nth-child(10) { width: 15% !important; }
    th:nth-child(12), td:nth-child(12) { width: 16% !important; }
    .session-details-row > td { display: table-cell !important; width: auto !important; }
}
</style>`;

export async function createDashboardHub({
    codexAdapter,
    copilotAdapter,
    host = "127.0.0.1",
    port = 4765,
    readFileImpl = readFile,
    createServerImpl = createServer,
} = {}) {
    if (!codexAdapter?.getSnapshot || !copilotAdapter?.getSnapshot) {
        throw new TypeError("codexAdapter and copilotAdapter are required.");
    }

    const copilotDirectory = copilotAdapter.extensionDirectory;
    const [landing, codexHtmlTemplate, codexUi, codexEntry, codexIcon,
        copilotHtmlTemplate, copilotUi] = await Promise.all([
        readFileImpl(path.join(moduleDirectory, "landing.html"), "utf8"),
        readFileImpl(path.join(codexDirectory, "dashboard.html"), "utf8"),
        readFileImpl(path.join(codexDirectory, "dashboard-ui.mjs"), "utf8"),
        readFileImpl(path.join(codexDirectory, "dashboard-entry.mjs"), "utf8"),
        readFileImpl(path.join(codexDirectory, "codex-icon.svg"), "utf8"),
        readFileImpl(path.join(copilotDirectory, "dashboard.html"), "utf8"),
        readFileImpl(path.join(copilotDirectory, "dashboard-ui.mjs"), "utf8"),
    ]);
    const codexHtml = injectMobileCss(
        codexHtmlTemplate.replace("__CODEX_API_URL__", "api/usage"),
        `${mobileTableCss}${codexMobileCss}`,
    );
    const copilotHtml = injectMobileCss(
        copilotHtmlTemplate.replace("__DASHBOARD_API_URL__", "api/usage"),
        `${mobileTableCss}${copilotMobileCss}`,
    );

    const server = createServerImpl(async (request, response) => {
        const url = new URL(request.url || "/", "http://127.0.0.1");
        if (url.pathname === "/dashboards") {
            redirect(response, "/dashboards/");
            return;
        }
        const pathname = stripServePrefix(url.pathname);
        if (request.method !== "GET") {
            sendJson(response, 405, { error: "Method not allowed." });
            return;
        }
        if (pathname === "" || pathname === "/") {
            send(response, 200, "text/html; charset=utf-8", landing);
            return;
        }
        if (pathname === "/api/health") {
            sendJson(response, 200, { ready: true, service: "cli-dashboard-hub" });
            return;
        }
        if (pathname === "/codex") {
            redirect(response, `${url.pathname}/`);
            return;
        }
        if (pathname === "/copilot") {
            redirect(response, `${url.pathname}/`);
            return;
        }
        if (pathname === "/codex/") {
            send(response, 200, "text/html; charset=utf-8", codexHtml);
            return;
        }
        if (pathname === "/codex/dashboard-ui.mjs") {
            send(response, 200, "text/javascript; charset=utf-8", codexUi);
            return;
        }
        if (pathname === "/codex/dashboard-entry.mjs") {
            send(response, 200, "text/javascript; charset=utf-8", codexEntry);
            return;
        }
        if (pathname === "/codex/codex-icon.svg") {
            send(response, 200, "image/svg+xml; charset=utf-8", codexIcon);
            return;
        }
        if (pathname === "/codex/api/usage") {
            await sendSnapshot(response, codexAdapter, url.searchParams);
            return;
        }
        if (pathname === "/copilot/") {
            send(response, 200, "text/html; charset=utf-8", copilotHtml);
            return;
        }
        if (pathname === "/copilot/dashboard-ui.mjs") {
            send(response, 200, "text/javascript; charset=utf-8", copilotUi);
            return;
        }
        if (pathname === "/copilot/api/usage") {
            await sendSnapshot(response, copilotAdapter, url.searchParams);
            return;
        }
        sendJson(response, 404, { error: "Not found." });
    });

    await new Promise((resolve, reject) => {
        server.once("error", reject);
        server.listen(port, host, resolve);
    });
    const address = server.address();
    const actualPort = typeof address === "object" && address ? address.port : port;
    return {
        host,
        port: actualPort,
        url: `http://${host}:${actualPort}/`,
        close: () => new Promise((resolve, reject) => {
            server.close((error) => error ? reject(error) : resolve());
        }),
    };
}

function stripServePrefix(pathname) {
    if (pathname === "/dashboards") {
        return "/";
    }
    return pathname.startsWith("/dashboards/")
        ? pathname.slice("/dashboards".length)
        : pathname;
}

async function sendSnapshot(response, adapter, searchParams) {
    const filter = parseFilter(searchParams);
    if (filter.error) {
        sendJson(response, 400, { error: filter.error });
        return;
    }
    try {
        sendJson(response, 200, await adapter.getSnapshot(filter.value));
    } catch (error) {
        sendJson(response, 500, {
            available: false,
            source: "unavailable",
            lastRefreshAt: new Date().toISOString(),
            diagnostics: {
                code: "dashboard_snapshot_failed",
                message: error?.message || "Unable to refresh dashboard usage.",
            },
        });
    }
}

function parseFilter(searchParams, now = Date.now()) {
    const windowValue = searchParams.get("recentWindowMs");
    const sinceValue = searchParams.get("recentSince");
    if (windowValue !== null && sinceValue !== null) {
        return { error: "Choose either a recent window or cutoff." };
    }
    if (windowValue !== null) {
        const recentWindowMs = Number(windowValue);
        return recentWindows.has(recentWindowMs)
            ? { value: { recentWindowMs } }
            : { error: "Unsupported recent session window." };
    }
    if (sinceValue !== null) {
        const date = new Date(sinceValue);
        const timestamp = date.getTime();
        if (!Number.isFinite(timestamp) || timestamp > now + 60_000) {
            return { error: "Invalid recent session cutoff." };
        }
        return { value: { recentSince: date.toISOString() } };
    }
    return { value: {} };
}

function injectMobileCss(html, css) {
    return html.replace("</head>", `${css}\n</head>`);
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
