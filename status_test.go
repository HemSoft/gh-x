package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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
	if got.Branch != "main" || got.Upstream != "origin/main" {
		t.Fatalf("unexpected branch status: %#v", got)
	}
	if got.Ahead != 0 || got.Behind != 0 {
		t.Fatalf("expected even branch, got ahead=%d behind=%d", got.Ahead, got.Behind)
	}
	if got.Modified != 3 || got.Untracked != 1 {
		t.Fatalf("unexpected change counts: %#v", got)
	}
}

func TestChangeStatusText(t *testing.T) {
	got := changeStatusText(statusSummary{Modified: 3, Untracked: 1})
	if want := "3 modified files, 1 untracked file."; got != want {
		t.Fatalf("changeStatusText() = %q, want %q", got, want)
	}
	if got := changeStatusText(statusSummary{}); got != "Clean working tree." {
		t.Fatalf("expected clean working tree, got %q", got)
	}
}

func TestParseStatusBranchRefs(t *testing.T) {
	output := strings.Join([]string{
		"refs/heads/main\tmain\torigin/main\t\t\tC:/repo",
		"refs/heads/old\told\torigin/old\t[gone]\t\t",
		"refs/remotes/origin/HEAD\torigin\t\t\trefs/remotes/origin/main\t",
		"refs/remotes/origin/main\torigin/main\t\t\t\t",
	}, "\n") + "\n"

	got := parseStatusBranchRefs(output)
	if got.LocalCount != 2 || got.RemoteCount != 1 || got.DanglingCount != 1 {
		t.Fatalf("unexpected branch inventory: %#v", got)
	}
	if got.Local["main"].WorktreePath != "C:/repo" {
		t.Fatalf("expected main worktree path, got %#v", got.Local["main"])
	}
	if defaultBranch := resolveStatusDefaultBranch(got); defaultBranch != "main" {
		t.Fatalf("default branch = %q, want main", defaultBranch)
	}
}

func TestParseStatusWorktrees(t *testing.T) {
	output := strings.Join([]string{
		"worktree C:/repo", "HEAD abc", "branch refs/heads/main", "",
		"worktree C:/repo worktrees/locked", "HEAD def", "branch refs/heads/locked", "locked maintenance", "",
		"worktree C:/missing", "HEAD fed", "detached", "prunable gitdir file points to non-existent location", "",
	}, "\x00")

	got := parseStatusWorktrees(output)
	if len(got) != 3 {
		t.Fatalf("worktree count = %d, want 3: %#v", len(got), got)
	}
	if !got[0].Primary || got[0].Branch != "main" {
		t.Fatalf("unexpected primary worktree: %#v", got[0])
	}
	if !got[1].Locked || got[1].Path != "C:/repo worktrees/locked" {
		t.Fatalf("unexpected locked worktree: %#v", got[1])
	}
	if !got[2].Prunable || !got[2].Detached || got[2].Head != "fed" || !strings.Contains(got[2].PrunableReason, "non-existent") {
		t.Fatalf("unexpected prunable worktree: %#v", got[2])
	}
}

