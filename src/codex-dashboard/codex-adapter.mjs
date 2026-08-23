import os from "node:os";
import path from "node:path";
import { createReadStream, statSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { createInterface } from "node:readline";

const DEFAULT_RECENT_WINDOW_MS = 24 * 60 * 60 * 1_000;

export function resolveCodexHome({
    env = process.env,
    homedir = os.homedir,
} = {}) {
    return env.CODEX_HOME || path.join(homedir(), ".codex");
}

export function createCodexAdapter({
    codexHome = resolveCodexHome(),
    now = () => new Date(),
    sqliteModuleLoader = () => import("node:sqlite"),
    stat = statSync,
    readLines = readJsonLines,
} = {}) {
    const rolloutCache = new Map();

    return {
        async getSnapshot(filter = {}) {
            const refreshedAt = now();
            try {
                const cutoff = resolveRecentCutoff(refreshedAt, filter);
                const threads = await readThreads({
                    cutoff,
                    codexHome,
                    sqliteModuleLoader,
                });
                const sessions = [];
                let latestRateLimits = null;
                let latestRateLimitAt = Number.NEGATIVE_INFINITY;

                for (const thread of threads) {
                    const rollout = await readRolloutCached(thread.rolloutPath, {
                        cache: rolloutCache,
                        readLines,
                        stat,
                    });
                    const updatedAtMs = Math.max(thread.updatedAtMs, rollout.updatedAtMs || 0);
                    const active = rollout.lastTaskStartedAt > rollout.lastTaskEndedAt
                        && refreshedAt.getTime() - updatedAtMs < 10 * 60 * 1_000;
                    const usage = rollout.usage;
                    const contextTokens = usage?.lastTokenUsage?.inputTokens ?? null;
                    const contextLimit = usage?.modelContextWindow ?? null;
                    const contextPercent = contextTokens !== null && contextLimit > 0
                        ? Math.min(100, contextTokens / contextLimit * 100)
                        : null;

                    if (rollout.rateLimits && rollout.rateLimitsAt > latestRateLimitAt) {
                        latestRateLimits = rollout.rateLimits;
                        latestRateLimitAt = rollout.rateLimitsAt;
                    }

                    sessions.push({
                        sessionId: thread.id,
                        status: active ? "active" : "idle",
                        project: projectNameFromCwd(thread.cwd),
                        cwd: thread.cwd,
                        branch: thread.branch || "Unavailable",
                        summary: rollout.latestUserPrompt || thread.title || "Untitled session",
                        lastPromptAt: rollout.latestUserPromptAt,
                        activity: buildRecentActivity(rollout),
                        model: thread.model || "Unavailable",
                        createdAt: new Date(thread.createdAtMs).toISOString(),
                        updatedAt: new Date(updatedAtMs).toISOString(),
                        totalTokens: thread.tokensUsed,
                        cachedInputTokens: usage?.totalTokenUsage?.cachedInputTokens ?? 0,
                        toolCalls: rollout.toolCalls,
                        requests: rollout.taskStarts,
                        context: contextPercent === null ? null : {
                            currentTokens: contextTokens,
                            tokenLimit: contextLimit,
                            percentUsed: contextPercent,
                        },
                    });
                }

                sessions.sort((left, right) => right.updatedAt.localeCompare(left.updatedAt));
                return {
                    available: true,
                    source: "codex-local",
                    lastRefreshAt: refreshedAt.toISOString(),
                    windows: {
                        recentSince: cutoff.toISOString(),
                        recentMs: refreshedAt.getTime() - cutoff.getTime(),
                    },
                    totals: {
                        recentSessionCount: sessions.length,
                        activeSessionCount: sessions.filter((session) => session.status === "active").length,
                        totalTokens: sum(sessions, "totalTokens"),
                        cachedInputTokens: sum(sessions, "cachedInputTokens"),
                        toolCalls: sum(sessions, "toolCalls"),
                        requests: sum(sessions, "requests"),
                    },
                    rateLimits: normalizeRateLimits(latestRateLimits),
                    sessions,
                    diagnostics: {
                        code: null,
                        message: null,
                    },
                };
            } catch (error) {
                return {
                    available: false,
                    source: "codex-local",
                    lastRefreshAt: refreshedAt.toISOString(),
                    windows: null,
                    totals: null,
                    rateLimits: null,
                    sessions: [],
                    diagnostics: {
                        code: error?.code || "codex_dashboard_unavailable",
                        message: error?.message || "Unable to read Codex session data.",
                    },
                };
            }
        },
    };
}

export function projectNameFromCwd(cwd) {
    return path.win32.basename(cwd) || cwd;
}

async function readThreads({
    cutoff,
    codexHome,
    sqliteModuleLoader,
}) {
    const sqlite = await sqliteModuleLoader();
    if (typeof sqlite?.DatabaseSync !== "function") {
        throw adapterError("codex_sqlite_unavailable", "This Node runtime does not expose node:sqlite.");
    }
    const databasePath = path.join(codexHome, "state_5.sqlite");
    let database;
    try {
        database = new sqlite.DatabaseSync(databasePath, { readOnly: true });
        const rows = database.prepare(`
            SELECT id, rollout_path, created_at_ms, updated_at_ms, cwd, title,
                   tokens_used, model, git_branch
            FROM threads
            WHERE archived = 0 AND updated_at_ms >= ?
            ORDER BY updated_at_ms DESC
        `).all(cutoff.getTime());
        return rows.map((row) => normalizeThread(row, codexHome));
    } catch (error) {
        throw adapterError(
            "codex_state_unavailable",
            `Unable to read ${databasePath}: ${error?.message || "unknown SQLite error"}`,
        );
    } finally {
        database?.close();
    }
}

function normalizeThread(row, codexHome) {
    const createdAtMs = finiteNonNegative(row.created_at_ms, "threads.created_at_ms");
    const updatedAtMs = finiteNonNegative(row.updated_at_ms, "threads.updated_at_ms");
    const rolloutPath = path.isAbsolute(row.rollout_path)
        ? row.rollout_path
        : path.join(codexHome, row.rollout_path);
    return {
        id: String(row.id),
        rolloutPath,
        createdAtMs,
        updatedAtMs,
        cwd: String(row.cwd || "Unavailable"),
        title: String(row.title || ""),
        tokensUsed: finiteNonNegative(row.tokens_used, "threads.tokens_used"),
        model: row.model ? String(row.model) : null,
        branch: row.git_branch ? String(row.git_branch) : null,
    };
}

async function readRolloutCached(filePath, {
    cache,
    readLines,
    stat,
}) {
    let fileStats;
    try {
        fileStats = stat(filePath);
    } catch (error) {
        if (error?.code === "ENOENT") {
            return emptyRollout();
        }
        throw error;
    }
    const cached = cache.get(filePath);
    if (cached && cached.size === fileStats.size && cached.mtimeMs === fileStats.mtimeMs) {
        return cached.value;
    }
    const value = await parseRollout(filePath, readLines);
    cache.set(filePath, {
        size: fileStats.size,
        mtimeMs: fileStats.mtimeMs,
        value,
    });
    return value;
}

export async function parseRollout(filePath, readLines = readJsonLines) {
    const result = emptyRollout();
    for await (const event of readLines(filePath)) {
        const timestamp = new Date(event?.timestamp).getTime();
        if (Number.isFinite(timestamp)) {
            result.updatedAtMs = Math.max(result.updatedAtMs, timestamp);
        }
        const payloadType = event?.payload?.type;
        const userPrompt = extractUserPrompt(event);
        if (userPrompt) {
            result.latestUserPrompt = userPrompt;
            result.latestUserPromptAt = Number.isFinite(timestamp)
                ? new Date(timestamp).toISOString()
                : null;
        }
        const assistantMessage = extractAssistantMessage(event);
        if (assistantMessage) {
            result.latestAssistantMessage = {
                text: assistantMessage,
                timestamp: event.timestamp || null,
            };
        }
        if (payloadType === "task_started") {
            result.taskStarts += 1;
            result.lastTaskStartedAt = Math.max(result.lastTaskStartedAt, timestamp || 0);
        } else if (payloadType === "task_complete" || payloadType === "turn_aborted") {
            result.lastTaskEndedAt = Math.max(result.lastTaskEndedAt, timestamp || 0);
        }
        if (
            event?.type === "response_item"
            && (payloadType === "function_call" || payloadType === "custom_tool_call")
        ) {
            result.toolCalls += 1;
            const toolName = extractToolName(event.payload);
            if (toolName) {
                result.recentToolCalls.push({
                    name: toolName,
                    timestamp: event.timestamp || null,
                });
                result.recentToolCalls = result.recentToolCalls.slice(-4);
            }
        }
        if (payloadType === "token_count") {
            const usage = normalizeUsage(event.payload.info);
            if (usage) {
                result.usage = usage;
            }
            if (event.payload.rate_limits) {
                result.rateLimits = event.payload.rate_limits;
                result.rateLimitsAt = timestamp || result.updatedAtMs;
            }
        }
    }
    return result;
}

function extractAssistantMessage(event) {
    const payload = event?.payload;
    if (
        event?.type !== "response_item"
        || payload?.type !== "message"
        || payload?.role !== "assistant"
        || !Array.isArray(payload.content)
    ) {
        return null;
    }
    const text = payload.content
        .filter((item) => item?.type === "output_text")
        .map((item) => normalizePrompt(item.text))
        .filter(Boolean)
        .join("\n\n");
    return text || null;
}

function extractToolName(payload) {
    let name = normalizePrompt(payload?.name);
    if (payload?.type === "custom_tool_call" && name === "exec") {
        const nestedName = String(payload.input || "").match(/tools\.([A-Za-z0-9_]+)\s*\(/)?.[1];
        name = nestedName || name;
    }
    if (!name) {
        return null;
    }
    return `${name.replace(/^mcp__/, "").replaceAll("__", ".")}()`;
}

function buildRecentActivity(rollout) {
    const activity = [];
    if (rollout.latestAssistantMessage) {
        activity.push({
            type: "message",
            text: rollout.latestAssistantMessage.text,
            timestamp: rollout.latestAssistantMessage.timestamp,
        });
    }
    activity.push(...rollout.recentToolCalls
        .slice()
        .reverse()
        .map((tool) => ({
            type: "tool",
            text: tool.name,
            timestamp: tool.timestamp,
        })));
    return activity.slice(0, 5);
}

function extractUserPrompt(event) {
    const payload = event?.payload;
    if (event?.type === "event_msg" && payload?.type === "user_message") {
        return normalizePrompt(payload.message);
    }
    if (
        event?.type !== "response_item"
        || payload?.type !== "message"
        || payload?.role !== "user"
        || !Array.isArray(payload.content)
    ) {
        return null;
    }
    const text = payload.content
        .filter((item) => item?.type === "input_text")
        .map((item) => normalizePrompt(item.text))
        .filter((item) => item && !isInternalUserRecord(item))
        .join("\n\n");
    return text || null;
}

function normalizePrompt(value) {
    return typeof value === "string" && value.trim() ? value.trim() : null;
}

function isInternalUserRecord(value) {
    return [
        "# AGENTS.md instructions for ",
        "<environment_context>",
        "<skill-context ",
        "<subagent_notification>",
        "<system_reminder>",
    ].some((prefix) => value.startsWith(prefix));
}

async function* readJsonLines(filePath) {
    const stream = createReadStream(filePath, { encoding: "utf8" });
    const lines = createInterface({
        input: stream,
        crlfDelay: Infinity,
    });
    for await (const line of lines) {
        if (!line.trim()) {
            continue;
        }
        try {
            yield JSON.parse(line);
        } catch (error) {
            throw adapterError(
                "codex_rollout_invalid",
                `Invalid JSONL in ${path.basename(filePath)}: ${error.message}`,
            );
        }
    }
}

function normalizeUsage(info) {
    if (!info || typeof info !== "object") {
        return null;
    }
    return {
        totalTokenUsage: normalizeTokenUsage(info.total_token_usage),
        lastTokenUsage: normalizeTokenUsage(info.last_token_usage),
        modelContextWindow: nullableNonNegative(info.model_context_window),
    };
}

function normalizeTokenUsage(value) {
    if (!value || typeof value !== "object") {
        return null;
    }
    return {
        inputTokens: nullableNonNegative(value.input_tokens) ?? 0,
        cachedInputTokens: nullableNonNegative(value.cached_input_tokens) ?? 0,
        outputTokens: nullableNonNegative(value.output_tokens) ?? 0,
        reasoningOutputTokens: nullableNonNegative(value.reasoning_output_tokens) ?? 0,
        totalTokens: nullableNonNegative(value.total_tokens) ?? 0,
    };
}

function normalizeRateLimits(value) {
    if (!value || typeof value !== "object") {
        return null;
    }
    const primary = normalizeRateWindow(value.primary);
    const secondary = normalizeRateWindow(value.secondary);
    const credits = value.credits && typeof value.credits === "object"
        ? {
            hasCredits: Boolean(value.credits.has_credits),
            unlimited: Boolean(value.credits.unlimited),
            balance: value.credits.balance === null || value.credits.balance === undefined
                ? null
                : String(value.credits.balance),
        }
        : null;
    return {
        limitId: value.limit_id || null,
        limitName: value.limit_name || null,
        planType: value.plan_type || null,
        primary,
        secondary,
        credits,
        spendControlReached: Boolean(value.spend_control_reached),
        reachedType: value.rate_limit_reached_type || null,
    };
}

function normalizeRateWindow(value) {
    if (!value || typeof value !== "object") {
        return null;
    }
    return {
        usedPercent: nullableNonNegative(value.used_percent),
        windowMinutes: nullableNonNegative(value.window_minutes),
        resetsAt: nullableNonNegative(value.resets_at),
    };
}

function resolveRecentCutoff(now, {
    recentSince,
    recentWindowMs = DEFAULT_RECENT_WINDOW_MS,
} = {}) {
    if (recentSince !== undefined) {
        const date = new Date(recentSince);
        if (!Number.isFinite(date.getTime()) || date > now) {
            throw adapterError("codex_filter_invalid", "recentSince must be a valid date at or before now.");
        }
        return date;
    }
    const windowMs = finiteNonNegative(recentWindowMs, "recentWindowMs");
    return new Date(now.getTime() - windowMs);
}

function emptyRollout() {
    return {
        updatedAtMs: 0,
        taskStarts: 0,
        toolCalls: 0,
        lastTaskStartedAt: 0,
        lastTaskEndedAt: 0,
        latestUserPrompt: null,
        latestUserPromptAt: null,
        latestAssistantMessage: null,
        recentToolCalls: [],
        usage: null,
        rateLimits: null,
        rateLimitsAt: 0,
    };
}

function sum(items, property) {
    return items.reduce((total, item) => total + item[property], 0);
}

function finiteNonNegative(value, field) {
    if (typeof value !== "number" || !Number.isFinite(value) || value < 0) {
        throw adapterError("codex_data_invalid", `${field} must contain a finite, non-negative number.`);
    }
    return value;
}

function nullableNonNegative(value) {
    return typeof value === "number" && Number.isFinite(value) && value >= 0
        ? value
        : null;
}

function adapterError(code, message) {
    return Object.assign(new Error(message), { code });
}
