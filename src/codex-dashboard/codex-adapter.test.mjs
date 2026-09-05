import assert from "node:assert/strict";
import test from "node:test";

import {
    createCodexAdapter,
    parseRollout,
    projectNameFromCwd,
} from "./codex-adapter.mjs";

const now = () => new Date("2026-08-18T16:30:00.000Z");

test("derives project names from Windows and POSIX paths", () => {
    const cases = [
        ["D:\\github\\HemSoft\\gh-x", "gh-x"],
        ["/home/runner/work/gh-x", "gh-x"],
        ["/home/runner/work/foo\\bar", "foo\\bar"],
        ["team\\repo", "team\\repo"],
    ];

    for (const [cwd, expected] of cases) {
        assert.equal(projectNameFromCwd(cwd), expected);
    }
});

test("parses rollout usage, rate limits, tools, and active state evidence", async () => {
    const events = [
        event("2026-08-18T16:00:00.000Z", "event_msg", "task_started"),
        userMessage("2026-08-18T16:00:00.500Z", "Original user prompt"),
        responseUserMessage("2026-08-18T16:00:00.600Z", "<environment_context>internal</environment_context>"),
        assistantMessage("2026-08-18T16:00:00.700Z", "The dashboard is ready."),
        toolCall("2026-08-18T16:00:01.000Z", "shell_command"),
        responseUserMessage("2026-08-18T16:00:01.500Z", "Most recent user prompt"),
        responseUserMessage("2026-08-18T16:00:01.600Z", "<subagent_notification>internal</subagent_notification>"),
        nestedToolCall("2026-08-18T16:00:01.700Z", "mcp__node_repl__js"),
        toolCall("2026-08-18T16:00:01.710Z", "wait"),
        toolCall("2026-08-18T16:00:01.720Z", "view_image"),
        toolCall("2026-08-18T16:00:01.730Z", "apply_patch"),
        {
            ...event("2026-08-18T16:00:02.000Z", "event_msg", "token_count"),
            payload: {
                type: "token_count",
                info: {
                    total_token_usage: {
                        input_tokens: 50_000,
                        cached_input_tokens: 20_000,
                        output_tokens: 1_000,
                        reasoning_output_tokens: 200,
                        total_tokens: 51_000,
                    },
                    last_token_usage: {
                        input_tokens: 25_000,
                        cached_input_tokens: 10_000,
                        output_tokens: 500,
                        reasoning_output_tokens: 100,
                        total_tokens: 25_500,
                    },
                    model_context_window: 100_000,
                },
                rate_limits: {
                    limit_id: "codex",
                    primary: {
                        used_percent: 71,
                        window_minutes: 10_080,
                        resets_at: 1_787_196_686,
                    },
                    credits: {
                        has_credits: false,
                        unlimited: false,
                        balance: "0",
                    },
                    plan_type: "pro",
                },
            },
        },
    ];
    const rollout = await parseRollout("fixture.jsonl", async function* () {
        yield* events;
    });

    assert.equal(rollout.taskStarts, 1);
    assert.equal(rollout.toolCalls, 5);
    assert.equal(rollout.usage.lastTokenUsage.inputTokens, 25_000);
    assert.equal(rollout.rateLimits.primary.used_percent, 71);
    assert.equal(rollout.latestUserPrompt, "Most recent user prompt");
    assert.equal(rollout.latestUserPromptAt, "2026-08-18T16:00:01.500Z");
    assert.equal(rollout.latestAssistantMessage.text, "The dashboard is ready.");
    assert.deepEqual(
        rollout.recentToolCalls.map((tool) => tool.name),
        ["node_repl.js()", "wait()", "view_image()", "apply_patch()"],
    );
    assert.ok(rollout.lastTaskStartedAt > rollout.lastTaskEndedAt);
});

test("builds a complete snapshot through one adapter interface", async () => {
    const row = {
        id: "thread-1",
        rollout_path: "sessions/rollout.jsonl",
        created_at_ms: Date.parse("2026-08-18T15:00:00.000Z"),
        updated_at_ms: Date.parse("2026-08-18T16:29:50.000Z"),
        cwd: "D:\\github\\HemSoft\\gh-x",
        title: "Build Codex dashboard",
        tokens_used: 75_000,
        model: "gpt-5.5",
        git_branch: "feature/codex-dashboard",
    };
    const database = {
        prepare: () => ({ all: () => [row] }),
        close() {},
    };
    const adapter = createCodexAdapter({
        codexHome: "C:\\Users\\User\\.codex",
        now,
        sqliteModuleLoader: async () => ({
            DatabaseSync: class {
                constructor() {
                    return database;
                }
            },
        }),
        stat: () => ({ size: 10, mtimeMs: 20 }),
        readLines: async function* () {
            yield event("2026-08-18T16:29:40.000Z", "event_msg", "task_started");
            yield responseUserMessage("2026-08-18T16:29:41.000Z", "Latest dashboard request");
            yield assistantMessage("2026-08-18T16:29:42.000Z", "I am updating the dashboard.");
            yield toolCall("2026-08-18T16:29:43.000Z", "shell_command");
            yield {
                ...event("2026-08-18T16:29:45.000Z", "event_msg", "token_count"),
                payload: {
                    type: "token_count",
                    info: {
                        total_token_usage: {
                            input_tokens: 60_000,
                            cached_input_tokens: 12_000,
                            output_tokens: 2_000,
                            total_tokens: 62_000,
                        },
                        last_token_usage: {
                            input_tokens: 40_000,
                            cached_input_tokens: 8_000,
                            output_tokens: 1_000,
                            total_tokens: 41_000,
                        },
                        model_context_window: 100_000,
                    },
                    rate_limits: null,
                },
            };
        },
    });

    const snapshot = await adapter.getSnapshot({ recentWindowMs: 3_600_000 });
    assert.equal(snapshot.available, true);
    assert.equal(snapshot.totals.recentSessionCount, 1);
    assert.equal(snapshot.totals.totalTokens, 75_000);
    assert.equal(snapshot.totals.cachedInputTokens, 12_000);
    assert.equal(snapshot.sessions[0].status, "active");
    assert.equal(snapshot.sessions[0].project, "gh-x");
    assert.equal(snapshot.sessions[0].summary, "Latest dashboard request");
    assert.equal(snapshot.sessions[0].lastPromptAt, "2026-08-18T16:29:41.000Z");
    assert.deepEqual(snapshot.sessions[0].activity, [
        {
            type: "message",
            text: "I am updating the dashboard.",
            timestamp: "2026-08-18T16:29:42.000Z",
        },
        {
            type: "tool",
            text: "shell_command()",
            timestamp: "2026-08-18T16:29:43.000Z",
        },
    ]);
    assert.equal(snapshot.sessions[0].context.percentUsed, 40);
});

