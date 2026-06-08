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

type issueListOptions struct {
	repo      string
	limit     int
	state     string
	author    string
	assignee  string
	milestone string
	search    string
	web       bool
	labels    stringSliceFlag
}

type issueAssignee struct {
	Login string `json:"login"`
}

type issueLabel struct {
	Name string `json:"name"`
}

type issueEntry struct {
	Number    int             `json:"number"`
	Title     string          `json:"title"`
	Author    *author         `json:"author"`
	State     string          `json:"state"`
	Labels    []issueLabel    `json:"labels"`
	Assignees []issueAssignee `json:"assignees"`
	UpdatedAt time.Time       `json:"updatedAt"`
	URL       string          `json:"url"`
}

type displayIssue struct {
	Number    int
	Title     string
	Author    string
	State     string
	Labels    string
	Assignees string
	Updated   string
	URL       string
}

const issueJSONFields = "number,title,author,state,labels,assignees,updatedAt,url"

// fetchIssuesFunc is swapped in tests to avoid real API calls.
var fetchIssuesFunc = fetchIssues

func runIssueList(args []string, stdout io.Writer, stderr io.Writer) error {
	options, err := parseIssueListOptions(args, stderr)
	if err != nil {
		if errors.Is(err, errHelpDisplayed) {
			return nil
		}
		return err
	}
	return executeIssueListFunc(options, stdout, time.Now())
}

// executeIssueListFunc is swapped in tests to avoid real API calls.
var executeIssueListFunc = executeIssueList

