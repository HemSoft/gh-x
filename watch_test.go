package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseListOptionsWatch(t *testing.T) {
	options, err := parseListOptions([]string{"--monitor", "--interval", "45s"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !options.watch {
		t.Fatal("expected --monitor to enable watch mode")
	}
	if options.interval != 45*time.Second {
		t.Fatalf("expected 45s interval, got %s", options.interval)
	}
}

func TestParseListOptionsWatchUsesThirtySecondDefault(t *testing.T) {
	options, err := parseListOptions([]string{"--watch"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if options.interval != 30*time.Second {
		t.Fatalf("expected 30s default interval, got %s", options.interval)
	}
}

func TestParseListOptionsWatchRejectsInvalidCombinations(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "json", args: []string{"--watch", "--json"}, want: "--watch and --json"},
		{name: "web", args: []string{"--monitor", "--web"}, want: "--watch and --web"},
		{name: "short interval", args: []string{"--watch", "--interval", "5s"}, want: "at least 10s"},
		{name: "interval without watch", args: []string{"--interval", "45s"}, want: "requires --watch"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := parseListOptions(testCase.args, io.Discard)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("expected error containing %q, got %v", testCase.want, err)
			}
		})
	}
}

func TestRunPrRejectsWatchForNonListCommands(t *testing.T) {
	for _, args := range [][]string{{"me", "--watch"}, {"atm", "--monitor"}, {"42", "--watch"}} {
		if err := runPr(args, io.Discard, io.Discard); err == nil || !strings.Contains(err.Error(), "only supported for gh x pr list") {
			t.Fatalf("runPr(%v) returned unexpected error: %v", args, err)
		}
	}
}

func TestRunPrTreatsWatchValueAsRepoValue(t *testing.T) {
	for _, args := range [][]string{{"--repo", "--watch"}, {"-R", "--monitor"}} {
		if watchRequested(args) {
			t.Fatalf("watchRequested(%v) misclassified a repo value", args)
		}
	}
}

func TestWatchRequestedSkipsEveryPRValueFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--instructions", "--watch"},
		{"--command", "--monitor"},
		{"--org", "--watch"},
		{"--version", "--monitor"},
		{"--limit", "--watch"},
		{"--state", "--monitor"},
		{"--assignee", "--watch"},
		{"--search", "--monitor"},
		{"--label", "--watch"},
		{"--interval", "--monitor"},
	} {
		if watchRequested(args) {
			t.Fatalf("watchRequested(%v) misclassified a flag value", args)
		}
	}
}

func TestReconcileWatchRowsPreservesExistingOrder(t *testing.T) {
	previous := []displayPullRequest{
		{Number: 2, Title: "two", Checks: "pending", State: "open"},
		{Number: 1, Title: "one", Checks: "pass", State: "open"},
	}
	current := []displayPullRequest{
		{Number: 3, Title: "three", Checks: "pass", State: "open"},
		{Number: 2, Title: "two", Checks: "pass", State: "open"},
	}

	rows, changes := reconcileWatchRows(previous, current)
	if got := []int{rows[0].Number, rows[1].Number}; got[0] != 2 || got[1] != 3 {
		t.Fatalf("expected stable existing order plus new row, got %v", got)
	}
	if len(changes) != 3 {
		t.Fatalf("expected three changes, got %#v", changes)
	}
	if changes[0].text != "#3 added" || changes[1].text != "#1 removed" {
		t.Fatalf("unexpected change priority: %#v", changes)
	}
	if !strings.Contains(changes[2].text, "Checks pending -> pass") {
		t.Fatalf("expected check change, got %q", changes[2].text)
	}
}

func TestWatchFieldChangesIgnoreMetadata(t *testing.T) {
	before := displayPullRequest{Number: 1, Title: "old", Branch: "old", Updated: "1h", State: "open", Checks: "pass"}
	after := displayPullRequest{Number: 1, Title: "new", Branch: "new", Updated: "1m", State: "open", Checks: "pass"}
	if changes := watchFieldChanges(before, after); len(changes) != 0 {
		t.Fatalf("expected metadata-only changes to be ignored, got %#v", changes)
	}
}

