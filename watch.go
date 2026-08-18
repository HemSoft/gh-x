package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cli/go-gh/v2/pkg/term"
	xterm "golang.org/x/term"
)

const (
	defaultWatchInterval = 30 * time.Second
	minimumWatchInterval = 10 * time.Second
	maximumWatchBackoff  = 5 * time.Minute
)

var (
	watchNowFunc   = time.Now
	watchAfterFunc = time.After
	watchTTYFunc   = func() bool {
		return xterm.IsTerminal(int(os.Stdin.Fd())) && xterm.IsTerminal(int(os.Stdout.Fd()))
	}
	openWatchTerminalFunc = openWatchTerminal
)

type watchChange struct {
	number   int
	priority int
	text     string
}

type watchFieldChange struct {
	label    string
	before   string
	after    string
	priority int
}

type watchTerminal interface {
	Keys() <-chan byte
	Close() error
}

type watchState struct {
	rows        []displayPullRequest
	lastRefresh time.Time
	changes     []watchChange
	refreshErr  error
	nextDelay   time.Duration
	backoff     time.Duration
}

type rawWatchTerminal struct {
	stdout    io.Writer
	fd        int
	state     *xterm.State
	keys      chan byte
	closeOnce sync.Once
	closeErr  error
}

func runWatch(options listOptions, stdout io.Writer, _ io.Writer) (err error) {
	if !watchTTYFunc() {
		return errors.New("watch mode requires an interactive terminal")
	}

	now := watchNowFunc()
	result, err := fetchPullRequestListFunc(options, now)
	if err != nil {
		return err
	}

	repoLabel := resolveRepoLabel(options.repo)
	terminal, err := openWatchTerminalFunc(stdout)
	if err != nil {
		return err
	}
	return runWatchSession(options, stdout, repoLabel, terminal, result, now)
}

func runWatchSession(options listOptions, stdout io.Writer, repoLabel string, terminal watchTerminal, result pullRequestListResult, now time.Time) (err error) {
	defer func() {
		if closeErr := terminal.Close(); err == nil {
			err = closeErr
		}
	}()

	state := watchState{rows: result.Rendered, lastRefresh: now, nextDelay: options.interval, backoff: options.interval}
	keys := terminal.Keys()
	return runWatchLoop(options, stdout, repoLabel, state, keys)
}

func runWatchLoop(options listOptions, stdout io.Writer, repoLabel string, state watchState, keys <-chan byte) error {
	for {
		if err := renderWatchScreen(stdout, options, repoLabel, state.rows, state.lastRefresh, state.nextDelay, state.changes, state.refreshErr, false); err != nil {
			return err
		}

		key, refresh, waitErr := waitWatchEvent(keys, state.nextDelay)
		if waitErr != nil {
			return waitErr
		}
		if watchExitRequested(key, refresh) {
			return nil
		}
		if !refresh {
			continue
		}

		if err := renderWatchScreen(stdout, options, repoLabel, state.rows, state.lastRefresh, 0, nil, nil, true); err != nil {
			return err
		}
		refreshWatchState(&state, options)
	}
}

func watchExitRequested(key byte, refresh bool) bool {
	return !refresh && (key == 0x1b || key == 0x03)
}

func waitWatchEvent(keys <-chan byte, delay time.Duration) (byte, bool, error) {
	select {
	case key, ok := <-keys:
		if !ok {
			return 0, false, errors.New("watch input closed")
		}
		return key, false, nil
	case <-watchAfterFunc(delay):
		return 0, true, nil
	}
}

func refreshWatchState(state *watchState, options listOptions) {
	now := watchNowFunc()
	result, err := fetchPullRequestListFunc(options, now)
	if err != nil {
		state.refreshErr = err
		state.changes = nil
		state.nextDelay = state.backoff
		state.backoff = nextWatchBackoff(state.backoff)
		return
	}
	state.rows, state.changes = reconcileWatchRows(state.rows, result.Rendered)
	state.lastRefresh = now
	state.refreshErr = nil
	state.nextDelay = options.interval
	state.backoff = options.interval
}

func nextWatchBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > maximumWatchBackoff {
		return maximumWatchBackoff
	}
	return next
}

func reconcileWatchRows(previous, current []displayPullRequest) ([]displayPullRequest, []watchChange) {
	currentByNumber := make(map[int]displayPullRequest, len(current))
	for _, pr := range current {
		currentByNumber[pr.Number] = pr
	}

	ordered := make([]displayPullRequest, 0, len(current))
	seen := make(map[int]bool, len(current))
	groups := make(map[int]*watchChange)
	for _, before := range previous {
		after, ok := currentByNumber[before.Number]
		if !ok {
			groups[before.Number] = &watchChange{number: before.Number, priority: 1, text: fmt.Sprintf("#%d removed", before.Number)}
			continue
		}
		ordered = append(ordered, after)
		seen[after.Number] = true
		addWatchFieldChanges(groups, before, after)
	}

	for _, after := range current {
		if seen[after.Number] {
			continue
		}
		ordered = append(ordered, after)
		groups[after.Number] = &watchChange{number: after.Number, priority: 0, text: fmt.Sprintf("#%d added", after.Number)}
	}

	changes := make([]watchChange, 0, len(groups))
	for _, change := range groups {
		changes = append(changes, *change)
	}
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].priority != changes[j].priority {
			return changes[i].priority < changes[j].priority
		}
		return changes[i].number < changes[j].number
	})
	return ordered, changes
}

func addWatchFieldChanges(groups map[int]*watchChange, before, after displayPullRequest) {
	fields := watchFieldChanges(before, after)
	if len(fields) == 0 {
		return
	}
	change := &watchChange{number: after.Number, priority: fields[0].priority}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, fmt.Sprintf("%s %s -> %s", field.label, field.before, field.after))
		if field.priority < change.priority {
			change.priority = field.priority
		}
	}
	change.text = fmt.Sprintf("#%d %s", after.Number, strings.Join(parts, "; "))
	groups[after.Number] = change
}

func watchFieldChanges(before, after displayPullRequest) []watchFieldChange {
	changes := make([]watchFieldChange, 0, 8)
	appendChange := func(label, beforeValue, afterValue string, priority int) {
		if beforeValue != afterValue {
			changes = append(changes, watchFieldChange{label: label, before: beforeValue, after: afterValue, priority: priority})
		}
	}
	appendChange("State", before.State, after.State, 2)
	appendChange("Review", before.Review, after.Review, 3)
	appendChange("SFL", before.SFLReview, after.SFLReview, 4)
	appendChange("AI", before.AIReview, after.AIReview, 5)
	appendChange("AI clean", formatWatchClean(before.AIClean), formatWatchClean(after.AIClean), 5)
	appendChange("Approvals", fmt.Sprintf("%d", before.Approvals), fmt.Sprintf("%d", after.Approvals), 6)
	appendChange("Checks", before.Checks, after.Checks, 7)
	appendChange("Comments", before.Comments, after.Comments, 8)
	return changes
}

func formatWatchClean(clean *bool) string {
	if clean != nil && *clean {
		return "yes"
	}
	return "no"
}

func summarizeWatchChanges(changes []watchChange) string {
	if len(changes) == 0 {
		return "none"
	}
	shown := make([]string, 0, 2)
	for _, change := range changes {
		if len(shown) == 2 {
			break
		}
		shown = append(shown, change.text)
	}
	if remaining := len(changes) - len(shown); remaining > 0 {
		shown = append(shown, fmt.Sprintf("+%d more changes", remaining))
	}
	return strings.Join(shown, "; ")
}

