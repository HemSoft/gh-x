const THEMES = new Set([
    "system",
    "light",
    "dark",
    "midnight",
    "aurora",
    "ember",
    "synthwave",
    "custom",
]);
const RECENT_WINDOWS = new Set([
    "today",
    "3600000",
    "21600000",
    "43200000",
    "86400000",
    "259200000",
    "604800000",
    "2592000000",
]);
const DEFAULT_CUSTOM_THEME = Object.freeze({
    font: "system",
    tableFont: "mono",
    background: "#07111f",
    panel: "#10243a",
    text: "#e9f7ff",
    muted: "#91abc0",
    accent: "#53d8fb",
    secondary: "#a78bfa",
    active: "#72f1b8",
    idle: "#7890a3",
});
const FONT_STACKS = {
    system: 'Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
    rounded: '"Segoe UI Rounded", "Arial Rounded MT Bold", ui-rounded, system-ui, sans-serif',
    mono: 'ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace',
    serif: 'Georgia, Cambria, "Times New Roman", serif',
};

export function buildDashboardView(snapshot, now = Date.now()) {
    if (!snapshot?.available) {
        return {
            available: false,
            detail: `${snapshot?.diagnostics?.code || "unavailable"}: ${snapshot?.diagnostics?.message || "Unable to read Codex usage."}`,
            sessions: [],
            rateLimit: unavailableRateLimit(),
        };
    }
    const sessions = Array.isArray(snapshot.sessions) ? snapshot.sessions : [];
    return {
        available: true,
        status: `${formatInteger(sessions.length)} recent ${sessions.length === 1 ? "session" : "sessions"}`,
        sessionsCount: formatInteger(sessions.length),
        totalTokens: formatTokenCount(snapshot.totals?.totalTokens),
        cachedTokens: formatTokenCount(snapshot.totals?.cachedInputTokens),
        requests: formatInteger(snapshot.totals?.requests),
        toolCalls: formatInteger(snapshot.totals?.toolCalls),
        plan: snapshot.rateLimits?.planType || "Unavailable",
        refreshed: formatRelativeAge(snapshot.lastRefreshAt, now),
        rateLimit: buildRateLimit(snapshot.rateLimits, now),
        sessions: sessions.map((session) => ({
            sessionId: session.sessionId,
            status: session.status === "active" ? "Active" : "Idle",
            project: session.project || "Unavailable",
            branch: session.branch || "Unavailable",
            summary: session.summary || "Untitled session",
            activity: normalizeActivity(session.activity),
            model: session.model || "Unavailable",
            contextPercent: normalizePercent(session.context?.percentUsed),
            totalTokens: formatTokenCount(session.totalTokens),
            requests: formatInteger(session.requests),
            toolCalls: formatInteger(session.toolCalls),
            sessionRuntime: formatDuration(
                Date.parse(session.createdAt),
                session.status === "active" ? now : Date.parse(session.updatedAt),
            ),
            promptRuntime: formatDuration(
                Date.parse(session.lastPromptAt),
                session.status === "active" ? now : Date.parse(session.updatedAt),
            ),
            updated: formatRelativeAge(session.updatedAt, now),
            updatedAt: session.updatedAt,
        })),
    };
}

export function buildUsageApiUrl(apiUrl, recentWindow, now = Date.now()) {
    const value = normalizeRecentWindow(recentWindow);
    const separator = String(apiUrl).includes("?") ? "&" : "?";
    if (value === "today") {
        const start = new Date(now);
        start.setHours(0, 0, 0, 0);
        return `${apiUrl}${separator}recentSince=${encodeURIComponent(start.toISOString())}`;
    }
    return `${apiUrl}${separator}recentWindowMs=${value}`;
}

