# Repository rulesets

[`main.json`](main.json) is the source configuration for the active
`main-quality-gate` repository ruleset in `HemSoft/gh-x`.

The ruleset targets only `refs/heads/main`. Changes must arrive through a pull
request, the `Quality Gate` check from GitHub Actions must pass, and every
review conversation must be resolved. Integration ID `15368` is the GitHub
Actions app that publishes this repository's check.

The ruleset has no bypass actors and requires zero approving reviews. It does
not dismiss stale approvals after a push, require code-owner review, require
approval of the last push, or require a branch to be current with `main`.

Run the local consistency check before applying the configuration:

```bash
go test ./.github/scripts/...
go run .github/scripts/validate-main-ruleset.go
```

Create the ruleset when it does not exist:

```powershell
gh api --method POST repos/HemSoft/gh-x/rulesets `
  --input .github/rulesets/main.json
```

Update the existing ruleset by resolving its ID from its stable name:

```powershell
$rulesetId = gh api repos/HemSoft/gh-x/rulesets `
  --jq '.[] | select(.name == "main-quality-gate") | .id'
gh api --method PUT "repos/HemSoft/gh-x/rulesets/$rulesetId" `
  --input .github/rulesets/main.json
```

Verify the stored configuration and the rules effective on `main`:

```powershell
gh api repos/HemSoft/gh-x/rulesets
gh api repos/HemSoft/gh-x/rules/branches/main
```

Keep the final job name in `.github/workflows/ci.yml` equal to `Quality Gate`.
Changing that name without updating the ruleset would leave pull requests
waiting for a check that never reports.
