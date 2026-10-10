package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func testStatusProgress(t *testing.T, width, height int, color bool) (*statusProgress, *bytes.Buffer) {
	t.Helper()
	var output bytes.Buffer
	p := &statusProgress{output: &output, width: width, height: height, color: color}
	if err := p.start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := p.close(); err != nil {
			t.Fatal(err)
		}
	})
	return p, &output
}

func TestStatusProgressBeforeBlockedFetches(t *testing.T) {
	defer saveStatusFuncs()()
	installStatusDashboardGitFixture()
	statusRepoLabelFunc = func(string) string { return "owner/repo" }
	p, _ := testStatusProgress(t, 120, 40, false)
	issuesStarted, releaseIssues := make(chan struct{}), make(chan struct{})
	runsStarted, releaseRuns := make(chan struct{}), make(chan struct{})
	statusIssueListFunc = func(issueListOptions, time.Time) (issueListResult, error) {
		close(issuesStarted)
		<-releaseIssues
		return issueListResult{Display: []displayIssue{{Number: 7, Title: "Ready issue", State: "open"}}}, nil
	}
	statusPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) {
		return pullRequestListResult{}, errors.New("fixture PR failure")
	}
	statusWorkflowRunListFunc = func(runListOptions, time.Time) (workflowRunListResult, error) {
		close(runsStarted)
		<-releaseRuns
		return workflowRunListResult{}, nil
	}
	done := make(chan struct{})
	var dashboard statusDashboard
	var fetchErr error
	go func() {
		defer close(done)
		dashboard, fetchErr = fetchStatusDashboard(false, statusOptions{refresh: true, mergedLimit: 5, progress: p})
	}()
	// Join every failure path before restoring the global seams.
	defer func() {
		select {
		case <-releaseIssues:
		default:
			close(releaseIssues)
		}
		select {
		case <-releaseRuns:
		default:
			close(releaseRuns)
		}
		<-done
	}()
	waitStatusProgressSignal(t, issuesStarted)
	p.mu.Lock()
	frame, active := p.frame, p.active
	p.mu.Unlock()
	if !strings.Contains(frame, "synced with origin/main") || !strings.Contains(frame, "cleanup pending") || !strings.Contains(frame, "0 stashes") || active != "Loading open issues" {
		t.Fatalf("local results missing before blocked fetch: %q, %q", frame, active)
	}
	if strings.Contains(frame, "✓ Worktrees") || strings.Contains(frame, "No open issues") {
		t.Fatalf("pending data shown healthy or empty: %q", frame)
	}
	close(releaseIssues)
	waitStatusProgressSignal(t, runsStarted)
	p.mu.Lock()
	frame, active = p.frame, p.active
	p.mu.Unlock()
	if !strings.Contains(frame, "Ready issue") || !strings.Contains(frame, "fixture PR failure") || active != "Loading workflow runs" {
		t.Fatalf("completed sections missing: %q, %q", frame, active)
	}
	if strings.Index(frame, "Open issues") > strings.Index(frame, "Open pull requests") {
		t.Fatalf("section order changed: %q", frame)
	}
	close(releaseRuns)
	<-done
	if fetchErr != nil || dashboard.WorktreesPending || statusCleanupCandidateCount(dashboard.Worktrees) != 0 {
		t.Fatalf("cleanup not conservative: %+v, %v", dashboard, fetchErr)
	}
}

func waitStatusProgressSignal(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("controlled fetch blocker not reached")
	}
}

func TestStatusProgressAnimationAndRestoration(t *testing.T) {
	for _, color := range []bool{false, true} {
		t.Run(fmt.Sprint(color), func(t *testing.T) {
			p, output := testStatusProgress(t, 80, 24, color)
			p.begin("Loading open pull requests")
			p.tick()
			p.tick()
			if err := p.close(); err != nil {
				t.Fatal(err)
			}
			text := output.String()
			for _, want := range []string{"\x1b[?25l", "| Loading open pull requests", "/ Loading open pull requests", "- Loading open pull requests", "\x1b[?25h"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %q", want, text)
				}
			}
			if strings.Contains(text, "\x1b[36m") != color || !strings.HasSuffix(text, "\r\x1b[J\x1b[?25h") {
				t.Fatalf("unexpected color or cleanup: %q", text)
			}
		})
	}
}

