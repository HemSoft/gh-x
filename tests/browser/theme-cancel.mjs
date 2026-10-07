// Run against the real dashboard, after its initial snapshot renders.
// Also exercise Escape with trusted keyboard input; requestClose tests the
// browser's native cancel event and default close action without synthesizing Escape.
export async function runThemeCancelRegression(documentRef) {
    const windowRef = documentRef.defaultView;
    const element = id => documentRef.getElementById(id);
    const dialog = element("theme-dialog");
    const assert = (condition, message) => {
        if (!condition) throw new Error(message);
    };
    const initialCookies = documentRef.cookie;
    const initialTheme = documentRef.documentElement.dataset.theme;
    const cookieNames = ["codex-theme", "codex-custom-theme"];
    const cookie = name => documentRef.cookie.split("; ").find(value => value.startsWith(`${name}=`));
    const initialValues = cookieNames.map(cookie);
    const select = value => {
        element("theme").value = value;
        element("theme").dispatchEvent(new windowRef.Event("change", { bubbles: true }));
    };
    const edit = value => {
        element("theme-background").value = value;
        element("theme-fields").dispatchEvent(new windowRef.Event("input", { bubbles: true }));
    };
    const state = () => JSON.stringify({
        theme: documentRef.documentElement.dataset.theme,
        selector: element("theme").value,
        editHidden: element("customize-theme").hidden,
        styles: documentRef.documentElement.style.cssText,
        cookies: cookieNames.map(cookie),
    });
    const dismiss = async action => {
        if (action === "cancel") dialog.requestClose();
        else element("cancel-theme").click();
        await new Promise(resolve => windowRef.requestAnimationFrame(resolve));
        assert(!dialog.open, "Dismissal must close the native dialog");
    };
    const cases = [];
    try {
        for (const theme of ["light", "dark"]) {
            select(theme);
            const saved = state();
            for (const action of ["cancel", "button"]) {
                select("custom");
                edit("#ff0000");
                assert(documentRef.documentElement.style.getPropertyValue("--bg") === "#ff0000", "Preview must apply");
                await dismiss(action);
                assert(state() === saved, `${action} must restore saved ${theme} values and cookies`);
                cases.push(`${theme}-${action}`);
            }
        }
        select("custom");
        edit("#123456");
        element("save-theme").click();
        const savedCustom = state();
        assert(cookie("codex-custom-theme").includes(encodeURIComponent("#123456")), "Save must commit the custom value");
        for (const action of ["cancel", "button"]) {
            element("customize-theme").click();
            edit("#ff0000");
            await dismiss(action);
            assert(state() === savedCustom, `${action} must restore saved custom values`);
            cases.push(`custom-${action}`);
            element("customize-theme").click();
            element("reset-theme").click();
            assert(documentRef.documentElement.style.getPropertyValue("--bg") === "#07111f", "Reset must preview defaults");
            await dismiss(action);
            assert(state() === savedCustom, `${action} must discard Reset without saving it`);
            cases.push(`reset-${action}`);
        }
        cases.push("save");
        return { passed: true, cases };
    } finally {
        if (dialog.open) element("cancel-theme").click();
        select(initialTheme === "custom" ? "system" : initialTheme);
        cookieNames.forEach((name, index) => {
            documentRef.cookie = initialValues[index]
                ? `${initialValues[index]}; Path=/; SameSite=Strict`
                : `${name}=; Max-Age=0; Path=/; SameSite=Strict`;
        });
        // Reload after this fixture to restore the saved custom object held by
        // the app closure. Cookies above restore its input, never preview values.
        assert(cookieNames.every((name, index) => cookie(name) === initialValues[index]), `Fixture must restore original theme cookies: ${initialCookies}`);
    }
}
