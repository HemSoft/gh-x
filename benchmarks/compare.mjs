export const sampleCount = 7;

export function median(values) {
    const sorted = [...values].sort((a, b) => a - b);
    return sorted[Math.floor(sorted.length / 2)];
}

export function parseGo(raw) {
    const samples = {};
    for (const line of raw.split(/\r?\n/)) {
        const match = line.match(/^BenchmarkCritical\/(\w+)\/(\w+)(?:-\d+)?\s+\d+\s+([\d.]+) ns\/op\s+([\d.]+) B\/op\s+([\d.]+) allocs\/op\s*$/);
        if (!match) continue;
        const [, name, size, ns, bytes, allocs] = match;
        (samples[`go/${name}/${size}`] ??= []).push({ ns: Number(ns), bytes: Number(bytes), allocs: Number(allocs) });
    }
    return samples;
}

export function compare(samples, budgets, baseline = null) {
    const rows = [];
    const errors = [];
    for (const name of Object.keys(samples)) {
        if (!Object.hasOwn(budgets, name)) errors.push(`Unbudgeted path: ${name}`);
    }
    for (const [name, limits] of Object.entries(budgets)) {
        if (Object.keys(limits).length === 0) errors.push(`${name}: no metric budgets`);
        const measured = samples[name];
        if (!Array.isArray(measured) || measured.length !== sampleCount) {
            errors.push(`${name}: expected ${sampleCount} samples, received ${measured?.length ?? 0}`);
            continue;
        }
        for (const [metric, limit] of Object.entries(limits)) {
            const values = measured.map((sample) => sample[metric]);
            if (!Number.isFinite(limit) || limit <= 0 || values.some((v) => !Number.isFinite(v) || v < 0)) {
                errors.push(`${name}/${metric}: invalid limit or measurement`);
                continue;
            }
            const exceedances = values.filter((v) => v > limit).length;
            // Six of seven samples must exceed the ceiling. One or two noisy
            // samples cannot fail a path, and a sustained regression cannot hide.
            const failed = exceedances >= 6;
            const reference = baseline?.[name]?.map((sample) => sample[metric]);
            if (baseline !== null && (reference?.length !== sampleCount || reference.some((v) => !Number.isFinite(v) || v < 0))) {
                errors.push(`${name}/${metric}: invalid baseline samples`);
                continue;
            }
            const baselineMedian = reference?.length ? median(reference) : null;
            rows.push({ name, metric, median: median(values), min: Math.min(...values), max: Math.max(...values), limit, exceedances, failed,
                baselineMedian, baselineRatio: baselineMedian > 0 ? median(values) / baselineMedian : null });
            if (failed) errors.push(`${name}/${metric}: ${exceedances}/7 samples exceed ${limit}; median ${median(values)}`);
        }
    }
    if (Object.keys(budgets).length === 0) errors.push("No budgets configured");
    return { passed: errors.length === 0, rows, errors };
}