func TestStatusProgressBoundedViewport(t *testing.T) {
	tests := []struct {
		name, frame        string
		width, limit, want int
	}{
		{"empty", "", 80, 22, 0}, {"short", "header\nlast\n", 80, 22, 2},
		{"long", "header\n" + strings.Repeat("earlier\n", 40) + "last\n", 80, 22, 22},
		{"tiny", "one\ntwo\nthree\nfour\n", 20, 2, 2},
		{"wide colored", "\x1b[32m" + strings.Repeat("界", 50) + "\x1b[0m\n", 80, 22, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lines := statusProgressLines(tc.frame, tc.width, tc.limit)
			if len(lines) != tc.want {
				t.Fatalf("lines=%d, want %d: %q", len(lines), tc.want, lines)
			}
			for _, line := range lines {
				if ansi.StringWidth(line) >= tc.width {
					t.Fatalf("line wraps: %q", line)
				}
			}
			if tc.name == "long" && (!strings.Contains(strings.Join(lines, "\n"), "earlier lines") || lines[0] != "header" || lines[len(lines)-1] != "last") {
				t.Fatalf("preview lost header, notice or latest result: %q", lines)
			}
		})
	}
}

func TestStatusProgressCacheReadiness(t *testing.T) {
	p, _ := testStatusProgress(t, 120, 40, false)
	now := time.Now()
	dashboard := statusDashboard{Repository: "cached/repo", remoteSections: []statusCacheSection{
		{RetryAfter: now.Add(time.Second)}, {RetryAfter: now}, {RetryAfter: now.Add(-time.Second)}, {RetryAfter: now.Add(time.Minute)},
	}}
	p.cached(&dashboard, now)
	if p.ready != [4]bool{true, false, false, true} {
		t.Fatalf("expired sections ready: %v", p.ready)
	}
	p.mu.Lock()
	frame := p.frame
	p.mu.Unlock()
	if !strings.Contains(frame, "Open issues") || !strings.Contains(frame, "Recent workflow runs") || strings.Contains(frame, "Open pull requests") {
		t.Fatalf("incorrect cached visibility: %q", frame)
	}
}

func TestStatusProgressPlainOutput(t *testing.T) {
	defer saveStatusFuncs()()
	t.Setenv("NO_COLOR", "1")
	fetchStatusDashboardFunc = func(bool, statusOptions) (statusDashboard, error) {
		return statusDashboard{Repository: "owner/repo"}, nil
	}
	var output bytes.Buffer
	if err := runStatus(nil, &output, io.Discard); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if strings.Contains(text, "\x1b") || strings.Contains(text, "Loading") || strings.Count(text, "Repository") != 1 || strings.Count(text, "Open issues") != 1 {
		t.Fatalf("plain output contains progress or duplicates: %q", text)
	}
	if newStatusProgress(&output, true) != nil {
		t.Fatal("non-file writer treated as terminal")
	}
	file, err := os.CreateTemp(t.TempDir(), "plain")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	})
	if newStatusProgress(file, true) != nil {
		t.Fatal("redirected file treated as terminal")
	}
}

type failedStatusProgressWriter struct{ restored bool }

func (w *failedStatusProgressWriter) Write(data []byte) (int, error) {
	if string(data) == "\x1b[?25h" {
		w.restored = true
		return len(data), nil
	}
	return 0, io.ErrClosedPipe
}
func TestStatusProgressWriterFailure(t *testing.T) {
	w := &failedStatusProgressWriter{}
	p := &statusProgress{output: w, width: 80, height: 24}
	if !errors.Is(p.start(), io.ErrClosedPipe) {
		t.Fatal("startup error lost")
	}
	if !errors.Is(p.close(), io.ErrClosedPipe) || !w.restored {
		t.Fatal("error lost or restoration skipped")
	}
}

