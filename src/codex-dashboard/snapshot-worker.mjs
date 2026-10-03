export class SnapshotBusyError extends Error {
    constructor() {
        super("Two snapshot reads are already in progress. Retry after a read finishes.");
        this.code = "dashboard_snapshot_busy";
    }
}

export class SnapshotTimeoutError extends Error {
    constructor() {
        super("Dashboard snapshot read timed out.");
        this.code = "dashboard_snapshot_timeout";
    }
}

export function createSnapshotWorker(readSnapshot, {
    timeoutMs = 30_000,
    closeReader = () => readSnapshot.close?.(),
} = {}) {
    if (!Number.isFinite(timeoutMs) || timeoutMs <= 0 || timeoutMs > 2_147_483_647) {
        throw new TypeError("Snapshot timeout must be a positive supported timer duration.");
    }
    const jobs = new Set();
    let closed = false;
    let closing;
    const worker = (filter, { signal } = {}) => {
        if (closed) return Promise.reject(new Error("Snapshot worker has closed."));
        signal?.throwIfAborted();
        if (jobs.size >= 2) return Promise.reject(new SnapshotBusyError());
        const controller = new AbortController();
        const onAbort = () => controller.abort(signal.reason);
        signal?.addEventListener("abort", onAbort, { once: true });
        const timeout = setTimeout(() => controller.abort(new SnapshotTimeoutError()), timeoutMs);
        timeout.unref();
        const job = { controller };
        jobs.add(job);
        job.promise = (async () => {
            try {
                return await readSnapshot(filter, { signal: controller.signal });
            } finally {
                // Only true reader settlement, including process exit, frees capacity.
                jobs.delete(job);
                clearTimeout(timeout);
                signal?.removeEventListener("abort", onAbort);
            }
        })();
        return job.promise;
    };
    worker.serve = async (response, filter) => {
        const controller = new AbortController();
        const onClose = () => {
            if (!response.writableFinished) controller.abort(new Error("Dashboard client disconnected."));
        };
        response.once("close", onClose);
        if (response.destroyed) onClose();
        try {
            return await worker(filter, { signal: controller.signal });
        } finally {
            response.removeListener("close", onClose);
        }
    };
    worker.close = () => {
        if (!closing) {
            closed = true;
            const pending = [...jobs];
            closing = Promise.resolve().then(async () => {
                await closeReader();
                await Promise.allSettled(pending.map(job => job.promise));
            });
            for (const job of pending) job.controller.abort(new Error("Dashboard server has stopped."));
        }
        return closing;
    };
    return worker;
}