func renderWatchScreen(stdout io.Writer, options listOptions, repoLabel string, rows []displayPullRequest, lastRefresh time.Time, nextDelay time.Duration, changes []watchChange, refreshErr error, refreshing bool) error {
	if _, err := io.WriteString(stdout, "\x1b[2J\x1b[H"); err != nil {
		return err
	}
	if repoLabel != "" && len(rows) > 0 {
		fmt.Fprintf(stdout, "Pull requests for %s\n\n", repoLabel)
	}
	if err := renderTableWithStyle(stdout, options, rows, term.FromEnv().IsColorEnabled()); err != nil {
		return err
	}

	fmt.Fprintln(stdout)
	writeWatchStatus(stdout, len(rows), lastRefresh, nextDelay, refreshErr != nil, refreshing)
	fmt.Fprintf(stdout, "Changes: %s\n", summarizeWatchChanges(changes))
	writeWatchError(stdout, refreshErr, term.FromEnv().IsColorEnabled())
	return nil
}

func writeWatchStatus(stdout io.Writer, count int, lastRefresh time.Time, nextDelay time.Duration, stale, refreshing bool) {
	if refreshing {
		fmt.Fprintln(stdout, "Status: refreshing...")
		return
	}
	if lastRefresh.IsZero() {
		fmt.Fprintf(stdout, "Status: watching %d pull requests\n", count)
		return
	}
	if stale {
		fmt.Fprintf(stdout, "Status: stale | Last refresh %s | Retry in %s\n", lastRefresh.Format("15:04:05"), formatWatchDelay(nextDelay))
		return
	}
	label := "Next refresh in"
	if nextDelay > 0 {
		fmt.Fprintf(stdout, "Status: watching %d pull requests | Last refresh %s | %s %s\n", count, lastRefresh.Format("15:04:05"), label, formatWatchDelay(nextDelay))
		return
	}
	fmt.Fprintf(stdout, "Status: stale | Last refresh %s\n", lastRefresh.Format("15:04:05"))
}

func formatWatchDelay(delay time.Duration) string {
	if delay <= 0 {
		return "0s"
	}
	seconds := int64(delay / time.Second)
	if delay%time.Second != 0 {
		seconds++
	}
	return (time.Duration(seconds) * time.Second).String()
}

func writeWatchError(stdout io.Writer, refreshErr error, colorEnabled bool) {
	if refreshErr == nil {
		fmt.Fprintln(stdout)
		return
	}
	if colorEnabled {
		fmt.Fprintf(stdout, "%sError: %v%s\n", "\x1b[31m", refreshErr, "\x1b[0m")
		return
	}
	fmt.Fprintf(stdout, "Error: %v\n", refreshErr)
}

func openWatchTerminal(stdout io.Writer) (watchTerminal, error) {
	fd := int(os.Stdin.Fd())
	state, err := xterm.MakeRaw(fd)
	if err != nil {
		return nil, fmt.Errorf("enter raw terminal mode: %w", err)
	}
	if _, err := io.WriteString(stdout, "\x1b[?1049h\x1b[2J\x1b[H"); err != nil {
		_ = xterm.Restore(fd, state)
		return nil, fmt.Errorf("enter alternate screen: %w", err)
	}

	terminal := &rawWatchTerminal{stdout: stdout, fd: fd, state: state, keys: make(chan byte, 1)}
	go terminal.readKeys()
	return terminal, nil
}

func (t *rawWatchTerminal) Keys() <-chan byte {
	return t.keys
}

func (t *rawWatchTerminal) readKeys() {
	var buffer [1]byte
	for {
		count, err := os.Stdin.Read(buffer[:])
		if err != nil {
			close(t.keys)
			return
		}
		if count > 0 {
			t.keys <- buffer[0]
		}
	}
}

func (t *rawWatchTerminal) Close() error {
	t.closeOnce.Do(func() {
		restoreErr := xterm.Restore(t.fd, t.state)
		_, screenErr := io.WriteString(t.stdout, "\x1b[?1049l")
		t.closeErr = errors.Join(restoreErr, screenErr)
	})
	return t.closeErr
}
