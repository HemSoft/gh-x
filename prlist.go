package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	gh "github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/repository"
	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/muesli/termenv"
)

const jsonFields = "number,title,author,state,isDraft,reviewDecision,statusCheckRollup,updatedAt,headRefName,baseRefName,url,latestReviews,mergeable"

// executeListFunc is swapped in tests to avoid real API calls.
var executeListFunc = executeList

// ghExecFunc wraps gh.Exec for testability in author resolution functions.
var ghExecFunc = gh.Exec

// fetchPRSupplementalBatchFunc is swapped in tests to avoid real API calls.
var fetchPRSupplementalBatchFunc = fetchPRSupplementalBatch

type listOptions struct {
	repo      string
	limit     int
	state     string
	author    string
	assignee  string
	app       string
	base      string
	head      string
	search    string
	draftOnly bool
	web       bool
	json      bool
	labels    stringSliceFlag
}

type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	return strings.Join(*s, ",")
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}

type pullRequest struct {
	Number            int         `json:"number"`
	Title             string      `json:"title"`
	State             string      `json:"state"`
	IsDraft           bool        `json:"isDraft"`
	ReviewDecision    string      `json:"reviewDecision"`
	StatusCheckRollup []checkItem `json:"statusCheckRollup"`
	UpdatedAt         time.Time   `json:"updatedAt"`
	HeadRefName       string      `json:"headRefName"`
	BaseRefName       string      `json:"baseRefName"`
	URL               string      `json:"url"`
	Author            *author     `json:"author"`
	LatestReviews     []review    `json:"latestReviews"`
	Mergeable         string      `json:"mergeable"`
}

type author struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

type review struct {
	State  string  `json:"state"`
	Author *author `json:"author"`
}

// checkItem represents a single entry in the statusCheckRollup array.
// CheckRun items use Status+Conclusion; StatusContext items use State.
type checkItem struct {
	Typename   string `json:"__typename"`
	Name       string `json:"name"`    // CheckRun name
	Context    string `json:"context"` // StatusContext context
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type displayPullRequest struct {
	Number    int    `json:"number"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	State     string `json:"state"`
	Review    string `json:"review"`
	Approvals int    `json:"approvals"`
	Checks    string `json:"checks"`
	Comments  string `json:"comments"`
	AIReview  string `json:"aiReview"`
	AIClean   *bool  `json:"aiClean,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Updated   string `json:"updated"`
	URL       string `json:"url"`
	Repo      string `json:"repo,omitempty"`

	updatedAt time.Time // unexported; used for sorting
}

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
		return s.colored(review, termenv.ANSIGreen)
	case "changes":
		return s.colored(review, termenv.ANSIRed)
	case "review":
		return s.colored(review, termenv.ANSIYellow)
	default:
		return s.plain(review)
	}
}

func (s tableStyler) checksCell(checks string) tableCell {
	switch checks {
	case "pass":
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

func defaultListOptions() listOptions {
	return listOptions{
		limit: 30,
		state: "open",
	}
}

func executeList(options listOptions, stdout io.Writer) error {
	arguments := buildListArgs(options)
	commandOutput, commandError, err := gh.Exec(arguments...)
	if err != nil {
		return wrapExecError(err, commandError.String())
	}
	if options.web {
		return nil
	}
	var pullRequests []pullRequest
	if err := json.Unmarshal(commandOutput.Bytes(), &pullRequests); err != nil {
		return fmt.Errorf("decode gh pr list output: %w", err)
	}
	now := time.Now().UTC()
	supplemental, supplementalFailed, repoOwner, repoName := fetchSupplementalData(options.repo, pullRequests)
	requiredByBranch := fetchRequiredChecks(repoOwner, repoName, pullRequests)
	rendered := enrichPullRequests(pullRequests, supplemental, supplementalFailed, requiredByBranch, now)
	return renderListOutput(stdout, options, rendered)
}

func wrapExecError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return fmt.Errorf("%w: %s", err, stderr)
	}
	return err
}

func renderListOutput(stdout io.Writer, options listOptions, rendered []displayPullRequest) error {
	if options.json {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rendered)
	}
	return renderTable(stdout, options, rendered)
}

