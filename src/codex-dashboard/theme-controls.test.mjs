import assert from "node:assert/strict";
import test from "node:test";
import { startDashboard } from "./dashboard-ui.mjs";

const savedCustom = {
    font: "serif", tableFont: "rounded", background: "#123456", panel: "#234567",
    text: "#ffffff", muted: "#abcdef", accent: "#fedcba", secondary: "#654321",
    active: "#123abc", idle: "#456def",
};

for (const theme of ["light", "dark", "system", "custom"]) {
    for (const dismissal of ["cancel", "button"]) {
        test(`${dismissal} discards previews and restores the saved ${theme} theme`, async t => {
            const fixture = await themeFixture(t, theme);
            const before = fixture.state();
            fixture.open();
            fixture.edit();
            assert.equal(fixture.documentRef.documentElement.dataset.theme, "custom");
            assert.equal(fixture.properties.get("--bg"), "#ff0000");
            fixture.dismiss(dismissal);
            assert.deepEqual(fixture.state(), before);
            assert.equal(fixture.cookieWrites.length, 0);
            fixture.open();
            for (const [key, value] of Object.entries(savedCustom)) {
                assert.equal(fixture.element(`theme-${key}`).value, value);
            }
            fixture.dismiss(dismissal);
            const reloaded = await themeFixture(t, theme, fixture.cookies);
            assert.deepEqual(reloaded.state(), before);
        });
    }
}

test("Save commits custom values while Reset remains a discardable preview", async t => {
    const fixture = await themeFixture(t, "light");
    fixture.open();
    fixture.edit();
    fixture.element("save-theme").dispatch("click");
    const saved = fixture.state();
    assert.equal(saved.theme, "custom");
    assert.equal(saved.properties["--bg"], "#ff0000");
    assert.equal(fixture.cookieWrites.length, 2);
    const reloaded = await themeFixture(t, "custom", fixture.cookies);
    assert.deepEqual(reloaded.state(), saved);
    for (const dismissal of ["cancel", "button"]) {
        fixture.open();
        fixture.element("reset-theme").dispatch("click");
        assert.equal(fixture.properties.get("--bg"), "#07111f");
        assert.equal(fixture.cookieWrites.length, 2);
        fixture.dismiss(dismissal);
        assert.deepEqual(fixture.state(), saved);
        assert.equal(fixture.cookieWrites.length, 2);
    }
    fixture.open();
    fixture.element("reset-theme").dispatch("click");
    fixture.element("save-theme").dispatch("click");
    const resetSaved = fixture.state();
    assert.equal(resetSaved.properties["--bg"], "#07111f");
    const resetReloaded = await themeFixture(t, "custom", fixture.cookies);
    assert.deepEqual(resetReloaded.state(), resetSaved);
});

async function themeFixture(t, theme, existingCookies) {
    const elements = new Map();
    const element = id => {
        if (!elements.has(id)) elements.set(id, fakeElement());
        return elements.get(id);
    };
    const cookies = new Map(existingCookies ?? [
        ["codex-theme", encodeURIComponent(theme)],
        ["codex-custom-theme", encodeURIComponent(JSON.stringify(savedCustom))],
    ]);
    const cookieWrites = [];
    const properties = new Map();
    const documentRef = {
        get cookie() { return [...cookies].map(([name, value]) => `${name}=${value}`).join("; "); },
        set cookie(value) {
            cookieWrites.push(value);
            const [name, encoded] = value.split(";")[0].split("=");
            cookies.set(name, encoded);
        },
        documentElement: {
            dataset: {},
            style: {
                setProperty: (name, value) => properties.set(name, value),
                removeProperty: name => properties.delete(name),
            },
        },
        querySelector: () => ({ content: "/api/usage" }),
        createElement: fakeElement,
        createTextNode: text => ({ textContent: text }),
        getElementById: element,
    };
    const dashboard = await startDashboard({
        documentRef,
        fetchImpl: async () => ({ ok: true, json: async () => ({ available: false }) }),
    });
    t.after(() => dashboard.stop());
    const state = () => ({
        theme: documentRef.documentElement.dataset.theme,
        selector: element("theme").value,
        editHidden: element("customize-theme").hidden,
        properties: Object.fromEntries(properties),
        cookie: documentRef.cookie,
        dialogOpen: element("theme-dialog").open,
    });
    const open = () => element("theme").dispatch("change", { target: { value: "custom" } });
    const edit = () => {
        element("theme-background").value = "#ff0000";
        element("theme-font").value = "mono";
        element("theme-fields").dispatch("input");
    };
    const dismiss = how => {
        if (how === "button") element("cancel-theme").dispatch("click");
        else {
            element("theme-dialog").dispatch("cancel", {
                preventDefault() { assert.fail("Native dialog cancellation must keep its default close action"); },
            });
            element("theme-dialog").close();
        }
    };
    return { documentRef, properties, cookies, cookieWrites, element, state, open, edit, dismiss };
}

function fakeElement() {
    const listeners = new Map();
    return {
        children: [], dataset: {}, style: {}, hidden: false, open: false,
        classList: { toggle() {}, add() {}, contains() { return false; } },
        parentElement: {},
        addEventListener: (type, listener) => listeners.set(type, listener),
        dispatch: (type, event = {}) => listeners.get(type)?.(event),
        append(...children) { this.children.push(...children); },
        replaceChildren() { this.children = []; },
        showModal() { this.open = true; },
        close() { this.open = false; },
    };
}
