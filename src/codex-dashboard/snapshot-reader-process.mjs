let adapter;

process.on("disconnect", () => process.exit(0));
process.on("message", async ({ id, moduleUrl, factoryName, options, filter }) => {
    try {
        if (!adapter) {
            const module = await import(moduleUrl);
            adapter = module[factoryName](options);
        }
        const snapshot = await adapter.getSnapshot(filter);
        process.send({ id, snapshot });
    } catch (error) {
        process.send({ id, error: {
            message: error?.message || "Unable to read dashboard snapshot.",
            code: error?.code || "dashboard_snapshot_failed",
        } });
    }
});