// runView handles "gh x pr view <number>" and "gh x pr <number>".
// parseViewArgs extracts the PR number and optional repo from the view command arguments.
func parseViewArgs(args []string) (number, repo string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "--repo" || arg == "-R") && i+1 < len(args) {
			i++
			repo = args[i]
		} else if !looksLikeFlag(arg) {
			if number != "" {
				return "", "", fmt.Errorf("usage: gh x pr view <number> [--repo OWNER/REPO]")
			}
			number = arg
		}
	}
	if number == "" {
		return "", "", fmt.Errorf("usage: gh x pr view <number> [--repo OWNER/REPO]")
	}
	return number, repo, nil
}

func runView(args []string, stdout io.Writer, _ io.Writer) error {
	number, repo, err := parseViewArgs(args)
	if err != nil {
		return err
	}

	// Fetch single PR via gh pr view --json
	ghArgs := []string{"pr", "view", number, "--json", jsonFields}
	if repo != "" {
		ghArgs = append(ghArgs, "--repo", repo)
	}
	commandOutput, commandError, err := gh.Exec(ghArgs...)
	if err != nil {
		return wrapExecError(err, commandError.String())
	}

	var pr pullRequest
	if err := json.Unmarshal(commandOutput.Bytes(), &pr); err != nil {
		return fmt.Errorf("decode gh pr view output: %w", err)
	}

	prs := []pullRequest{pr}
	supplemental, supplementalFailed, repoOwner, repoName := fetchSupplementalData(repo, prs)
	requiredByBranch := fetchRequiredChecks(repoOwner, repoName, prs)
	rendered := enrichPullRequests(prs, supplemental, supplementalFailed, requiredByBranch, time.Now().UTC())

	// Render as a single-row table with no limit footer
	opts := defaultListOptions()
	opts.repo = repo
	opts.limit = 0 // suppress "limit reached" footer
	return renderTable(stdout, opts, rendered)
}

// fetchSupplementalData retrieves supplemental PR data via GraphQL (best-effort).
func fetchSupplementalData(repo string, prs []pullRequest) (map[int]prSupplementalInfo, bool, string, string) {
	owner, name, err := resolveRepo(repo)
	if err != nil {
		return nil, true, "", ""
	}
	numbers := make([]int, len(prs))
	for i, pr := range prs {
		numbers[i] = pr.Number
	}
	fetched, err := fetchPRSupplemental(owner, name, numbers)
	if err != nil {
		return nil, true, owner, name
	}
	return fetched, false, owner, name
}

// fetchRequiredChecks retrieves required check contexts per base branch (best-effort).
func fetchRequiredChecks(owner, name string, prs []pullRequest) map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	if owner == "" {
		return result
	}
	for _, base := range uniqueBaseBranches(prs) {
		if ctx := fetchRequiredCheckContexts(owner, name, base); ctx != nil {
			result[base] = ctx
		}
	}
	return result
}

func uniqueBaseBranches(prs []pullRequest) []string {
	seen := make(map[string]bool)
	var branches []string
	for _, pr := range prs {
		if !seen[pr.BaseRefName] {
			seen[pr.BaseRefName] = true
			branches = append(branches, pr.BaseRefName)
		}
	}
	return branches
}

// enrichPullRequests builds display PRs by merging supplemental data and
// applying required-check downgrade logic.
func enrichPullRequests(prs []pullRequest, supplemental map[int]prSupplementalInfo, supplementalFailed bool, requiredByBranch map[string]map[string]bool, now time.Time) []displayPullRequest {
	rendered := make([]displayPullRequest, 0, len(prs))
	for _, pr := range prs {
		dp := buildDisplayPullRequest(pr, now)
		applySupplementalInfo(&dp, supplemental, pr.Number, supplementalFailed)
		downgradeChecksIfMissing(&dp, requiredByBranch, pr.BaseRefName, pr.StatusCheckRollup)
		rendered = append(rendered, dp)
	}
	return rendered
}

func applySupplementalInfo(dp *displayPullRequest, supplemental map[int]prSupplementalInfo, number int, failed bool) {
	if failed {
		dp.Comments = "?"
		dp.AIReview = "?"
		return
	}
	info := supplemental[number]
	dp.Comments = formatComments(info.Threads)
	dp.AIReview = info.AIReview
	if info.AIClean {
		dp.AIClean = &info.AIClean
	}
	dp.Approvals = info.Approvals
	if dp.AIReview == "" {
		dp.AIReview = "-"
	}
}

