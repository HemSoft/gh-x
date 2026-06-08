package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/muesli/termenv"
)

type statusSummary struct {
	Branch           string
	Upstream         string
	Ahead            int
	Behind           int
	Staged           int
	Modified         int
	Deleted          int
	Renamed          int
	Untracked        int
	Conflicted       int
	DanglingBranches int
	OpenIssues       *int
	OpenPRs          *int
}

type statusFileCounts struct {
	Staged     int
	Modified   int
	Deleted    int
	Renamed    int
	Untracked  int
	Conflicted int
}

func runStatus(args []string, stdout io.Writer, stderr io.Writer) error {
	if err := parseStatusArgs(args, stderr); err != nil {
		if errors.Is(err, errHelpDisplayed) {
			return nil
		}
		return err
	}

	summary, err := fetchStatusSummaryFunc()
	if err != nil {
		return err
	}

	return renderStatus(stdout, summary, term.FromEnv().IsColorEnabled())
}

func parseStatusArgs(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		writeStatusUsage(stderr)
	}

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelpDisplayed
		}
		return err
	}

	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), ", "))
	}

	return nil
}

var fetchStatusSummaryFunc = fetchStatusSummary

func fetchStatusSummary() (statusSummary, error) {
	output, err := statusCommandFunc("git", "status", "--porcelain=v2", "--branch")
	if err != nil {
		return statusSummary{}, fmt.Errorf("git status: %w", err)
	}

	summary := parseGitStatus(output)

	branches, err := countDanglingBranches()
	if err == nil {
		summary.DanglingBranches = branches
	}

	if issues, err := countOpenGitHubItems("issue"); err == nil {
		summary.OpenIssues = &issues
	}
	if prs, err := countOpenGitHubItems("pr"); err == nil {
		summary.OpenPRs = &prs
	}

	return summary, nil
}

var statusCommandFunc = runStatusCommand

func runStatusCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(output))
		if text != "" {
			return "", fmt.Errorf("%s: %w", text, err)
		}
		return "", err
	}
	return string(output), nil
}

func parseGitStatus(output string) statusSummary {
	var summary statusSummary
	seen := make(map[string]bool)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}

		switch {
		case strings.HasPrefix(line, "# "):
			parseGitStatusHeader(line, &summary)
		case strings.HasPrefix(line, "? "):
			path := strings.TrimPrefix(line, "? ")
			if markSeen(seen, path) {
				summary.Untracked++
			}
		case strings.HasPrefix(line, "u "):
			if path := statusPath(line); markSeen(seen, path) {
				summary.Conflicted++
			}
		case strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 "):
			if path := statusPath(line); markSeen(seen, path) {
				applyStatusXY(statusXY(line), &summary)
			}
		}
	}

	return summary
}

func parseGitStatusHeader(line string, summary *statusSummary) {
	switch {
	case strings.HasPrefix(line, "# branch.head "):
		summary.Branch = strings.TrimPrefix(line, "# branch.head ")
	case strings.HasPrefix(line, "# branch.upstream "):
		summary.Upstream = strings.TrimPrefix(line, "# branch.upstream ")
	case strings.HasPrefix(line, "# branch.ab "):
		fields := strings.Fields(strings.TrimPrefix(line, "# branch.ab "))
		if len(fields) == 2 {
			summary.Ahead = parseSignedCount(fields[0])
			summary.Behind = parseSignedCount(fields[1])
		}
	}
}

func parseSignedCount(value string) int {
	value = strings.TrimPrefix(value, "+")
	n, _ := strconv.Atoi(value)
	if n < 0 {
		return -n
	}
	return n
}

func statusXY(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

func statusPath(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return line
	}
	return fields[len(fields)-1]
}

func markSeen(seen map[string]bool, path string) bool {
	if path == "" || seen[path] {
		return false
	}
	seen[path] = true
	return true
}

func applyStatusXY(xy string, summary *statusSummary) {
	if len(xy) < 2 {
		return
	}

	x := xy[0]
	y := xy[1]

	if x != '.' {
		summary.Staged++
	}
	if x == 'R' {
		summary.Renamed++
	}
	if x == 'M' || y == 'M' {
		summary.Modified++
	}
	if x == 'D' || y == 'D' {
		summary.Deleted++
	}
}

func countDanglingBranches() (int, error) {
	output, err := statusCommandFunc("git", "for-each-ref", "--format=%(refname:short)|%(upstream:track)", "refs/heads")
	if err != nil {
		return 0, err
	}

	count := 0
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(strings.ToLower(line), "gone") {
			count++
		}
	}
	return count, nil
}

