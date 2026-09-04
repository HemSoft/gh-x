# Repository rulesets

[`main.json`](main.json) is the source configuration for the active
`main-quality-gate` repository ruleset in `HemSoft/gh-x`.

The ruleset targets only `refs/heads/main` and requires the `Quality Gate`
check from GitHub Actions. Integration ID `15368` is the GitHub Actions app
that publishes this repository's check. The configuration has no bypass
actors. It does not require a branch to be current with `main`; it requires
the check to pass on the pull request's current head.

Run the local consistency check before applying the configuration:

```bash
go run .github/scripts/validate-main-ruleset.go
```

Create the ruleset when it does not exist:

```powershell
gh api --method POST repos/HemSoft/gh-x/rulesets `
  --input .github/rulesets/main.json
```

Update the existing ruleset by replacing `RULESET_ID` with its numeric ID:

```powershell
gh api --method PUT repos/HemSoft/gh-x/rulesets/RULESET_ID `
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
