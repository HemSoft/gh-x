import { execFileSync } from "node:child_process";
import { appendFileSync, readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

export function relevant(paths) {
    return paths.some((name) => /^(?:src\/|benchmarks\/|go\.(?:mod|sum)$|\.github\/workflows\/ci\.yml$|\.github\/scripts\/validate-main-ruleset\.go$|\.github\/quality-tools\.env$)/.test(name));
}

export function comparisonBase(eventName, event) {
    if (eventName === "pull_request") return event.pull_request?.base?.sha;
    if (eventName === "push") return event.before;
    return null; // Manual/called CI and unknown events run the complete suite.
}

if (import.meta.url === pathToFileURL(process.argv[1]).href) {
    const event = JSON.parse(readFileSync(process.env.GITHUB_EVENT_PATH, "utf8"));
    const base = comparisonBase(process.env.GITHUB_EVENT_NAME, event);
    let shouldRun = true;
    if (base && /^[a-f0-9]{40}$/.test(base) && !/^0+$/.test(base)) {
        // Checkout fetch-depth: 0 supplies the base and the tested merge commit.
        // A missing/unreadable base is an error, never a false "unrelated" skip.
        const paths = execFileSync("git", ["diff", "--name-only", "-z", base, "HEAD"], { encoding: "utf8" }).split("\0");
        shouldRun = relevant(paths);
    }
    appendFileSync(process.env.GITHUB_OUTPUT, `run=${shouldRun}\n`);
    process.stdout.write(`Performance workload changes: ${shouldRun}\n`);
}