func TestStatusWorktreeCandidateReason(t *testing.T) {
	base := statusWorktree{Path: "C:/wt", Branch: "feature", Exists: true, CleanKnown: true, Clean: true}
	merged := map[string]bool{"feature": true}
	tests := []struct {
		name        string
		mutate      func(*statusWorktree)
		openHeads   map[string]bool
		mergedKnown bool
		prsKnown    bool
		want        bool
	}{
		{name: "clean merged no open PR", mergedKnown: true, prsKnown: true, want: true},
		{name: "current worktree", mutate: func(w *statusWorktree) { w.Current = true }, mergedKnown: true, prsKnown: true},
		{name: "primary worktree", mutate: func(w *statusWorktree) { w.Primary = true }, mergedKnown: true, prsKnown: true},
		{name: "locked", mutate: func(w *statusWorktree) { w.Locked = true }, mergedKnown: true, prsKnown: true},
		{name: "default branch", mutate: func(w *statusWorktree) { w.Branch = "main" }, mergedKnown: true, prsKnown: true},
		{name: "dirty", mutate: func(w *statusWorktree) { w.Clean = false }, mergedKnown: true, prsKnown: true},
		{name: "unmerged", mergedKnown: false, prsKnown: true},
		{name: "open PR", openHeads: map[string]bool{"feature": true}, mergedKnown: true, prsKnown: true},
		{name: "PR state unavailable", mergedKnown: true, prsKnown: false},
		{name: "Git prunable", mutate: func(w *statusWorktree) { w.Prunable = true; w.Exists = false }, mergedKnown: true, prsKnown: true, want: true},
		{name: "unmerged Git prunable", mutate: func(w *statusWorktree) { w.Prunable = true; w.Exists = false }, prsKnown: true},
		{name: "Git prunable with open PR", mutate: func(w *statusWorktree) { w.Prunable = true; w.Exists = false }, openHeads: map[string]bool{"feature": true}, mergedKnown: true, prsKnown: true},
		{name: "Git prunable with PR state unavailable", mutate: func(w *statusWorktree) { w.Prunable = true; w.Exists = false }, mergedKnown: true},
		{name: "locked Git prunable", mutate: func(w *statusWorktree) { w.Prunable = true; w.Locked = true }},
		{name: "merged detached Git prunable", mutate: func(w *statusWorktree) { w.Prunable = true; w.Detached = true; w.DetachedMerged = true; w.Branch = "" }, want: true},
		{name: "unmerged detached Git prunable", mutate: func(w *statusWorktree) { w.Prunable = true; w.Detached = true; w.Branch = "" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			worktree := base
			if tc.mutate != nil {
				tc.mutate(&worktree)
			}
			reason := statusWorktreeCandidateReason(worktree, "main", merged, tc.openHeads, tc.mergedKnown, tc.prsKnown)
			if got := reason != ""; got != tc.want {
				t.Fatalf("candidate = %v (%q), want %v", got, reason, tc.want)
			}
		})
	}
}

func TestAssessDetachedWorktreeMerge(t *testing.T) {
	defer saveStatusFuncs()()
	statusCommandFunc = func(name string, args ...string) (string, error) {
		if got := name + " " + strings.Join(args, " "); got != "git merge-base --is-ancestor abc refs/heads/main" {
			return "", fmt.Errorf("unexpected command: %s", got)
		}
		return "", nil
	}

	worktree := statusWorktree{Head: "abc", Detached: true, Prunable: true}
	assessDetachedWorktreeMerge(&worktree, "main")
	if !worktree.DetachedMerged {
		t.Fatal("expected detached HEAD merged into main")
	}
}

func TestFetchDefaultBranchStatusNotCheckedOut(t *testing.T) {
	defer saveStatusFuncs()()
	statusCommandFunc = func(name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		if key != "git rev-list --left-right --count trunk...origin/trunk" {
			return "", fmt.Errorf("unexpected command: %s", key)
		}
		return "2\t1\n", nil
	}
	inventory := statusBranchInventory{Local: map[string]statusBranchRef{
		"trunk": {ShortName: "trunk", Upstream: "origin/trunk"},
	}}

	got, checkedOut, err := fetchDefaultBranchStatus("trunk", inventory)
	if err != nil {
		t.Fatal(err)
	}
	if checkedOut || got.Ahead != 2 || got.Behind != 1 {
		t.Fatalf("unexpected branch status: checkedOut=%v summary=%#v", checkedOut, got)
	}
}

