# Changelog

GitHub Releases is the authoritative source for published release notes and
version history. This file links the latest published release, compares it with
the current branch, and preserves notes from the repository's earlier versioning
scheme.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses semantic versioning.

## [Unreleased]

## [0.13.12] - 2026-09-22

- Show approvers and approval times in open PR lists (#150)
- docs: record v0.13.11 release (#148)

## [0.13.11] - 2026-09-21

- fix: retry empty private repository searches (#146)
- docs: record v0.13.10 release (#141)

## [0.13.10] - 2026-09-20

- Show recent merged pull requests in status (#140)
- docs: record issue #135 quality audit (#138)
- docs: record v0.13.9 release (#137)

## [0.13.9] - 2026-09-20

- fix: honor Copilot review verdicts (#135) (#136)
- docs: record v0.13.8 release (#134)

## [0.13.8] - 2026-09-18

- fix: complete local audit qualification (#130) (#133)
- docs: record v0.13.7 release (#132)

## [0.13.7] - 2026-09-18

- fix: isolate malformed dashboard requests (#131)
- docs: record v0.13.6 release (#128)

## [0.13.6] - 2026-09-17

- chore(deps): bump gh-aw setup CLI to v0.89.7 (#126)
- docs: record v0.13.5 release (#127)

## [0.13.5] - 2026-09-17

- chore(deps): bump github.com/mattn/go-runewidth in the go-modules group (#124)
- docs: record issue #120 completion history (#123)
- docs: record v0.13.4 release (#122)

## [0.13.4] - 2026-09-14

- Show parent and sub-issue progress across issue views (#121)
- docs: record v0.13.3 release (#119)

## [0.13.3] - 2026-09-12

- fix: bound and cancel GitHub subprocesses (#118)
- docs: record v0.13.2 release (#117)

## [0.13.2] - 2026-09-12

- ci: build every release target before merge (#116)
- docs: record v0.13.1 release (#115)

## [0.13.1] - 2026-09-12

- ci: enforce mutator coverage floor (#114)
- docs: record v0.13.0 release (#113)

## [0.13.0] - 2026-09-12

- feat: publish provenance for release binaries (#112)
- docs: record v0.12.8 release (#111)

## [0.12.8] - 2026-09-12

- fix: isolate authoritative changelog review runs (#110)
- docs: record v0.12.7 release (#109)

## [0.12.7] - 2026-09-12

- docs: record v0.12.6 release (#108)
- fix: require current-head Codex review in CI (#106)

## [0.12.6] - 2026-09-12

- fix: complete Codex review evidence compatibility (#107)
- docs: record v0.12.5 release (#105)

## [0.12.5] - 2026-09-12

- refactor: prepare generic Codex review verification (#104)
- docs: record v0.12.4 release (#97)

## [0.12.4] - 2026-09-12

- chore(deps): bump the github-actions group across 1 directory with 3 updates (#95)
- docs: record v0.12.3 release (#96)

## [0.12.3] - 2026-09-12

- chore(deps): bump the go-modules group across 1 directory with 3 updates (#94)
- docs: record v0.12.2 release (#93)

## [0.12.2] - 2026-09-12

- fix: resolve SSH aliases for status API routing (#92)
- docs: record v0.12.1 release (#90)

## [0.12.1] - 2026-09-09

- fix: restore available supplemental data with fail-closed diagnostics (#88) (#89)
- docs: record v0.12.0 release (#85)

## [0.12.0] - 2026-09-06

- feat: show local date and time in status header (#84)
- docs: record v0.11.10 release (#83)

## [0.11.10] - 2026-09-06

- test: establish critical CLI performance budgets (#63) (#82)
- docs: record v0.11.9 release (#81)

## [0.11.9] - 2026-09-06

- fix: stabilize changelog reviews with connected Codex (#80)

## [0.11.8] - 2026-09-05

- fix: guard release changelog auto-merge with current reviews (#77)
- docs: record v0.11.7 release (#76)

## [0.11.7] - 2026-09-05

- fix: preserve Unicode separators in Codex rollout JSONL (#75)
- docs: record v0.11.6 release (#74)

## [0.11.6] - 2026-09-05

- chore: pin GitHub Actions dependencies to immutable commits (#73)
- docs: record v0.11.5 release (#71)

## [0.11.5] - 2026-09-05

- fix: support CRLF changelog validation (#69)

## [0.11.4] - 2026-09-05

- chore: require pull requests for main (#66)
- docs: record v0.11.3 release (#68)

## [0.11.3] - 2026-09-05

- docs: reconcile changelog with published releases (#67)

## [0.11.2] - 2026-09-05

### Changed

- Required CodeQL analysis and dependency review in the repository's CI quality
  gate.

## Legacy history

The entries below came from an earlier versioning scheme. `HemSoft/gh-x` has no
`v0.16.0`, `v0.17.0`, or `v0.18.0` tags, so these are not published releases
and intentionally have no version links.

### 0.18.0 legacy entry (2026-06-08)

#### Release notes

- Added `gh x pr review [number]` for read-only agentic PR review using
  configurable CLI providers.
- Supports provider presets for Codex, Claude Code, GitHub Copilot CLI,
  Gemini CLI, OpenCode, plus custom command templates.
- Added top-level `gh x changelog [n]`.
- Shows the latest release changelog by default, or the latest `n` release
  changelogs when a count is supplied.

### 0.17.0 legacy entry (2026-06-06)

#### Improved

- Improved `gh x workflow list` trigger labels so scheduled workflows show
  readable UTC schedule phrases instead of raw cron expressions.
- Added readable formatting for hourly schedules such as `0 * * * *`.

#### New

- Added `gh x status` for a compact git and GitHub repository summary.
- Shows upstream sync state, working tree change counts, dangling local branch
  count, open issue count, and open pull request count.
- Renamed `workflow_dispatch` trigger output to `manual`.
- Renamed `workflow_run` trigger output to `after workflow run` to distinguish
  dependent workflow triggers from manual triggers.

### 0.16.0 legacy entry (2026-06-06)

#### Added

- Added a `TRIGGERS` column to `gh x workflow list`.
- Shows common GitHub Actions triggers such as `push`, `pull_request`, and
  `workflow_dispatch`.
- Shows schedule cron expressions inline, for example
  `schedule: 15 6 * * 1-5`.
- Shows useful trigger filters for branch and pull request event types, such as
  `branches: main` and `types: opened, synchronize, reopened`.

#### Changed

- Workflow list output now enriches GitHub workflow metadata from workflow YAML
  definitions when available.
- Dynamic or unreadable workflow definitions now display `unknown` in the
  trigger column instead of failing the list command.

[Unreleased]: https://github.com/HemSoft/gh-x/compare/v0.13.12...HEAD
[0.13.12]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.12
[0.13.11]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.11
[0.13.10]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.10
[0.13.9]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.9
[0.13.8]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.8
[0.13.7]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.7
[0.13.6]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.6
[0.13.5]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.5
[0.13.4]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.4
[0.13.3]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.3
[0.13.2]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.2
[0.13.1]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.1
[0.13.0]: https://github.com/HemSoft/gh-x/releases/tag/v0.13.0
[0.12.8]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.8
[0.12.7]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.7
[0.12.6]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.6
[0.12.5]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.5
[0.12.4]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.4
[0.12.3]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.3
[0.12.2]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.2
[0.12.1]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.1
[0.12.0]: https://github.com/HemSoft/gh-x/releases/tag/v0.12.0
[0.11.10]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.10
[0.11.9]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.9
[0.11.8]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.8
[0.11.7]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.7
[0.11.6]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.6
[0.11.5]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.5
[0.11.4]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.4
[0.11.3]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.3
[0.11.2]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.2
