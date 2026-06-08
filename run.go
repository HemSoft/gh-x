package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	gh "github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/muesli/termenv"
)

type runListOptions struct {
	repo     string
	limit    int
	status   string
	workflow string
	branch   string
	event    string
	user     string
}

type workflowRun struct {
	DatabaseID   int       `json:"databaseId"`
	DisplayTitle string    `json:"displayTitle"`
	WorkflowName string    `json:"workflowName"`
	HeadBranch   string    `json:"headBranch"`
	Event        string    `json:"event"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	URL          string    `json:"url"`
	CreatedAt    time.Time `json:"createdAt"`
	StartedAt    time.Time `json:"startedAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type displayWorkflowRun struct {
	Status   string
	Title    string
	Workflow string
	Branch   string
	Event    string
	ID       string
	URL      string
	Elapsed  string
	Age      string
}

const runJSONFields = "databaseId,displayTitle,workflowName,headBranch,event,status,conclusion,url,createdAt,startedAt,updatedAt"

// executeRunListFunc is swapped in tests to avoid real API calls.
var executeRunListFunc = executeRunList

func runRunList(args []string, stdout io.Writer, stderr io.Writer) error {
	options, err := parseRunListOptions(args, stderr)
	if err != nil {
		if errors.Is(err, errHelpDisplayed) {
			return nil
		}
		return err
	}
	return executeRunListFunc(options, stdout, time.Now())
}

func parseRunListOptions(args []string, stderr io.Writer) (runListOptions, error) {
	var options runListOptions
	options.limit = 20

	flags := flag.NewFlagSet("run list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		writeRunListUsage(stderr)
	}

	flags.StringVar(&options.repo, "repo", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flags.StringVar(&options.repo, "R", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flags.IntVar(&options.limit, "limit", 20, "Maximum number of runs to fetch")
	flags.IntVar(&options.limit, "L", 20, "Maximum number of runs to fetch")
	flags.StringVar(&options.status, "status", "", "Filter runs by status")
	flags.StringVar(&options.status, "s", "", "Filter runs by status")
	flags.StringVar(&options.workflow, "workflow", "", "Filter runs by workflow")
	flags.StringVar(&options.workflow, "w", "", "Filter runs by workflow")
	flags.StringVar(&options.branch, "branch", "", "Filter runs by branch")
	flags.StringVar(&options.branch, "b", "", "Filter runs by branch")
	flags.StringVar(&options.event, "event", "", "Filter runs by event type")
	flags.StringVar(&options.event, "e", "", "Filter runs by event type")
	flags.StringVar(&options.user, "user", "", "Filter runs by user who triggered the run")
	flags.StringVar(&options.user, "u", "", "Filter runs by user who triggered the run")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options, errHelpDisplayed
		}
		return options, err
	}

	if flags.NArg() > 0 {
		return options, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), ", "))
	}

	if options.limit < 1 {
		return options, errors.New("limit must be greater than zero")
	}

	return options, nil
}

func buildRunListArgs(options runListOptions) []string {
	args := []string{"run", "list", "--json", runJSONFields}

	if options.repo != "" {
		args = append(args, "--repo", options.repo)
	}
	args = append(args, "--limit", strconv.Itoa(options.limit))

	if options.status != "" {
		args = append(args, "--status", options.status)
	}
	if options.workflow != "" {
		args = append(args, "--workflow", options.workflow)
	}
	if options.branch != "" {
		args = append(args, "--branch", options.branch)
	}
	if options.event != "" {
		args = append(args, "--event", options.event)
	}
	if options.user != "" {
		args = append(args, "--user", options.user)
	}

	return args
}

func executeRunList(options runListOptions, stdout io.Writer, now time.Time) error {
	ghArgs := buildRunListArgs(options)
	stdoutBuf, stderrBuf, err := gh.Exec(ghArgs...)
	if err != nil {
		return fmt.Errorf("gh run list: %s: %w", stderrBuf.String(), err)
	}

	var runs []workflowRun
	if err := json.Unmarshal(stdoutBuf.Bytes(), &runs); err != nil {
		return fmt.Errorf("parsing run list: %w", err)
	}

	displayRuns := make([]displayWorkflowRun, len(runs))
	for i, r := range runs {
		displayRuns[i] = buildDisplayWorkflowRun(r, now)
	}

	colorEnabled := term.FromEnv().IsColorEnabled()
	return renderRunTable(stdout, displayRuns, colorEnabled)
}