func TestStatusProgressCanceledCommand(t *testing.T) {
	saved := commandContext
	ctx, cancel := context.WithCancel(context.Background())
	commandContext = ctx
	t.Cleanup(func() { commandContext = saved })
	p, output := testStatusProgress(t, 80, 24, false)
	p.local(&statusDashboard{Repository: "partial/repo"})
	cancel()
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "partial/repo") || !strings.HasSuffix(output.String(), "\x1b[?25h") {
		t.Fatal("partial results or restoration lost")
	}
	if _, err := runStatusCommand("git", "--version"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Git cancellation lost: %v", err)
	}
	if _, _, err := execGH("api", "user"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GitHub cancellation lost: %v", err)
	}
}

func TestRunStatusProgressLifecycle(t *testing.T) {
	defer saveStatusFuncs()()
	savedProgress, savedContext := statusProgressFunc, commandContext
	t.Cleanup(func() { statusProgressFunc, commandContext = savedProgress, savedContext })
	t.Setenv("NO_COLOR", "1")
	tests := []struct {
		name       string
		fetchError error
		cancel     bool
	}{
		{name: "success"},
		{name: "fetch failure", fetchError: io.EOF},
		{name: "cancellation", cancel: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			commandContext = ctx
			statusProgressFunc = func(output io.Writer, color bool) *statusProgress {
				return &statusProgress{output: output, color: color, width: 80, height: 24}
			}
			fetchStatusDashboardFunc = func(_ bool, options statusOptions) (statusDashboard, error) {
				dashboard := statusDashboard{Repository: "owner/repo"}
				options.progress.local(&dashboard)
				if tc.cancel {
					cancel()
				}
				return dashboard, tc.fetchError
			}
			var output bytes.Buffer
			err := runStatus(nil, &output, io.Discard)
			want := tc.fetchError
			if tc.cancel {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("error=%v, want %v", err, want)
			}
			if !strings.Contains(output.String(), "\x1b[?25h") {
				t.Fatal("cursor not restored")
			}
		})
	}
	statusProgressFunc = func(output io.Writer, color bool) *statusProgress {
		return &statusProgress{output: output, color: color, width: 80, height: 24}
	}
	commandContext = context.Background()
	w := &failedStatusProgressWriter{}
	if err := runStatus(nil, w, io.Discard); !errors.Is(err, io.ErrClosedPipe) || !w.restored {
		t.Fatalf("startup error or cursor restoration lost: %v, %v", err, w.restored)
	}
}

func TestCanceledStatusPreservesCache(t *testing.T) {
	defer saveStatusFuncs()()
	saved := commandContext
	ctx, cancel := context.WithCancel(context.Background())
	commandContext = ctx
	t.Cleanup(func() { commandContext = saved })
	cancel()
	statusCacheDirectoryFunc = func() (string, string, error) {
		t.Fatal("canceled invocation must not inspect identity or publish a cache")
		return "", "", nil
	}
	saveStatusCacheIfSameIdentity(statusOptions{}, false, time.Now(), statusDashboard{}, nil, false, t.TempDir(), "identity")
}

func TestCompleteCommand(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"success", nil, 0}, {"failure", io.EOF, 1},
		{"failure with hint", errors.New("gh: authentication required"), 1},
		{"interrupt", context.Canceled, 130},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if got := completeCommand(&stderr, nil, tc.err); got != tc.want {
				t.Fatalf("exit=%d, want %d", got, tc.want)
			}
			if tc.want == 130 && stderr.Len() != 0 {
				t.Fatalf("interrupt printed an error or update: %q", stderr.String())
			}
			if tc.want == 1 && !strings.Contains(stderr.String(), "Error: ") {
				t.Fatalf("failure was hidden: %q", stderr.String())
			}
		})
	}
}
