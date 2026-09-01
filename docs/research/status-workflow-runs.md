# Research: workflow runs in `gh x status`

Date: 2026-09-01

## Requested outcome

Add the five most recent GitHub Actions runs to `gh x status`. Give the section the same bordered table treatment
as the issue and pull request sections. When all five fetched runs completed successfully, print a short, fun
perfection message.

## What the repository does today

### Status fetch and error boundaries

`statusDashboard` has data and independent error fields for issues and pull requests, but no workflow-run fields
yet. See [`src/status.go:69-82`](../../src/status.go#L69-L82).

`fetchStatusDashboard` gathers local Git state first. It then uses one captured `now` value to fetch 30 open
issues and 30 open pull requests. GitHub failures stay on the dashboard instead of failing the whole command.
This preserves the local repository report when a remote section is unavailable. See
[`src/status.go:136-190`](../../src/status.go#L136-L190) and the failure regression test at
[`src/status_test.go:303-327`](../../src/status_test.go#L303-L327).

Rendering follows the same boundary. `renderStatus` writes the repository header, issue section, and pull request
section in order. Each GitHub section prints a count, a table or empty-state message, and its own `Unavailable`
message on failure. See [`src/status.go:590-597`](../../src/status.go#L590-L597) and
[`src/status.go:796-842`](../../src/status.go#L796-L842).

The README promises that local status remains available when GitHub data cannot be fetched. A workflow-run
addition should preserve that contract. See [`README.md:100-114`](../../README.md#L100-L114).

### Issue and pull request header treatment

The issue and pull request row renderers use `styler.header` and `writeTableHeader`. The shared helper renders bold
cyan labels with a full-width cyan rule above and below the header. Plain output contains the same rules without
ANSI escapes. See [`src/issue.go:338-373`](../../src/issue.go#L338-L373),
[`src/prlist_render.go:151-190`](../../src/prlist_render.go#L151-L190),
[`src/table.go:59-64`](../../src/table.go#L59-L64), and
[`src/table.go:103-109`](../../src/table.go#L103-L109).

This is the "line header" presentation the new status section should match.

### Existing workflow-run fetch and representation

The repository already has nearly all of the needed workflow-run behavior in `src/run.go`:

- `workflowRun` decodes GitHub CLI JSON fields. `displayWorkflowRun` holds the rendered status, title, workflow,
  branch, event, clickable ID, duration, and age. See [`src/run.go:17-53`](../../src/run.go#L17-L53).
- `buildRunListArgs` constructs `gh run list --json <fields> --limit <n>` and supports repository and other filters.
  See [`src/run.go:112-137`](../../src/run.go#L112-L137).
- `executeRunList` calls the repository's shared `ghExecFunc`, decodes JSON, and builds display rows. See
  [`src/run.go:139-171`](../../src/run.go#L139-L171).
- `resolveRunStatus` maps raw status and conclusion values to the existing glyphs. Completed `success` is `✓`;
  failure, timeout, and startup failure are `X`; cancelled and action-required runs are `!`; skipped and neutral
  runs are dimmed. See [`src/run.go:174-198`](../../src/run.go#L174-L198).
- `renderRunTable` already fits the title, workflow, and branch columns to the terminal and keeps run IDs clickable.
  See [`src/run.go:263-301`](../../src/run.go#L263-L301).

Focused tests cover arguments, status mapping, display conversion, terminal alignment, clickable run IDs, and
clipboard behavior. See [`src/run_test.go:98-177`](../../src/run_test.go#L98-L177) and
[`src/run_test.go:209-405`](../../src/run_test.go#L209-L405).

Two details prevent calling `renderRunTable` directly from `gh x status`:

1. The run table currently uses faint labels and `writeRow`, not the bordered `writeTableHeader` used by issues
   and pull requests. See [`src/run.go:269-299`](../../src/run.go#L269-L299).
2. In a color-enabled terminal, it copies `gh run view <latest-id>` through OSC 52 and prints a clipboard hint.
   Status output deliberately forbids clipboard side effects. See
   [`src/run.go:303-307`](../../src/run.go#L303-L307) and
   [`src/status_test.go:375-387`](../../src/status_test.go#L375-L387).

## What GitHub guarantees

The official GitHub CLI manual describes `gh run list` as listing recent workflow runs. It supports `--limit`,
inherits `[HOST/]OWNER/REPO` selection, and exposes every field already used by `gh-x`, including `status`,
`conclusion`, `databaseId`, `displayTitle`, `workflowName`, timestamps, and URL.
Source: [GitHub CLI `gh run list` manual](https://cli.github.com/manual/gh_run_list).

The GitHub CLI source passes the requested limit to its shared run fetcher and writes the returned order without a
client-side re-sort. This supports using `gh run list --limit 5` as the existing `gh-x` path for the five most
recent runs.
Source: [GitHub CLI `pkg/cmd/run/list/list.go`](https://github.com/cli/cli/blob/trunk/pkg/cmd/run/list/list.go).

GitHub separates lifecycle `status` from terminal `conclusion`. The REST response may have `status: queued` with a
null conclusion; a successful run has completed with conclusion `success`.
Source: [GitHub REST API workflow-run endpoint][workflow-runs-api].

The official CLI also notes one presentation limitation: runs created by organization or enterprise ruleset
workflows may have no workflow name because the API does not provide it. The status table should tolerate an empty
workflow name rather than treating it as a fetch error.
Source: [GitHub CLI `gh run list` manual](https://cli.github.com/manual/gh_run_list).

## Live checks

Authenticated as `HemSoft`, I ran:

```powershell
gh run list --repo HemSoft/gh-x --limit 5 --json 'databaseId,displayTitle,workflowName,headBranch,event,status,conclusion,url,createdAt,startedAt,updatedAt'
```

The command returned five rows in newest-first order. All five were `completed` with conclusion `success`, so the
proposed perfection message has a real current example. A live open-issue query returned no open issues, so no
existing issue duplicates this request as of the research date.

## Recommended design

1. Add a fixed `statusWorkflowRunLimit = 5` and dashboard fields for rendered runs, the raw success decision, and an
   independent workflow-run error.
2. Extract the run command's fetch/decode/build logic into a helper that returns both raw and display runs. Call it
   from `fetchStatusDashboard` with an unfiltered `runListOptions{limit: 5}` and the same captured `now` used by
   issues and pull requests. Keep using `ghExecFunc` so current repository resolution, host routing, and account
   fallback stay consistent.
3. Extract a side-effect-free `renderWorkflowRunRows` helper. Use `styler.header` plus `writeTableHeader` so both
   `gh x run list` and the status section get the same bordered header style as issue and pull request tables. Keep
   the OSC 52 copy and hint only in the standalone `renderRunTable` wrapper.
4. Add `Recent workflow runs (<count>)` after the pull request section. Render `No workflow runs found.` for an
   empty result and `Unavailable: <concise error>` on fetch failure. Neither case should fail or hide local status.
5. Define perfection as exactly five returned runs where every raw row has `status == "completed"` and
   `conclusion == "success"`. Do not infer this from the `✓` display glyph, and do not celebrate when fewer than
   five runs exist.
6. Print exactly one message from a small workflow-specific praise pool when the perfection condition holds. Reuse
   the deterministic selector pattern in [`src/praise.go:10-38`](../../src/praise.go#L10-L38), but do not reuse the
   backlog text because queue-clearing messages describe a different condition. Suitable examples include
   `Five for five. Flawless.` and `CI perfection: five straight successes.`
7. Update status help, root help, README status documentation, and the unreleased changelog to mention recent
   workflow runs and the five-success celebration.

## Suggested acceptance criteria

- `gh x status` fetches and displays at most the five most recent workflow runs for the current repository, with no
  event, branch, workflow, or status filter.
- The section is headed `Recent workflow runs (<count>)` and appears after open pull requests.
- Run rows retain the existing status glyphs, title, workflow, branch, event, clickable ID, elapsed time, and age.
- The table uses the shared full-width rules and bold cyan header labels. Plain output has no ANSI escapes.
- Status rendering never emits OSC 52 or a copied-command hint.
- If exactly five runs are returned and all are completed with conclusion `success`, status prints exactly one
  deterministic-testable perfection message.
- Four successes, a fifth non-success conclusion, an in-progress or queued run, no runs, and fetch failure do not
  print perfection praise.
- A workflow-run fetch failure renders that section as unavailable without suppressing local repository, issue, or
  pull request output.
- Empty workflow names from ruleset-created runs render safely.
- Tests cover fetch arguments, success eligibility, section count, empty and unavailable states, bordered headers,
  clickable IDs, terminal fitting, and the no-clipboard guarantee.
- README, help text, changelog, formatting, static analysis, race, coverage, CRAP, and mutation gates pass.

## Issue-writing precedent

Recent `HemSoft/gh-x` feature issues use a concise title and sections for `Problem`, `Requested behavior` or
`Proposed behavior`, and `Acceptance criteria`. Issues #31 and #36 are the closest precedents because they cover
shared table headers and fun status messages. The implementation issue should follow that structure and cite this
note for the research evidence.

[workflow-runs-api]: https://docs.github.com/en/rest/actions/workflow-runs#list-workflow-runs-for-a-repository
