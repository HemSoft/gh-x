import assert from "node:assert/strict";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import {
    createCanvasControl,
    openCanvasInstance,
} from "./canvas-control.mjs";

test("opens a registered Canvas instance through the Copilot session RPC", async () => {
    let request;
    const canvasRpc = {
        async open(value) {
            assert.equal(this, canvasRpc);
            request = value;
        },
    };
    await openCanvasInstance({
        capabilities: { ui: { canvases: true } },
        rpc: { canvas: canvasRpc },
    }, {
        canvasId: "codex-usage-dashboard",
        instanceId: "instance-1",
    });

    assert.deepEqual(request, {
        canvasId: "codex-usage-dashboard",
        instanceId: "instance-1",
    });
});

test("rejects sessions without Canvas rendering", async () => {
    await assert.rejects(
        openCanvasInstance({}, {
            canvasId: "codex-usage-dashboard",
            instanceId: "instance-1",
        }),
        /Canvas rendering is unavailable/,
    );
});

test("publishes a private endpoint that opens the Codex Canvas", async () => {
    const directory = await mkdtemp(path.join(os.tmpdir(), "codex-canvas-control-"));
    let opens = 0;
    const control = await createCanvasControl({
        openCanvas: async () => {
            opens += 1;
        },
        workingDirectory: "D:\\github\\HemSoft\\gh-x",
        directory,
        token: "known-token",
        pid: 1234,
        now: () => new Date("2026-08-18T14:00:00.000Z"),
    });

    try {
        const marker = JSON.parse(await readFile(control.markerPath, "utf8"));
        assert.equal(marker.pid, 1234);
        assert.equal(marker.workingDirectory, path.resolve("D:\\github\\HemSoft\\gh-x"));
        assert.equal(marker.endpoint, control.endpoint);

        const opened = await fetch(control.endpoint, { method: "POST" });
        assert.equal(opened.status, 204);
        assert.equal(opens, 1);

        const unauthorized = await fetch(new URL("/wrong/open", control.endpoint), {
            method: "POST",
        });
        assert.equal(unauthorized.status, 403);
    } finally {
        await control.close();
        await rm(directory, { recursive: true, force: true });
    }
    await assert.rejects(fetch(control.endpoint, { method: "POST" }));
});
