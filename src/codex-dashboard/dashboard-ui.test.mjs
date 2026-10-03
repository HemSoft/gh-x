import assert from "node:assert/strict";
import test from "node:test";

import {
    buildDashboardView,
    buildUsageApiUrl,
    renderDashboard,
    renderSessions,
    startDashboard,
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

test("startDashboard ignores an older manual response and its freshness", async (t) => {
    const fixture = await startRefreshFixture(t);
    const older = fixture.dashboard.refresh();
    const newer = fixture.dashboard.refresh();
    fixture.respond(2, refreshSnapshot("newer", 200));
    await newer;
    const current = fixture.view();
    fixture.respond(1, refreshSnapshot("older", 100, 60_000));
    await older;
    assert.deepEqual(fixture.view(), current);
    assert.equal(current.sessionId, "newer");
    assert.equal(current.totalTokens, "200");
});

test("startDashboard invalidates the previous filter before rendering its response", async (t) => {
    const fixture = await startRefreshFixture(t);
    const older = fixture.dashboard.refresh();
    fixture.element("recent-window").dispatch("change", { target: { value: "3600000" } });
    assert.match(fixture.requests[1].url, /recentWindowMs=86400000$/);
    assert.match(fixture.requests[2].url, /recentWindowMs=3600000$/);
    fixture.respond(2, refreshSnapshot("one-hour", 200));
    await flushRefresh();
    const current = fixture.view();
    fixture.respond(1, refreshSnapshot("24-hour", 100, 60_000));
    await older;
    assert.deepEqual(fixture.view(), current);
    assert.equal(current.recentWindow, "3600000");
    assert.equal(current.sessionId, "one-hour");
    assert.match(fixture.documentRef.cookie, /^codex-recent=3600000;/);
});

for (const failure of ["network", "http", "json"]) {
    test(`startDashboard ignores a stale ${failure} failure after a filter change`, async (t) => {
        const fixture = await startRefreshFixture(t);
        const older = fixture.dashboard.refresh();
        fixture.element("recent-window").dispatch("change", { target: { value: "3600000" } });
        fixture.respond(2, refreshSnapshot("one-hour", 200));
        await flushRefresh();
        const current = fixture.view();
        fixture.fail(1, failure);
        await older;
        assert.deepEqual(fixture.view(), current);
        assert.equal(current.sessionId, "one-hour");
    });

    test(`startDashboard shows a current ${failure} failure and recovers`, async (t) => {
        const fixture = await startRefreshFixture(t);
        const failed = fixture.dashboard.refresh();
        fixture.fail(1, failure);
        await failed;
        assert.equal(fixture.view().totalTokens, "Unavailable");
        assert.equal(fixture.view().freshness, "Refresh unavailable");
        assert.equal(fixture.view().sessionId, undefined);
        const recovered = fixture.dashboard.refresh();
        fixture.respond(2, refreshSnapshot("recovered", 300));
        await recovered;
        assert.equal(fixture.view().totalTokens, "300");
        assert.equal(fixture.view().sessionId, "recovered");
        assert.match(fixture.view().freshness, /^Updated /);
    });
}

for (const failure of [false, true]) {
    test(`startDashboard ignores a stale body ${failure ? "failure" : "success"} after JSON parsing starts`, async (t) => {
        const fixture = await startRefreshFixture(t);
        const body = Promise.withResolvers();
        const older = fixture.dashboard.refresh();
        fixture.requests[1].resolve({ ok: true, json: () => body.promise });
        await flushRefresh();
        const newer = fixture.dashboard.refresh();
        fixture.respond(2, refreshSnapshot("newer", 200));
        await newer;
        const current = fixture.view();
        if (failure) {
            body.reject(new Error("Late JSON failure"));
        } else {
            body.resolve(refreshSnapshot("older", 100, 60_000));
        }
        await older;
        assert.deepEqual(fixture.view(), current);
        assert.equal(current.sessionId, "newer");
    });
}

test("startDashboard orders Refresh button and timer requests identically", async (t) => {
    const fixture = await startRefreshFixture(t);
    t.mock.timers.tick(5_000);
    fixture.element("refresh").dispatch("click");
    assert.equal(fixture.requests.length, 3);
    fixture.respond(2, refreshSnapshot("button", 300));
    await flushRefresh();
    const current = fixture.view();
    fixture.respond(1, refreshSnapshot("timer", 200, 60_000));
    await flushRefresh();
    assert.deepEqual(fixture.view(), current);
    assert.equal(current.sessionId, "button");
    t.mock.timers.tick(5_000);
    assert.equal(fixture.requests.length, 4);
    fixture.respond(3, refreshSnapshot("next-timer", 400));
    await flushRefresh();
    assert.equal(fixture.view().sessionId, "next-timer");
});

for (const failure of [false, true]) {
    test(`startDashboard stop prevents a pending ${failure ? "failure" : "success"} and future refreshes`, async (t) => {
        const fixture = await startRefreshFixture(t);
        const pending = fixture.dashboard.refresh();
        const current = fixture.view();
        fixture.dashboard.stop();
        if (failure) {
            fixture.fail(1, "network");
        } else {
            fixture.respond(1, refreshSnapshot("stopped", 200));
        }
        await pending;
        assert.deepEqual(fixture.view(), current);
        t.mock.timers.tick(20_000);
        await fixture.dashboard.refresh();
        fixture.element("refresh").dispatch("click");
        fixture.element("recent-window").dispatch("change", { target: { value: "3600000" } });
        await flushRefresh();
        assert.equal(fixture.requests.length, 2);
    });
}

test("startDashboard skips busy timer ticks so a slow response can render", async (t) => {
    const fixture = await startRefreshFixture(t);
    t.mock.timers.tick(5_000);
    assert.equal(fixture.requests.length, 2);
    t.mock.timers.tick(15_000);
    assert.equal(fixture.requests.length, 2);
    fixture.respond(1, refreshSnapshot("slow-timer", 200));
    await flushRefresh();
    assert.equal(fixture.view().sessionId, "slow-timer");
    t.mock.timers.tick(5_000);
    assert.equal(fixture.requests.length, 3);
    fixture.respond(2, refreshSnapshot("next-timer", 300));
    await flushRefresh();
    assert.equal(fixture.view().sessionId, "next-timer");
});

test("startDashboard stale completion cannot clear the current pending refresh", async (t) => {
    const fixture = await startRefreshFixture(t);
    t.mock.timers.tick(5_000);
    const manual = fixture.dashboard.refresh();
    fixture.respond(1, refreshSnapshot("obsolete-timer", 200));
    await flushRefresh();
    assert.equal(fixture.view().sessionId, "initial");
    t.mock.timers.tick(5_000);
    assert.equal(fixture.requests.length, 3);
    fixture.respond(2, refreshSnapshot("current-manual", 300));
    await manual;
    assert.equal(fixture.view().sessionId, "current-manual");
    t.mock.timers.tick(5_000);
    assert.equal(fixture.requests.length, 4);
});

test("startDashboard skips timer ticks during body parsing and resumes after failure", async (t) => {
    const fixture = await startRefreshFixture(t);
    const body = Promise.withResolvers();
    t.mock.timers.tick(5_000);
    fixture.requests[1].resolve({ ok: true, json: () => body.promise });
    await flushRefresh();
    t.mock.timers.tick(10_000);
    assert.equal(fixture.requests.length, 2);
    body.reject(new Error("Slow JSON failure"));
    await flushRefresh();
    assert.equal(fixture.view().freshness, "Refresh unavailable");
    t.mock.timers.tick(5_000);
    assert.equal(fixture.requests.length, 3);
    fixture.respond(2, refreshSnapshot("timer-recovered", 300));
    await flushRefresh();
    assert.equal(fixture.view().sessionId, "timer-recovered");
});

for (const phase of ["fetch", "body"]) {
    test(`startDashboard bounds an unsettled ${phase} and resumes polling`, async (t) => {
        const fixture = await startRefreshFixture(t);
        const pending = fixture.dashboard.refresh();
        if (phase === "body") {
            fixture.requests[1].resolve({ ok: true, json: () => new Promise(() => {}) });
            await flushRefresh();
        }
        t.mock.timers.tick(30_000);
        await flushRefresh();
        assert.equal(fixture.view().freshness, "Refresh unavailable");
        assert.equal(fixture.requests[1].options.signal?.aborted, true);
        await pending;
        t.mock.timers.tick(5_000);
        assert.equal(fixture.requests.length, 3);
        fixture.respond(2, refreshSnapshot("timeout-recovered", 300));
        await flushRefresh();
        assert.equal(fixture.view().sessionId, "timeout-recovered");
        assert.equal(fixture.view().totalTokens, "300");
    });
}

test("startDashboard an obsolete deadline cannot render or clear newer pending work", async (t) => {
    const fixture = await startRefreshFixture(t);
    const older = fixture.dashboard.refresh();
    t.mock.timers.tick(20_000);
    const newer = fixture.dashboard.refresh();
    const current = fixture.view();
    t.mock.timers.tick(10_000);
    await flushRefresh();
    assert.equal(fixture.requests[1].options.signal?.aborted, true);
    await older;
    assert.deepEqual(fixture.view(), current);
    assert.equal(fixture.requests[2].options.signal.aborted, false);
    t.mock.timers.tick(5_000);
    assert.equal(fixture.requests.length, 3);
    fixture.respond(2, refreshSnapshot("current-after-old-timeout", 300));
    await newer;
    assert.equal(fixture.view().sessionId, "current-after-old-timeout");
});

test("startDashboard removes a completed request deadline", async (t) => {
    const fixture = await startRefreshFixture(t);
    const completed = fixture.dashboard.refresh();
    fixture.respond(1, refreshSnapshot("completed", 300));
    await completed;
    t.mock.timers.tick(30_000);
    await flushRefresh();
    assert.equal(fixture.requests[1].options.signal?.aborted, false);
    assert.equal(fixture.view().sessionId, "completed");
});

test("startDashboard stop settles and cancels all outstanding requests", async (t) => {
    const fixture = await startRefreshFixture(t);
    const older = fixture.dashboard.refresh();
    const newer = fixture.dashboard.refresh();
    const current = fixture.view();
    fixture.dashboard.stop();
    await flushRefresh();
    assert.equal(fixture.requests[1].options.signal?.aborted, true);
    assert.equal(fixture.requests[2].options.signal?.aborted, true);
    await Promise.all([older, newer]);
    t.mock.timers.tick(30_000);
    await flushRefresh();
    assert.deepEqual(fixture.view(), current);
    assert.equal(fixture.requests.length, 3);
});

test("startDashboard bounds the initial request and starts recovery polling", async (t) => {
    const fixture = await startRefreshFixture(t);
    fixture.dashboard.stop();
    const requests = [];
    const started = startDashboard({
        documentRef: fixture.documentRef,
        fetchImpl: (url, options) => {
            const request = Promise.withResolvers();
            requests.push({ ...request, options });
            return request.promise;
        },
    });
    t.mock.timers.tick(30_000);
    await flushRefresh();
    assert.equal(fixture.view().freshness, "Refresh unavailable");
    const dashboard = await started;
    t.after(() => dashboard.stop());
    t.mock.timers.tick(5_000);
    assert.equal(requests.length, 2);
    requests[1].resolve({ ok: true, json: async () => refreshSnapshot("startup-recovered", 300) });
    await flushRefresh();
    assert.equal(fixture.view().sessionId, "startup-recovered");
});

test("startDashboard preserves theme, zoom and filter controls", async (t) => {
    const fixture = await startRefreshFixture(t);
    assert.equal(fixture.element("theme").value, "system");
    assert.equal(fixture.element("zoom-level").textContent, "100%");
    assert.equal(fixture.element("recent-window").value, "86400000");
    fixture.element("theme").dispatch("change", { target: { value: "dark" } });
    assert.equal(fixture.documentRef.documentElement.dataset.theme, "dark");
    assert.match(fixture.documentRef.cookie, /^codex-theme=dark;/);
    fixture.element("zoom-in").dispatch("click");
    assert.equal(fixture.element("zoom-level").textContent, "110%");
    assert.match(fixture.documentRef.cookie, /^codex-zoom=110;/);
    fixture.element("zoom-out").dispatch("click");
    assert.equal(fixture.element("zoom-level").textContent, "100%");
    assert.equal(fixture.requests.length, 1);
});

async function startRefreshFixture(t) {
    t.mock.timers.enable({ apis: ["setInterval", "setTimeout", "Date"], now: Date.parse("2026-09-30T22:00:00Z") });
    const elements = new Map();
    const element = (id) => {
        if (!elements.has(id)) {
            elements.set(id, fakeElement("div"));
        }
        return elements.get(id);
    };
    element("rate-bar").parentElement = fakeElement("div");
    const documentRef = {
        cookie: "",
        documentElement: {
            dataset: {},
            style: { setProperty() {}, removeProperty() {} },
        },
        querySelector: () => ({ content: "/api/usage" }),
        createElement: fakeElement,
        createTextNode: (text) => ({ textContent: text }),
        getElementById: element,
    };
    const requests = [];
    const respond = (index, snapshot) => requests[index].resolve({
        ok: true,
        status: 200,
        json: async () => snapshot,
    });
    const fail = (index, failure) => {
        if (failure === "network") {
            requests[index].reject(new Error("Network unavailable"));
        } else {
            requests[index].resolve({
                ok: failure !== "http",
                status: 503,
                json: async () => { throw new Error("Invalid JSON"); },
            });
        }
    };
    const dashboard = await startDashboard({
        documentRef,
        fetchImpl: (url, options) => {
            const request = Promise.withResolvers();
            requests.push({ url, options, ...request });
            if (requests.length === 1) {
                respond(0, refreshSnapshot("initial", 50));
            }
            return request.promise;
        },
    });
    t.after(() => dashboard.stop());
    return {
        documentRef,
        dashboard,
        requests,
        respond,
        fail,
        element,
        view: () => ({
            recentWindow: element("recent-window").value,
            totalTokens: element("total-tokens").textContent,
            freshness: element("last-refresh").textContent,
            sessionId: element("sessions").children[0]?.dataset.sessionId,
        }),
    };
}

function refreshSnapshot(sessionId, totalTokens, ageMs = 1_000) {
    return {
        available: true,
        lastRefreshAt: new Date(Date.now() - ageMs).toISOString(),
        totals: { totalTokens, cachedInputTokens: 0, requests: 1, toolCalls: 0 },
        sessions: [{ sessionId, summary: sessionId, totalTokens, activity: [] }],
    };
}

function flushRefresh() {
    return new Promise(setImmediate);
}

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
