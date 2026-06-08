# Changelog

All notable changes to `gh-x` will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses semantic versioning.

## [Unreleased]

### Improved

- Defaulted `gh x pr review` to Codex with model `gpt-5.5`, high reasoning
  effort, and `strict` review mode.
- Added `medium` review mode between `strict` and `fast-lane`.

## [0.18.0] - 2026-06-08

### Release Notes

- Added `gh x pr review [number]` for read-only agentic PR review using
  configurable CLI providers.
- Supports provider presets for Codex, Claude Code, GitHub Copilot CLI,
  Gemini CLI, OpenCode, plus custom command templates.
- Added top-level `gh x changelog [n]`.
- Shows the latest release changelog by default, or the latest `n` release
  changelogs when a count is supplied.

## [0.17.0] - 2026-06-06

### Improved

- Improved `gh x workflow list` trigger labels so scheduled workflows show
  readable UTC schedule phrases instead of raw cron expressions.
- Added readable formatting for hourly schedules such as `0 * * * *`.

### New

- Added `gh x status` for a compact git and GitHub repository summary.
- Shows upstream sync state, working tree change counts, dangling local branch
  count, open issue count, and open pull request count.
- Renamed `workflow_dispatch` trigger output to `manual`.
- Renamed `workflow_run` trigger output to `after workflow run` to distinguish
  dependent workflow triggers from manual triggers.

## [0.16.0] - 2026-06-06

### Added

- Added a `TRIGGERS` column to `gh x workflow list`.
- Shows common GitHub Actions triggers such as `push`, `pull_request`, and
  `workflow_dispatch`.
- Shows schedule cron expressions inline, for example
  `schedule: 15 6 * * 1-5`.
- Shows useful trigger filters for branch and pull request event types, such as
  `branches: main` and `types: opened, synchronize, reopened`.

### Changed

- Workflow list output now enriches GitHub workflow metadata from workflow YAML
  definitions when available.
- Dynamic or unreadable workflow definitions now display `unknown` in the
  trigger column instead of failing the list command.

[Unreleased]: https://github.com/HemSoft/gh-x/compare/v0.18.0...HEAD
[0.18.0]: https://github.com/HemSoft/gh-x/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/HemSoft/gh-x/compare/v0.16.1...v0.17.0
[0.16.0]: https://github.com/HemSoft/gh-x/compare/v0.15.4...v0.16.0