func downgradeChecksIfMissing(dp *displayPullRequest, requiredByBranch map[string]map[string]bool, base string, checkItems []checkItem) {
	if dp.Checks != "pass" {
		return
	}
	required, ok := requiredByBranch[base]
	if !ok {
		return
	}
	reported := extractReportedContexts(checkItems)
	for ctx := range required {
		if !reported[ctx] {
			dp.Checks = "pending"
			return
		}
	}
}

// appendNonEmpty appends a flag and its value only when value is non-empty.
func appendNonEmpty(args []string, flag, value string) []string {
	if value == "" {
		return args
	}
	return append(args, flag, value)
}

func buildListArgs(options listOptions) []string {
	arguments := []string{"pr", "list"}

	if options.web {
		arguments = append(arguments, "--web")
	} else {
		arguments = append(arguments, "--json", jsonFields)
	}

	arguments = appendNonEmpty(arguments, "--repo", options.repo)
	arguments = append(arguments, "--limit", fmt.Sprintf("%d", options.limit))
	arguments = append(arguments, "--state", options.state)
	arguments = appendNonEmpty(arguments, "--author", options.author)
	arguments = appendNonEmpty(arguments, "--assignee", options.assignee)
	arguments = appendNonEmpty(arguments, "--app", options.app)
	arguments = appendNonEmpty(arguments, "--base", options.base)
	arguments = appendNonEmpty(arguments, "--head", options.head)
	arguments = appendNonEmpty(arguments, "--search", options.search)

	if options.draftOnly {
		arguments = append(arguments, "--draft")
	}

	for _, label := range options.labels {
		arguments = append(arguments, "--label", label)
	}

	return arguments
}