export async function startDashboard({
    documentRef = document,
    fetchImpl = fetch,
    refreshIntervalMs = 5_000,
} = {}) {
    const apiUrl = documentRef.querySelector('meta[name="codex-api-url"]')?.content;
    let recentWindow = applyRecentWindow(
        documentRef,
        readCookie(documentRef.cookie, "codex-recent", "86400000"),
    );
    let theme = applyTheme(
        documentRef,
        readCookie(documentRef.cookie, "codex-theme", "system"),
    );
    let customTheme = readCustomTheme(documentRef.cookie);
    if (theme === "custom") {
        applyCustomTheme(documentRef, customTheme);
    }
    let zoom = applyZoom(
        documentRef,
        Number(readCookie(documentRef.cookie, "codex-zoom", "100")),
    );
    let refreshTimer;

    const refresh = async () => {
        try {
            const response = await fetchImpl(buildUsageApiUrl(apiUrl, recentWindow), {
                cache: "no-store",
            });
            if (!response.ok) {
                throw new Error(`Dashboard request failed with HTTP ${response.status}.`);
            }
            renderDashboard(documentRef, await response.json());
        } catch (error) {
            renderDashboard(documentRef, {
                available: false,
                diagnostics: {
                    code: "dashboard_request_failed",
                    message: error.message,
                },
            });
        }
    };

    documentRef.getElementById("recent-window").addEventListener("change", (event) => {
        recentWindow = applyRecentWindow(documentRef, event.target.value);
        writeCookie(documentRef, "codex-recent", recentWindow);
        refresh();
    });
    documentRef.getElementById("refresh").addEventListener("click", refresh);
    documentRef.getElementById("zoom-in").addEventListener("click", () => {
        zoom = applyZoom(documentRef, zoom + 10);
        writeCookie(documentRef, "codex-zoom", zoom);
    });
    documentRef.getElementById("zoom-out").addEventListener("click", () => {
        zoom = applyZoom(documentRef, zoom - 10);
        writeCookie(documentRef, "codex-zoom", zoom);
    });

    const themeDialog = documentRef.getElementById("theme-dialog");
    const openThemeDialog = () => {
        populateThemeForm(documentRef, customTheme);
        applyTheme(documentRef, "custom");
        applyCustomTheme(documentRef, customTheme);
        themeDialog.showModal();
    };
    documentRef.getElementById("theme").addEventListener("change", (event) => {
        if (event.target.value === "custom") {
            openThemeDialog();
            return;
        }
        theme = applyTheme(documentRef, event.target.value);
        writeCookie(documentRef, "codex-theme", theme);
    });
    documentRef.getElementById("customize-theme").addEventListener("click", openThemeDialog);
    documentRef.getElementById("theme-fields").addEventListener("input", () => {
        applyCustomTheme(documentRef, readThemeForm(documentRef));
    });
    documentRef.getElementById("cancel-theme").addEventListener("click", () => {
        theme = applyTheme(documentRef, theme);
        if (theme === "custom") {
            applyCustomTheme(documentRef, customTheme);
        }
        themeDialog.close();
    });
    documentRef.getElementById("save-theme").addEventListener("click", () => {
        customTheme = readThemeForm(documentRef);
        writeCookie(documentRef, "codex-custom-theme", JSON.stringify(customTheme));
        theme = applyTheme(documentRef, "custom");
        applyCustomTheme(documentRef, customTheme);
        writeCookie(documentRef, "codex-theme", theme);
        themeDialog.close();
    });
    documentRef.getElementById("reset-theme").addEventListener("click", () => {
        populateThemeForm(documentRef, DEFAULT_CUSTOM_THEME);
        applyCustomTheme(documentRef, DEFAULT_CUSTOM_THEME);
    });

    await refresh();
    refreshTimer = setInterval(refresh, refreshIntervalMs);
    refreshTimer.unref?.();
    return {
        refresh,
        stop() {
            clearInterval(refreshTimer);
        },
    };
}

export function renderDashboard(documentRef, snapshot) {
    const view = buildDashboardView(snapshot);
    for (const id of ["sessions-count", "total-tokens", "cached-tokens", "requests", "tool-calls", "plan"]) {
        const key = id.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
        setText(documentRef, id, view.available ? view[key] : "Unavailable");
    }
    setText(documentRef, "last-refresh", view.available ? `Updated ${view.refreshed}` : "Refresh unavailable");
    setText(documentRef, "rate-title", view.rateLimit.title);
    setText(documentRef, "rate-value", view.rateLimit.value);
    setText(documentRef, "rate-meta", view.rateLimit.meta);
    setText(documentRef, "rate-projected-title", view.rateLimit.projection.title);
    setText(documentRef, "rate-projected-value", view.rateLimit.projection.value);
    const rateBar = documentRef.getElementById("rate-bar");
    rateBar.style.width = `${view.rateLimit.percent}%`;
    rateBar.classList.toggle("warning", view.rateLimit.percent >= 80);
    const rateProgress = rateBar.parentElement;
    rateProgress.ariaValueMin = "0";
    rateProgress.ariaValueMax = "100";
    rateProgress.ariaValueNow = String(Math.round(view.rateLimit.percent));
    const projectedBar = documentRef.getElementById("rate-projected-bar");
    const projectedPercent = Math.min(100, Math.round(view.rateLimit.projection.percent));
    projectedBar.style.width = `${projectedPercent}%`;
    projectedBar.classList.toggle("warning", view.rateLimit.projection.percent >= 100);
    const projectedMarker = documentRef.getElementById("rate-projected-marker");
    projectedMarker.style.left = `${projectedPercent}%`;
    projectedMarker.hidden = !view.rateLimit.projection.available;
    projectedMarker.classList.toggle("warning", view.rateLimit.projection.percent >= 100);
    rateProgress.ariaValueText = view.rateLimit.projection.available
        ? `${view.rateLimit.value} used, ${view.rateLimit.projection.value} projected at reset`
        : `${view.rateLimit.value} used, projection unavailable`;
    renderSessions(documentRef, view.sessions);
}