func TestSummarizeWatchChangesLimitsFooter(t *testing.T) {
	changes := []watchChange{
		{number: 1, text: "#1 added"},
		{number: 2, text: "#2 removed"},
		{number: 3, text: "#3 Checks pending -> pass"},
	}
	got := summarizeWatchChanges(changes)
	want := "#1 added; #2 removed; +1 more changes"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestNextWatchBackoffCaps(t *testing.T) {
	if got := nextWatchBackoff(30 * time.Second); got != time.Minute {
		t.Fatalf("expected one doubling, got %s", got)
	}
	if got := nextWatchBackoff(maximumWatchBackoff); got != maximumWatchBackoff {
		t.Fatalf("expected cap to remain %s, got %s", maximumWatchBackoff, got)
	}
}

func TestWaitWatchEventIgnoresTerminalEscapeSequences(t *testing.T) {
	keys := make(chan byte, 4)
	keys <- 0x1b
	keys <- '['
	keys <- 'A'
	keys <- 'x'

	key, refresh, err := waitWatchEvent(keys, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if refresh || key != 'x' {
		t.Fatalf("expected navigation sequence to be ignored before x, got key %q refresh=%t", key, refresh)
	}
}

func TestWatchRefreshRemainsExitableWhileFetchIsBlocked(t *testing.T) {
	savedTTY := watchTTYFunc
	savedOpen := openWatchTerminalFunc
	savedFetch := fetchPullRequestListFunc
	savedAfter := watchAfterFunc
	defer func() {
		watchTTYFunc = savedTTY
		openWatchTerminalFunc = savedOpen
		fetchPullRequestListFunc = savedFetch
		watchAfterFunc = savedAfter
	}()

	terminal := &fakeWatchTerminal{keys: make(chan byte, 1)}
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	watchTTYFunc = func() bool { return true }
	openWatchTerminalFunc = func(io.Writer) (watchTerminal, error) { return terminal, nil }
	fetchCalls := 0
	fetchPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) {
		fetchCalls++
		if fetchCalls == 1 {
			return pullRequestListResult{Rendered: []displayPullRequest{{Number: 1, State: "open", Checks: "pass"}}}, nil
		}
		close(refreshStarted)
		<-releaseRefresh
		return pullRequestListResult{}, nil
	}
	watchAfterFunc = func(time.Duration) <-chan time.Time {
		channel := make(chan time.Time, 1)
		channel <- time.Now()
		return channel
	}

	go func() {
		<-refreshStarted
		terminal.keys <- 0x1b
	}()

	var output bytes.Buffer
	err := runWatch(listOptions{repo: "HemSoft/gh-x", limit: 30, interval: 30 * time.Second}, &output, io.Discard)
	close(releaseRefresh)
	if err != nil {
		t.Fatal(err)
	}
	if !terminal.closed {
		t.Fatal("expected terminal cleanup after escape during refresh")
	}
}

func TestWatchSignalExitCode(t *testing.T) {
	if got := watchSignalExitCode(syscall.SIGTERM); got != 128+int(syscall.SIGTERM) {
		t.Fatalf("expected SIGTERM exit code %d, got %d", 128+int(syscall.SIGTERM), got)
	}
}

func TestRenderWatchScreenIncludesFixedFooter(t *testing.T) {
	var output bytes.Buffer
	lastRefresh := time.Date(2026, 8, 17, 15, 4, 5, 0, time.UTC)
	rows := []displayPullRequest{{Number: 7, Title: "Watch me", Author: "user", State: "open", Review: "review", SFLReview: "-", AIReview: "-", Checks: "pending", Comments: "-", Branch: "feature/watch", Updated: "1m"}}
	err := renderWatchScreen(&output, listOptions{limit: 30}, "HemSoft/gh-x", rows, lastRefresh, 30*time.Second, []watchChange{{number: 7, text: "#7 Checks pending -> pass"}}, errors.New("temporary API failure"), false)
	if err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, fragment := range []string{"Pull requests for HemSoft/gh-x", "Status: stale | Last refresh 15:04:05 | Retry in 30s", "Changes: #7 Checks pending -> pass", "Error: temporary API failure"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected %q in output %q", fragment, text)
		}
	}
}