func buildDisplayPullRequest(pullRequest pullRequest, now time.Time) displayPullRequest {
	authorName := "-"
	if pullRequest.Author != nil && pullRequest.Author.Login != "" {
		authorName = formatAuthor(pullRequest.Author.Login, pullRequest.Author.Name)
	}

	return displayPullRequest{
		Number:    pullRequest.Number,
		Title:     trimTitle(pullRequest.Title, 51),
		Author:    authorName,
		State:     normalizeState(pullRequest.State, pullRequest.IsDraft),
		Review:    normalizeReviewDecision(pullRequest.ReviewDecision),
		Approvals: countApprovals(pullRequest.LatestReviews),
		Checks:    resolveChecksState(pullRequest),
		Comments:  "-",
		AIReview:  "-",
		Branch:    formatBranch(pullRequest.HeadRefName),
		Updated:   formatRelativeTime(pullRequest.UpdatedAt, now),
		URL:       pullRequest.URL,
		updatedAt: pullRequest.UpdatedAt,
	}
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
		fmt.Fprintln(stdout, "No pull requests found.")
		return nil
	}

	styler := newTableStyler(stdout, colorEnabled)

	headerLabels := []string{"#", "Title", "Author", "State", "Review", "AI", "Appv", "Checks", "Cmts", "Branch", "Updated"}
	headers := make([]tableCell, len(headerLabels))
	for i, label := range headerLabels {
		headers[i] = styler.dim(label)
	}

	rows := make([][]tableCell, len(pullRequests))
	for i, pr := range pullRequests {
		rows[i] = []tableCell{
			styler.numberCell(pr.Number, pr.URL),
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

	// Fit to terminal: Title(1), Author(2), Branch(9) are flexible
	flexibleCols := []int{1, 2, 9}
	colWidths = fitColumnsToTerminal(colWidths, flexibleCols, getTerminalWidth())
	rows = truncateCells(rows, colWidths, flexibleCols)

	writeRow(stdout, headers, colWidths)
	for _, row := range rows {
		writeRow(stdout, row, colWidths)
	}

	if options.limit > 0 && len(pullRequests) >= options.limit {
		fmt.Fprintf(stdout, "\nShowing %d pull requests (limit reached). Use --limit to show more.\n", options.limit)
	}

	return nil
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

func normalizeState(state string, isDraft bool) string {
	if isDraft {
		return "draft"
	}

	switch strings.ToUpper(state) {
	case "OPEN":
		return "open"
	case "CLOSED":
		return "closed"
	case "MERGED":
		return "merged"
	default:
		if state == "" {
			return "-"
		}

		return strings.ToLower(state)
	}
}

func normalizeReviewDecision(reviewDecision string) string {
	switch strings.ToUpper(reviewDecision) {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes"
	case "REVIEW_REQUIRED":
		return "review"
	case "":
		return "-"
	default:
		return strings.ToLower(reviewDecision)
	}
}

// classifyCheckItem determines whether a single status check item
// represents a failure or pending state.
func classifyCheckItem(item checkItem) (fail, pending bool) {
	if item.Typename == "StatusContext" {
		switch strings.ToUpper(item.State) {
		case "ERROR", "FAILURE":
			return true, false
		case "EXPECTED", "PENDING":
			return false, true
		}
		return false, false
	}
	// CheckRun
	switch strings.ToUpper(item.Conclusion) {
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED":
		return true, false
	case "":
		// No conclusion yet — still running
		return false, true
	}
	if strings.ToUpper(item.Status) != "COMPLETED" {
		return false, true
	}
	return false, false
}

// resolveChecksState returns the checks column value, surfacing merge
// conflicts ("merge") in preference to the underlying status check rollup.
func resolveChecksState(pr pullRequest) string {
	if strings.EqualFold(pr.Mergeable, "CONFLICTING") {
		return "merge"
	}
	return normalizeCheckState(pr.StatusCheckRollup)
}

func normalizeCheckState(items []checkItem) string {
	if len(items) == 0 {
		return "-"
	}

	hasFail := false
	hasPending := false
	for _, item := range items {
		f, p := classifyCheckItem(item)
		hasFail = hasFail || f
		hasPending = hasPending || p
	}

	switch {
	case hasFail:
		return "fail"
	case hasPending:
		return "pending"
	default:
		return "pass"
	}
}

func formatAuthor(login, name string) string {
	if name != "" {
		return name
	}
	return strings.TrimPrefix(login, "app/")
}

func formatBranch(head string) string {
	if head == "" {
		return "-"
	}
	if idx := strings.LastIndex(head, "/"); idx >= 0 {
		head = head[idx+1:]
	}
	if len(head) > 16 {
		head = head[:15] + "…"
	}
	return head
}

func formatRelativeTime(updatedAt time.Time, now time.Time) string {
	if updatedAt.IsZero() {
		return "-"
	}

	if now.Before(updatedAt) {
		return "0m"
	}

	age := now.Sub(updatedAt)
	switch {
	case age < time.Minute:
		return fmt.Sprintf("%ds", int(age.Seconds()))
	case age < time.Hour:
		return fmt.Sprintf("%dm", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh", int(age.Hours()))
	case age < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(age.Hours()/24))
	case age < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(age.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(age.Hours()/(24*365)))
	}
}

func countApprovals(reviews []review) int {
	count := 0
	for _, r := range reviews {
		if strings.EqualFold(r.State, "APPROVED") {
			count++
		}
	}
	return count
}

type reviewThreadInfo struct {
	Total    int
	Resolved int
}

type prSupplementalInfo struct {
	Threads   reviewThreadInfo
	AIReview  string
	AIClean   bool
	Approvals int
}

// aiReviewNode holds the fields needed to detect bot reviewer status.
type aiReviewNode struct {
	State        string
	AuthorLogin  string
	AuthorType   string
	CommentCount int
}

// aiReviewThread holds thread resolution state and authorship for AI review detection.
type aiReviewThread struct {
	AuthorLogin string
	AuthorType  string
	IsResolved  bool
}

func formatComments(info reviewThreadInfo) string {
	if info.Total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", info.Resolved, info.Total)
}

// countResolvedThreads counts the number of resolved threads from a slice.
func countResolvedThreads(threads []aiReviewThread) int {
	n := 0
	for _, t := range threads {
		if t.IsResolved {
			n++
		}
	}
	return n
}

// countUniqueApprovers counts unique logins from approved review nodes.
func countUniqueApprovers(logins []string) int {
	seen := make(map[string]bool, len(logins))
	for _, login := range logins {
		if login != "" {
			seen[strings.ToLower(login)] = true
		}
	}
	return len(seen)
}

// Known AI reviewer logins that don't use the [bot] suffix convention.
var knownAIReviewers = map[string]bool{
	"copilot-pull-request-reviewer": true,
}

func isAIReviewer(login string) bool {
	return strings.HasSuffix(login, "[bot]") || knownAIReviewers[login]
}

// classifyAIReviews scans review nodes for bot-authored reviews and returns
// whether a clean pass (no comments), issues, or bot participation was found.
// hasCleanPass is true when a bot review (APPROVED or COMMENTED) has zero
// review comments, indicating the reviewer found nothing to flag.
func classifyAIReviews(reviews []aiReviewNode) (hasCleanPass, hasIssues, hasBotReview bool) {
	for _, r := range reviews {
		if !isAIReviewer(r.AuthorLogin) && r.AuthorType != "Bot" {
			continue
		}
		hasBotReview = true

		switch strings.ToUpper(r.State) {
		case "APPROVED":
			if r.CommentCount == 0 {
				hasCleanPass = true
			} else {
				hasIssues = true
			}
		case "CHANGES_REQUESTED":
			hasIssues = true
		case "COMMENTED":
			if r.CommentCount == 0 {
				hasCleanPass = true
			} else {
				hasIssues = true
			}
		}
	}
	return
}

// isAIReviewClean returns true when a bot-authored review is present, gave a
// clean pass (APPROVED or COMMENTED state with zero review comments), and
// raised no issues across any bot review.
func isAIReviewClean(reviews []aiReviewNode) bool {
	hasCleanPass, hasIssues, hasBotReview := classifyAIReviews(reviews)
	return hasBotReview && hasCleanPass && !hasIssues
}

// allAIThreadsResolved returns true when at least one AI-authored thread exists
// and every such thread is resolved.
func allAIThreadsResolved(threads []aiReviewThread) bool {
	aiThreadCount := 0
	for _, t := range threads {
		if !isAIReviewer(t.AuthorLogin) && t.AuthorType != "Bot" {
			continue
		}
		aiThreadCount++
		if !t.IsResolved {
			return false
		}
	}
	return aiThreadCount > 0
}

// detectAIReview determines the AI review status by combining review state with
// thread resolution. A review that left comments is only "pass" when positive
// evidence exists that all AI-authored threads have been resolved.
func detectAIReview(reviews []aiReviewNode, threads []aiReviewThread) string {
	hasCleanPass, hasIssues, hasBotReview := classifyAIReviews(reviews)

	if !hasBotReview {
		return "-"
	}
	if hasIssues {
		if allAIThreadsResolved(threads) {
			return "pass"
		}
		return "fail"
	}
	if hasCleanPass {
		return "pass"
	}
	return "-"
}

// extractReportedContexts collects the context/check names from statusCheckRollup items.
func extractReportedContexts(items []checkItem) map[string]bool {
	contexts := make(map[string]bool)
	for _, item := range items {
		switch item.Typename {
		case "CheckRun":
			if item.Name != "" {
				contexts[item.Name] = true
			}
		case "StatusContext":
			if item.Context != "" {
				contexts[item.Context] = true
			}
		}
	}
	return contexts
}

// fetchRequiredCheckContexts returns the set of required status check context
// names for a branch, derived from repository rulesets. Best-effort: returns
// nil on error so callers fall back to per-item normalization only.
func fetchRequiredCheckContexts(owner, name, branch string) map[string]bool {
	endpoint := fmt.Sprintf("repos/%s/%s/rules/branches/%s", owner, name, url.PathEscape(branch))
	stdout, _, err := gh.Exec("api", endpoint)
	if err != nil {
		return nil
	}
	return parseRequiredCheckRules(stdout.Bytes())
}

func parseRequiredCheckRules(data []byte) map[string]bool {
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil
	}
	contexts := make(map[string]bool)
	for _, rule := range rules {
		if rule.Type == "required_status_checks" {
			for _, check := range rule.Parameters.RequiredStatusChecks {
				if check.Context != "" {
					contexts[check.Context] = true
				}
			}
		}
	}
	if len(contexts) == 0 {
		return nil
	}
	return contexts
}

func resolveRepo(repoOverride string) (string, string, error) {
	if repoOverride != "" {
		parts := strings.Split(repoOverride, "/")
		if len(parts) < 2 {
			return "", "", fmt.Errorf("invalid repo format: %s", repoOverride)
		}
		return parts[len(parts)-2], parts[len(parts)-1], nil
	}

	repo, err := repository.Current()
	if err == nil {
		return repo.Owner, repo.Name, nil
	}

	// Fall back to gh repo view for SSH aliases and non-standard remotes
	stdout, _, execErr := gh.Exec("repo", "view", "--json", "owner,name")
	if execErr != nil {
		return "", "", fmt.Errorf("repo resolution failed: %w; fallback: %v", err, execErr)
	}
	return parseRepoViewResponse(stdout.Bytes())
}

func parseRepoViewResponse(data []byte) (string, string, error) {
	var info struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return "", "", err
	}
	if info.Owner.Login == "" || info.Name == "" {
		return "", "", fmt.Errorf("could not resolve repo from gh repo view")
	}
	return info.Owner.Login, info.Name, nil
}

