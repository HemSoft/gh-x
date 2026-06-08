---
name: perfection
description: "V1.0 - Commands: audit, coverage, crap, mutate, simplify, harden. Comprehensive Go code quality enforcement for gh-x: test coverage analysis, CRAP score tracking, mutation testing, simplification patterns, and static analysis. Use when improving code quality, writing tests, or before merging."
compatibility: Requires go 1.23+, git
hooks:
  PostToolUse:
    - matcher: "Read|Write|Edit"
      hooks:
        - type: prompt
          prompt: |
            If a file was read, written, or edited in the perfection directory (path contains 'perfection'), verify that history logging occurred.
            
            Check if History/{YYYY-MM-DD}.md exists and contains an entry for this interaction with:
            - Format: "## HH:MM - {Action Taken}"
            - One-line summary
            - Accurate timestamp (obtained via `Get-Date -Format "HH:mm"` command, never guessed)
            
            If history entry is missing or incomplete, provide specific feedback on what needs to be added.
            If history entry exists and is properly formatted, acknowledge completion.
  Stop:
    - matcher: "*"
      hooks:
        - type: prompt
          prompt: |
            Before stopping, if perfection was used (check if any files in perfection directory were modified), verify that the interaction was logged:
            
            1. Check if History/{YYYY-MM-DD}.md exists in perfection directory
            2. Verify it contains an entry with format "## HH:MM - {Action Taken}" where HH:MM was obtained via `Get-Date -Format "HH:mm"` (never guessed)
            3. Ensure the entry includes a one-line summary of what was done
            
            If history entry is missing:
            - Return {"decision": "block", "reason": "History entry missing. Please log this interaction to History/{YYYY-MM-DD}.md with format: ## HH:MM - {Action Taken}\n{One-line summary}\n\nCRITICAL: Get the current time using `Get-Date -Format \"HH:mm\"` command - never guess the timestamp."}
            
            If history entry exists:
            - Return {"decision": "approve"}
            
            Include a systemMessage with details about the history entry status.
---

# Perfection — Go Code Quality Enforcement

Comprehensive quality enforcement for this Go CLI extension. Every command operates from the repo root and assumes `go.mod` is present.

## Project Context

- **Module**: `github.com/HemSoft/gh-x`
- **Package**: `main` (single package)
- **Source files**: `main.go`, `prlist.go`, `atm.go`, `me.go`, `changelog.go`
- **Test files**: `main_test.go`, `atm_test.go`, `me_test.go`, `changelog_test.go`
- **Build**: `go build ./...`
- **Test**: `go test -race -count=1 ./...`
- **Vet**: `go vet ./...`
- **Coverage**: `go test -race "-coverprofile=coverage.out" ./... && go tool cover -func=coverage.out`
- **CI workflow**: `.github/workflows/ci.yml` — single source of truth for all quality gates

## Required Tools

Install missing tools before first use:

```powershell
# Static analysis
go install honnef.co/go/tools/cmd/staticcheck@latest

# Cyclomatic complexity
go install github.com/fzipp/gocyclo/cmd/gocyclo@latest

# Cognitive complexity
go install github.com/uudashr/gocognit/cmd/gocognit@latest

# Mutation testing
go install github.com/go-gremlins/gremlins/cmd/gremlins@latest

# Dead code detection
go install golang.org/x/tools/cmd/deadcode@latest

# Vulnerability scanning
go install golang.org/x/vuln/cmd/govulncheck@latest
```

Check if tools are installed: `Get-Command staticcheck, gocyclo, gocognit, gremlins, deadcode, govulncheck -ErrorAction SilentlyContinue | Select-Object Name`

Markdown linting (runs via npx, no install needed): `npx --yes markdownlint-cli2 '**/*.md' '#node_modules' '#.agents' '#.github/agents'`

## Commands

### `audit` — Full Quality Report

Run all checks and produce a single quality scorecard. Execute in this order:

