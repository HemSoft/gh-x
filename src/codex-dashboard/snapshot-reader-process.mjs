import { SHARE_ENV, Worker } from "node:worker_threads";

// Never load provider code on this control loop. A blocked computation must
// not prevent parent-death notification from terminating the whole process.
const terminate = () => process.kill(process.pid, "SIGKILL");
process.on("disconnect", terminate);
const reader = new Worker(new URL("./snapshot-reader-thread.mjs", import.meta.url), { env: SHARE_ENV });
reader.on("error", terminate);
reader.on("exit", code => process.exit(code || 1));
reader.on("message", message => {
    if (!process.connected) { terminate(); return; }
    try {
        process.send(message, error => { if (error) terminate(); });
    } catch {
        terminate();
    }
});
process.on("message", message => {
    try { reader.postMessage(message); }
    catch { terminate(); }
});