// resolveAuthorLoginFunc is swapped in tests to avoid real API calls.
var resolveAuthorLoginFunc = resolveAuthorLogin

// resolveAuthorLogin resolves an author value to a GitHub login.
// If the value contains no spaces, it's assumed to be a login already (with
// optional "@" prefix stripped). If it contains a space, it's treated as a
// display name and resolved via org member search (GraphQL), falling back to
// global GitHub user search.
func resolveAuthorLogin(author, org string) (string, error) {
	author = strings.TrimPrefix(author, "@")
	if !strings.Contains(author, " ") {
		return author, nil
	}

	// Try org-scoped member lookup first (most reliable for org repos).
	if org != "" {
		if login := resolveAuthorFromOrg(author, org); login != "" {
			return login, nil
		}
	}

	// Fall back to global GitHub user search.
	query := url.QueryEscape(author + " in:name")
	endpoint := fmt.Sprintf("search/users?q=%s&per_page=1", query)
	stdout, _, err := ghExecFunc("api", endpoint, "--jq", ".items[0].login")
	if err != nil {
		return "", fmt.Errorf("resolving author %q: %w", author, err)
	}
	login := strings.TrimSpace(stdout.String())
	if login == "" || login == "null" {
		return "", fmt.Errorf("no GitHub user found matching name %q", author)
	}
	return login, nil
}

