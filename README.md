# gh-x

A GitHub CLI extension that supercharges `gh pr list` with a richer,
color-coded table view — approvals, AI reviewer status, check details,
comment resolution, and clickable PR links.
Also includes `gh x pr atm` for org-wide PR visibility.

```text
#    Title                                             Author         State  Review    AI    Appv  Checks   Cmts   Branch                 Updated
#12  PLAT-18678: Migrate user-groups to .NET 10        jdoe           open   approved  pass  2     pending  19/19  feature/PLAT-18678     23h
#10  .net 10 upgradation                               asmith         open   review    -     0     fail     -      feature/PLAT-8516      17d
#5   feat(user-groups): Add golden-path IaC structure  bclark         open   review    fail  0     pass     2/4    golden-path-alignment  4mo
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
gh x run list [flags]   # workflow runs with clickable IDs
gh x version            # show version and check for updates (also: --version, -v)
```

## What `gh x pr list` adds

Compared to `gh pr list`, this command keeps all existing filters but renders a denser, color-coded table:

| Column   | Description |
|----------|-------------|
| **#**    | PR number — clickable link to the PR on GitHub (terminals with OSC 8 support) |
| **Title**| Truncated to 51 chars |
| **Author**| PR author login |
| **State**| `open`, `draft`, `closed`, or `merged` |
| **Review**| Review decision: `approved`, `changes`, or `review` (pending) |
| **AI**   | AI reviewer status: `pass` (approved/no issues), `fail` (issues found), or `-` (no AI review). Detects CodeRabbit, Copilot PR reviewer, and other `[bot]` reviewers |
| **Appv** | Count of human approvals |
| **Checks**| CI status: `pass`, `fail`, `pending`, `merge`, or `-`. `merge` (red) indicates merge conflicts with the base branch. Includes required checks from repo rulesets that haven't reported yet |
| **Cmts** | Review thread resolution: `resolved/total` (e.g., `3/5`). `-` if no threads |
| **Branch**| Head branch name |
| **Updated**| Relative time: `12m`, `3h`, `2d`, `4mo` |

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

### Examples

```bash
gh x pr list
gh x pr list --author "@me" --state all
gh x pr list --repo owner/repo --limit 10
gh x pr list --label bug --label urgent
gh x pr list --search "review:required status:success"
gh x pr list --json
```

## What `gh x pr me` adds

All your open PRs — authored or assigned — across every repo in the org.

```text
#    Title                                       Repo       Author    State  Review  AI    Appv  Checks  Cmts   Updated
#42  feat: add repo governance (CI lint, Cop...  my-app     jdoe      open   review  fail  0     fail    0/1    2d
#15  fix: update auth token refresh logic        api        bsmith    open   review  -     0     pass    3/3    5d
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
#    Title                                       Repo       Author    State  Review  AI    Appv  Checks  Cmts   Updated
#42  feat: add repo governance (CI lint, Cop...  my-app     jdoe      open   review  fail  0     fail    0/1    2d
#41  feat: add contract-testing for PactNet...   my-app     jdoe      open   review  fail  0     pass    12/12  2d
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

Runs a read-only PR review through an agentic CLI. The command resolves PR
metadata with `gh pr view`, builds a review prompt, and delegates analysis to a
provider preset. It does not post PR comments, approve, request changes, merge,
commit, or edit files.

Default provider is `codex` with model `gpt-5.5`, high reasoning effort, and
`strict` review mode. These are configurable with flags or environment
variables.

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
| `--dry-run` | Print the resolved command and prompt without running the agent |

### `review` examples

```bash
gh x pr review
gh x pr review 42 --mode strict
gh x pr review 42 --mode medium
gh x pr review 42 --mode fast-lane
gh x pr review 42 --agent claude --model sonnet
gh x pr review 42 --agent copilot
gh x pr review 42 --dry-run
GH_X_PR_REVIEW_AGENT=claude GH_X_PR_REVIEW_MODE=medium gh x pr review 42
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
go build -o gh-x.exe .   # Windows
go build -o gh-x .        # macOS/Linux
gh extension install .

# After code changes, just rebuild — no reinstall needed
go build -o gh-x.exe .
gh x pr list
```

A convenience script is provided for Windows:

```powershell
.\build.ps1   # runs vet → test → build
```

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
