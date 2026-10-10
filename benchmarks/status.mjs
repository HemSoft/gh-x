import { readFileSync } from "node:fs";
import { median } from "./compare.mjs";

export function parseStatusAcquisition(raw) {
    const match = raw.match(/^STATUS_ACQUISITION=(.+)$/m);
    if (!match) throw new Error("Missing status acquisition measurements");
    return JSON.parse(match[1]);
}

export function compareStatusAcquisition(samples, budgets) {
    const rows = [];
    for (const [mode, limits] of Object.entries(budgets)) {
        const measured = samples.filter((sample) => sample.mode === mode);
        if (measured.length !== 7) throw new Error(`${mode}: expected seven status samples`);
        for (const [metric, maximum] of Object.entries(limits)) {
            const values = measured.map((sample) => sample[metric]);
            if (values.some((value) => !Number.isFinite(value) || value < 0)) throw new Error(`${mode}/${metric}: invalid measurement`);
            const value = median(values);
            // Command counts are structural; any request on an eligible warm run fails.
            const failed = metric === "ms" ? value > maximum : values.some((value) => value > maximum);
            rows.push({ mode, metric, median: value, maximum, failed });
        }
    }
    return { passed: rows.every((row) => !row.failed), rows };
}

export function statusBudgets(root) {
    return JSON.parse(readFileSync(`${root}/benchmarks/status-budgets.json`, "utf8"));
}
