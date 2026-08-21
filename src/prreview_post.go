package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type structuredReviewOutput struct {
	Summary          string          `json:"summary"`
	Critical         []reviewFinding `json:"critical"`
	Medium           []reviewFinding `json:"medium"`
	Nitpicks         []reviewFinding `json:"nitpicks"`
	Recommendations  []string        `json:"recommendations"`
	ResidualRisk     string          `json:"residual_risk"`
	ApprovalEligible bool            `json:"approval_eligible"`
}

type reviewFinding struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Suggestion string `json:"suggestion"`
}

type pullRequestReviewRequest struct {
	Body     string                     `json:"body"`
	Event    string                     `json:"event"`
	Comments []pullRequestReviewComment `json:"comments,omitempty"`
}

type pullRequestReviewComment struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
	Body string `json:"body"`
}

type reviewPullFile struct {
	Filename string `json:"filename"`
	Patch    string `json:"patch"`
}

var (
	runReviewAgentCaptureFunc   = runReviewAgentCapture
	submitPullRequestReviewFunc = submitPullRequestReview
	fetchReviewCommentLinesFunc = fetchReviewCommentableLines
	reviewNowFunc               = time.Now
)

func (options prReviewOptions) postsReview() bool {
	return options.post || options.allowApprove
}

func reviewBoolEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func executePostedReview(options prReviewOptions, pr reviewPullRequest, invocation reviewAgentInvocation, stdout io.Writer, stderr io.Writer) error {
	rawOutput, err := runReviewAgentCaptureFunc(invocation, stderr)
	if err != nil {
		return err
	}

	result, err := parseStructuredReviewOutput(rawOutput)
	if err != nil {
		return err
	}

	commentableLines, err := fetchReviewCommentLinesFunc(options, pr)
	if err != nil {
		return err
	}

	request := buildPullRequestReviewRequest(options, pr, result, commentableLines, reviewNowFunc().UTC())
	if err := submitPullRequestReviewFunc(options, pr, request); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Posted %s review for PR #%d with %d inline comment(s).\n", request.Event, pr.Number, len(request.Comments))
	return nil
}

func buildStructuredReviewPrompt(options prReviewOptions, pr reviewPullRequest, extra string) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Review GitHub pull request #%d", pr.Number)
	if pr.Title != "" {
		fmt.Fprintf(&b, ": %s", pr.Title)
	}
	b.WriteString(".\n\n")
	b.WriteString("Operate in read-only mode. Do not edit files, commit, push, merge, approve, request changes, or post PR comments. gh-x will submit the GitHub review after validating your structured output.\n")
	b.WriteString("Report only actionable review findings, ordered by severity. Include file and line references only when you can verify them against the pull request diff.\n")
	if err := appendReviewModeInstructions(&b, options.mode); err != nil {
		return "", err
	}
	b.WriteString("\n")

	b.WriteString("Return only a JSON object with this schema and no markdown fence:\n")
	b.WriteString(`{
  "summary": "Short overall assessment.",
  "critical": [{"path": "relative/file.go", "line": 123, "title": "Issue title", "body": "Why this blocks the PR.", "suggestion": "Concrete fix."}],
  "medium": [{"path": "relative/file.go", "line": 123, "title": "Issue title", "body": "Why this matters.", "suggestion": "Concrete fix."}],
  "nitpicks": [{"path": "relative/file.go", "line": 123, "title": "Issue title", "body": "Small but worthwhile polish.", "suggestion": "Concrete fix."}],
  "recommendations": ["Optional follow-up."],
  "residual_risk": "Coverage, runtime, or uncertainty that remains.",
  "approval_eligible": false
}
`)
	b.WriteString("\nSet approval_eligible true only when strict approval criteria pass: no critical, medium, or nitpick findings and no meaningful residual risk.\n")
	b.WriteString("Use empty arrays when a section has no findings. If a finding cannot be tied to a diff line, set path to empty and line to 0 so it appears only in the formal review body.\n")

	b.WriteString("\nPull request context:\n")
	fmt.Fprintf(&b, "- Number: %d\n", pr.Number)
	appendPromptValue(&b, "URL", pr.URL)
	appendPromptValue(&b, "Repository", reviewRepoLabel(options))
	appendPromptValue(&b, "Author", reviewAuthorLabel(pr.Author))
	appendPromptValue(&b, "Base branch", pr.BaseRefName)
	appendPromptValue(&b, "Head branch", pr.HeadRefName)
	if body := strings.TrimSpace(pr.Body); body != "" {
		fmt.Fprintf(&b, "- Body:\n%s\n", indentPromptBlock(trimPromptText(body, reviewBodyLimit)))
	}

	b.WriteString("\nUseful read-only commands:\n")
	fmt.Fprintf(&b, "- gh pr view %s%s --json %s\n", reviewTarget(pr), reviewRepoFlag(options), reviewPRFields)
	fmt.Fprintf(&b, "- gh pr diff %s%s\n", reviewTarget(pr), reviewRepoFlag(options))
	if pr.BaseRefName != "" && pr.HeadRefName != "" {
		fmt.Fprintf(&b, "- git diff %s...%s\n", pr.BaseRefName, pr.HeadRefName)
	}

	if extra != "" {
		b.WriteString("\nAdditional instructions:\n")
		b.WriteString(extra)
		if !strings.HasSuffix(extra, "\n") {
			b.WriteByte('\n')
		}
	}

	return b.String(), nil
}