test("evicts rollout summaries that leave the current snapshot", async () => {
    let currentPath = "sessions/rollout-0.jsonl";
    const reads = new Map();
    const adapter = createCodexAdapter({
        codexHome: "C:\\Users\\User\\.codex",
        now,
        sqliteModuleLoader: fakeSqliteLoader(() => [snapshotRow(currentPath)]),
        stat: () => ({ size: 10, mtimeMs: 20 }),
        readLines: async function* (filePath) {
            reads.set(filePath, (reads.get(filePath) || 0) + 1);
            yield userMessage("2026-08-18T16:29:41.000Z", filePath);
        },
    });

    await adapter.getSnapshot();
    await adapter.getSnapshot();
    const [firstRolloutPath] = reads.keys();
    assert.equal(reads.get(firstRolloutPath), 1);

    for (let index = 1; index <= 128; index += 1) {
        currentPath = `sessions/rollout-${index}.jsonl`;
        await adapter.getSnapshot();
    }

    currentPath = "sessions/rollout-0.jsonl";
    await adapter.getSnapshot();
    assert.equal(reads.get(firstRolloutPath), 2);
});

test("invalidates changed and missing rollout cache entries", async () => {
    let mtimeMs = 20;
    let missing = false;
    let reads = 0;
    const adapter = createCodexAdapter({
        codexHome: "C:\\Users\\User\\.codex",
        now,
        sqliteModuleLoader: fakeSqliteLoader(() => [snapshotRow("sessions/rollout.jsonl")]),
        stat: () => {
            if (missing) {
                throw Object.assign(new Error("missing rollout"), { code: "ENOENT" });
            }
            return { size: 10, mtimeMs };
        },
        readLines: async function* () {
            reads += 1;
            yield userMessage("2026-08-18T16:29:41.000Z", `prompt-${reads}`);
        },
    });

    assert.equal((await adapter.getSnapshot()).sessions[0].summary, "prompt-1");
    assert.equal((await adapter.getSnapshot()).sessions[0].summary, "prompt-1");

    mtimeMs += 1;
    assert.equal((await adapter.getSnapshot()).sessions[0].summary, "prompt-2");

    missing = true;
    assert.equal((await adapter.getSnapshot()).sessions[0].summary, "Snapshot title");

    missing = false;
    assert.equal((await adapter.getSnapshot()).sessions[0].summary, "prompt-3");
    assert.equal(reads, 3);
});

function snapshotRow(rolloutPath) {
    return {
        id: rolloutPath,
        rollout_path: rolloutPath,
        created_at_ms: Date.parse("2026-08-18T15:00:00.000Z"),
        updated_at_ms: Date.parse("2026-08-18T16:29:50.000Z"),
        cwd: "D:\\github\\HemSoft\\gh-x",
        title: "Snapshot title",
        tokens_used: 1,
        model: "gpt-5.5",
        git_branch: "main",
    };
}

function fakeSqliteLoader(rows) {
    return async () => ({
        DatabaseSync: class {
            prepare() {
                return { all: rows };
            }

            close() {}
        },
    });
}

function event(timestamp, type, payloadType) {
    return {
        timestamp,
        type,
        payload: {
            type: payloadType,
        },
    };
}

function userMessage(timestamp, message) {
    return {
        timestamp,
        type: "event_msg",
        payload: {
            type: "user_message",
            message,
        },
    };
}

function responseUserMessage(timestamp, text) {
    return {
        timestamp,
        type: "response_item",
        payload: {
            type: "message",
            role: "user",
            content: [{
                type: "input_text",
                text,
            }],
        },
    };
}

function assistantMessage(timestamp, text) {
    return {
        timestamp,
        type: "response_item",
        payload: {
            type: "message",
            role: "assistant",
            content: [{
                type: "output_text",
                text,
            }],
        },
    };
}

function toolCall(timestamp, name) {
    return {
        ...event(timestamp, "response_item", "function_call"),
        payload: {
            type: "function_call",
            name,
        },
    };
}

function nestedToolCall(timestamp, name) {
    return {
        ...event(timestamp, "response_item", "custom_tool_call"),
        payload: {
            type: "custom_tool_call",
            name: "exec",
            input: `const result = await tools.${name}({});`,
        },
    };
}
