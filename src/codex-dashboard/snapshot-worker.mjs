export class SnapshotBusyError extends Error {
    constructor() {
        super("Two snapshot reads are already in progress. Retry after a read finishes.");
        this.code = "dashboard_snapshot_busy";
    }
}

export function createSnapshotWorker(readSnapshot) {
    let pending = 0;
    return async (filter) => {
        if (pending >= 2) {
            throw new SnapshotBusyError();
        }
        pending++;
        try {
            return await readSnapshot(filter);
        } finally {
            // Client cancellation cannot release work that the adapter still owns.
            pending--;
        }
    };
}
