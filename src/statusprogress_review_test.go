package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestStatusGitCancellationHelper(t *testing.T) {
	address := os.Getenv("GH_X_STATUS_CANCEL_HELPER")
	if address == "" {
		t.Skip("child process fixture")
	}
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("ready")); err != nil {
		t.Fatal(err)
	}
	<-time.After(time.Minute)
}

func TestStatusCancellationAfterGitStarts(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	t.Setenv("GH_X_STATUS_CANCEL_HELPER", listener.Addr().String())
	saved := commandContext
	ctx, cancel := context.WithCancel(context.Background())
	commandContext = ctx
	t.Cleanup(func() { commandContext = saved })
	finished := make(chan struct{})
	var commandErr error
	go func() {
		_, commandErr = runStatusCommand(os.Args[0], "-test.run=^TestStatusGitCancellationHelper$")
		close(finished)
	}()
	defer func() { cancel(); <-finished }()
	accepted := make(chan net.Conn, 1)
	go func() { conn, _ := listener.Accept(); accepted <- conn }()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("Git child did not start")
	}
	if conn == nil {
		t.Fatal("child handshake failed")
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	ready := make([]byte, 5)
	if _, err := io.ReadFull(conn, ready); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-finished
	if !errors.Is(commandErr, context.Canceled) {
		t.Fatalf("started child cancellation=%v, want context.Canceled", commandErr)
	}
	var stderr bytes.Buffer
	if completeCommand(&stderr, nil, commandErr) != 130 || stderr.Len() != 0 {
		t.Fatalf("interrupt must exit silently with 130: %q", stderr.String())
	}
}

func TestStatusProgressFallbackNotice(t *testing.T) {
	notifiedMu.Lock()
	savedWriter, savedLogins := accountWarningWriter, notifiedLogins
	notifiedLogins = map[string]bool{}
	notifiedMu.Unlock()
	t.Cleanup(func() {
		notifiedMu.Lock()
		accountWarningWriter, notifiedLogins = savedWriter, savedLogins
		notifiedMu.Unlock()
	})
	p, output := testStatusProgress(t, 80, 24, false)
	p.local(&statusDashboard{Repository: "notice/repo"})
	notifiedMu.Lock()
	accountWarningWriter = output
	notifiedMu.Unlock()
	noteFallback("secondary-fixture", "github.com")
	p.tick()
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	notice := "[gh-x] note: retried as secondary-fixture (github.com) after an access failure\n"
	if strings.Count(text, notice) != 1 || strings.Index(text, notice) > strings.LastIndex(text, "notice/repo") {
		t.Fatalf("notice was duplicated or not retained above reanchored preview: %q", text)
	}
	notifiedMu.Lock()
	active := accountProgress
	notifiedMu.Unlock()
	if active != nil {
		t.Fatal("closed progress still intercepts notices")
	}
}

func TestStatusProgressResize(t *testing.T) {
	p, output := testStatusProgress(t, 120, 30, false)
	var sizeMu sync.Mutex
	width, height, sizeErr := 120, 30, error(nil)
	p.mu.Lock()
	p.size = func() (int, int, error) { sizeMu.Lock(); defer sizeMu.Unlock(); return width, height, sizeErr }
	p.frame = strings.Repeat("x", 100) + "\nshort\n"
	p.paint()
	p.mu.Unlock()
	setSize := func(w, h int, err error) { sizeMu.Lock(); width, height, sizeErr = w, h, err; sizeMu.Unlock() }
	setSize(80, 15, nil)
	p.tick()
	p.mu.Lock()
	w, h, rows, drawn := p.width, p.height, p.lines, p.drawn
	p.mu.Unlock()
	if w != 80 || h != 15 || rows > 14 {
		t.Fatalf("preview did not fit shrink: %dx%d, %d rows", w, h, rows)
	}
	for _, line := range strings.Split(drawn, "\n") {
		if ansi.StringWidth(line) >= 80 {
			t.Fatalf("resized line wraps: %q", line)
		}
	}
	setSize(2, 2, nil)
	p.tick()
	p.mu.Lock()
	rows = p.lines
	p.mu.Unlock()
	if rows != 0 {
		t.Fatal("tiny terminal retained animation")
	}
	setSize(120, 30, nil)
	p.tick()
	p.mu.Lock()
	rows, w = p.lines, p.width
	p.mu.Unlock()
	if rows == 0 || w != 120 {
		t.Fatal("preview did not return after grow")
	}
	setSize(0, 0, io.EOF)
	p.tick()
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b[3A") {
		t.Fatal("resize did not account for the old 100-column line wrapping at 80 columns")
	}
}

func TestStatusProgressPhysicalRows(t *testing.T) {
	tests := []struct {
		name, frame string
		width, want int
	}{
		{"empty", "", 80, 0}, {"blank line", "\n", 80, 2},
		{"wrap", "1234567890\nshort", 6, 3},
		{"wide colored", "\x1b[32m界界界\x1b[0m", 4, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusProgressRows(tc.frame, tc.width); got != tc.want {
				t.Fatalf("physical rows=%d, want %d", got, tc.want)
			}
		})
	}
}

