package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

func TestStatusInventoryChecksExactCleanState(t *testing.T) {
	for _, test := range []struct {
		name      string
		branches  statusBranchInventory
		worktrees []statusWorktree
		stashes   int
		stashErr  error
		want      [3]bool
	}{
		{name: "clean", branches: statusBranchInventory{LocalCount: 1, RemoteCount: 1}, worktrees: []statusWorktree{{}}, want: [3]bool{true, true, true}},
		{name: "no local branch", branches: statusBranchInventory{RemoteCount: 1}, want: [3]bool{false, false, true}},
		{name: "extra local branch", branches: statusBranchInventory{LocalCount: 2, RemoteCount: 1}, worktrees: []statusWorktree{{}}, want: [3]bool{false, true, true}},
		{name: "missing remote branch", branches: statusBranchInventory{LocalCount: 1}, want: [3]bool{false, false, true}},
		{name: "extra remote branch", branches: statusBranchInventory{LocalCount: 1, RemoteCount: 2}, want: [3]bool{false, false, true}},
		{name: "dangling", branches: statusBranchInventory{LocalCount: 1, RemoteCount: 1, DanglingCount: 1}, want: [3]bool{false, false, true}},
		{name: "extra worktree", worktrees: []statusWorktree{{}, {}}, want: [3]bool{false, false, true}},
		{name: "cleanup candidate", worktrees: []statusWorktree{{CleanupCandidate: true}}, want: [3]bool{false, false, true}},
		{name: "one stash", stashes: 1},
		{name: "multiple stashes", stashes: 2},
		{name: "stash unavailable", stashErr: errors.New("reflog unreadable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, color := range []bool{false, true} {
				var output bytes.Buffer
				styler := newTableStyler(&output, color)
				rows := [][]tableCell{statusBranchInventoryRow(styler, test.branches), statusWorktreeInventoryRow(styler, test.worktrees), statusStashInventoryRow(styler, test.stashes, test.stashErr)}
				for i, row := range rows {
					if got := row[0].text == "✓"; got != test.want[i] {
						t.Fatalf("%s check = %v, want %v", row[1].text, got, test.want[i])
					}
					if test.want[i] && color && row[0].styled != styler.colored("✓", termenv.ANSIGreen).styled {
						t.Fatalf("%s check is not green", row[1].text)
					}
				}
				if test.stashErr != nil && !strings.Contains(rows[2][2].text, "Unavailable: reflog unreadable") {
					t.Fatalf("stash error = %q", rows[2][2].text)
				}
			}
		})
	}
}

func TestStatusDefaultBranchCheck(t *testing.T) {
	for _, test := range []struct {
		name       string
		branch     string
		summary    statusSummary
		checkedOut bool
		err        error
		track      string
		want       bool
	}{
		{name: "clean synced main", branch: "main", summary: statusSummary{Upstream: "origin/main"}, checkedOut: true, want: true},
		{name: "other default branch", branch: "trunk", summary: statusSummary{Upstream: "origin/trunk"}, checkedOut: true, want: true},
		{name: "unknown default branch", summary: statusSummary{Upstream: "origin/main"}, checkedOut: true},
		{name: "not checked out", branch: "main", summary: statusSummary{Upstream: "origin/main"}},
		{name: "unavailable", branch: "main", summary: statusSummary{Upstream: "origin/main"}, checkedOut: true, err: errors.New("status unavailable")},
		{name: "missing upstream", branch: "main", checkedOut: true},
		{name: "gone upstream", branch: "main", summary: statusSummary{Upstream: "origin/main"}, checkedOut: true, track: "[gone]"},
		{name: "ahead", branch: "main", summary: statusSummary{Upstream: "origin/main", Ahead: 1}, checkedOut: true},
		{name: "behind", branch: "main", summary: statusSummary{Upstream: "origin/main", Behind: 1}, checkedOut: true},
		{name: "diverged", branch: "main", summary: statusSummary{Upstream: "origin/main", Ahead: 1, Behind: 1}, checkedOut: true},
		{name: "staged", branch: "main", summary: statusSummary{Upstream: "origin/main", Staged: 1}, checkedOut: true},
		{name: "modified", branch: "main", summary: statusSummary{Upstream: "origin/main", Modified: 1}, checkedOut: true},
		{name: "deleted", branch: "main", summary: statusSummary{Upstream: "origin/main", Deleted: 1}, checkedOut: true},
		{name: "renamed", branch: "main", summary: statusSummary{Upstream: "origin/main", Renamed: 1}, checkedOut: true},
		{name: "untracked", branch: "main", summary: statusSummary{Upstream: "origin/main", Untracked: 1}, checkedOut: true},
		{name: "conflicted", branch: "main", summary: statusSummary{Upstream: "origin/main", Conflicted: 1}, checkedOut: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, color := range []bool{false, true} {
				var output bytes.Buffer
				styler := newTableStyler(&output, color)
				dashboard := statusDashboard{
					DefaultBranch: test.branch, DefaultStatus: test.summary, DefaultCheckedOut: test.checkedOut, DefaultStatusErr: test.err,
					CurrentStatus: statusSummary{Branch: "feature/dirty", Modified: 1, Untracked: 1},
					Branches:      statusBranchInventory{Local: map[string]statusBranchRef{test.branch: {Track: test.track}}},
				}
				row := statusDefaultBranchRow(styler, dashboard)
				if got := row[0].text == "✓"; got != test.want {
					t.Fatalf("default branch check = %v, want %v", got, test.want)
				}
				if test.want && row[0].styled != styler.colored("✓", termenv.ANSIGreen).styled {
					t.Fatalf("check style = %q, want green check", row[0].styled)
				}
				original := statusDefaultBranchCell(styler, dashboard)
				if row[2].text != original.text || row[2].styled != original.styled {
					t.Fatalf("default branch status changed: %#v", row[2])
				}
			}
		})
	}
}

