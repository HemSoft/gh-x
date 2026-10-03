import { readFile } from "node:fs/promises";

function processFields(stat) {
    // comm is parenthesized and may itself contain spaces or parentheses.
    return stat.slice(stat.lastIndexOf(")") + 2).trim().split(/\s+/);
}

export function hasRunningProcessState(stat, expectedStartTime) {
    const fields = processFields(stat);
    if (!/^[A-Za-z]$/.test(fields[0])) throw new Error("Unable to read process state.");
    if (expectedStartTime !== undefined && fields[19] !== expectedStartTime) return false;
    return fields[0] !== "Z" && fields[0] !== "X";
}

export async function readProcessStartTime(pid) {
    if (process.platform !== "linux") return undefined;
    const fields = processFields(await readFile(`/proc/${pid}/stat`, "utf8"));
    if (!/^\d+$/.test(fields[19])) throw new Error("Unable to read process identity.");
    return fields[19]; // Linux stat field 22, preserved as a string.
}

export async function isProcessRunning(pid, expectedStartTime) {
    try { process.kill(pid, 0); }
    catch (error) { if (error.code === "ESRCH") return false; throw error; }
    // Windows has no POSIX zombie state. macOS/BSD only get a signalability
    // check here; their orphan-exit behavior is not runtime-qualified by this PR.
    if (process.platform !== "linux") return true;
    try { return hasRunningProcessState(await readFile(`/proc/${pid}/stat`, "utf8"), expectedStartTime); }
    catch (error) { if (error.code === "ENOENT" || error.code === "ESRCH") return false; throw error; }
}