func runReviewAgentCapture(invocation reviewAgentInvocation, stderr io.Writer) (string, error) {
	cmd := exec.Command(invocation.Name, invocation.Args...)
	if invocation.PromptOnStdin {
		cmd.Stdin = strings.NewReader(invocation.Prompt)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("run %s: %w", invocation.Name, err)
	}
	return stdout.String(), nil
}

func parseStructuredReviewOutput(raw string) (structuredReviewOutput, error) {
	payload, err := extractJSONObject(raw)
	if err != nil {
		return structuredReviewOutput{}, err
	}

	var result structuredReviewOutput
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return structuredReviewOutput{}, fmt.Errorf("decode structured review output: %w", err)
	}
	normalizeStructuredReview(&result)
	return result, nil
}

func extractJSONObject(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
			if strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				lines = lines[:len(lines)-1]
			}
			text = strings.TrimSpace(strings.Join(lines, "\n"))
		}
	}

	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end < start {
		return "", errors.New("review agent did not return a JSON object")
	}
	return text[start : end+1], nil
}

func normalizeStructuredReview(result *structuredReviewOutput) {
	result.Summary = strings.TrimSpace(result.Summary)
	result.ResidualRisk = strings.TrimSpace(result.ResidualRisk)
	result.Critical = normalizeReviewFindings(result.Critical)
	result.Medium = normalizeReviewFindings(result.Medium)
	result.Nitpicks = normalizeReviewFindings(result.Nitpicks)
	for i := range result.Recommendations {
		result.Recommendations[i] = strings.TrimSpace(result.Recommendations[i])
	}
}

func normalizeReviewFindings(findings []reviewFinding) []reviewFinding {
	normalized := make([]reviewFinding, 0, len(findings))
	for _, finding := range findings {
		finding.Path = normalizeReviewPath(finding.Path)
		finding.Title = strings.TrimSpace(finding.Title)
		finding.Body = strings.TrimSpace(finding.Body)
		finding.Suggestion = strings.TrimSpace(finding.Suggestion)
		if finding.Title == "" && finding.Body == "" {
			continue
		}
		normalized = append(normalized, finding)
	}
	return normalized
}

func buildPullRequestReviewRequest(options prReviewOptions, pr reviewPullRequest, result structuredReviewOutput, commentableLines map[string]map[int]bool, reviewedAt time.Time) pullRequestReviewRequest {
	return pullRequestReviewRequest{
		Body:     renderFormalReview(options, pr, result, reviewedAt),
		Event:    selectPullRequestReviewEvent(options, result),
		Comments: buildInlineReviewComments(result, commentableLines),
	}
}

func selectPullRequestReviewEvent(options prReviewOptions, result structuredReviewOutput) string {
	if len(result.Critical) > 0 {
		return "REQUEST_CHANGES"
	}
	if canApproveStructuredReview(options, result) {
		return "APPROVE"
	}
	return "COMMENT"
}

func canApproveStructuredReview(options prReviewOptions, result structuredReviewOutput) bool {
	return options.allowApprove &&
		options.mode == "strict" &&
		result.ApprovalEligible &&
		reviewFindingCount(result) == 0 &&
		reviewResidualRiskClear(result.ResidualRisk)
}

func reviewFindingCount(result structuredReviewOutput) int {
	return len(result.Critical) + len(result.Medium) + len(result.Nitpicks)
}

