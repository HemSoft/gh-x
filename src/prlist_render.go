package main

import (
	"encoding/json"
	"fmt"
	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/muesli/termenv"
	"io"
	"strings"
)

func (s tableStyler) numberCell(number int, url string) tableCell {
	return s.linkCell(fmt.Sprintf("#%d", number), url, termenv.ANSIGreen)
}

func (s tableStyler) stateCell(state string) tableCell {
	switch state {
	case "open":
		return s.colored(state, termenv.ANSIGreen)
	case "draft":
		return s.colored(state, termenv.ANSIYellow)
	case "closed":
		return s.colored(state, termenv.ANSIRed)
	case "merged":
		return s.colored(state, termenv.ANSIMagenta)
	default:
		return s.plain(state)
	}
}

func (s tableStyler) reviewCell(review string) tableCell {
	switch review {
	case "approved":
		return s.colored("✓", termenv.ANSIGreen)
	case "changes":
		return s.colored("✗", termenv.ANSIRed)
	case "review":
		return s.colored("•", termenv.ANSIYellow)
	default:
		return s.plain(review)
	}
}

func (s tableStyler) checksCell(checks string) tableCell {
	switch checks {
	case "pass", "review":
		return s.colored(checks, termenv.ANSIGreen)
	case "fail":
		return s.colored(checks, termenv.ANSIRed)
	case "pending":
		return s.colored(checks, termenv.ANSIYellow)
	case "merge":
		return s.colored(checks, termenv.ANSIRed)
	default:
		return s.plain(checks)
	}
}

func (s tableStyler) branchCell(branch string) tableCell {
	return s.colored(branch, termenv.ANSICyan)
}

func (s tableStyler) approvalCell(count int) tableCell {
	text := fmt.Sprintf("%d", count)
	if count > 0 {
		return s.colored(text, termenv.ANSIGreen)
	}
	return s.dim(text)
}

func (s tableStyler) commentsCell(comments string, aiClean *bool) tableCell {
	base := s.commentsCellBase(comments)
	if aiClean == nil || !*aiClean {
		return base
	}
	// AI reviewed cleanly but left no threads — show "0/0" instead of "-"
	if comments == "-" {
		base = s.colored("0/0", termenv.ANSIGreen)
	}
	return tableCell{
		text:   base.text + "!",
		styled: base.styled + s.output.String("!").Foreground(termenv.ANSIBrightGreen).String(),
	}
}

func (s tableStyler) commentsCellBase(comments string) tableCell {
	if comments == "-" || comments == "?" {
		return s.plain(comments)
	}
	parts := strings.SplitN(comments, "/", 2)
	if len(parts) == 2 && parts[0] == parts[1] {
		return s.colored(comments, termenv.ANSIGreen)
	}
	if len(parts) == 2 && parts[0] == "0" {
		return s.colored(comments, termenv.ANSIRed)
	}
	return s.colored(comments, termenv.ANSIYellow)
}

func (s tableStyler) aiReviewCell(aiReview string) tableCell {
	switch aiReview {
	case "pass":
		return s.colored(aiReview, termenv.ANSIGreen)
	case "fail":
		return s.colored(aiReview, termenv.ANSIRed)
	default:
		return s.plain(aiReview)
	}
}

func renderListOutput(stdout io.Writer, options listOptions, rendered []displayPullRequest) error {
	if options.json {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rendered)
	}
	return renderTable(stdout, options, rendered)
}

func renderTable(stdout io.Writer, options listOptions, pullRequests []displayPullRequest) error {
	if len(pullRequests) > 0 {
		if repoLabel := resolveRepoLabel(options.repo); repoLabel != "" {
			fmt.Fprintf(stdout, "Pull requests for %s\n\n", repoLabel)
		}
	}
	colorEnabled := term.FromEnv().IsColorEnabled()
	return renderTableWithStyle(stdout, options, pullRequests, colorEnabled)
}

func renderTableWithStyle(stdout io.Writer, options listOptions, pullRequests []displayPullRequest, colorEnabled bool) error {
	if len(pullRequests) == 0 {
		if isOpenPullRequestBacklog(options) {
			writeBacklogPraise(stdout)
		} else {
			fmt.Fprintln(stdout, "No pull requests found.")
		}
		return nil
	}

	if err := renderPullRequestRows(stdout, pullRequests, colorEnabled); err != nil {
		return err
	}

	if options.limit > 0 && len(pullRequests) >= options.limit {
		fmt.Fprintf(stdout, "\nShowing %d pull requests (limit reached). Use --limit to show more.\n", options.limit)
	}

	return nil
}

func renderPullRequestRows(stdout io.Writer, pullRequests []displayPullRequest, colorEnabled bool) error {
	styler := newTableStyler(stdout, colorEnabled)

	headerLabels := []string{"#", "Issues", "Title", "Author", "State", "Rev", "AI", "Appv", "Checks", "Cmts", "Branch", "Upd"}
	headers := make([]tableCell, len(headerLabels))
	for i, label := range headerLabels {
		headers[i] = styler.header(label)
	}

	rows := make([][]tableCell, len(pullRequests))
	for i, pr := range pullRequests {
		rows[i] = []tableCell{
			styler.numberCell(pr.Number, pr.URL),
			styler.relationshipCell(pr.Issues, pr.issueRefs),
			styler.plain(pr.Title),
			styler.plain(pr.Author),
			styler.stateCell(pr.State),
			styler.reviewCell(pr.Review),
			styler.aiReviewCell(pr.AIReview),
			styler.approvalCell(pr.Approvals),
			styler.checksCell(pr.Checks),
			styler.commentsCell(pr.Comments, pr.AIClean),
			styler.branchCell(pr.Branch),
			styler.dim(pr.Updated),
		}
	}

	colWidths := computeColumnWidths(headers, rows)

	// Fit to terminal: Issues(1), Title(2), Author(3), Branch(10) are flexible
	flexibleCols := []int{1, 2, 3, 10}
	colWidths = fitColumnsToTerminal(colWidths, flexibleCols, getTerminalWidth())
	rows = truncateCells(rows, colWidths, flexibleCols)

	writeTableHeader(stdout, styler, headers, colWidths)
	for _, row := range rows {
		writeRow(stdout, row, colWidths)
	}

	return nil
}

// resolveOrgHint returns the owner half of an explicit repo override, or the
// current repo's owner when no override is given. Empty on failure. Handles
// the optional [HOST/] prefix by taking the penultimate path segment.
func resolveOrgHint(repoOverride string) string {
	if repoOverride != "" {
		parts := strings.Split(strings.Trim(repoOverride, "/"), "/")
		if len(parts) >= 2 {
			return parts[len(parts)-2]
		}
		return ""
	}
	owner, _, err := resolveRepo("")
	if err != nil {
		return ""
	}
	return owner
}

func resolveRepoLabel(repoOverride string) string {
	if repoOverride != "" {
		return repoOverride
	}

	owner, name, err := resolveRepo("")
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s/%s", owner, name)
}