func buildDisplayWorkflowRun(r workflowRun, now time.Time) displayWorkflowRun {
	return displayWorkflowRun{
		Status:   resolveRunStatus(r.Status, r.Conclusion),
		Title:    trimTitle(r.DisplayTitle, 40),
		Workflow: trimTitle(r.WorkflowName, 20),
		Branch:   trimTitle(r.HeadBranch, 24),
		Event:    r.Event,
		ID:       strconv.Itoa(r.DatabaseID),
		URL:      r.URL,
		Elapsed:  formatElapsed(r.Status, r.StartedAt, r.UpdatedAt, now),
		Age:      formatRelativeTime(r.CreatedAt, now),
	}
}

func resolveRunStatus(status, conclusion string) string {
	if status != "completed" {
		switch status {
		case "in_progress":
			return "*"
		case "queued", "requested", "waiting", "pending":
			return "○"
		default:
			return "·"
		}
	}
	switch conclusion {
	case "success":
		return "✓"
	case "failure", "timed_out", "startup_failure":
		return "X"
	case "cancelled":
		return "!"
	case "skipped", "neutral":
		return "—"
	case "action_required":
		return "!"
	default:
		return "·"
	}
}

func formatElapsed(status string, startedAt, updatedAt time.Time, now time.Time) string {
	if startedAt.IsZero() {
		return "-"
	}

	var end time.Time
	switch status {
	case "completed":
		end = updatedAt
	case "in_progress":
		end = now
	default:
		return "-"
	}

	if end.IsZero() || end.Before(startedAt) {
		return "-"
	}

	d := end.Sub(startedAt)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		m := int(d.Minutes())
		s := int(d.Seconds()) % 60
		if s > 0 {
			return fmt.Sprintf("%dm%ds", m, s)
		}
		return fmt.Sprintf("%dm", m)
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dh", h)
}

func (s tableStyler) runStatusCell(status string) tableCell {
	switch status {
	case "✓":
		return s.colored(status, termenv.ANSIGreen)
	case "X":
		return s.colored(status, termenv.ANSIRed)
	case "*":
		return s.colored(status, termenv.ANSIYellow)
	case "!":
		return s.colored(status, termenv.ANSIYellow)
	case "—":
		return s.dim(status)
	case "○":
		return s.dim(status)
	default:
		return s.plain(status)
	}
}

func (s tableStyler) runIDCell(id, url string) tableCell {
	return s.linkCell(id, url, termenv.ANSICyan)
}

func renderRunTable(stdout io.Writer, runs []displayWorkflowRun, colorEnabled bool) error {
	if len(runs) == 0 {
		fmt.Fprintln(stdout, "No workflow runs found.")
		return nil
	}

	styler := newTableStyler(stdout, colorEnabled)

	headerLabels := []string{"", "Title", "Workflow", "Branch", "Event", "ID", "Elapsed", "Age"}
	headers := make([]tableCell, len(headerLabels))
	for i, label := range headerLabels {
		headers[i] = styler.dim(label)
	}

	rows := make([][]tableCell, len(runs))
	for i, r := range runs {
		rows[i] = []tableCell{
			styler.runStatusCell(r.Status),
			styler.plain(r.Title),
			styler.plain(r.Workflow),
			styler.plain(r.Branch),
			styler.dim(r.Event),
			styler.runIDCell(r.ID, r.URL),
			styler.dim(r.Elapsed),
			styler.dim(r.Age),
		}
	}

	colWidths := computeColumnWidths(headers, rows)

	// Fit to terminal: Title(1), Workflow(2), Branch(3) are flexible
	flexibleCols := []int{1, 2, 3}
	colWidths = fitColumnsToTerminal(colWidths, flexibleCols, getTerminalWidth())
	rows = truncateCells(rows, colWidths, flexibleCols)

	writeRow(stdout, headers, colWidths)
	for _, row := range rows {
		writeRow(stdout, row, colWidths)
	}

	if colorEnabled {
		cmd := "gh run view " + runs[0].ID
		writeOSC52(stdout, cmd)
		fmt.Fprintf(stdout, "\n→ %s  (copied — Ctrl+V to paste)\n", cmd)
	}

	return nil
}

func writeRunListUsage(w io.Writer) {
	fmt.Fprint(w, runListUsage)
}

const runListUsage = `Usage:
  gh x run list [flags]

List workflow runs for the current repository.

Flags:
  -R, --repo string       Select another repository using the [HOST/]OWNER/REPO format
  -L, --limit int         Maximum number of runs to fetch (default 20)
  -s, --status string     Filter runs by status: queued, completed, in_progress, etc.
  -w, --workflow string   Filter runs by workflow name
  -b, --branch string     Filter runs by branch
  -e, --event string      Filter runs by event type (push, pull_request, etc.)
  -u, --user string       Filter runs by user who triggered the run
`
