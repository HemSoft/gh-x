// Run in a browser console after serving the dashboard:
// await (await import('/tests/browser/session-focus.mjs')).runSessionFocusRegression(document, renderSessions)
// Pass renderSessions imported from the dashboard's token-scoped module URL.
export function runSessionFocusRegression(documentRef, renderSessions) {
    const fixture = documentRef.createElement("section");
    fixture.innerHTML = '<button id="refresh">Refresh</button><input id="filter"><table><tbody id="sessions"></tbody></table><p id="sessions-empty">No sessions</p>';
    documentRef.body.append(fixture);
    const previousFocus = documentRef.activeElement;
    const scopedDocument = {
        get activeElement() { return documentRef.activeElement; },
        createElement: (tag) => documentRef.createElement(tag),
        createTextNode: (text) => documentRef.createTextNode(text),
        getElementById: (id) => fixture.querySelector(`[id="${id}"]`),
    };
    const assert = (condition, message) => {
        if (!condition) { throw new Error(message); }
    };
    const sessions = ["one/a", "one-a", "third"].map((sessionId) => ({
        sessionId, status: "Idle", project: "Fixture", activity: [],
    }));
    const buttons = () => [...fixture.querySelectorAll(".row-toggle")];
    const checkRelationships = () => {
        const ids = buttons().map((button) => button.getAttribute("aria-controls"));
        assert(new Set(ids).size === ids.length, "Activity panel IDs must be unique");
        for (const button of buttons()) {
            const panel = scopedDocument.getElementById(button.getAttribute("aria-controls"));
            assert(panel !== null, "aria-controls must name a real panel");
            assert(button.getAttribute("aria-expanded") === String(!panel.hidden), "Expansion must agree with panel visibility");
            assert(button.tabIndex === 0, "Native disclosure must remain in tab order");
        }
    };
    try {
        renderSessions(scopedDocument, sessions);
        buttons()[1].focus();
        buttons()[1].click();
        for (const refreshed of [sessions, sessions.map((session) => ({ ...session, requests: "2" })), [sessions[1], sessions[0], sessions[2]]]) {
            renderSessions(scopedDocument, refreshed);
            const focused = buttons().find((button) => button.closest("tr").dataset.sessionId === sessions[1].sessionId);
            assert(documentRef.activeElement === focused, "Refresh must preserve focused session through updates and reorder");
            assert(focused.getAttribute("aria-expanded") === "true", "Refresh must preserve expansion");
            checkRelationships();
        }
        renderSessions(scopedDocument, [sessions[0], sessions[2]]);
        assert(documentRef.activeElement === buttons()[0], "Removed session must focus the row at its old position");
        buttons()[1].focus();
        renderSessions(scopedDocument, [sessions[0]]);
        assert(documentRef.activeElement === buttons()[0], "Removal of the final row must focus the nearest remaining row");
        renderSessions(scopedDocument, []);
        assert(documentRef.activeElement === scopedDocument.getElementById("refresh"), "Empty table must focus Refresh");
        const filter = scopedDocument.getElementById("filter");
        filter.focus();
        renderSessions(scopedDocument, sessions);
        assert(documentRef.activeElement === filter, "Refresh must not steal focus from the filter");
        checkRelationships();
        return { passed: true, cases: ["unchanged", "changed", "reordered", "removed", "last-removed", "empty", "external-focus", "aria", "tab-order"] };
    } finally {
        fixture.remove();
        previousFocus?.focus();
    }
}
