#!/usr/bin/env python3
"""Validate the versioned main-branch ruleset against the CI workflow."""

import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
RULESET_PATH = ROOT / ".github" / "rulesets" / "main.json"
WORKFLOW_PATH = ROOT / ".github" / "workflows" / "ci.yml"
AUTO_RELEASE_PATH = ROOT / ".github" / "workflows" / "auto-release.yml"


def require(condition: bool, message: str) -> None:
    if not condition:
        raise SystemExit(message)


ruleset = json.loads(RULESET_PATH.read_text(encoding="utf-8"))
workflow = WORKFLOW_PATH.read_text(encoding="utf-8")
auto_release = AUTO_RELEASE_PATH.read_text(encoding="utf-8")

require(ruleset.get("name") == "main-quality-gate", "unexpected ruleset name")
require(ruleset.get("target") == "branch", "ruleset must target branches")
require(ruleset.get("enforcement") == "active", "ruleset must be active")
require(ruleset.get("bypass_actors") == [], "ruleset must not have bypass actors")

ref_name = ruleset.get("conditions", {}).get("ref_name", {})
require(ref_name.get("include") == ["refs/heads/main"], "ruleset must target only main")
require(ref_name.get("exclude") == [], "ruleset must not exclude refs")

required_rules = [rule for rule in ruleset.get("rules", []) if rule.get("type") == "required_status_checks"]
require(len(required_rules) == 1, "ruleset must define one required-status-check rule")

parameters = required_rules[0].get("parameters", {})
checks = parameters.get("required_status_checks")
require(
    checks == [{"context": "Quality Gate", "integration_id": 15368}],
    "ruleset must require Quality Gate from GitHub Actions",
)
require(parameters.get("strict_required_status_checks_policy") is False, "strict mode must remain disabled")
require(parameters.get("do_not_enforce_on_create") is False, "checks must apply to new refs")

pull_request_trigger = "  pull_request:\n    branches: [main]\n    types: [opened, synchronize, reopened, edited]"
require(pull_request_trigger in workflow, "CI must run when pull requests target or retarget main")
require("  push:\n    branches: [main]" in workflow, "CI must report status for the main branch badge")
require("  gate:\n    name: Quality Gate" in workflow, "CI must publish the Quality Gate check")
require(
    '  workflow_run:\n    workflows: ["CI Quality Gates"]\n    types: [completed]\n    branches: [main]'
    in auto_release,
    "auto-release must follow completed main-branch CI runs",
)
require("uses: ./.github/workflows/ci.yml" not in auto_release, "auto-release must not duplicate the CI suite")
require(
    "github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.event == 'push'"
    in auto_release,
    "auto-release must require a successful push-triggered CI run",
)
require(
    "git tag --merged HEAD --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname" in auto_release,
    "auto-release must locate the latest reachable release",
)
require(
    'git diff --name-only "$range_start" HEAD' in auto_release,
    "auto-release must inspect every unreleased change",
)
require(
    "git log --format='%s%n%b' \"${latest}..HEAD\"" in auto_release,
    "auto-release must derive the version bump from every unreleased commit",
)
require(
    "current_main=$(git ls-remote origin refs/heads/main | awk '{print $1}')" in auto_release,
    "auto-release must reject superseded CI results",
)
require('--target "$RELEASE_SHA"' in auto_release, "auto-release must tag the exact validated commit")

print("main ruleset and CI workflow are consistent")
