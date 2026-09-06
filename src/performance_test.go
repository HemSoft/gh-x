package main

import (
	"fmt"
	"io"
	"testing"
)

// BenchmarkCritical uses only synthetic display records and the real local
// rendering/reconciliation functions. Fixture construction is outside B.Loop.
func BenchmarkCritical(b *testing.B) {
	for _, size := range []struct {
		name string
		rows int
	}{{"small", 20}, {"large", 500}} {
		prs, issues, previous, current := performanceRows(size.rows)
		dashboard := statusDashboard{
			Repository: "fixture/project", RepositoryURL: "https://example.invalid/fixture/project",
			DefaultBranch: "main", DefaultCheckedOut: true,
			DefaultStatus: statusSummary{Branch: "main", Upstream: "origin/main"},
			Issues:        issues, PullRequests: prs,
			WorkflowRuns: []displayWorkflowRun{{Status: "pass", Title: "Synthetic CI", Workflow: "CI", Branch: "main", Event: "push", ID: "42", Elapsed: "1m", Age: "2h"}},
		}
		for _, path := range []struct {
			name string
			run  func() error
		}{
			{"PR", func() error { return renderPullRequestRows(io.Discard, prs, false) }},
			{"Issue", func() error { return renderIssueRows(io.Discard, issues, false) }},
			{"Status", func() error { return renderStatus(io.Discard, dashboard, false) }},
			{"Monitor", func() error {
				rows, changes := reconcileMonitorRows(previous, current)
				if len(rows) != size.rows || len(changes) != size.rows/10*3 {
					return fmt.Errorf("unexpected reconciliation result: %d rows, %d changes", len(rows), len(changes))
				}
				return nil
			}},
		} {
			b.Run(path.name+"/"+size.name, func(b *testing.B) {
				b.ReportAllocs()
				// One untimed call warms renderer/runtime state and validates input.
				if err := path.run(); err != nil {
					b.Fatal(err)
				}
				for b.Loop() {
					if err := path.run(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func performanceRows(count int) ([]displayPullRequest, []displayIssue, []monitorRow, []monitorRow) {
	prs := make([]displayPullRequest, count)
	issues := make([]displayIssue, count)
	previous := make([]monitorRow, count)
	current := make([]monitorRow, count)
	states := []string{"open", "closed", "merged", "draft"}
	for i := range count {
		title := fmt.Sprintf("Synthetic change %04d: Unicode café 界 and a representative description", i)
		prs[i] = displayPullRequest{Number: i + 1, Title: title, Author: "fixture-author", State: states[i%4], Review: "approved", AIReview: "pass", Approvals: 1, Checks: "pass", Comments: "2/2", Branch: "feature/synthetic-change", Updated: "2h", URL: "https://example.invalid/pr", Issues: "#42"}
		issues[i] = displayIssue{Number: i + 1, Title: title, Author: "fixture-author", State: "open", Labels: "bug, cli", Assignees: "fixture-owner", Updated: "2h", URL: "https://example.invalid/issue", PullRequests: "#43"}
		previous[i] = monitorRow{Kind: monitorKindPR, Repo: "fixture/project", Number: i + 1, Title: title, State: "open", Checks: "pending"}
		current[i] = previous[i]
	}
	for i := range count / 10 {
		current[i].Number += count          // 10% removed and 10% added.
		current[count/10+i].Checks = "pass" // 10% modified; 80% unchanged.
	}
	return prs, issues, previous, current
}
