import assert from "node:assert/strict";
import test from "node:test";

import {
    buildDashboardView,
    buildUsageApiUrl,
    renderDashboard,
    renderSessions,
} from "./dashboard-ui.mjs";

test("builds compact Codex usage and session values", () => {
    const activityTimestamp = new Date(2026, 7, 18, 17, 1).toISOString();
    const view = buildDashboardView({
        available: true,
        lastRefreshAt: "2026-08-18T16:29:50.000Z",
        totals: {
            totalTokens: 1_271_249_556,
            cachedInputTokens: 12_000,
            requests: 2,
            toolCalls: 4,
        },
        rateLimits: {
            planType: "pro",
            primary: {
                usedPercent: 20,
                windowMinutes: 300,
                resetsAt: Date.parse("2026-08-18T18:30:00.000Z") / 1_000,
            },
            secondary: {
                usedPercent: 71.4,
                windowMinutes: 10_080,
                resetsAt: Date.parse("2026-08-20T16:30:00.000Z") / 1_000,
            },
        },
        sessions: [{
            sessionId: "thread-1",
            status: "idle",
            project: "gh-x",
            branch: "main",
            summary: "Build dashboard",
            model: "gpt-5.5",
            createdAt: "2026-08-18T15:00:00.000Z",
            updatedAt: "2026-08-18T16:00:00.000Z",
            lastPromptAt: "2026-08-18T15:45:00.000Z",
            totalTokens: 8_645_761,
            requests: 2,
            toolCalls: 4,
            context: { percentUsed: 40.4 },
            activity: [
                { type: "message", text: "The dashboard is ready.", timestamp: activityTimestamp },
                { type: "tool", text: "node_repl.js()", timestamp: activityTimestamp },
            ],
        }],
    }, Date.parse("2026-08-18T16:30:00.000Z"));

    assert.equal(view.totalTokens, "1.27B");
    assert.equal(view.cachedTokens, "12K");
    assert.equal(view.sessions[0].totalTokens, "8.65M");
    assert.equal(view.rateLimit.title, "Weekly limit");
    assert.equal(view.rateLimit.value, "71%");
    assert.match(view.rateLimit.meta, /^Resets in 2d 0h \(.+\)$/);
    assert.equal(view.rateLimit.projection.title, "Projected at weekly reset");
    assert.equal(view.rateLimit.projection.value, "100%");
    assert.equal(view.rateLimit.projection.meta, "At current pace · on track to reach the limit");
    assert.equal(view.sessions[0].contextPercent, 40.4);
    assert.equal(view.sessions[0].sessionRuntime, "1h");
    assert.equal(view.sessions[0].promptRuntime, "15m");
    assert.equal(view.sessions[0].updated, "30m ago");
    assert.deepEqual(view.sessions[0].activity, [
        {
            type: "message",
            text: "The dashboard is ready.",
            timestamp: activityTimestamp,
            localTime: "2026-08-18 05:01 PM",
        },
        {
            type: "tool",
            text: "node_repl.js()",
            timestamp: activityTimestamp,
            localTime: "2026-08-18 05:01 PM",
        },
    ]);
});

test("glows a session row only after its displayed data changes", () => {
    const elements = new Map([
        ["sessions", fakeElement("tbody")],
        ["sessions-empty", fakeElement("div")],
    ]);
    const documentRef = {
        createElement: fakeElement,
        createTextNode: (text) => ({ textContent: text }),
        getElementById: (id) => elements.get(id),
    };
    const session = {
        sessionId: "thread-1",
        status: "Idle",
        project: "gh-x",
        branch: "main",
        summary: "Build dashboard",
        model: "gpt-5.5",
        contextPercent: 40,
        totalTokens: "75K",
        requests: "1",
        toolCalls: "2",
        sessionRuntime: "1h",
        promptRuntime: "15m",
        updated: "30m ago",
        updatedAt: "2026-08-18T16:00:00.000Z",
        activity: [],
    };

    renderSessions(documentRef, [session]);
    assert.equal(elements.get("sessions").children[0].classList.contains("row-updated"), false);

    renderSessions(documentRef, [{ ...session, requests: "2" }]);
    assert.equal(elements.get("sessions").children[0].classList.contains("row-updated"), true);

    renderSessions(documentRef, [{ ...session, requests: "2", sessionRuntime: "2h" }]);
    assert.equal(elements.get("sessions").children[0].classList.contains("row-updated"), false);
});

