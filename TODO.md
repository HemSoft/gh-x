# Project TODO

GitHub issues are the authoritative backlog. This file records the current
repository-level work and quality policy without treating old measurements as
permanent facts.

## Current backlog

| Issue | Work |
| --- | --- |
| [#22](https://github.com/HemSoft/gh-x/issues/22) | Refresh Go dependencies and automate dependency hygiene. |
| [#23](https://github.com/HemSoft/gh-x/issues/23) | Split the PR-list implementation and tests by responsibility. |

## Enforced quality gates

The CI workflow and local perfection audit enforce the same core checks:

- build, `go vet`, race-enabled Go tests, and Node dashboard tests;
- `gofmt`, module tidiness, staticcheck, gocritic, errcheck, dead-code checks,
  govulncheck, and the repository's suppression and output policies;
- at least 70% total coverage, cyclomatic complexity no higher than 10,
  cognitive complexity no higher than 15, and every CRAP score below 30;
- at least 90% mutation efficacy and a non-empty mutation result; and
- Markdown lint.

Analyzer and markdown-lint versions are pinned in
`.github/quality-tools.env`. Run
`.agents/skills/perfection/scripts/perfection-audit.ps1` for a current
scorecard instead of relying on a result copied into this file.
