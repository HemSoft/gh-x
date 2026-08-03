---
description: |
  Standalone full-spectrum pull request review triggered by the sfl-review
  label. Performs security, correctness and reliability, and quality and
  maintainability passes, posts one inline thread per finding, submits a
  consolidated review, and publishes the SFL Reviewer Approval check.

on:
  # Fork pull requests are intentionally unsupported. gh-aw's repository-ID
  # safety gate skips them before activation, so no review/check is produced
  # and the trigger label remains for a maintainer to remove.
  label_command:
    name: sfl-review
    events: [pull_request]
    remove_label: true

if: >-
  github.event_name != 'pull_request' ||
  github.event.pull_request.head.repo.id == github.repository_id

permissions:
  contents: read
  pull-requests: read

models:
  default-ai-credits-pricing:
    input: 3
    output: 15

engine:
  id: copilot
  env:
    COPILOT_PROVIDER_BASE_URL: https://openrouter.ai/api/v1
    COPILOT_PROVIDER_API_KEY: ${{ secrets.OPENROUTER_API_KEY }}
    COPILOT_PROVIDER_TYPE: openai
    COPILOT_PROVIDER_WIRE_API: responses
    COPILOT_MODEL: moonshotai/kimi-k3

model: moonshotai/kimi-k3

network:
  allowed:
    - openrouter.ai

tools:
  github:
    toolsets: [pull_requests, repos]
    github-app:
      client-id: ${{ vars.SFL_APP_CLIENT_ID }}
      private-key: ${{ secrets.SFL_APP_PRIVATE_KEY }}

safe-outputs:
  threat-detection:
    post-steps:
      - name: Generate SFL App token for final head verification
        id: sfl-final-head-token
        uses: actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3.2.0
        with:
          client-id: ${{ vars.SFL_APP_CLIENT_ID }}
          private-key: ${{ secrets.SFL_APP_PRIVATE_KEY }}
          owner: ${{ github.repository_owner }}
          repositories: ${{ github.event.repository.name }}
          permission-pull-requests: read
      - name: Verify PR head before safe outputs
        env:
          GH_TOKEN: ${{ steps.sfl-final-head-token.outputs.token }}
          EXPECTED_HEAD: ${{ github.event.pull_request.head.sha }}
          PR_NUMBER: ${{ github.event.pull_request.number }}
          REPOSITORY: ${{ github.repository }}
        run: |
          live_head="$(gh api "repos/${REPOSITORY}/pulls/${PR_NUMBER}" --jq '.head.sha')"
          if [[ "$live_head" != "$EXPECTED_HEAD" ]]; then
            echo "::error::PR head changed from ${EXPECTED_HEAD} to ${live_head}; suppressing stale SFL outputs"
            exit 1
          fi
  github-app:
    client-id: ${{ vars.SFL_APP_CLIENT_ID }}
    private-key: ${{ secrets.SFL_APP_PRIVATE_KEY }}
  create-pull-request-review-comment:
    commit-id: ${{ github.event.pull_request.head.sha }}
    side: RIGHT
    max: 20
  submit-pull-request-review:
    allowed-events: [APPROVE, REQUEST_CHANGES]
    commit-id: ${{ github.event.pull_request.head.sha }}
    supersede-older-reviews: true
    footer: always
  create-check-run:
    max: 1
    name: "SFL Reviewer Approval"
---
<!--
Deployed from: HemSoft/set-it-free-loop/deployment/workflows/sfl-pr-review.md@78483bbf7edf0a4f8d3bf2f68e58678da36044ae
-->
<!-- To upgrade: re-run deploy-workflow.ps1 at the desired SHA -->

<!-- sfl:
  status: active
  version: "1.0.0"
  category: review
  risk-class: trivial
  target-labels: [sfl-review]
  outcome-definition: |
    A same-repository triggering pull request receives a current-head structured
    review, one inline thread per finding, and an SFL Reviewer Approval check.
    Fork pull requests are intentionally skipped by the gh-aw safety gate.
  acceptance-criteria:
    - The sfl-review label triggers exactly one current-head review run for a same-repository pull request
    - Fork pull requests are skipped without a review/check and retain the trigger label
    - The trigger label is consumed during authorized activation
    - Security, correctness/reliability, and quality/maintainability are reviewed
    - Every finding is an inline thread classified Critical, High, Medium, or Low
    - The review body reports the run ID, head SHA, verdict, and severity counts
    - Critical or High findings fail the approval check and request changes
    - Still-applicable unresolved SFL findings remain part of the verdict on reruns
    - Medium or Low findings do not fail the approval check
    - Zero findings produce an approving review and successful approval check
    - The live PR head is rechecked after threat detection before safe outputs
    - Threat-detection or output-publication infrastructure failures fail closed
  source-repo: HemSoft/set-it-free-loop
-->

# SFL Review - Full-Spectrum Pull Request Review