1. `go build ./...`
2. `go vet ./...`
3. `gofmt -l .` (must produce no output)
4. `go mod tidy` (must produce no diff)
5. `staticcheck ./...`
6. `deadcode ./...`
7. `govulncheck ./...`
8. `grep -rn '//nolint\|//lint:ignore\|//nosec\|#nosec' --include='*.go' .` (must find nothing)
9. `grep -rn 'fmt\.Print' --include='*.go' . | grep -v '_test\.go'` (anti-pattern: use writer injection)
10. `go test -race -count=1 -coverprofile=coverage.out ./... && go tool cover -func=coverage.out`
11. `gocyclo -over 10 -ignore "_test\.go" .` (must produce no output)
12. `gocognit -over 15 -ignore "_test\.go" .` (must produce no output)
13. `npx --yes markdownlint-cli2 '**/*.md' '#node_modules' '#.agents' '#.github/agents'`

Present results as a scorecard table:

```
## Quality Scorecard

| Metric              | Value   | Target  | Status |
|---------------------|---------|---------|--------|
| Build               | pass    | pass    | ✅     |
| Vet                 | pass    | pass    | ✅     |
| Formatting (gofmt)  | pass    | pass    | ✅     |
| Module tidy         | pass    | pass    | ✅     |
| Staticcheck         | pass    | pass    | ✅     |
| Dead Code           | 0 funcs | 0       | ✅     |
| Vulnerability Scan  | 0 vulns | 0       | ✅     |
| Lint Suppressions   | 0 found | 0       | ✅     |
| Anti-Patterns       | 0 found | 0       | ✅     |
| Race Detection      | pass    | pass    | ✅     |
| Test Coverage       | 77.8%   | ≥70%    | ✅     |
| Max Cyclomatic      | 4       | ≤10     | ✅     |
| Max Cognitive       | 8       | ≤15     | ✅     |
| CRAP Score          | 4.2     | <30     | ✅     |
| Mutation Score      | 92%     | ≥90%    | ✅     |
| Markdown Lint       | pass    | pass    | ✅     |
```

After the table, list the top 3 actionable improvements ranked by impact.

### `coverage` — Detailed Coverage Analysis

Generate and analyze test coverage:

```powershell
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

For each function with <80% coverage:
1. List the function name, file, and current coverage
2. Identify which branches/paths are untested
3. Write the missing test cases using table-driven tests (this repo's established pattern)

**Coverage targets:**
- 🟢 ≥80% — excellent
- 🟡 60-79% — acceptable, improve opportunistically
- 🔴 <60% — requires immediate attention

After analysis, **write the tests** — don't just list what's missing. Follow existing patterns in `main_test.go`:
- Table-driven tests with `t.Run`
- Descriptive test case names
- `t.Fatalf` for assertions (not `t.Errorf` — fail fast)
- Test edge cases: nil inputs, empty slices, boundary values, error paths

Always clean up: `Remove-Item coverage.out -ErrorAction SilentlyContinue`

### `crap` — CRAP Score Analysis

CRAP = Complexity² × (1 - Coverage)³ + Complexity

For each exported and significant unexported function:

1. Get cyclomatic complexity: `gocyclo -over 0 .`
2. Get per-function coverage: `go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out`
3. Calculate CRAP score per function

**CRAP score interpretation:**
- 🟢 ≤5 — clean code, well tested
- 🟡 5-30 — manageable, consider simplifying or adding tests
- 🔴 >30 — high risk: too complex + undertested

Present as table sorted by CRAP score descending:

```
| Function              | Complexity | Coverage | CRAP  | Risk |
|-----------------------|------------|----------|-------|------|
| normalizeCheckState   | 8          | 85%      | 8.0   | 🟡   |
| executeList           | 12         | 0%       | 156   | 🔴   |
```

For any 🔴 function, recommend: simplify first (extract helpers, reduce nesting), then add tests.

Always clean up: `Remove-Item coverage.out -ErrorAction SilentlyContinue`

### `mutate` — Mutation Testing

Mutation testing validates test quality by injecting faults and checking if tests catch them.

**Primary tool**: [gremlins](https://github.com/go-gremlins/gremlins) — the actively maintained Go mutation testing framework used in CI.

```powershell
gremlins unleash --threshold-efficacy 90 ./...
```

**Timeout**: Mutation testing is CPU-intensive. CI allows 30 minutes. Locally, expect similar.

**Understanding results**:
- **Killed**: Test suite detected the mutation (good)
- **Survived**: Mutation wasn't caught — test gap exists
- **Not covered**: Mutated code has no test coverage at all
- **Timed out**: Mutation caused infinite loop (counts as killed)

Report format:

```
## Mutation Testing Results