func TestStatusHeaderInventoryAlignment(t *testing.T) {
	for _, color := range []bool{false, true} {
		t.Run(map[bool]string{false: "no color", true: "color"}[color], func(t *testing.T) {
			dashboard := statusDashboard{Repository: "demo/service", DefaultBranch: "main", DefaultStatus: statusSummary{Upstream: "origin/main"}, DefaultCheckedOut: true, CurrentStatus: statusSummary{Branch: "feature/example"}, Branches: statusBranchInventory{LocalCount: 1, RemoteCount: 1}, Worktrees: []statusWorktree{{}}}
			var buf bytes.Buffer
			renderStatusHeader(&buf, newTableStyler(&buf, color), dashboard)
			output := buf.String()
			if !color && strings.Contains(output, "\x1b") {
				t.Fatal("no-color header contains ANSI escapes")
			}
			lines := strings.Split(strings.TrimSpace(stripANSIForTest(output)), "\n")
			labels := []string{"Main", "Branches", "Worktrees", "Stashes", "Current"}
			indices := make([]int, len(labels))
			for i, label := range labels {
				found := false
				for n, line := range lines {
					if at := strings.Index(line, label); at >= 0 {
						indices[i] = n
						found = true
						if runewidth.StringWidth(line[:at]) != 2 {
							t.Fatalf("%s column=%d, want 2: %q", label, at, line)
						}
						break
					}
				}
				if !found {
					t.Fatalf("missing %s in header: %s", label, output)
				}
			}
			for i := 1; i < len(indices); i++ {
				if indices[i] != indices[i-1]+1 {
					t.Fatalf("inventory order=%v", indices)
				}
			}
			for _, n := range indices[:4] {
				line := lines[n]
				if runewidth.StringWidth(line) > 80 {
					t.Fatalf("inventory wraps at 80 columns: %q", line)
				}
			}
			for _, want := range []string{"✓ Main       synced with origin/main · Clean working tree", "✓ Branches   1 local (0 dangling) · 1 remote", "✓ Worktrees  1 total · 0 cleanup candidates", "✓ Stashes    0 stashes"} {
				if !strings.Contains(stripANSIForTest(output), want) {
					t.Fatalf("missing aligned row %q in %s", want, output)
				}
			}
		})
	}
}

func TestFetchStatusStashes(t *testing.T) {
	for _, test := range []struct {
		name, output string
		err          error
		count        int
	}{
		{name: "zero"}, {name: "one", output: strings.Repeat("a", 40) + "\n", count: 1}, {name: "multiple", output: strings.Repeat("a", 40) + "\n" + strings.Repeat("b", 40) + "\r\n", count: 2}, {name: "same commit twice", output: strings.Repeat("a", 40) + "\n" + strings.Repeat("a", 40) + "\n", count: 2}, {name: "unavailable", output: "abc\n", err: errors.New("failed")},
		{name: "SHA-256", output: strings.Repeat("a", 64) + "\n", count: 1},
		{name: "uppercase hex", output: strings.Repeat("B", 40) + "\n", count: 1},
		{name: "blank lines", output: "\n\r\n" + strings.Repeat("a", 40) + "\n\n", count: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer saveStatusFuncs()()
			statusCommandFunc = func(name string, args ...string) (string, error) {
				if name != "git" || strings.Join(args, " ") != "stash list --format=%H" {
					t.Fatalf("unexpected stash command: %s %v", name, args)
				}
				return test.output, test.err
			}
			count, err := fetchStatusStashes()
			if count != test.count || !errors.Is(err, test.err) {
				t.Fatalf("stashes=%d err=%v, want %d err=%v", count, err, test.count, test.err)
			}
		})
	}
}