Review only the pull request that triggered this workflow. The reviewed commit
must be `${{ github.event.pull_request.head.sha }}` and the SFL run ID is
`${{ github.run_id }}`.

Use the GitHub pull request tools to read the triggering PR, its changed files,
and the complete diff. Before creating comments, list existing review comments
and unresolved threads on the current head. Re-evaluate every unresolved SFL
finding against the current head. The finding set for this run is the union of
new findings and still-applicable unresolved SFL findings. Do not duplicate an
existing finding's inline comment, but keep that finding in the current run's
severity counts and verdict. Exclude resolved findings and findings that are no
longer applicable to the current head. An existing SFL finding is an unresolved
inline review comment authored by the SFL reviewer whose body starts with one of
the exact severity prefixes below.

## Required review passes

Perform all three evidence-based passes independently before producing output.

1. **Security**
   - Injection, unsafe command or path construction, XSS, SSRF, and deserialization
   - Authentication, authorization, privilege boundaries, and secret exposure
   - Dependency, workflow, and supply-chain risks
2. **Correctness and Reliability**
   - Logic errors, regressions, incorrect assumptions, null and boundary cases
   - Error handling, races, resource leaks, data loss, and compatibility
   - Whether tests cover every meaningful new or changed behavior
3. **Quality and Maintainability**
   - Excessive complexity, duplication, coupling, unclear ownership, and dead code
   - Type safety, performance regressions, operational risk, and repository conventions
   - Whether the implementation is the smallest complete and defensible change

## Finding policy

Classify every finding into exactly one severity:

- **CRITICAL** - exploitable security issue, data loss, production crash,
  public API break, race, or deadlock
- **HIGH** - serious correctness, authorization, reliability, or operational defect
- **MEDIUM** - material bug avenue, missing logic-branch tests, performance
  regression, or maintainability problem
- **LOW** - actionable improvement with concrete value and low implementation risk

Do not report style preferences, speculative concerns, or findings without
specific evidence from the changed code.

## Current-head gate

Immediately before producing any safe output, re-read the triggering pull
request with the GitHub pull request tools. Compare its live head SHA with
`${{ github.event.pull_request.head.sha }}`. Do not rely on an earlier PR read
for this comparison.

If the SHAs differ, do not create inline comments and do not submit a review.
Call only `create_check_run` with conclusion `failure`, title
`SFL review canceled: PR head changed`, and a summary containing the reviewed
SHA, live SHA, run ID, and an instruction to reapply the `sfl-review` label.
Then stop. This stale-head result takes precedence over the approval policy.

The review and its inline comments are also infrastructure-pinned to
`${{ github.event.pull_request.head.sha }}`. Never target a later commit. The
approval check uses the triggering pull request event's head SHA, so a newer
head must receive its own SFL run and check. A final fail-closed head comparison
runs after threat detection and prevents stale safe outputs from being published.

For each finding, call `create_pull_request_review_comment` on the most precise
changed line. Set `side` to `RIGHT` for an added or context line and `LEFT` for
a deleted line. The comment body must begin with one of these exact prefixes:

- `**CRITICAL Finding**`
- `**HIGH Finding**`
- `**MEDIUM Finding**`
- `**LOW Finding**`

After the prefix, state the defect, impact, evidence, and a concrete fix.
Create exactly one inline thread per new finding. Do not create another thread
for a still-applicable existing finding. If there are no new findings, create no
inline comments.

## Approval policy

Count the complete current-run finding set by severity, including every
still-applicable unresolved SFL finding even when its existing thread was not
duplicated during this run.

- If any Critical or High finding exists, submit `REQUEST_CHANGES` and create
  the `SFL Reviewer Approval` check with conclusion `failure`.
- If only Medium or Low findings exist, submit `APPROVE` and create the check
  with conclusion `success`.
- If no findings exist, submit `APPROVE` and create the check with conclusion
  `success`.

Call `submit_pull_request_review` exactly once. Use this body and replace every
`<...>` placeholder with the actual value:

```markdown
## SFL Full-Spectrum Review

SFL run ID: ${{ github.run_id }}
Head SHA: ${{ github.event.pull_request.head.sha }}
Verdict: <APPROVE or CHANGES_REQUESTED>

| Severity | Count |
| --- | ---: |
| Critical | <count> |
| High | <count> |
| Medium | <count> |
| Low | <count> |

### Review passes

- Security: complete
- Correctness and Reliability: complete
- Quality and Maintainability: complete

### Summary

Concise evidence-based summary of the review result.
```

Use `Verdict: CHANGES_REQUESTED` when Critical or High findings exist.

Call `create_check_run` exactly once for the check named
`SFL Reviewer Approval` with:

- `title`: `SFL full-spectrum review complete`
- `summary`: the verdict, head SHA, run ID, and severity counts
- `conclusion`: the approval-policy result above

Do not modify code, branches, pull request labels, or pull request metadata.