| Metric          | Value |
|-----------------|-------|
| Total mutations | 120   |
| Killed          | 72    |
| Survived        | 18    |
| Not covered     | 30    |
| Efficacy        | 60%   |
| Threshold       | ≥90%  |
| Status          | ❌    |

### Surviving Mutations (tests need strengthening)
1. `normalizeCheckState`: swapping `hasFail` check order — no test catches this
   → Add test: single FAILURE item should return "fail" even with passing items
```

**Target**: ≥90% mutation efficacy (CI-enforced gate).

#### Architectural Boundary: Untested I/O Functions

The following functions are intentionally excluded from mutation testing coverage.
They are thin wrappers around `gh` CLI calls with no branching logic — the parsing
logic they delegate to IS fully tested. Testing them would require mocking
`exec.Command` through the entire call chain, a significant refactor with poor ROI.

**Excluded functions** (0% coverage, ~53 mutations marked "not covered"):

- `executeAtm`, `fetchAtmNodes`, `fetchAtmSingleSearch`, `fetchAtmMultiSearch`, `resolveCurrentUser` (atm.go)
- `fetchReleases`, `fetchReleaseByTag` (changelog.go)
- `main`, `runList`, `fetchLatestRelease` (main.go)
- `executeList`, `fetchSupplementalData`, `fetchRequiredChecks`, `renderTable`, `resolveRepoLabel`, `fetchRequiredCheckContexts`, `fetchPRSupplemental` (prlist.go)
- `buildMeQueries`, `resolveOwnerQualifier`, `executeMe`, `fetchMeNodes` (me.go)

**When to revisit**: If any of these functions gain retry logic, caching, error
recovery, or conditional branching beyond "call CLI → check error → parse result",
they become testable logic and should be covered.

### `simplify` — Simplification Patterns

Scan for Go-specific simplification opportunities:

1. **`gofmt -s -d .`** — mechanical simplifications (slice expressions, range loops, composite literals)
2. **Complexity reduction** — functions with cyclomatic complexity >10:
   - Extract switch cases into helper functions
   - Replace nested if/else with early returns
   - Use lookup tables (maps) instead of long switch statements
3. **Duplication detection** — look for repeated patterns across functions:
   - Similar switch/case structures
   - Repeated error handling patterns
   - Copy-paste code with minor variations
4. **Interface extraction** — identify groups of methods on the same type that could be interfaces for testability
5. **Dead code** — `deadcode .` to find unreachable functions

For each finding:
- Show the current code
- Show the simplified version
- Explain the improvement (readability, testability, maintainability)
- **Apply the change** if it's safe (preserves behavior)

Run `go vet ./... && go test ./...` after each batch of simplifications to verify nothing broke.

### `harden` — Test Hardening

Improve test quality and robustness:

1. **Analyze existing tests** in `main_test.go`:
   - Are edge cases covered? (nil, empty, zero, negative, max values)
   - Are error paths tested?
   - Do table-driven tests have sufficient case variety?
   - Are assertions checking the right things?

2. **Identify missing test categories**:
   - Functions with 0% coverage
   - Functions tested only for happy path
   - Boundary conditions (off-by-one, empty input, single element)
   - Error injection (malformed JSON, nil pointers, empty strings)

3. **Write hardened tests** following repo conventions:
   - Table-driven with `t.Run`
   - Descriptive case names that explain the scenario
   - `t.Fatalf` for assertions
   - Test both the function output AND side effects
   - Add `t.Helper()` to test helper functions

4. **Verify test independence**:
   - No test should depend on another test's state
   - No test should depend on external services (use test doubles)
   - Tests should be deterministic (inject `time.Now`, avoid random)

After writing tests, run:
```powershell
go test -v -count=1 ./...
go test -race ./...
```

## Quality Gates

These thresholds are enforced in CI (`.github/workflows/ci.yml`). Every gate is a hard blocker — no advisory-only gates, no suppressions allowed.

| Gate | Threshold | CI Job |
|------|-----------|--------|
| `go build` | 0 errors | Build & Test |
| `go vet` | 0 errors | Build & Test |
| `go test -race` | 0 failures, 0 races | Build & Test |
| `gofmt` | 0 unformatted files | Lint |
| `go mod tidy` | no diff | Lint |
| `staticcheck` | 0 findings | Lint |
| Dead code (`deadcode`) | 0 unreachable funcs | Lint |
| `govulncheck` | 0 vulnerabilities | Lint |
| Lint suppressions | 0 `//nolint` etc. | Lint |
| Anti-patterns | 0 `fmt.Print*` in prod code | Lint |
| Markdown lint | 0 errors | Lint |
| Test coverage | ≥70% | Quality Gates |
| Cyclomatic complexity | ≤10 per function | Quality Gates |
| Cognitive complexity | ≤15 per function | Quality Gates |
| CRAP score | <30 per function | Quality Gates |
| Mutation efficacy | ≥90% | Mutation Testing |