func reviewResidualRiskClear(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.Trim(normalized, ".! ")
	switch normalized {
	case "", "none", "none identified", "no residual risk", "no meaningful residual risk":
		return true
	default:
		return false
	}
}

func renderFormalReview(options prReviewOptions, pr reviewPullRequest, result structuredReviewOutput, reviewedAt time.Time) string {
	var b strings.Builder
	b.WriteString("# PR Review Report\n\n")
	fmt.Fprintf(&b, "**PR:** #%d", pr.Number)
	if pr.Title != "" {
		fmt.Fprintf(&b, " - %s", pr.Title)
	}
	b.WriteString("\n")
	if pr.HeadRefName != "" || pr.BaseRefName != "" {
		fmt.Fprintf(&b, "**Branch:** `%s` -> `%s`\n", pr.HeadRefName, pr.BaseRefName)
	}
	fmt.Fprintf(&b, "**Reviewed:** %s\n", reviewedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "**Reviewer:** %s\n", options.reviewer)
	fmt.Fprintf(&b, "**Mode:** %s\n", options.mode)
	fmt.Fprintf(&b, "**Agent:** %s", reviewAgentLabel(options))
	if options.model != "" {
		fmt.Fprintf(&b, " / %s", options.model)
	}
	b.WriteString("\n\n")

	b.WriteString("## Summary\n\n")
	if result.Summary != "" {
		b.WriteString(result.Summary)
	} else {
		b.WriteString("No summary was provided by the review agent.")
	}
	b.WriteString("\n\n")

	appendFormalFindingSection(&b, "Critical Issues", result.Critical)
	appendFormalFindingSection(&b, "Medium Issues", result.Medium)
	appendFormalFindingSection(&b, "Nitpicks", result.Nitpicks)
	appendRecommendationsSection(&b, result.Recommendations)

	b.WriteString("## Residual Risk\n\n")
	if result.ResidualRisk != "" {
		b.WriteString(result.ResidualRisk)
	} else {
		b.WriteString("None identified.")
	}
	b.WriteString("\n")
	return b.String()
}

func appendFormalFindingSection(b *strings.Builder, title string, findings []reviewFinding) {
	fmt.Fprintf(b, "## %s\n\n", title)
	if len(findings) == 0 {
		b.WriteString("None.\n\n")
		return
	}
	for _, finding := range findings {
		location := reviewFindingLocation(finding)
		if location != "" {
			fmt.Fprintf(b, "- `%s`", location)
		} else {
			b.WriteString("-")
		}
		if finding.Title != "" {
			fmt.Fprintf(b, " **%s**", finding.Title)
		}
		if finding.Body != "" {
			fmt.Fprintf(b, " - %s", finding.Body)
		}
		if finding.Suggestion != "" {
			fmt.Fprintf(b, " Suggestion: %s", finding.Suggestion)
		}
		b.WriteString("\n")
	}
	b.WriteString("\n")
}

func appendRecommendationsSection(b *strings.Builder, recommendations []string) {
	b.WriteString("## Recommendations\n\n")
	wrote := false
	for _, recommendation := range recommendations {
		recommendation = strings.TrimSpace(recommendation)
		if recommendation == "" {
			continue
		}
		fmt.Fprintf(b, "- %s\n", recommendation)
		wrote = true
	}
	if !wrote {
		b.WriteString("None.\n")
	}
	b.WriteString("\n")
}

func reviewFindingLocation(finding reviewFinding) string {
	if finding.Path == "" || finding.Line <= 0 {
		return ""
	}
	return finding.Path + ":" + strconv.Itoa(finding.Line)
}

func buildInlineReviewComments(result structuredReviewOutput, commentableLines map[string]map[int]bool) []pullRequestReviewComment {
	var comments []pullRequestReviewComment
	comments = appendReviewComments(comments, "CRITICAL", "🔴", result.Critical, commentableLines)
	comments = appendReviewComments(comments, "MEDIUM", "🟡", result.Medium, commentableLines)
	comments = appendReviewComments(comments, "NITPICK", "🟢", result.Nitpicks, commentableLines)
	return comments
}

func appendReviewComments(comments []pullRequestReviewComment, severity string, marker string, findings []reviewFinding, commentableLines map[string]map[int]bool) []pullRequestReviewComment {
	for _, finding := range findings {
		if !isReviewLineCommentable(commentableLines, finding.Path, finding.Line) {
			continue
		}
		comments = append(comments, pullRequestReviewComment{
			Path: finding.Path,
			Line: finding.Line,
			Side: "RIGHT",
			Body: formatInlineReviewComment(severity, marker, finding),
		})
	}
	return comments
}