// resolveAuthorFromOrg resolves a display name to a login within an org.
// Searches globally for matching users, then verifies org membership.
// Returns the matching login or empty string if not found.
func resolveAuthorFromOrg(name, org string) string {
	query := url.QueryEscape(name + " in:name")
	endpoint := fmt.Sprintf("search/users?q=%s&per_page=5", query)
	stdout, _, err := ghExecFunc("api", endpoint, "--jq", ".items[].login")
	if err != nil {
		return ""
	}
	logins := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for _, login := range logins {
		login = strings.TrimSpace(login)
		if login == "" || login == "null" {
			continue
		}
		memberEndpoint := fmt.Sprintf("orgs/%s/members/%s", org, login)
		_, _, memberErr := ghExecFunc("api", memberEndpoint)
		if memberErr == nil {
			return login
		}
	}
	return ""
}

func fetchPRSupplemental(owner, name string, prNumbers []int) (map[int]prSupplementalInfo, error) {
	if len(prNumbers) == 0 {
		return nil, nil
	}

	// Batch PRs to avoid exceeding Windows command-line length limits (~32K chars).
	// Each PR's query fragment is ~350 chars; batches of 30 stay well under the limit.
	const batchSize = 30
	result := make(map[int]prSupplementalInfo)
	for i := 0; i < len(prNumbers); i += batchSize {
		end := i + batchSize
		if end > len(prNumbers) {
			end = len(prNumbers)
		}
		batch, err := fetchPRSupplementalBatchFunc(owner, name, prNumbers[i:end])
		if err != nil {
			return nil, err
		}
		for k, v := range batch {
			result[k] = v
		}
	}
	return result, nil
}

func fetchPRSupplementalBatch(owner, name string, prNumbers []int) (map[int]prSupplementalInfo, error) {
	var queryParts []string
	for _, num := range prNumbers {
		queryParts = append(queryParts, fmt.Sprintf(
			`pr%d: pullRequest(number: %d) { number reviewThreads(first: 100) { totalCount nodes { isResolved comments(first: 1) { nodes { author { login __typename } } } } } latestReviews(first: 50) { nodes { state author { login __typename } comments { totalCount } } } approvedReviews: reviews(states: [APPROVED], last: 50) { nodes { author { login } } } }`,
			num, num,
		))
	}

	query := fmt.Sprintf(
		`query { repository(owner: %q, name: %q) { %s } }`,
		owner, name, strings.Join(queryParts, " "),
	)

	stdout, _, err := gh.Exec("api", "graphql", "-f", fmt.Sprintf("query=%s", query))
	if err != nil {
		return nil, err
	}
	return parseSupplementalResponse(stdout.Bytes())
}