All jobs feed into a single **Quality Gate** status check required by branch protection.

## CI Integration

The CI workflow (`.github/workflows/ci.yml`) is the single source of truth for enforcement. This skill document describes the same gates for local use. When running `audit`, `coverage`, `crap`, `mutate`, `simplify`, or `harden`, use the same thresholds and tools as CI.

### Branch Protection (repo ruleset ID: 16258786)

- **Required status check**: "Quality Gate" must pass
- **All review conversations must be resolved** before merge
- **Stale reviews are dismissed** on new pushes

### Copilot Code Review

Copilot code review is enabled via repo settings (no API — manual configuration only):

1. Go to **Settings → Copilot → Code Review**
2. Enable **"Automatically review pull requests"** for all PRs
3. Copilot reviews trigger on PR open and on every push to the PR branch

This is NOT enforced in CI — it's a repo-level setting alongside branch protection.

## Patterns Established in This Repo

Follow these when writing code or tests:

- **Table-driven tests**: Always use `[]struct` + `t.Run` (see `TestFormatRelativeTime`, `TestCountApprovals`)
- **Fail-fast assertions**: Use `t.Fatalf`, not `t.Errorf`
- **Time injection**: Pass `now time.Time` as parameter, don't call `time.Now()` inside testable functions
- **Nil safety**: Check pointer fields before access (see `Author` nil check in `buildDisplayPullRequest`)
- **Error wrapping**: Use `fmt.Errorf("context: %w", err)` for wrapped errors
- **Sentinel errors**: Define as `var errX = errors.New(...)` at package level (see `errHelpDisplayed`)
- **Graceful degradation**: External calls (GraphQL) use best-effort pattern — failures produce fallback values, not crashes
- **IO abstraction**: Functions accept `io.Writer` for testability (see `run`, `executeList`, `renderTableWithStyle`)
- **Flag parsing**: Use `flag.NewFlagSet` with `ContinueOnError` for subcommand flags

## Anti-Patterns to Flag

When running any command, also flag these if found. Items marked **[CI]** are enforced in the CI workflow:

- **[CI]** `fmt.Println` / `fmt.Print` / `fmt.Printf` in production code (use `fmt.Fprintln(w, ...)` with injected writer)
- **[CI]** `//nolint`, `//lint:ignore`, `//nosec`, `#nosec` suppression comments (zero tolerance policy)
- Naked `panic` or `log.Fatal` in non-main functions
- `time.Now()` called directly in testable logic
- Unchecked type assertions
- `interface{}` or `any` without type switches
- Goroutines without synchronization
- `os.Exit` outside of `main()`
- Ignoring returned errors (use `errcheck` if available)