func countOpenGitHubItems(kind string) (int, error) {
	output, err := statusCommandFunc("gh", kind, "list", "--state", "open", "--limit", "1000", "--json", "number", "--jq", "length")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(output))
}

func renderStatus(stdout io.Writer, summary statusSummary, colorEnabled bool) error {
	styler := newTableStyler(stdout, colorEnabled)

	fmt.Fprintln(stdout, styleBranchStatus(styler, summary))
	fmt.Fprintln(stdout, styleChangeStatus(styler, summary))
	fmt.Fprintln(stdout, styleRepoCounts(styler, summary))
	return nil
}

func styleBranchStatus(styler tableStyler, summary statusSummary) string {
	text := branchStatusText(summary)
	if summary.Behind > 0 {
		return styler.colored(text, termenv.ANSIRed).styled
	}
	if summary.Ahead > 0 || summary.Upstream == "" {
		return styler.colored(text, termenv.ANSIYellow).styled
	}
	return styler.colored(text, termenv.ANSIGreen).styled
}

func branchStatusText(summary statusSummary) string {
	if summary.Upstream == "" {
		if summary.Branch == "" {
			return "No upstream configured."
		}
		return fmt.Sprintf("On %s. No upstream configured.", summary.Branch)
	}

	switch {
	case summary.Ahead == 0 && summary.Behind == 0:
		return fmt.Sprintf("Up to date with %s.", summary.Upstream)
	case summary.Ahead > 0 && summary.Behind == 0:
		return fmt.Sprintf("Ahead of %s by %s.", summary.Upstream, plural(summary.Ahead, "commit", "commits"))
	case summary.Ahead == 0 && summary.Behind > 0:
		return fmt.Sprintf("Behind %s by %s.", summary.Upstream, plural(summary.Behind, "commit", "commits"))
	default:
		return fmt.Sprintf("Diverged from %s: %d ahead, %d behind.", summary.Upstream, summary.Ahead, summary.Behind)
	}
}

func styleChangeStatus(styler tableStyler, summary statusSummary) string {
	text := changeStatusText(summary)
	if hasWorkingChanges(summary) {
		return styler.colored(text, termenv.ANSIYellow).styled
	}
	return styler.colored(text, termenv.ANSIGreen).styled
}

func hasWorkingChanges(summary statusSummary) bool {
	return summary.Staged+summary.Modified+summary.Deleted+summary.Renamed+summary.Untracked+summary.Conflicted > 0
}

func changeStatusText(summary statusSummary) string {
	parts := make([]string, 0, 6)
	appendCount := func(count int, singular, pluralText string) {
		if count > 0 {
			parts = append(parts, plural(count, singular, pluralText))
		}
	}

	appendCount(summary.Staged, "staged file", "staged files")
	appendCount(summary.Modified, "modified file", "modified files")
	appendCount(summary.Deleted, "deleted file", "deleted files")
	appendCount(summary.Renamed, "renamed file", "renamed files")
	appendCount(summary.Untracked, "untracked file", "untracked files")
	appendCount(summary.Conflicted, "conflicted file", "conflicted files")

	if len(parts) == 0 {
		return "Clean working tree."
	}
	return strings.Join(parts, ", ") + "."
}

func styleRepoCounts(styler tableStyler, summary statusSummary) string {
	return strings.Join([]string{
		styleCount(styler, "Branches", summary.DanglingBranches, "dangling"),
		styleOptionalCount(styler, "Issues", summary.OpenIssues, "open"),
		styleOptionalCount(styler, "PRs", summary.OpenPRs, "open"),
	}, " · ")
}

func styleCount(styler tableStyler, label string, count int, suffix string) string {
	text := fmt.Sprintf("%s: %d %s", label, count, suffix)
	if count == 0 {
		return styler.colored(text, termenv.ANSIGreen).styled
	}
	return styler.colored(text, termenv.ANSIYellow).styled
}

func styleOptionalCount(styler tableStyler, label string, count *int, suffix string) string {
	if count == nil {
		return styler.dim(fmt.Sprintf("%s: unavailable", label)).styled
	}
	text := fmt.Sprintf("%s: %d %s", label, *count, suffix)
	if *count == 0 {
		return styler.colored(text, termenv.ANSIGreen).styled
	}
	return styler.colored(text, termenv.ANSICyan).styled
}

func plural(count int, singular, pluralText string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, pluralText)
}

func writeStatusUsage(w io.Writer) {
	fmt.Fprint(w, statusUsage)
}

const statusUsage = `Usage:
  gh x status

Show a compact repository status summary.
`