func formatInlineReviewComment(severity string, marker string, finding reviewFinding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**[%s]** %s", severity, marker)
	if finding.Title != "" {
		fmt.Fprintf(&b, " %s", finding.Title)
	}
	if finding.Body != "" {
		fmt.Fprintf(&b, "\n\n%s", finding.Body)
	}
	if finding.Suggestion != "" {
		fmt.Fprintf(&b, "\n\nSuggestion: %s", finding.Suggestion)
	}
	return b.String()
}

func isReviewLineCommentable(commentableLines map[string]map[int]bool, path string, line int) bool {
	if line <= 0 {
		return false
	}
	lines := commentableLines[normalizeReviewPath(path)]
	return lines != nil && lines[line]
}

func fetchReviewCommentableLines(options prReviewOptions, pr reviewPullRequest) (map[string]map[int]bool, error) {
	owner, name, err := resolveRepo(options.repo)
	if err != nil {
		return nil, err
	}

	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/files?per_page=100", owner, name, pr.Number)
	stdout, stderr, err := ghExecFunc("api", endpoint, "--paginate", "--slurp")
	if err != nil {
		return nil, wrapExecError(fmt.Errorf("gh api pull request files: %w", err), stderr.String())
	}

	files, err := decodeReviewPullFiles(stdout.Bytes())
	if err != nil {
		return nil, err
	}

	commentableLines := make(map[string]map[int]bool)
	for _, file := range files {
		path := normalizeReviewPath(file.Filename)
		if path == "" || file.Patch == "" {
			continue
		}
		lines := commentableLinesForPatch(file.Patch)
		if len(lines) > 0 {
			commentableLines[path] = lines
		}
	}
	return commentableLines, nil
}

func decodeReviewPullFiles(data []byte) ([]reviewPullFile, error) {
	var pages [][]reviewPullFile
	if err := json.Unmarshal(data, &pages); err == nil {
		var files []reviewPullFile
		for _, page := range pages {
			files = append(files, page...)
		}
		return files, nil
	}

	var files []reviewPullFile
	if err := json.Unmarshal(data, &files); err != nil {
		return nil, fmt.Errorf("decode gh api pull request files output: %w", err)
	}
	return files, nil
}

func commentableLinesForPatch(patch string) map[int]bool {
	lines := make(map[int]bool)
	currentLine := 0
	inHunk := false
	for _, patchLine := range strings.Split(patch, "\n") {
		if strings.HasPrefix(patchLine, "@@") {
			nextLine, ok := parsePatchNewStart(patchLine)
			currentLine = nextLine
			inHunk = ok
			continue
		}
		if !inHunk || strings.HasPrefix(patchLine, `\`) {
			continue
		}
		switch {
		case strings.HasPrefix(patchLine, "+") && !strings.HasPrefix(patchLine, "+++"):
			lines[currentLine] = true
			currentLine++
		case strings.HasPrefix(patchLine, "-") && !strings.HasPrefix(patchLine, "---"):
			continue
		default:
			lines[currentLine] = true
			currentLine++
		}
	}
	return lines
}

func parsePatchNewStart(hunk string) (int, bool) {
	plus := strings.Index(hunk, "+")
	if plus < 0 {
		return 0, false
	}
	rest := hunk[plus+1:]
	end := len(rest)
	for i, r := range rest {
		if r == ',' || r == ' ' || r == '@' {
			end = i
			break
		}
	}
	if end == 0 {
		return 0, false
	}
	line, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return line, true
}

func normalizeReviewPath(path string) string {
	return strings.Trim(strings.ReplaceAll(strings.TrimSpace(path), "\\", "/"), "/")
}

func submitPullRequestReview(options prReviewOptions, pr reviewPullRequest, request pullRequestReviewRequest) error {
	owner, name, err := resolveRepo(options.repo)
	if err != nil {
		return err
	}

	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode pull request review: %w", err)
	}

	endpoint := fmt.Sprintf("repos/%s/%s/pulls/%d/reviews", owner, name, pr.Number)
	cmd := exec.Command("gh", "api", endpoint, "--method", "POST", "--input", "-")
	cmd.Stdin = bytes.NewReader(data)
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return wrapExecError(fmt.Errorf("gh api create pull request review: %w", err), stderr.String())
	}
	return nil
}