const sessionTableState = new WeakMap();
const expandedSessionRows = new WeakMap();

export function renderSessions(documentRef, sessions) {
    const body = documentRef.getElementById("sessions");
    const previousState = sessionTableState.get(body) || new Map();
    const expandedRows = expandedSessionRows.get(body) || new Set();
    const currentState = new Map();
    const currentIds = new Set(sessions.map((session) => session.sessionId));
    for (const sessionId of expandedRows) {
        if (!currentIds.has(sessionId)) {
            expandedRows.delete(sessionId);
        }
    }
    body.replaceChildren();
    sessions.forEach((session, index) => {
        const fingerprint = JSON.stringify([
            session.status,
            session.project,
            session.branch,
            session.summary,
            session.model,
            session.contextPercent,
            session.totalTokens,
            session.requests,
            session.toolCalls,
            session.updatedAt,
            session.activity,
        ]);
        const changed = previousState.has(session.sessionId)
            && previousState.get(session.sessionId) !== fingerprint;
        currentState.set(session.sessionId, fingerprint);
        const row = documentRef.createElement("tr");
        row.classList.add("session-row");
        row.dataset.sessionId = session.sessionId;
        const detailId = `session-activity-${index}-${safeId(session.sessionId)}`;
        const expanded = expandedRows.has(session.sessionId);
        if (changed) {
            row.classList.add("row-updated");
        }
        const disclosure = statusCell(documentRef, session.status, detailId, expanded);
        row.append(disclosure.cell);
        for (const value of [
            session.project,
            session.branch,
            session.summary,
            session.model,
        ]) {
            const cell = documentRef.createElement("td");
            cell.textContent = value;
            cell.title = value;
            row.append(cell);
        }
        row.append(contextCell(documentRef, session.contextPercent));
        for (const value of [
            session.totalTokens,
            session.requests,
            session.toolCalls,
            session.sessionRuntime,
            session.promptRuntime,
            session.updated,
        ]) {
            const cell = documentRef.createElement("td");
            cell.textContent = value;
            row.append(cell);
        }
        const detailRow = activityRow(documentRef, session.activity, detailId, expanded);
        const toggle = () => {
            const isExpanded = !expandedRows.has(session.sessionId);
            if (isExpanded) {
                expandedRows.add(session.sessionId);
            } else {
                expandedRows.delete(session.sessionId);
            }
            row.classList.toggle("is-expanded", isExpanded);
            disclosure.button.ariaExpanded = String(isExpanded);
            disclosure.button.ariaLabel = `${isExpanded ? "Collapse" : "Expand"} ${session.status.toLowerCase()} session activity`;
            detailRow.hidden = !isExpanded;
        };
        row.classList.toggle("is-expanded", expanded);
        row.addEventListener("pointerdown", (event) => {
            if (event.button === undefined || event.button === 0) {
                toggle();
            }
        });
        disclosure.button.addEventListener("click", (event) => {
            if (event.detail === 0) {
                toggle();
            }
        });
        body.append(row, detailRow);
    });
    sessionTableState.set(body, currentState);
    expandedSessionRows.set(body, expandedRows);
    documentRef.getElementById("sessions-empty").hidden = sessions.length > 0;
}

