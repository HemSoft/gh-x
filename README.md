# gh-x

A GitHub CLI extension that supercharges `gh pr list` with a richer,
color-coded table view — approvals, AI reviewer status, check details,
comment resolution, and clickable PR links.
Also includes `gh x pr atm` for org-wide PR visibility.

```text
#   Title                                             Author State Rev AI   Appv Checks  Cmts  Branch                Upd
#12 PLAT-18678: Migrate user-groups to .NET 10        jdoe   open  ✓   pass 2    pending 19/19 feature/PLAT-18678    23h
#10 .net 10 upgradation                               asmith open  •   -    0    fail    -     feature/PLAT-8516     17d
#5  feat(user-groups): Add golden-path IaC structure  bclark open  •   fail 0    pass    2/4   golden-path-alignment 4mo
```

## Installation

Requires [GitHub CLI](https://cli.github.com/) (`gh`) authenticated with your account.

```bash
gh extension install HemSoft/gh-x
```

That's it. Prebuilt binaries are available for all platforms — no Go toolchain needed.

## Usage

```bash
gh x pr list [flags]    # enriched PR list for current repo
gh x pr me [flags]      # all your open PRs (authored + assigned) across an org
gh x pr atm [flags]     # org-wide PRs needing your attention
gh x pr review [number] # read-only agentic PR review
gh x pr changelog       # show release notes for recent versions
gh x status             # repository, branch, worktree, issue, and PR health
gh x run list [flags]   # workflow runs with clickable IDs
gh x version            # show version and check for updates (also: --version, -v)
```

### Multi-account fallback

If you switch between personal and work accounts, `gh x` adapts automatically.
When the active account cannot access the target repository, commands retry
with each other logged-in account until one succeeds (`gh auth status --json
hosts` supplies candidates) and print a one-line notice on stderr, for
example:

```text
[gh-x] note: retried as fhemmerrelias after an access failure
```

The global active account is never modified — the retry token lives only
inside the retried subprocess. Explicit `GH_TOKEN` or `GITHUB_TOKEN` overrides
are respected as-is (no fallback), auth commands never fall back, and public
repositories never trigger a retry because either token can read them. Tokens
resolve lazily and are cached for the current process; they are not stored or
shared between runs.

Two flows are pinned to the active account and never fall back: identity-scoped
queries (`gh x pr me`, `gh x pr atm`, `gh x codespace list` — they resolve
"me" or list the authenticated user's resources, so retrying under a different
identity could answer with someone else's data), and the entire
`gh x pr review` operation (fetch, agent, and submission are identity-bound,
and posting a review is non-idempotent). `@me` in author, assignee, and
`--search` filters resolves to the active account's login up front for the
same reason.

## What `gh x status` adds

`gh x status` starts with a repository health header for the resolved default
branch and current worktree. It counts local, remote, and dangling branches,
then reports linked worktrees and conservative cleanup candidates. A linked
worktree is suggested only when it is unlocked, clean, merged into the default
branch, and has no open pull request. Git-prunable records must also have a
branch or detached commit known to be merged. Current, primary, locked, and
default-branch worktrees are never suggested. Suggestions are informational;
the command never deletes or prunes a worktree.

Open issues and enriched open pull requests appear in separate tables below the
header. Local Git status still renders when GitHub data is unavailable.

## What `gh x pr list` adds

Compared to `gh pr list`, this command keeps all existing filters but renders a denser, color-coded table:

| Column   | Description |
|----------|-------------|
| **#**    | PR number — clickable link to the PR on GitHub (terminals with OSC 8 support) |
| **Title**| Truncated to 51 chars |
| **Author**| PR author login |
| **State**| `open`, `draft`, `closed`, or `merged` |
| **Rev**  | Overall review decision: `✓` approved, `✗` changes requested, or `•` review required |
| **AI**   | AI reviewer status: `pass` (approved/no issues), `fail` (issues found), or `-` (no AI review). Detects CodeRabbit, Copilot PR reviewer, and other `[bot]` reviewers |
| **Appv** | Count of unique formal approvals, including bot reviewers |
| **Checks**| CI status: `pass`, `fail`, `pending`, `merge`, or `-`. `merge` (red) indicates merge conflicts with the base branch. Includes required checks from repo rulesets that haven't reported yet |
| **Cmts** | Review thread resolution: `resolved/total` (e.g., `3/5`). `-` if no threads |
| **Branch**| Head branch name |
| **Upd**  | Relative time: `12m`, `3h`, `2d`, `4mo` |

### Supported flags

| Flag | Description |
|------|-------------|
| `-R, --repo OWNER/REPO` | Target a specific repository |
| `-L, --limit N` | Maximum PRs to show (default: 30) |
| `-s, --state STATE` | Filter: `open`, `closed`, `merged`, `all` |
| `-A, --author USER` | Filter by PR author |
| `-a, --assignee USER` | Filter by assignee |
| `--app APP` | Filter by GitHub App |
| `-B, --base BRANCH` | Filter by base branch |
| `-H, --head BRANCH` | Filter by head branch |
| `-l, --label LABEL` | Filter by label (repeatable) |
| `-S, --search QUERY` | GitHub search syntax |
| `-d, --draft` | Show only draft PRs |
| `-w, --web` | Open in browser |
| `--json` | Output as JSON |
| `--watch` | Refresh the table until `Esc` or `Ctrl+C` |
| `--monitor` | Alias for `--watch` |
| `--interval DUR` | Refresh interval for watch mode (default: `30s`) |

### Examples

```bash
gh x pr list
gh x pr list --author "@me" --state all
gh x pr list --repo owner/repo --limit 10
gh x pr list --label bug --label urgent
gh x pr list --search "review:required status:success"
gh x pr list --json
gh x pr list --watch
gh x pr list --monitor --interval 45s
```

Watch mode is an interactive, read-only view. It reruns the same query, keeps
the table visible during transient refresh failures, and reports concise state
changes below the table. It requires a terminal and cannot be combined with
`--json` or `--web`.

## What `gh x pr me` adds

All your open PRs — authored or assigned — across every repo in the org.

```text
#   Title                                      Repo   Author State Rev AI   Appv Checks Cmts Upd
#42 feat: add repo governance (CI lint, Cop... my-app jdoe   open  •   fail 0    fail   0/1  2d
#15 fix: update auth token refresh logic       api    bsmith open  •   -    0    pass   3/3  5d
```

Works with both organizations and personal accounts.

### `me` flags

| Flag | Description |
|------|-------------|
| `-o, --org ORG` | Organization or user to search (default: inferred from current repo) |
| `-L, --limit N` | Maximum PRs to show (default: 30) |
| `--json` | Output as JSON |

### `me` examples

```bash
gh x pr me                           # my PRs across current org
gh x pr me --org AcmeCorp            # my PRs in a specific org
gh x pr me --limit 10                # capped at 10
gh x pr me --json                    # machine-readable output
```

## What `gh x pr atm` adds

An org-wide view of PRs that need your attention — no more checking each repo individually.

```text
#   Title                                      Repo   Author State Rev AI   Appv Checks Cmts  Upd
#42 feat: add repo governance (CI lint, Cop... my-app jdoe   open  •   fail 0    fail   0/1   2d
#41 feat: add contract-testing for PactNet...  my-app jdoe   open  •   pass 0    pass   12/12 2d
```

By default, shows open PRs you authored across the org.
Use `--review-required` to see PRs awaiting your review.

### `atm` flags

| Flag | Description |
|------|-------------|
| `-o, --org ORG` | Organization to search (default: inferred from current repo) |
| `-L, --limit N` | Maximum PRs to show (default: 30) |
| `-r, --review-required` | Show PRs where your review is requested |
| `--json` | Output as JSON |

### `atm` examples

```bash
gh x pr atm                              # my PRs across current org
gh x pr atm --org AcmeCorp                # my PRs in a specific org
gh x pr atm --review-required            # PRs awaiting my review
gh x pr atm --org AcmeCorp -r --limit 10   # review requests, capped
gh x pr atm --json                       # machine-readable output
```

## What `gh x pr review` adds

Runs a PR review through an agentic CLI. The command resolves PR metadata with
`gh pr view`, builds a review prompt, and delegates analysis to a provider
preset. By default it prints the agent's read-only review locally. With
`--post`, it captures structured findings and posts one GitHub PR review with a
formal Markdown body and validated inline review comments.

Default provider is `codex` with model `gpt-5.5`, high reasoning effort, and
`strict` review mode. These are configurable with flags or environment
variables.

Approvals are opt-in. `--allow-approve` implies posting a review, and approval
is only selected in `strict` mode when the structured result is explicitly
approval-eligible and has no critical, medium, or nitpick findings. Critical
findings post as `REQUEST_CHANGES`; other findings post as `COMMENT`.

### `review` flags

| Flag | Description |
| --- | --- |
| `-R, --repo OWNER/REPO` | Target a specific repository |
| `-a, --agent AGENT` | Provider preset: `codex`, `claude`, `copilot`, `gemini`, `opencode`, or `custom` |
| `--command COMMAND` | Custom command template for `--agent custom` |
| `-m, --model MODEL` | Model passed through to supported providers |
| `--effort EFFORT` | Reasoning effort for supported providers: `low`, `medium`, or `high` |
| `--mode MODE` | Review mode: `strict`, `medium`, or `fast-lane` |
| `--preset MODE` | Alias for `--mode` |
| `-B, --base BRANCH` | Override the base branch in the review prompt |
| `-i, --instructions TEXT` | Additional review instructions |
| `--instructions-file FILE` | Read additional instructions from a file |
| `--reviewer NAME` | Reviewer identity used in posted review reports |
| `--dry-run` | Print the resolved command and prompt without running the agent |
| `--post` | Post a GitHub PR review with inline comments |
| `--allow-approve` | Allow strict-mode approval when the review has no findings |

### `review` examples

```bash
gh x pr review
gh x pr review 42 --mode strict
gh x pr review 42 --mode medium
gh x pr review 42 --mode fast-lane
gh x pr review 42 --post
gh x pr review 42 --post --allow-approve
gh x pr review 42 --agent claude --model sonnet
gh x pr review 42 --agent copilot
gh x pr review 42 --dry-run
GH_X_PR_REVIEW_AGENT=claude GH_X_PR_REVIEW_MODE=medium gh x pr review 42
GH_X_PR_REVIEW_POST=true GH_X_PR_REVIEW_ALLOW_APPROVE=true gh x pr review 42
gh x pr review 42 --agent custom --command 'my-reviewer --prompt "{prompt}"'
```

## What `gh x run list` adds

Workflow run listing with clickable run IDs — ctrl-click any ID to open
the run in your browser.

```text
   Title                                     Workflow             Branch                    Event         ID           Elapsed  Age
✓  feat: add pr subcommand group for mul...  Copilot Setup Steps  v0.4.0                    push          25714589506  10s      19m
✓  feat: add pr subcommand group for mul...  Auto Release         main                      push          25714553634  45s      20m
X  feat: comprehensive quality improvem...   CI Quality Gates     feature/quality-imp...     pull_request  25708704123  1m43s    3h
```

| Column     | Description |
|------------|-------------|
| **Status** | `✓` success, `X` failure, `*` in progress, `○` queued, `!` cancelled |
| **Title**  | Commit or PR title (truncated) |
| **Workflow** | Workflow name |
| **Branch** | Head branch |
| **Event**  | Trigger: `push`, `pull_request`, `dynamic`, etc. |
| **ID**     | Run ID — clickable link to the run on GitHub (OSC 8) |
| **Elapsed** | Run duration: `10s`, `1m56s`, `2h15m` |
| **Age**    | Time since created: `5m`, `2h`, `3d` |

### `run list` flags

| Flag | Description |
|------|-------------|
| `-R, --repo OWNER/REPO` | Target a specific repository |
| `-L, --limit N` | Maximum runs to show (default: 20) |
| `-s, --status STATUS` | Filter: `queued`, `in_progress`, `completed`, `success`, `failure`, etc. |
| `-w, --workflow NAME` | Filter by workflow name |
| `-b, --branch BRANCH` | Filter by branch |
| `-e, --event EVENT` | Filter by event type |
| `-u, --user USER` | Filter by user who triggered the run |

### `run list` examples

```bash
gh x run list                                 # recent runs
gh x run list --status failure                # failed runs only
gh x run list --workflow "CI Quality Gates"   # specific workflow
gh x run list --branch main --limit 10        # main branch, last 10
gh x run list --event pull_request            # PR-triggered runs
```

## Changelog

View release notes directly from the CLI:

```bash
gh x pr changelog                     # last 5 releases
gh x pr changelog --limit 10          # last 10 releases
gh x pr changelog --version 0.3.0     # specific version
```

The currently installed version is marked with `← installed`.

## Checking for updates

```bash
gh x version
```

```text
gh-x v0.1.2 © 2026 HemSoft Developments · gh extension install HemSoft/gh-x
✓ Up to date
```

If a newer release exists:

```text
gh-x v0.1.0 © 2026 HemSoft Developments · gh extension install HemSoft/gh-x
↑ v0.1.2 available · gh extension upgrade gh-x
```

## Local development

Requires Go 1.26+.

```bash
# Build and install locally (one-time symlink setup)
go build -o gh-x.exe ./src   # Windows
go build -o gh-x ./src       # macOS/Linux
gh extension install .

# After code changes, just rebuild — no reinstall needed
go build -o gh-x.exe ./src
gh x pr list
```

A convenience script is provided for Windows:

```powershell
.\build.ps1   # runs vet → test → build
```

### Local usage dashboards

The repository includes launchers for the local Copilot CLI and Codex CLI
usage dashboards:

```powershell
.\run.ps1        # open the Copilot dashboard in the active Copilot Canvas
.\run-codex.ps1  # open the Codex dashboard in a standalone window
.\run-dashboard-hub.ps1  # open the combined local dashboard hub
```

The Codex dashboard reads `~\.codex\state_5.sqlite` and local rollout JSONL
files in read-only mode. It shows recent sessions, token and cache totals,
requests, tool calls, context usage, runtimes, the latest weekly rate-limit
snapshot, and projected usage at reset based on the current pace. Changed
session rows glow after each refresh. The server listens only on `127.0.0.1`
behind a random URL token. The Codex launcher starts that server in the
background and opens it in a dedicated Microsoft Edge or Google Chrome app
window. Later launches reuse the running local server.

For phone access over Tailscale, install the persistent loopback-only hub once:

```powershell
.\install-dashboard-hub.ps1
```

The installer registers the `HemSoft CLI Dashboard Hub` logon task and mounts
the hub at `/dashboards/` with Tailscale Serve. Existing Serve mounts are left
in place. Open `https://<home-magicdns-name>/dashboards/` from another device
on the tailnet. The installer also adds a raw TCP forward on tailnet port 80,
so `http://<home-tailscale-ip>/` works when a client cannot resolve MagicDNS.
Neither route is exposed to the LAN or public internet. The hub reads Codex state directly and uses the installed
`copilot-spend` extension's read-only session-store adapter and current UI.

## How it works

- Wraps `gh pr list --json` for core PR data and authentication
- Makes a single GraphQL call for supplemental data (review threads, AI reviewer detection, comment counts)
- Fetches required status check contexts from repo rulesets to detect pending-but-unreported CI checks
- Uses [termenv](https://github.com/muesli/termenv) for color output, respecting `NO_COLOR` and `CLICOLOR` conventions
- SSH host aliases (e.g., `github-work:org/repo`) are handled gracefully via `gh repo view` fallback

## Releases

Every push to `main` that includes code changes automatically creates
a new patch release with prebuilt binaries for all platforms.
Documentation-only changes are skipped.

For major or minor version bumps, tag manually:

```bash
git tag v1.0.0
git push origin v1.0.0
```

## License

MIT
