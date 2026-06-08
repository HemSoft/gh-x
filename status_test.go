package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestParseGitStatus(t *testing.T) {
	output := `# branch.oid abc123
# branch.head main
# branch.upstream origin/main
# branch.ab +0 -0
1 .M N... 100644 100644 100644 abc abc CHANGELOG.md
1 .M N... 100644 100644 100644 abc abc workflow.go
1 .M N... 100644 100644 100644 abc abc workflow_test.go
? status.go
`

	got := parseGitStatus(output)

	if got.Branch != "main" {
		t.Fatalf("expected branch main, got %q", got.Branch)
	}
	if got.Upstream != "origin/main" {
		t.Fatalf("expected upstream origin/main, got %q", got.Upstream)
	}
	if got.Ahead != 0 || got.Behind != 0 {
		t.Fatalf("expected even branch, got ahead=%d behind=%d", got.Ahead, got.Behind)
	}
	if got.Modified != 3 {
		t.Fatalf("expected 3 modified files, got %d", got.Modified)
	}
	if got.Untracked != 1 {
		t.Fatalf("expected 1 untracked file, got %d", got.Untracked)
	}
}

func TestBranchStatusText(t *testing.T) {
	tests := []struct {
		name    string
		summary statusSummary
		want    string
	}{
		{
			name:    "up to date",
			summary: statusSummary{Upstream: "origin/main"},
			want:    "Up to date with origin/main.",
		},
		{
			name:    "ahead",
			summary: statusSummary{Upstream: "origin/main", Ahead: 2},
			want:    "Ahead of origin/main by 2 commits.",
		},
		{
			name:    "behind",
			summary: statusSummary{Upstream: "origin/main", Behind: 1},
			want:    "Behind origin/main by 1 commit.",
		},
		{
			name:    "diverged",
			summary: statusSummary{Upstream: "origin/main", Ahead: 1, Behind: 3},
			want:    "Diverged from origin/main: 1 ahead, 3 behind.",
		},
		{
			name:    "no upstream",
			summary: statusSummary{Branch: "feature/status"},
			want:    "On feature/status. No upstream configured.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := branchStatusText(tc.summary); got != tc.want {
				t.Fatalf("branchStatusText() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChangeStatusText(t *testing.T) {
	got := changeStatusText(statusSummary{
		Modified:  3,
		Untracked: 1,
	})
	want := "3 modified files, 1 untracked file."
	if got != want {
		t.Fatalf("changeStatusText() = %q, want %q", got, want)
	}

	if got := changeStatusText(statusSummary{}); got != "Clean working tree." {
		t.Fatalf("expected clean working tree, got %q", got)
	}
}

func TestRenderStatusNoColor(t *testing.T) {
	issues := 4
	prs := 2
	summary := statusSummary{
		Upstream:         "origin/main",
		Modified:         3,
		DanglingBranches: 1,
		OpenIssues:       &issues,
		OpenPRs:          &prs,
	}

	var buf bytes.Buffer
	if err := renderStatus(&buf, summary, false); err != nil {
		t.Fatal(err)
	}

	output := buf.String()
	for _, want := range []string{
		"Up to date with origin/main.",
		"3 modified files.",
		"Branches: 1 dangling",
		"Issues: 4 open",
		"PRs: 2 open",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in output:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\x1b") {
		t.Fatal("expected no ANSI escape sequences")
	}
}

func TestFetchStatusSummary(t *testing.T) {
	defer saveStatusFuncs()()

	statusCommandFunc = func(name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "git status --porcelain=v2 --branch":
			return "# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n1 .M N... 100644 100644 100644 abc abc workflow.go\n", nil
		case "git for-each-ref --format=%(refname:short)|%(upstream:track) refs/heads":
			return "main|\nold-feature|[gone]\n", nil
		case "gh issue list --state open --limit 1000 --json number --jq length":
			return "5\n", nil
		case "gh pr list --state open --limit 1000 --json number --jq length":
			return "2\n", nil
		default:
			return "", fmt.Errorf("unexpected command: %s", key)
		}
	}

	got, err := fetchStatusSummary()
	if err != nil {
		t.Fatal(err)
	}

	if got.Modified != 1 {
		t.Fatalf("expected 1 modified file, got %d", got.Modified)
	}
	if got.DanglingBranches != 1 {
		t.Fatalf("expected 1 dangling branch, got %d", got.DanglingBranches)
	}
	if got.OpenIssues == nil || *got.OpenIssues != 5 {
		t.Fatalf("expected 5 open issues, got %#v", got.OpenIssues)
	}
	if got.OpenPRs == nil || *got.OpenPRs != 2 {
		t.Fatalf("expected 2 open PRs, got %#v", got.OpenPRs)
	}
}

func TestFetchStatusSummaryKeepsGitHubCountsOptional(t *testing.T) {
	defer saveStatusFuncs()()

	statusCommandFunc = func(name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "git status --porcelain=v2 --branch":
			return "# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n", nil
		case "git for-each-ref --format=%(refname:short)|%(upstream:track) refs/heads":
			return "", nil
		default:
			return "", fmt.Errorf("offline")
		}
	}

	got, err := fetchStatusSummary()
	if err != nil {
		t.Fatal(err)
	}

	if got.OpenIssues != nil {
		t.Fatalf("expected unavailable issue count, got %#v", got.OpenIssues)
	}
	if got.OpenPRs != nil {
		t.Fatalf("expected unavailable PR count, got %#v", got.OpenPRs)
	}
}

func TestRunStatusUsesFetcher(t *testing.T) {
	defer saveStatusFuncs()()

	issues := 0
	prs := 1
	fetchStatusSummaryFunc = func() (statusSummary, error) {
		return statusSummary{
			Upstream:         "origin/main",
			DanglingBranches: 0,
			OpenIssues:       &issues,
			OpenPRs:          &prs,
		}, nil
	}

	var stdout, stderr bytes.Buffer
	_, err := run([]string{"status"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Up to date with origin/main.") {
		t.Fatalf("expected status output, got %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Clean working tree.") {
		t.Fatalf("expected clean status, got %q", stdout.String())
	}
}

func TestRunStatusHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"status", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "gh x status") {
		t.Fatalf("expected status usage, got %q", stderr.String())
	}
}

func saveStatusFuncs() func() {
	savedFetch := fetchStatusSummaryFunc
	savedCommand := statusCommandFunc
	return func() {
		fetchStatusSummaryFunc = savedFetch
		statusCommandFunc = savedCommand
	}
}