function statusCell(documentRef, status, detailId, expanded) {
    const cell = documentRef.createElement("td");
    const button = documentRef.createElement("button");
    button.type = "button";
    button.className = "row-toggle";
    button.ariaExpanded = String(expanded);
    button.ariaControls = detailId;
    button.ariaLabel = `${expanded ? "Collapse" : "Expand"} ${status.toLowerCase()} session activity`;
    const badge = documentRef.createElement("span");
    badge.className = `status-badge ${status.toLowerCase()}`;
    const dot = documentRef.createElement("span");
    dot.className = "status-dot";
    dot.ariaHidden = "true";
    badge.append(dot, documentRef.createTextNode(status));
    button.append(badge);
    cell.append(button);
    return { cell, button };
}

function activityRow(documentRef, activity, detailId, expanded) {
    const row = documentRef.createElement("tr");
    row.id = detailId;
    row.classList.add("session-details-row");
    row.hidden = !expanded;
    const cell = documentRef.createElement("td");
    cell.colSpan = 12;
    const list = documentRef.createElement("ol");
    list.className = "activity-list";
    for (const item of activity) {
        const listItem = documentRef.createElement("li");
        listItem.className = `activity-item ${item.type}`;
        const time = documentRef.createElement("time");
        time.className = "activity-time";
        time.dateTime = item.timestamp || "";
        time.textContent = `${item.localTime} -- `;
        const kind = documentRef.createElement("span");
        kind.className = "activity-kind";
        kind.textContent = item.type === "message" ? "Assistant" : "Tool";
        const content = documentRef.createElement(item.type === "tool" ? "code" : "span");
        content.className = "activity-text";
        content.textContent = item.text;
        listItem.append(time, kind, content);
        list.append(listItem);
    }
    if (activity.length === 0) {
        const empty = documentRef.createElement("li");
        empty.className = "activity-empty";
        empty.textContent = "No assistant activity recorded yet.";
        list.append(empty);
    }
    cell.append(list);
    row.append(cell);
    return row;
}

function normalizeActivity(activity) {
    if (!Array.isArray(activity)) {
        return [];
    }
    return activity
        .filter((item) => item && (item.type === "message" || item.type === "tool"))
        .map((item) => ({
            type: item.type,
            text: String(item.text || "").trim(),
            timestamp: normalizeTimestamp(item.timestamp),
            localTime: formatLocalTimestamp(item.timestamp),
        }))
        .filter((item) => item.text)
        .slice(0, 5);
}

function normalizeTimestamp(value) {
    const timestamp = Date.parse(value);
    return Number.isFinite(timestamp) ? new Date(timestamp).toISOString() : null;
}

function formatLocalTimestamp(value) {
    const timestamp = Date.parse(value);
    if (!Number.isFinite(timestamp)) {
        return "Time unavailable";
    }
    const date = new Date(timestamp);
    const year = date.getFullYear();
    const month = String(date.getMonth() + 1).padStart(2, "0");
    const day = String(date.getDate()).padStart(2, "0");
    const hour = String(date.getHours() % 12 || 12).padStart(2, "0");
    const minute = String(date.getMinutes()).padStart(2, "0");
    const period = date.getHours() >= 12 ? "PM" : "AM";
    return `${year}-${month}-${day} ${hour}:${minute} ${period}`;
}

function safeId(value) {
    return String(value).replace(/[^A-Za-z0-9_-]/g, "-");
}

function contextCell(documentRef, percent) {
    const cell = documentRef.createElement("td");
    if (percent === null) {
        cell.textContent = "Unavailable";
        return cell;
    }
    const rounded = Math.round(percent);
    const meter = documentRef.createElement("div");
    meter.className = "context-meter";
    meter.role = "progressbar";
    meter.ariaLabel = `Context window: ${rounded}% used`;
    meter.ariaValueMin = "0";
    meter.ariaValueMax = "100";
    meter.ariaValueNow = String(rounded);
    const track = documentRef.createElement("span");
    track.className = "context-track";
    const fill = documentRef.createElement("span");
    fill.className = "context-fill";
    fill.style.width = `${percent}%`;
    track.append(fill);
    const label = documentRef.createElement("span");
    label.textContent = `${rounded}%`;
    meter.append(track, label);
    cell.append(meter);
    return cell;
}

