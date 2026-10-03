import { parentPort } from "node:worker_threads";

let adapter;
parentPort.on("message", async ({ id, moduleUrl, factoryName, options, filter }) => {
    try {
        if (!adapter) {
            const module = await import(moduleUrl);
            adapter = module[factoryName](options);
        }
        const snapshot = await adapter.getSnapshot(filter);
        parentPort.postMessage({ id, snapshot });
    } catch (error) {
        parentPort.postMessage({ id, error: {
            message: error?.message || "Unable to read dashboard snapshot.",
            code: error?.code || "dashboard_snapshot_failed",
        } });
    }
});
