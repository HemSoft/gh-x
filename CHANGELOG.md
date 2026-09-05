# Changelog

GitHub Releases is the authoritative source for published release notes and
version history. This file links the latest published release, compares it with
the current branch, and preserves notes from the repository's earlier versioning
scheme.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses semantic versioning.

## [Unreleased]

See the comparison link below for changes since the latest published release.

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

[Unreleased]: https://github.com/HemSoft/gh-x/compare/v0.11.6...HEAD
[0.11.6]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.6
[0.11.5]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.5
[0.11.4]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.4
[0.11.3]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.3
[0.11.2]: https://github.com/HemSoft/gh-x/releases/tag/v0.11.2