function buildRateLimit(rateLimits, now) {
    const window = selectWeeklyRateWindow(rateLimits);
    const percent = normalizePercent(window?.usedPercent);
    if (percent === null) {
        return unavailableRateLimit();
    }
    const minutes = Number(window.windowMinutes);
    const title = minutes >= 7 * 24 * 60 ? "Weekly limit" : `${formatInteger(minutes)} minute limit`;
    const reset = typeof window.resetsAt === "number" && Number.isFinite(window.resetsAt)
        ? window.resetsAt * 1_000
        : Number.NaN;
    return {
        title,
        value: `${Math.round(percent)}%`,
        percent,
        meta: Number.isFinite(reset)
            ? `Resets ${formatRelativeFuture(reset, now)} (${formatLocalResetTime(reset)})`
            : "Reset unavailable",
        projection: buildRateLimitProjection({
            percent,
            reset,
            windowMinutes: minutes,
            now,
            weekly: minutes >= 7 * 24 * 60,
        }),
    };
}

function selectWeeklyRateWindow(rateLimits) {
    const windows = [rateLimits?.primary, rateLimits?.secondary].filter(Boolean);
    return windows.find((window) => Number(window.windowMinutes) >= 7 * 24 * 60)
        || windows[0];
}

function buildRateLimitProjection({ percent, reset, windowMinutes, now, weekly }) {
    const windowMs = windowMinutes * 60_000;
    const start = reset - windowMs;
    if (
        !Number.isFinite(reset)
        || !Number.isFinite(windowMs)
        || windowMs <= 0
        || reset <= now
        || start > now
    ) {
        return unavailableRateLimitProjection(weekly);
    }
    const elapsedMs = Math.min(windowMs, Math.max(24 * 60 * 60 * 1_000, now - start));
    const projectedPercent = Math.max(percent, percent / (elapsedMs / windowMs));
    const rounded = Math.round(projectedPercent);
    let meta;
    if (rounded === 100) {
        meta = "At current pace · on track to reach the limit";
    } else if (rounded > 100) {
        meta = `At current pace · ${rounded - 100} points over limit`;
    } else {
        meta = `At current pace · ${100 - rounded} points below limit`;
    }
    return {
        title: weekly ? "Projected at weekly reset" : "Projected at reset",
        value: `${rounded}%`,
        percent: projectedPercent,
        meta,
        available: true,
    };
}

function unavailableRateLimitProjection(weekly = false) {
    return {
        title: weekly ? "Projected at weekly reset" : "Projected at reset",
        value: "Unavailable",
        percent: 0,
        meta: "Projection unavailable",
        available: false,
    };
}

function unavailableRateLimit() {
    return {
        title: "Rate limit",
        value: "Unavailable",
        percent: 0,
        meta: "No rate-limit snapshot found",
        projection: unavailableRateLimitProjection(),
    };
}

function applyTheme(documentRef, value) {
    const theme = THEMES.has(value) ? value : "system";
    documentRef.documentElement.dataset.theme = theme;
    documentRef.getElementById("theme").value = theme;
    documentRef.getElementById("customize-theme").hidden = theme !== "custom";
    if (theme !== "custom") {
        clearCustomTheme(documentRef);
    }
    return theme;
}

function applyRecentWindow(documentRef, value) {
    const recentWindow = normalizeRecentWindow(value);
    documentRef.getElementById("recent-window").value = recentWindow;
    return recentWindow;
}

function normalizeRecentWindow(value) {
    const normalized = String(value || "86400000");
    return RECENT_WINDOWS.has(normalized) ? normalized : "86400000";
}

function applyZoom(documentRef, value) {
    const zoom = Math.min(160, Math.max(70, Math.round(Number(value) / 10) * 10 || 100));
    documentRef.documentElement.style.setProperty("--dashboard-zoom", zoom / 100);
    documentRef.getElementById("zoom-level").textContent = `${zoom}%`;
    return zoom;
}

function applyCustomTheme(documentRef, value) {
    const theme = normalizeCustomTheme(value);
    const style = documentRef.documentElement.style;
    const properties = {
        "--font-ui": FONT_STACKS[theme.font],
        "--font-table": FONT_STACKS[theme.tableFont],
        "--bg": theme.background,
        "--panel": theme.panel,
        "--panel-strong": theme.panel,
        "--text": theme.text,
        "--muted": theme.muted,
        "--accent": theme.accent,
        "--accent-secondary": theme.secondary,
        "--active": theme.active,
        "--idle": theme.idle,
    };
    for (const [name, propertyValue] of Object.entries(properties)) {
        style.setProperty(name, propertyValue);
    }
    return theme;
}