test("expands a session row with the last assistant message and newest tool calls", () => {
    const elements = new Map([
        ["sessions", fakeElement("tbody")],
        ["sessions-empty", fakeElement("div")],
    ]);
    const documentRef = {
        createElement: fakeElement,
        createTextNode: (text) => ({ textContent: text }),
        getElementById: (id) => elements.get(id),
    };
    const session = {
        sessionId: "thread-1",
        status: "Active",
        project: "gh-x",
        branch: "main",
        summary: "Expand dashboard row",
        model: "gpt-5.5",
        contextPercent: 40,
        totalTokens: "75K",
        requests: "1",
        toolCalls: "4",
        sessionRuntime: "1h",
        promptRuntime: "15m",
        updated: "now",
        updatedAt: "2026-08-18T16:00:00.000Z",
        activity: [
            { type: "message", text: "The pulse is now quicker and brighter.", timestamp: "2026-08-18T21:01:00.000Z", localTime: "2026-08-18 05:01 PM" },
            { type: "tool", text: "node_repl.js()", timestamp: "2026-08-18T21:02:00.000Z", localTime: "2026-08-18 05:02 PM" },
            { type: "tool", text: "shell_command()", timestamp: "2026-08-18T21:03:00.000Z", localTime: "2026-08-18 05:03 PM" },
            { type: "tool", text: "apply_patch()", timestamp: "2026-08-18T21:04:00.000Z", localTime: "2026-08-18 05:04 PM" },
            { type: "tool", text: "wait()", timestamp: "2026-08-18T21:05:00.000Z", localTime: "2026-08-18 05:05 PM" },
        ],
    };

    renderSessions(documentRef, [session]);
    const [row, detailRow] = elements.get("sessions").children;
    const toggle = row.children[0].children[0];
    const activity = detailRow.children[0].children[0];
    assert.equal(detailRow.hidden, true);
    assert.equal(toggle.ariaExpanded, "false");
    assert.equal(toggle.children.length, 1);
    assert.deepEqual(
        activity.children.map((item) => item.children[2].textContent),
        [
            "The pulse is now quicker and brighter.",
            "node_repl.js()",
            "shell_command()",
            "apply_patch()",
            "wait()",
        ],
    );
    assert.equal(activity.children[0].children[0].textContent, "2026-08-18 05:01 PM -- ");

    row.dispatch("pointerdown", { button: 0 });
    toggle.dispatch("click", { detail: 1 });
    assert.equal(detailRow.hidden, false);
    assert.equal(toggle.ariaExpanded, "true");
    assert.equal(row.classList.contains("is-expanded"), true);

    toggle.dispatch("click", { detail: 0 });
    assert.equal(detailRow.hidden, true);
    toggle.dispatch("click", { detail: 0 });
    assert.equal(detailRow.hidden, false);

    renderSessions(documentRef, [{ ...session, updated: "1s ago" }]);
    assert.equal(elements.get("sessions").children[1].hidden, false);
});

test("illustrates projected weekly usage on the current-usage progress bar", () => {
    const now = Date.now();
    const ids = [
        "sessions-count",
        "total-tokens",
        "cached-tokens",
        "requests",
        "tool-calls",
        "plan",
        "last-refresh",
        "rate-title",
        "rate-value",
        "rate-meta",
        "rate-projected-title",
        "rate-projected-value",
        "rate-bar",
        "rate-projected-bar",
        "rate-projected-marker",
        "sessions",
        "sessions-empty",
    ];
    const elements = new Map(ids.map((id) => [id, fakeElement("div")]));
    const progress = fakeElement("div");
    elements.get("rate-bar").parentElement = progress;
    elements.get("rate-projected-bar").parentElement = progress;
    elements.get("rate-projected-marker").parentElement = progress;
    const documentRef = {
        createElement: fakeElement,
        createTextNode: (text) => ({ textContent: text }),
        getElementById: (id) => elements.get(id),
    };

    renderDashboard(documentRef, {
        available: true,
        lastRefreshAt: "2026-08-18T16:30:00.000Z",
        totals: {
            totalTokens: 1_000,
            cachedInputTokens: 500,
            requests: 1,
            toolCalls: 2,
        },
        rateLimits: {
            planType: "pro",
            secondary: {
                usedPercent: 71.4,
                windowMinutes: 10_080,
                resetsAt: (now + 2 * 24 * 60 * 60 * 1_000) / 1_000,
            },
        },
        sessions: [],
    });

    assert.equal(elements.get("rate-bar").style.width, "71.4%");
    assert.equal(elements.get("rate-projected-bar").style.width, "100%");
    assert.equal(elements.get("rate-projected-marker").style.left, "100%");
    assert.equal(elements.get("rate-projected-marker").hidden, false);
    assert.equal(progress.ariaValueNow, "71");
    assert.equal(progress.ariaValueText, "71% used, 100% projected at reset");
});

test("builds a local-midnight Today request", () => {
    const now = new Date(2026, 7, 18, 16, 30).getTime();
    const url = buildUsageApiUrl("/api/usage", "today", now);
    const expected = new Date(now);
    expected.setHours(0, 0, 0, 0);
    assert.equal(url, `/api/usage?recentSince=${encodeURIComponent(expected.toISOString())}`);
});

function fakeElement(tagName) {
    const classes = new Set();
    const listeners = new Map();
    return {
        tagName,
        children: [],
        dataset: {},
        className: "",
        classList: {
            add(value) {
                classes.add(value);
            },
            contains(value) {
                return classes.has(value);
            },
            toggle(value, enabled) {
                if (enabled) {
                    classes.add(value);
                } else {
                    classes.delete(value);
                }
            },
        },
        hidden: false,
        style: {},
        textContent: "",
        append(...children) {
            this.children.push(...children);
        },
        addEventListener(type, listener) {
            listeners.set(type, listener);
        },
        dispatch(type, event = {}) {
            listeners.get(type)?.(event);
        },
        replaceChildren() {
            this.children = [];
        },
    };
}
