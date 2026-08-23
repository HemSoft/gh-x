# Changelog

All notable changes to `gh-x` will be documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project uses semantic versioning.

## [Unreleased]

### Added

- Added `gh x monitor`, a Bubble Tea dashboard across configured repositories
  with PR and issue sections, saved layout state, and per-section filters
  defined in `src/cfg.yml`. Replaces `gh x pr list --watch`.

### Changed

- Removed the `pr list` watch flags (`--watch`, `--monitor`, `--interval`);
  use `gh x monitor` instead.
- Moved all Go sources and tests under `src/`; build commands now target
  `./src`.
- Condensed pull request tables with one-space column gaps, compact review
  decision symbols, and `Rev` and `Upd` headers.

### Fixed

- Routed `gh x monitor` GraphQL requests by normalized GitHub host, so
  Enterprise and mixed-host configurations use host-specific endpoints while
  search qualifiers remain `OWNER/REPO`.
- Added automatic multi-account fallback: when the active GitHub account
  cannot access the target repository, commands retry with another logged-in
  account's token via `GH_TOKEN` on the retried subprocess only, print a
  one-line notice, and never modify the global active account. User-scoped
  commands (`pr me`, `pr atm`) and review submission always run as the active
  account.
- Added interactive, read-only `gh x pr list --watch` monitoring with a
  `--monitor` alias, configurable refresh intervals, stable rows, and concise
  refresh status and change summaries.
- Added local Copilot and Codex CLI usage dashboards (`src/codex-dashboard`,
  `src/dashboard-hub`) with launcher scripts, a combined hub, and an optional
  Tailscale Serve installer for tailnet access.
- Expanded `gh x status` into a structured repository health dashboard with
  default-branch health, local/remote/dangling branch counts, conservative
  worktree cleanup candidates, and separate open issue and pull request tables.
- Added an `SFL` column that shows the latest formal SFL Reviewer decision
  independently from aggregate AI finding status.
- Recognized current-head Codex Cloud review summaries posted as pull-request
  conversation comments, so clean reviews show `AI pass` and `0/0!` instead of
  appearing as if no AI review ran.
- Show the comments `!` marker when every AI review thread is resolved and
  AI status is `pass`, even if the latest bot review originally left inline
  findings that have since been addressed.
- Collapsed repeated check contexts from reruns so a stale failure no longer
  overrides a newer result for the same workflow and job.
- Ignored AI and SFL reviews that target an earlier head commit.
- Recognized `sfl-app[bot]` as an SFL reviewer.
- Reported `?` for AI and SFL status when GitHub returns incomplete review
  and thread data, instead of a possibly incorrect status.

### Removed

- Removed the SFL review automation from the repository (`sfl-pr-review`
  workflows, `sfl.json`, the `sfl-review` label, and gh-aw support files) and
  dropped the `SFL` column from `gh x pr list`, `gh x pr atm`, `gh x pr me`,
  and watch mode. AI PR reviews for this repository now come from cubic and
  Codex reviews only.

### Improved

- Defaulted `gh x pr review` to Codex with model `gpt-5.5`, high reasoning
  effort, and `strict` review mode.
- Added `medium` review mode between `strict` and `fast-lane`.
- Added opt-in posted PR reviews for `gh x pr review --post`, including a
  formal Markdown review body and validated inline GitHub review comments.
- Added `--allow-approve` approval gating for strict clean reviews.

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

[Unreleased]: https://github.com/HemSoft/gh-x/compare/v0.2.2...HEAD
[0.18.0]: https://github.com/HemSoft/gh-x/compare/v0.17.0...v0.18.0
[0.17.0]: https://github.com/HemSoft/gh-x/compare/v0.16.1...v0.17.0
[0.16.0]: https://github.com/HemSoft/gh-x/compare/v0.15.4...v0.16.0