func parseSupplementalResponse(data []byte) (map[int]prSupplementalInfo, error) {
	var resp struct {
		Data struct {
			Repository map[string]json.RawMessage `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	result := make(map[int]prSupplementalInfo)
	for _, raw := range resp.Data.Repository {
		num, info, ok := parsePRSupplementalNode(raw)
		if !ok {
			continue
		}
		result[num] = info
	}
	return result, nil
}

// parsePRSupplementalNode parses a single PR's supplemental data from raw JSON.
// Returns the PR number, supplemental info, and whether parsing succeeded.
func parsePRSupplementalNode(raw json.RawMessage) (int, prSupplementalInfo, bool) {
	var prData struct {
		Number        int `json:"number"`
		ReviewThreads struct {
			TotalCount int `json:"totalCount"`
			Nodes      []struct {
				IsResolved bool `json:"isResolved"`
				Comments   struct {
					Nodes []struct {
						Author struct {
							Login    string `json:"login"`
							Typename string `json:"__typename"`
						} `json:"author"`
					} `json:"nodes"`
				} `json:"comments"`
			} `json:"nodes"`
		} `json:"reviewThreads"`
		LatestReviews struct {
			Nodes []struct {
				State  string `json:"state"`
				Author struct {
					Login    string `json:"login"`
					Typename string `json:"__typename"`
				} `json:"author"`
				Comments struct {
					TotalCount int `json:"totalCount"`
				} `json:"comments"`
			} `json:"nodes"`
		} `json:"latestReviews"`
		ApprovedReviews struct {
			Nodes []struct {
				Author struct {
					Login string `json:"login"`
				} `json:"author"`
			} `json:"nodes"`
		} `json:"approvedReviews"`
	}
	if err := json.Unmarshal(raw, &prData); err != nil {
		return 0, prSupplementalInfo{}, false
	}
	if prData.Number <= 0 {
		return 0, prSupplementalInfo{}, false
	}

	var aiNodes []aiReviewNode
	for _, r := range prData.LatestReviews.Nodes {
		aiNodes = append(aiNodes, aiReviewNode{
			State:        r.State,
			AuthorLogin:  r.Author.Login,
			AuthorType:   r.Author.Typename,
			CommentCount: r.Comments.TotalCount,
		})
	}

	var aiThreads []aiReviewThread
	for _, t := range prData.ReviewThreads.Nodes {
		var login, authorType string
		if len(t.Comments.Nodes) > 0 {
			login = t.Comments.Nodes[0].Author.Login
			authorType = t.Comments.Nodes[0].Author.Typename
		}
		aiThreads = append(aiThreads, aiReviewThread{
			AuthorLogin: login,
			AuthorType:  authorType,
			IsResolved:  t.IsResolved,
		})
	}

	var approverLogins []string
	for _, r := range prData.ApprovedReviews.Nodes {
		approverLogins = append(approverLogins, r.Author.Login)
	}

	return prData.Number, prSupplementalInfo{
		Threads: reviewThreadInfo{
			Total:    prData.ReviewThreads.TotalCount,
			Resolved: countResolvedThreads(aiThreads),
		},
		AIReview:  detectAIReview(aiNodes, aiThreads),
		AIClean:   isAIReviewClean(aiNodes),
		Approvals: countUniqueApprovers(approverLogins),
	}, true
}

func boolPtr(b bool) *bool { return &b }

// aiCleanPtr returns a non-nil *bool (true) only when the AI review is clean.
// When not clean, it returns nil so the omitempty JSON tag omits the field.
func aiCleanPtr(reviews []aiReviewNode) *bool {
	if isAIReviewClean(reviews) {
		return boolPtr(true)
	}
	return nil
}

func trimTitle(title string, limit int) string {
	title = strings.TrimSpace(title)
	if limit <= 0 || len(title) <= limit {
		return title
	}

	if limit <= 3 {
		return title[:limit]
	}

	return title[:limit-3] + "..."
}
