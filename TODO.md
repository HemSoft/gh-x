# Project TODO

| Status | Priority | Task | Notes |
|--------|----------|------|-------|
| ✅ | High | Fix staticcheck finding | S1017 fix |
| ✅ | High | Remove dead code | Deleted unreachable func |
| ✅ | High | Raise test coverage to 70% | 56.2% → 77.8% |
| ✅ | High | Reduce `executeList` complexity | 20 → 4 cyclomatic |
| ✅ | Medium | Reduce complexity across codebase | All funcs ≤10 cyclomatic |
| ✅ | High | CI quality gate workflow | 6 jobs, 16 gates |
| ✅ | High | Initialize public GitHub repository | `HemSoft/gh-x` |
| ✅ | High | Scaffold Go extension for `gh x list` | Initial structure |
| ✅ | High | Install Go and validate scaffold | Go 1.26, all tests pass |
| ✅ | High | Verify `gh x list` end-to-end | Tested against live repos |
| ✅ | Medium | Add tests for `list` | Formatting, normalization |
| ✅ | Medium | Document install and usage flows | README rewritten |
| ✅ | Low | Prepare release packaging | Auto-release + 12 platforms |
| ✅ | High | Design `gh x atm` | Multi-search GraphQL |
| ✅ | High | Implement `gh x atm` | Full impl in `atm.go` |
| ✅ | Medium | Add tests for `atm` | 591 lines in `atm_test.go` |
| ✅ | Medium | CRAP score analysis | All functions < 30 CRAP |
| ✅ | Medium | Mutation testing baseline | Gremlins configured in CI |
| ✅ | Low | Markdown lint cleanup | Config + fixes applied |

## Progress

**Completed: 19 / 19** (100%)

## Notes

- Renamed from `gh-extensions` to `gh-x` for correct
  extension command resolution.
- Wraps GitHub CLI for `gh` auth and repo context.
- Auto-release creates a new patch on every `main` push.
- CI quality gates: build, vet, staticcheck, deadcode,
  govulncheck, gofmt, mod tidy, lint suppressions,
  anti-patterns, coverage ≥70%, cyclomatic ≤10,
  cognitive ≤15, CRAP <30, mutation ≥90%, markdown lint.
- Quality tools: `staticcheck`, `gocyclo`, `gocognit`,
  `deadcode`, `govulncheck`, `gremlins`.
  Run `perfection audit` for a full scorecard.