func TestFetchStatusStashesRejectsUnexpectedOutput(t *testing.T) {
	for _, test := range []struct{ name, output string }{
		{name: "successful command warning", output: "warning: stash reflog is incomplete\n"},
		{name: "warning after valid stash", output: strings.Repeat("a", 40) + "\nwarning: stash reflog is incomplete\n"},
		{name: "warning before valid stash", output: "warning: stash reflog is incomplete\n" + strings.Repeat("a", 40) + "\n"},
		{name: "hash inside warning", output: "warning: missing object " + strings.Repeat("a", 40) + "\n"},
		{name: "short hash", output: strings.Repeat("a", 39) + "\n"},
		{name: "wrong hash size", output: strings.Repeat("a", 41) + "\n"},
		{name: "long hash", output: strings.Repeat("a", 65) + "\n"},
		{name: "nonhex SHA-1", output: strings.Repeat("g", 40) + "\n"},
		{name: "nonhex SHA-256", output: strings.Repeat("g", 64) + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			defer saveStatusFuncs()()
			statusCommandFunc = func(string, ...string) (string, error) { return test.output, nil }
			count, err := fetchStatusStashes()
			if count != 0 || err == nil || !strings.Contains(err.Error(), "unexpected output") {
				t.Fatalf("unexpected output produced count=%d err=%v", count, err)
			}
			var output bytes.Buffer
			row := statusStashInventoryRow(newTableStyler(&output, false), count, err)
			if row[0].text != "" || !strings.HasPrefix(row[2].text, "Unavailable:") || runewidth.StringWidth(row[2].text) > 60 {
				t.Fatalf("unexpected output produced an incorrect stash row: %#v", row)
			}
		})
	}
}

func TestStatusStashesRefreshWithCachedGitHubData(t *testing.T) {
	defer saveStatusFuncs()()
	installStatusDashboardGitFixture()
	directory := t.TempDir()
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	statusNowFunc = func() time.Time { return now }
	statusCacheDirectoryFunc = func() (string, string, error) { return directory, "demo", nil }
	saveStatusCache(statusOptions{mergedLimit: 0}, false, now, statusDashboard{Repository: "demo/service", DefaultBranch: "main"}, nil, true)
	statusIssueListFunc = func(issueListOptions, time.Time) (issueListResult, error) {
		t.Fatal("warm cache fetched issues")
		return issueListResult{}, nil
	}
	statusPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) {
		t.Fatal("warm cache fetched PRs")
		return pullRequestListResult{}, nil
	}
	statusWorkflowRunListFunc = func(runListOptions, time.Time) (workflowRunListResult, error) {
		t.Fatal("warm cache fetched runs")
		return workflowRunListResult{}, nil
	}
	original := statusCommandFunc
	for _, test := range []struct {
		name, output string
		err          error
		count        int
	}{{name: "zero"}, {name: "two", output: strings.Repeat("a", 40) + "\n" + strings.Repeat("b", 40) + "\n", count: 2}, {name: "error", err: errors.New("stash denied")}, {name: "recovered"}} {
		t.Run(test.name, func(t *testing.T) {
			statusCommandFunc = func(name string, args ...string) (string, error) {
				if strings.Join(args, " ") == "stash list --format=%H" {
					return test.output, test.err
				}
				return original(name, args...)
			}
			dashboard, err := fetchStatusDashboard(false, statusOptions{mergedLimit: 0})
			if err != nil {
				t.Fatal(err)
			}
			if dashboard.Repository != "demo/service" || dashboard.Stashes != test.count || !errors.Is(dashboard.StashesErr, test.err) {
				t.Fatalf("cached dashboard=%#v", dashboard)
			}
		})
	}
}

func TestStatusStashesSharedWithLinkedWorktrees(t *testing.T) {
	defer saveStatusFuncs()()
	root := t.TempDir()
	main := filepath.Join(root, "main")
	linked := filepath.Join(root, "linked")
	if err := os.Mkdir(main, 0o700); err != nil {
		t.Fatal(err)
	}
	git := func(path string, args ...string) string {
		t.Helper()
		out, err := runStatusCommand("git", append([]string{"-C", path}, args...)...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	git(main, "init", "-q", "-b", "main")
	git(main, "config", "user.name", "Demo")
	git(main, "config", "user.email", "demo@example.test")
	file := filepath.Join(main, "file.txt")
	if err := os.WriteFile(file, []byte("initial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(main, "add", "file.txt")
	git(main, "commit", "-qm", "Initial")
	git(main, "worktree", "add", "-q", "-b", "example", linked)
	for _, value := range []string{"one\n", "two\n"} {
		if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
		git(main, "stash", "push", "-q", "-m", "demo stash\nwith a multiline subject")
	}
	before := git(main, "stash", "list", "--format=%H")
	for _, path := range []string{main, linked} {
		statusCommandFunc = func(name string, args ...string) (string, error) {
			return runStatusCommand(name, append([]string{"-C", path}, args...)...)
		}
		count, err := fetchStatusStashes()
		if err != nil || count != 2 {
			t.Fatalf("stash count from %s=%d, err=%v", path, count, err)
		}
	}
	if after := git(main, "stash", "list", "--format=%H"); after != before {
		t.Fatal("stash inspection changed the inventory")
	}
	git(linked, "stash", "drop", "-q")
	count, err := fetchStatusStashes()
	if err != nil || count != 1 {
		t.Fatalf("after drop, stashes=%d err=%v", count, err)
	}
}