func TestStatusDefaultBranchStates(t *testing.T) {
	styler := newTableStyler(&bytes.Buffer{}, false)
	tests := []struct {
		name      string
		dashboard statusDashboard
		want      string
	}{
		{name: "clean and synchronized", dashboard: statusDashboard{DefaultStatus: statusSummary{Upstream: "origin/main"}, DefaultCheckedOut: true}, want: "synced with origin/main · Clean working tree"},
		{name: "ahead", dashboard: statusDashboard{DefaultStatus: statusSummary{Upstream: "origin/main", Ahead: 2}}, want: "ahead of origin/main by 2 commits · not checked out"},
		{name: "behind", dashboard: statusDashboard{DefaultStatus: statusSummary{Upstream: "origin/main", Behind: 1}}, want: "behind origin/main by 1 commit · not checked out"},
		{name: "diverged", dashboard: statusDashboard{DefaultStatus: statusSummary{Upstream: "origin/main", Ahead: 1, Behind: 3}}, want: "diverged from origin/main: 1 ahead, 3 behind · not checked out"},
		{name: "missing upstream", dashboard: statusDashboard{DefaultStatus: statusSummary{Branch: "trunk"}}, want: "no upstream configured · not checked out"},
		{name: "unavailable", dashboard: statusDashboard{DefaultStatusErr: errors.New("default unavailable")}, want: "default unavailable"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusDefaultBranchCell(styler, tc.dashboard).text; got != tc.want {
				t.Fatalf("default branch cell = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFetchStatusDashboard(t *testing.T) {
	defer saveStatusFuncs()()
	statusRepoLabelFunc = func(string) string { return "owner/repo" }
	statusPathExistsFunc = func(string) bool { return true }
	statusNowFunc = func() time.Time { return time.Date(2026, 8, 12, 16, 0, 0, 0, time.UTC) }
	statusIssueListFunc = func(options issueListOptions, _ time.Time) ([]displayIssue, error) {
		if options.limit != statusListLimit || options.state != "open" {
			t.Fatalf("unexpected issue options: %#v", options)
		}
		return []displayIssue{{Number: 7, Title: "Status dashboard", State: "open"}}, nil
	}
	statusPullRequestListFunc = func(options listOptions, _ time.Time) (pullRequestListResult, error) {
		if options.limit != statusListLimit || options.state != "open" {
			t.Fatalf("unexpected PR options: %#v", options)
		}
		return pullRequestListResult{
			Entries:  []pullRequest{{HeadRefName: "open-branch"}},
			Rendered: []displayPullRequest{{Number: 2, Title: "Open PR", State: "open"}},
		}, nil
	}

	branchOutput := strings.Join([]string{
		"refs/heads/feature/status\tfeature/status\torigin/main\t\t\tC:/repo.worktrees/issue-7",
		"refs/heads/main\tmain\torigin/main\t\t\tC:/repo",
		"refs/heads/old\told\torigin/old\t[gone]\t\tC:/repo.worktrees/old",
		"refs/remotes/origin/HEAD\torigin\t\t\trefs/remotes/origin/main\t",
		"refs/remotes/origin/main\torigin/main\t\t\t\t",
	}, "\n") + "\n"
	worktreeOutput := strings.Join([]string{
		"worktree C:/repo", "HEAD a", "branch refs/heads/main", "",
		"worktree C:/repo.worktrees/issue-7", "HEAD b", "branch refs/heads/feature/status", "",
		"worktree C:/repo.worktrees/old", "HEAD c", "branch refs/heads/old", "",
	}, "\x00")
	statusCommandFunc = func(name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "git status --porcelain=v2 --branch":
			return "# branch.head feature/status\n# branch.upstream origin/main\n# branch.ab +0 -0\n", nil
		case "git for-each-ref --format=" + statusBranchFormat + " refs/heads refs/remotes":
			return branchOutput, nil
		case "git worktree list --porcelain -z":
			return worktreeOutput, nil
		case "git rev-parse --show-toplevel":
			return "C:/repo.worktrees/issue-7\n", nil
		case "git -C C:/repo status --porcelain=v2 --branch":
			return "# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n", nil
		case "git for-each-ref --merged=refs/heads/main --format=%(refname:short) refs/heads":
			return "main\nfeature/status\nold\n", nil
		case "git -C C:/repo.worktrees/old status --porcelain":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected command: %s", key)
		}
	}

	got, err := fetchStatusDashboard()
	if err != nil {
		t.Fatal(err)
	}
	if got.Repository != "owner/repo" || got.DefaultBranch != "main" || !got.DefaultCheckedOut {
		t.Fatalf("unexpected dashboard identity: %#v", got)
	}
	if got.Branches.LocalCount != 3 || got.Branches.RemoteCount != 1 || got.Branches.DanglingCount != 1 {
		t.Fatalf("unexpected branch inventory: %#v", got.Branches)
	}
	if got.CurrentStatus.Branch != "feature/status" || len(got.Issues) != 1 || len(got.PullRequests) != 1 {
		t.Fatalf("unexpected dashboard data: %#v", got)
	}
	if statusCleanupCandidateCount(got.Worktrees) != 1 || !got.Worktrees[2].CleanupCandidate {
		t.Fatalf("expected only old worktree to be a cleanup candidate: %#v", got.Worktrees)
	}
	if got.Worktrees[1].CleanupCandidate {
		t.Fatal("current feature worktree must never be a cleanup candidate")
	}
}

func TestFetchStatusDashboardTreatsLimitedPRRowsAsIncomplete(t *testing.T) {
	defer saveStatusFuncs()()
	statusRepoLabelFunc = func(string) string { return "owner/repo" }
	statusIssueListFunc = func(issueListOptions, time.Time) ([]displayIssue, error) { return nil, nil }
	statusPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) {
		return pullRequestListResult{Entries: make([]pullRequest, statusListLimit)}, nil
	}
	statusPathExistsFunc = func(string) bool { return true }
	statusCommandFunc = func(name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		switch key {
		case "git status --porcelain=v2 --branch":
			return "# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n", nil
		case "git for-each-ref --format=" + statusBranchFormat + " refs/heads refs/remotes":
			return "refs/heads/main\tmain\torigin/main\t\t\tC:/repo\nrefs/heads/old\told\torigin/old\t\t\tC:/old\nrefs/remotes/origin/HEAD\torigin\t\t\trefs/remotes/origin/main\t\n", nil
		case "git worktree list --porcelain -z":
			return strings.Join([]string{"worktree C:/repo", "HEAD a", "branch refs/heads/main", "", "worktree C:/old", "HEAD b", "branch refs/heads/old", ""}, "\x00"), nil
		case "git rev-parse --show-toplevel":
			return "C:/repo\n", nil
		case "git -C C:/repo status --porcelain=v2 --branch":
			return "# branch.head main\n# branch.upstream origin/main\n# branch.ab +0 -0\n", nil
		case "git for-each-ref --merged=refs/heads/main --format=%(refname:short) refs/heads":
			return "main\nold\n", nil
		case "git -C C:/old status --porcelain":
			return "", nil
		default:
			return "", fmt.Errorf("unexpected command: %s", key)
		}
	}

	dashboard, err := fetchStatusDashboard()
	if err != nil {
		t.Fatal(err)
	}
	if statusCleanupCandidateCount(dashboard.Worktrees) != 0 {
		t.Fatal("limited PR rows must suppress cleanup candidates")
	}
}

func TestRenderStatusNoColor(t *testing.T) {
	dashboard := sampleStatusDashboard()
	var buf bytes.Buffer
	if err := renderStatus(&buf, dashboard, false); err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	for _, want := range []string{
		"Repository", "owner/repo", "Main", "synced with origin/main", "Current", "feature/status",
		"3 local (1 dangling) · 2 remote", "3 total · 1 cleanup candidate",
		"old — C:/repo.worktrees/old", "Open issues (1)", "#7", "Open pull requests (1)", "#2",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected %q in output:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\x1b") || strings.Contains(output, "copied") {
		t.Fatalf("expected plain output without clipboard side effects:\n%s", output)
	}
}

func TestRenderStatusColorHasLinksButNoClipboard(t *testing.T) {
	dashboard := sampleStatusDashboard()
	var buf bytes.Buffer
	if err := renderStatus(&buf, dashboard, true); err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "\x1b]8;;https://github.com/owner/repo/issues/7") {
		t.Fatal("expected clickable issue link")
	}
	if strings.Contains(output, "\x1b]52;") || strings.Contains(output, "copied") {
		t.Fatal("status must not copy embedded table commands to the clipboard")
	}
}

func TestRenderStatusEmptyAndUnavailableSections(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		var buf bytes.Buffer
		if err := renderStatus(&buf, statusDashboard{}, false); err != nil {
			t.Fatal(err)
		}
		output := buf.String()
		if !strings.Contains(output, "No open issues.") || !strings.Contains(output, "No open pull requests.") {
			t.Fatalf("missing empty states:\n%s", output)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		var buf bytes.Buffer
		dashboard := statusDashboard{IssuesErr: errors.New("offline\nextra"), PullRequestsErr: errors.New("unauthorized")}
		if err := renderStatus(&buf, dashboard, false); err != nil {
			t.Fatal(err)
		}
		output := buf.String()
		if !strings.Contains(output, "Unavailable: offline extra") || !strings.Contains(output, "Unavailable: unauthorized") {
			t.Fatalf("missing unavailable states:\n%s", output)
		}
	})
}