func TestWriteWatchErrorUsesRedWhenColorEnabled(t *testing.T) {
	var output bytes.Buffer
	writeWatchError(&output, errors.New("failure"), true)
	if !strings.Contains(output.String(), "\x1b[31mError: failure\x1b[0m") {
		t.Fatalf("expected red error output, got %q", output.String())
	}
}

type fakeWatchTerminal struct {
	keys   chan byte
	closed bool
}

func (t *fakeWatchTerminal) Keys() <-chan byte {
	return t.keys
}

func (t *fakeWatchTerminal) Close() error {
	t.closed = true
	return nil
}

func TestRunWatchFetchesAndExitsOnEscape(t *testing.T) {
	savedTTY := watchTTYFunc
	savedOpen := openWatchTerminalFunc
	savedFetch := fetchPullRequestListFunc
	savedNow := watchNowFunc
	defer func() {
		watchTTYFunc = savedTTY
		openWatchTerminalFunc = savedOpen
		fetchPullRequestListFunc = savedFetch
		watchNowFunc = savedNow
	}()

	terminal := &fakeWatchTerminal{keys: make(chan byte, 1)}
	terminal.keys <- 0x1b
	watchTTYFunc = func() bool { return true }
	openWatchTerminalFunc = func(io.Writer) (watchTerminal, error) { return terminal, nil }
	watchNowFunc = func() time.Time { return time.Date(2026, 8, 17, 15, 4, 5, 0, time.UTC) }
	fetchCalls := 0
	fetchPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) {
		fetchCalls++
		return pullRequestListResult{Rendered: []displayPullRequest{{Number: 1, Title: "PR", State: "open", Checks: "pass"}}}, nil
	}

	var output bytes.Buffer
	err := runWatch(listOptions{repo: "HemSoft/gh-x", limit: 30, interval: 30 * time.Second}, &output, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if fetchCalls != 1 {
		t.Fatalf("expected only initial fetch before escape, got %d", fetchCalls)
	}
	if !terminal.closed {
		t.Fatal("expected terminal cleanup")
	}
}

func TestRunWatchShowsTransientRefreshError(t *testing.T) {
	savedTTY := watchTTYFunc
	savedOpen := openWatchTerminalFunc
	savedFetch := fetchPullRequestListFunc
	savedNow := watchNowFunc
	savedAfter := watchAfterFunc
	defer func() {
		watchTTYFunc = savedTTY
		openWatchTerminalFunc = savedOpen
		fetchPullRequestListFunc = savedFetch
		watchNowFunc = savedNow
		watchAfterFunc = savedAfter
	}()

	terminal := &fakeWatchTerminal{keys: make(chan byte, 1)}
	watchTTYFunc = func() bool { return true }
	openWatchTerminalFunc = func(io.Writer) (watchTerminal, error) { return terminal, nil }
	watchNowFunc = func() time.Time { return time.Date(2026, 8, 17, 15, 4, 5, 0, time.UTC) }
	fetchCalls := 0
	fetchPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) {
		fetchCalls++
		if fetchCalls == 1 {
			return pullRequestListResult{Rendered: []displayPullRequest{{Number: 1, Title: "PR", State: "open", Checks: "pass"}}}, nil
		}
		return pullRequestListResult{}, errors.New("temporary API failure")
	}
	watchAfterFunc = func(time.Duration) <-chan time.Time {
		channel := make(chan time.Time, 1)
		if fetchCalls == 1 {
			channel <- time.Now()
		} else {
			terminal.keys <- 0x1b
		}
		return channel
	}

	var output bytes.Buffer
	err := runWatch(listOptions{repo: "HemSoft/gh-x", limit: 30, interval: 30 * time.Second}, &output, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Error: temporary API failure") {
		t.Fatalf("expected transient error in output, got %q", output.String())
	}
	if fetchCalls != 2 {
		t.Fatalf("expected one refresh attempt after initial fetch, got %d calls", fetchCalls)
	}
}