func TestStatusProgressVirtualTerminal(t *testing.T) {
	savedSize, savedVT := statusTerminalSizeFunc, statusVirtualTerminalFunc
	t.Cleanup(func() { statusTerminalSizeFunc, statusVirtualTerminalFunc = savedSize, savedVT })
	forceTerminal(t, true)
	t.Setenv("TERM", "xterm")
	statusTerminalSizeFunc = func(int) (int, int, error) { return 80, 24, nil }
	file, err := os.CreateTemp(t.TempDir(), "console")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	calls, restores := 0, 0
	statusVirtualTerminalFunc = func(io.Writer) (func() error, error) { calls++; return func() error { restores++; return nil }, nil }
	p := newStatusProgress(file, false)
	if p == nil || calls != 1 {
		t.Fatal("VT not enabled before constructing animation")
	}
	if err := p.start(); err != nil {
		t.Fatal(err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
	if restores != 1 {
		t.Fatalf("VT restored %d times, want once", restores)
	}
	statusVirtualTerminalFunc = func(io.Writer) (func() error, error) { return nil, io.EOF }
	if newStatusProgress(file, false) != nil {
		t.Fatal("unsupported VT must disable animation")
	}
	statusTerminalSizeFunc = func(int) (int, int, error) { return 80, 24, io.EOF }
	if newStatusProgress(file, false) != nil {
		t.Fatal("unknown dimensions must disable animation")
	}
	statusTerminalSizeFunc = func(int) (int, int, error) { return 10, 3, nil }
	if newStatusProgress(file, false) != nil {
		t.Fatal("tiny terminal must disable animation")
	}
}

func TestStatusProgressVirtualTerminalRestoreFailure(t *testing.T) {
	p, _ := testStatusProgress(t, 80, 24, false)
	p.restoreVT = func() error { return io.EOF }
	if err := p.close(); !errors.Is(err, io.EOF) {
		t.Fatalf("VT restore error lost: %v", err)
	}
	// The fixture cleanup expects success; the restoration error was asserted above.
	p.err = nil
}

func TestStatusResolvedDefaultBranchAppearsBeforeFetch(t *testing.T) {
	for _, warm := range []bool{false, true} {
		t.Run(map[bool]string{false: "hosted", true: "cached"}[warm], func(t *testing.T) {
			defer saveStatusFuncs()()
			installStatusDashboardGitFixture()
			gitCommand := statusCommandFunc
			statusCommandFunc = func(name string, args ...string) (string, error) {
				output, err := gitCommand(name, args...)
				if len(args) > 0 && args[0] == "for-each-ref" {
					lines := strings.Split(output, "\n")
					kept := lines[:0]
					for _, line := range lines {
						if !strings.HasPrefix(line, "refs/remotes/origin/HEAD\t") {
							kept = append(kept, line)
						}
					}
					output = strings.Join(kept, "\n")
				}
				return output, err
			}
			now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
			statusNowFunc = func() time.Time { return now }
			statusRepoLabelFunc = func(string) string { return "owner/repo" }
			statusDefaultBranchFunc = func() string { return "main" }
			statusIssueListFunc = func(issueListOptions, time.Time) (issueListResult, error) { return issueListResult{}, nil }
			statusPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) { return pullRequestListResult{}, nil }
			statusWorkflowRunListFunc = func(runListOptions, time.Time) (workflowRunListResult, error) { return workflowRunListResult{}, io.EOF }
			useStatusCacheDirectory(t, t.TempDir(), "fixture")
			if warm {
				if _, err := fetchStatusDashboard(false, statusOptions{}); err != nil {
					t.Fatal(err)
				}
				now = now.Add(6 * time.Second)
				statusDefaultBranchFunc = func() string { t.Fatal("warm cache must supply hosted default branch"); return "" }
			}
			p, _ := testStatusProgress(t, 120, 40, false)
			checked := false
			verify := func() {
				p.mu.Lock()
				frame := p.frame
				p.mu.Unlock()
				if !strings.Contains(frame, "✓ Main") || !strings.Contains(frame, "synced with origin/main") || strings.Contains(frame, "default branch unavailable") {
					t.Fatalf("resolved branch still unavailable before fetch: %q", frame)
				}
				checked = true
			}
			if warm {
				statusWorkflowRunListFunc = func(runListOptions, time.Time) (workflowRunListResult, error) {
					verify()
					return workflowRunListResult{}, nil
				}
			} else {
				statusIssueListFunc = func(issueListOptions, time.Time) (issueListResult, error) { verify(); return issueListResult{}, nil }
			}
			if _, err := fetchStatusDashboard(false, statusOptions{progress: p}); err != nil {
				t.Fatal(err)
			}
			if !checked {
				t.Fatal("expected remote fetch did not run")
			}
		})
	}
}