func TestRunStatusUsesFetcher(t *testing.T) {
	defer saveStatusFuncs()()
	fetchStatusDashboardFunc = func() (statusDashboard, error) {
		return statusDashboard{Repository: "owner/repo"}, nil
	}

	var stdout, stderr bytes.Buffer
	_, err := run([]string{"status"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "owner/repo") || !strings.Contains(stdout.String(), "No open issues.") {
		t.Fatalf("unexpected status output: %q", stdout.String())
	}
}

func TestRunStatusHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"status", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"gh x status", "branches", "worktrees", "open issues", "open pull requests"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("expected %q in status usage, got %q", want, stderr.String())
		}
	}
}

func sampleStatusDashboard() statusDashboard {
	return statusDashboard{
		Repository:        "owner/repo",
		DefaultBranch:     "main",
		DefaultStatus:     statusSummary{Branch: "main", Upstream: "origin/main"},
		DefaultCheckedOut: true,
		CurrentStatus:     statusSummary{Branch: "feature/status", Upstream: "origin/main", Modified: 1},
		Branches:          statusBranchInventory{LocalCount: 3, RemoteCount: 2, DanglingCount: 1},
		Worktrees: []statusWorktree{
			{Path: "C:/repo", Branch: "main", Primary: true},
			{Path: "C:/repo.worktrees/current", Branch: "feature/status", Current: true},
			{Path: "C:/repo.worktrees/old", Branch: "old", CleanupCandidate: true, CleanupReason: "clean, merged branch with no open PR"},
		},
		Issues:       []displayIssue{{Number: 7, Title: "Status dashboard", Author: "alice", State: "open", Updated: "1m", URL: "https://github.com/owner/repo/issues/7"}},
		PullRequests: []displayPullRequest{{Number: 2, Title: "Open PR", Author: "bob", State: "open", Review: "required", SFLReview: "-", AIReview: "-", Checks: "pending", Comments: "-", Branch: "feature", Updated: "2m", URL: "https://github.com/owner/repo/pull/2"}},
	}
}

func saveStatusFuncs() func() {
	savedDashboard := fetchStatusDashboardFunc
	savedCommand := statusCommandFunc
	savedIssues := statusIssueListFunc
	savedPullRequests := statusPullRequestListFunc
	savedRepoLabel := statusRepoLabelFunc
	savedNow := statusNowFunc
	savedPathExists := statusPathExistsFunc
	return func() {
		fetchStatusDashboardFunc = savedDashboard
		statusCommandFunc = savedCommand
		statusIssueListFunc = savedIssues
		statusPullRequestListFunc = savedPullRequests
		statusRepoLabelFunc = savedRepoLabel
		statusNowFunc = savedNow
		statusPathExistsFunc = savedPathExists
	}
}