function clearCustomTheme(documentRef) {
    for (const name of [
        "--font-ui",
        "--font-table",
        "--bg",
        "--panel",
        "--panel-strong",
        "--text",
        "--muted",
        "--accent",
        "--accent-secondary",
        "--active",
        "--idle",
    ]) {
        documentRef.documentElement.style.removeProperty(name);
    }
}

function normalizeCustomTheme(value) {
    const source = value && typeof value === "object" ? value : {};
    const color = (key) => /^#[0-9a-f]{6}$/i.test(source[key])
        ? source[key]
        : DEFAULT_CUSTOM_THEME[key];
    return {
        font: FONT_STACKS[source.font] ? source.font : DEFAULT_CUSTOM_THEME.font,
        tableFont: FONT_STACKS[source.tableFont] ? source.tableFont : DEFAULT_CUSTOM_THEME.tableFont,
        background: color("background"),
        panel: color("panel"),
        text: color("text"),
        muted: color("muted"),
        accent: color("accent"),
        secondary: color("secondary"),
        active: color("active"),
        idle: color("idle"),
    };
}

function readCustomTheme(cookieHeader) {
    try {
        return normalizeCustomTheme(JSON.parse(readCookie(
            cookieHeader,
            "codex-custom-theme",
            JSON.stringify(DEFAULT_CUSTOM_THEME),
        )));
    } catch {
        return { ...DEFAULT_CUSTOM_THEME };
    }
}

function populateThemeForm(documentRef, theme) {
    const normalized = normalizeCustomTheme(theme);
    for (const [key, value] of Object.entries(normalized)) {
        documentRef.getElementById(`theme-${key}`).value = value;
    }
}

function readThemeForm(documentRef) {
    return normalizeCustomTheme(Object.fromEntries(
        Object.keys(DEFAULT_CUSTOM_THEME).map((key) => [
            key,
            documentRef.getElementById(`theme-${key}`).value,
        ]),
    ));
}

function readCookie(cookieHeader, name, fallback) {
    const match = String(cookieHeader || "").match(new RegExp(`(?:^|;\\s*)${name}=([^;]*)`));
    return match ? decodeURIComponent(match[1]) : fallback;
}

function writeCookie(documentRef, name, value) {
    documentRef.cookie = `${name}=${encodeURIComponent(value)}; Max-Age=31536000; Path=/; SameSite=Strict`;
}

function setText(documentRef, id, value) {
    documentRef.getElementById(id).textContent = value;
}

function normalizePercent(value) {
    return typeof value === "number" && Number.isFinite(value) && value >= 0
        ? Math.min(100, value)
        : null;
}

function formatInteger(value) {
    return typeof value === "number" && Number.isFinite(value)
        ? Math.round(value).toLocaleString()
        : "Unavailable";
}

function formatTokenCount(value) {
    return typeof value === "number" && Number.isFinite(value)
        ? new Intl.NumberFormat("en", {
            notation: "compact",
            maximumSignificantDigits: 3,
        }).format(Math.round(value))
        : "Unavailable";
}

function formatDuration(start, end) {
    if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
        return "Unavailable";
    }
    return formatElapsed(end - start);
}

function formatRelativeAge(value, now) {
    const timestamp = typeof value === "number" ? value : Date.parse(value);
    return Number.isFinite(timestamp) && timestamp <= now
        ? `${formatElapsed(now - timestamp)} ago`
        : "Unavailable";
}

function formatRelativeFuture(value, now) {
    return Number.isFinite(value) && value > now
        ? `in ${formatDetailedElapsed(value - now)}`
        : "now";
}

function formatDetailedElapsed(milliseconds) {
    const totalMinutes = Math.max(0, Math.floor(milliseconds / 60_000));
    const days = Math.floor(totalMinutes / (24 * 60));
    const hours = Math.floor(totalMinutes % (24 * 60) / 60);
    const minutes = totalMinutes % 60;
    if (days > 0) {
        return `${days}d ${hours}h`;
    }
    if (hours > 0) {
        return `${hours}h ${minutes}m`;
    }
    return `${minutes}m`;
}

function formatLocalResetTime(value) {
    return new Intl.DateTimeFormat(undefined, {
        weekday: "short",
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
        timeZoneName: "short",
    }).format(new Date(value));
}

function formatElapsed(milliseconds) {
    const seconds = Math.max(0, Math.floor(milliseconds / 1_000));
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes}m`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours}h`;
    const days = Math.floor(hours / 24);
    if (days < 30) return `${days}d`;
    return `${Math.floor(days / 30)}mo`;
}
