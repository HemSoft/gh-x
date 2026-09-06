import assert from "node:assert/strict";
import test from "node:test";
import { comparisonBase, relevant } from "./changed.mjs";

test("all measured production, dependency, fixture and gate changes run benchmarks", () => {
    for (const file of ["src/table.go", "src/monitorreconcile.go", "src/codex-dashboard/codex-adapter.mjs", "src/new-file.go", "benchmarks/budgets.json", "benchmarks/run.mjs", "go.mod", "go.sum", ".github/workflows/ci.yml", ".github/scripts/validate-main-ruleset.go", ".github/quality-tools.env"]) {
        assert.equal(relevant([file]), true, file);
    }
    assert.equal(relevant(["README.md", "CHANGELOG.md", "docs/notes.md"]), false);
    assert.equal(relevant([]), false);
});

test("PRs compare the entire branch, pushes compare before/after, manual runs always measure", () => {
    const event = { before: "previous-push", pull_request: { base: { sha: "pr-base" } } };
    assert.equal(comparisonBase("pull_request", event), "pr-base");
    assert.equal(comparisonBase("push", event), "previous-push");
    assert.equal(comparisonBase("workflow_dispatch", event), null);
    assert.equal(comparisonBase("workflow_call", event), null);
});
