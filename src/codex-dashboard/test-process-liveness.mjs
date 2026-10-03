import { readFile } from "node:fs/promises";

export function hasRunningProcessState(stat) {
    // comm is parenthesized and may itself contain spaces or parentheses.
    const state = stat.slice(stat.lastIndexOf(")") + 2).split(" ")[0];
    if (!/^[A-Za-z]$/.test(state)) throw new Error("Unable to read process state.");
    return state !== "Z" && state !== "X";
}

export async function isProcessRunning(pid) {
    try { process.kill(pid, 0); }
    catch (error) { if (error.code === "ESRCH") return false; throw error; }
    if (process.platform !== "linux") return true;
    try { return hasRunningProcessState(await readFile(`/proc/${pid}/stat`, "utf8")); }
    catch (error) { if (error.code === "ENOENT" || error.code === "ESRCH") return false; throw error; }
}