func parseIssueListOptions(args []string, stderr io.Writer) (issueListOptions, error) {
	var options issueListOptions
	options.limit = 30
	options.state = "open"

	flags := flag.NewFlagSet("issue list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		writeIssueListUsage(stderr)
	}

	flags.StringVar(&options.repo, "repo", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flags.StringVar(&options.repo, "R", "", "Select another repository using the [HOST/]OWNER/REPO format")
	flags.IntVar(&options.limit, "limit", 30, "Maximum number of issues to fetch")
	flags.IntVar(&options.limit, "L", 30, "Maximum number of issues to fetch")
	flags.StringVar(&options.state, "state", "open", "Filter by state: open, closed, or all")
	flags.StringVar(&options.state, "s", "open", "Filter by state: open, closed, or all")
	flags.StringVar(&options.author, "author", "", "Filter by author")
	flags.StringVar(&options.author, "A", "", "Filter by author")
	flags.StringVar(&options.assignee, "assignee", "", "Filter by assignee")
	flags.StringVar(&options.assignee, "a", "", "Filter by assignee")
	flags.StringVar(&options.milestone, "milestone", "", "Filter by milestone number or title")
	flags.StringVar(&options.milestone, "m", "", "Filter by milestone number or title")
	flags.StringVar(&options.search, "search", "", "Search issues with a GitHub search query")
	flags.StringVar(&options.search, "S", "", "Search issues with a GitHub search query")
	flags.BoolVar(&options.web, "web", false, "Open the matching issues in the browser")
	flags.BoolVar(&options.web, "w", false, "Open the matching issues in the browser")
	flags.Var(&options.labels, "label", "Filter by label (repeatable)")
	flags.Var(&options.labels, "l", "Filter by label (repeatable)")

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

func buildIssueListArgs(options issueListOptions) []string {
	args := []string{"issue", "list"}

	if options.web {
		args = append(args, "--web")
	} else {
		args = append(args, "--json", issueJSONFields)
	}

	args = appendNonEmpty(args, "--repo", options.repo)
	args = append(args, "--limit", strconv.Itoa(options.limit))
	args = append(args, "--state", options.state)
	args = appendNonEmpty(args, "--author", options.author)
	args = appendNonEmpty(args, "--assignee", options.assignee)
	args = appendNonEmpty(args, "--milestone", options.milestone)
	args = appendNonEmpty(args, "--search", options.search)

	for _, label := range options.labels {
		args = append(args, "--label", label)
	}

	return args
}

func fetchIssues(options issueListOptions) ([]issueEntry, error) {
	ghArgs := buildIssueListArgs(options)
	stdoutBuf, stderrBuf, err := gh.Exec(ghArgs...)
	if err != nil {
		return nil, wrapExecError(fmt.Errorf("gh issue list: %w", err), stderrBuf.String())
	}

	var issues []issueEntry
	if err := json.Unmarshal(stdoutBuf.Bytes(), &issues); err != nil {
		return nil, fmt.Errorf("parsing issue list: %w", err)
	}

	return issues, nil
}

func executeIssueList(options issueListOptions, stdout io.Writer, now time.Time) error {
	if options.author != "" {
		org := ""
		if options.repo != "" {
			if parts := strings.SplitN(options.repo, "/", 2); len(parts) == 2 {
				org = parts[0]
			}
		} else if o, _, err := resolveRepo(""); err == nil {
			org = o
		}
		resolved, err := resolveAuthorLoginFunc(options.author, org)
		if err != nil {
			return err
		}
		options.author = resolved
	}

	if options.web {
		ghArgs := buildIssueListArgs(options)
		_, stderrBuf, err := gh.Exec(ghArgs...)
		if err != nil {
			return wrapExecError(fmt.Errorf("gh issue list: %w", err), stderrBuf.String())
		}
		return nil
	}

	issues, err := fetchIssuesFunc(options)
	if err != nil {
		return err
	}

	displayIssues := make([]displayIssue, len(issues))
	for i, entry := range issues {
		displayIssues[i] = buildDisplayIssue(entry, now)
	}

	colorEnabled := term.FromEnv().IsColorEnabled()
	return renderIssueTable(stdout, displayIssues, options, colorEnabled)
}

func buildDisplayIssue(entry issueEntry, now time.Time) displayIssue {
	authorName := "-"
	if entry.Author != nil && entry.Author.Login != "" {
		authorName = formatAuthor(entry.Author.Login, entry.Author.Name)
	}

	return displayIssue{
		Number:    entry.Number,
		Title:     trimTitle(entry.Title, 51),
		Author:    authorName,
		State:     normalizeIssueState(entry.State),
		Labels:    joinLabels(entry.Labels),
		Assignees: joinAssignees(entry.Assignees),
		Updated:   formatRelativeTime(entry.UpdatedAt, now),
		URL:       entry.URL,
	}
}

func normalizeIssueState(state string) string {
	switch strings.ToUpper(state) {
	case "OPEN":
		return "open"
	case "CLOSED":
		return "closed"
	default:
		if state == "" {
			return "-"
		}
		return strings.ToLower(state)
	}
}

func joinLabels(labels []issueLabel) string {
	if len(labels) == 0 {
		return ""
	}
	names := make([]string, len(labels))
	for i, l := range labels {
		names[i] = l.Name
	}
	return strings.Join(names, ", ")
}

func joinAssignees(assignees []issueAssignee) string {
	if len(assignees) == 0 {
		return ""
	}
	logins := make([]string, len(assignees))
	for i, a := range assignees {
		logins[i] = a.Login
	}
	return strings.Join(logins, ", ")
}

func (s tableStyler) issueStateCell(state string) tableCell {
	switch state {
	case "open":
		return s.colored(state, termenv.ANSIGreen)
	case "closed":
		return s.colored(state, termenv.ANSIRed)
	default:
		return s.plain(state)
	}
}

func renderIssueTable(stdout io.Writer, issues []displayIssue, options issueListOptions, colorEnabled bool) error {
	if len(issues) == 0 {
		fmt.Fprintln(stdout, "No issues found.")
		return nil
	}

	if repoLabel := resolveRepoLabel(options.repo); repoLabel != "" {
		fmt.Fprintf(stdout, "Issues for %s\n\n", repoLabel)
	}

	styler := newTableStyler(stdout, colorEnabled)

	headerLabels := []string{"#", "Title", "Author", "State", "Labels", "Assignees", "Updated"}
	headers := make([]tableCell, len(headerLabels))
	for i, label := range headerLabels {
		headers[i] = styler.dim(label)
	}

	rows := make([][]tableCell, len(issues))
	for i, issue := range issues {
		rows[i] = []tableCell{
			styler.numberCell(issue.Number, issue.URL),
			styler.plain(issue.Title),
			styler.plain(issue.Author),
			styler.issueStateCell(issue.State),
			styler.dim(issue.Labels),
			styler.dim(issue.Assignees),
			styler.dim(issue.Updated),
		}
	}

	colWidths := computeColumnWidths(headers, rows)

	// Fit to terminal: Title(1), Author(2), Labels(4), Assignees(5) are flexible
	flexibleCols := []int{1, 2, 4, 5}
	colWidths = fitColumnsToTerminal(colWidths, flexibleCols, getTerminalWidth())
	rows = truncateCells(rows, colWidths, flexibleCols)

	writeRow(stdout, headers, colWidths)
	for _, row := range rows {
		writeRow(stdout, row, colWidths)
	}

	if colorEnabled {
		cmd := "gh issue view " + strconv.Itoa(issues[0].Number)
		if options.repo != "" {
			cmd += " --repo " + options.repo
		}
		writeOSC52(stdout, cmd)
		fmt.Fprintf(stdout, "\n→ %s  (copied — Ctrl+V to paste)\n", cmd)
	}

	return nil
}

func writeIssueListUsage(w io.Writer) {
	fmt.Fprint(w, issueListUsage)
}

const issueListUsage = `Usage:
  gh x issue list [flags]

List issues for the current repository.

Flags:
  -R, --repo string       Select another repository using the [HOST/]OWNER/REPO format
  -L, --limit int         Maximum number of issues to fetch (default 30)
  -s, --state string      Filter by state: open, closed, or all (default "open")
  -A, --author string     Filter by author
  -a, --assignee string   Filter by assignee
  -m, --milestone string  Filter by milestone number or title
  -l, --label string      Filter by label (repeatable)
  -S, --search string     Search issues with a GitHub search query
  -w, --web               Open the matching issues in the browser
`
