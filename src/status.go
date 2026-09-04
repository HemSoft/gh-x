package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/muesli/termenv"
)

type statusSummary struct {
	Branch     string
	Upstream   string
	Ahead      int
	Behind     int
	Staged     int
	Modified   int
	Deleted    int
	Renamed    int
	Untracked  int
	Conflicted int
}

type statusBranchRef struct {
	FullName     string
	ShortName    string
	Upstream     string
	Track        string
	Symref       string
	WorktreePath string
}

type statusBranchInventory struct {
	LocalCount    int
	RemoteCount   int
	DanglingCount int
	Local         map[string]statusBranchRef
	Refs          []statusBranchRef
}

type statusWorktree struct {
	Path             string
	Head             string
	Branch           string
	Detached         bool
	DetachedMerged   bool
	Locked           bool
	Prunable         bool
	PrunableReason   string
	Primary          bool
	Current          bool
	Exists           bool
	Clean            bool
	CleanKnown       bool
	CleanupCandidate bool
	CleanupReason    string
}

type statusDashboard struct {
	Repository          string
	RepositoryURL       string
	DefaultBranch       string
	DefaultStatus       statusSummary
	DefaultCheckedOut   bool
	DefaultStatusErr    error
	CurrentStatus       statusSummary
	Branches            statusBranchInventory
	Worktrees           []statusWorktree
	Issues              []displayIssue
	IssuesErr           error
	PullRequests        []displayPullRequest
	PullRequestsErr     error
	WorkflowRuns        []displayWorkflowRun
	WorkflowRunsPerfect bool
	WorkflowRunsErr     error
}

const (
	statusListLimit        = 30
	statusWorkflowRunLimit = 5
	statusBranchFormat     = "%(refname)%09%(refname:short)%09%(upstream:short)%09%(upstream:track)%09%(symref)"
)

func runStatus(args []string, stdout io.Writer, stderr io.Writer) error {
	if err := parseStatusArgs(args, stderr); err != nil {
		if errors.Is(err, errHelpDisplayed) {
			return nil
		}
		return err
	}

	dashboard, err := fetchStatusDashboardFunc()
	if err != nil {
		return err
	}

	return renderStatus(stdout, dashboard, term.FromEnv().IsColorEnabled())
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

var (
	fetchStatusDashboardFunc  = fetchStatusDashboard
	statusIssueListFunc       = fetchDisplayIssues
	statusPullRequestListFunc = fetchPullRequestList
	statusWorkflowRunListFunc = fetchWorkflowRunList
	statusRepoLabelFunc       = resolveRepoLabel
	statusRepoURLFunc         = resolveRepoURL
	statusDefaultBranchFunc   = fetchHostedDefaultBranch
	statusNowFunc             = func() time.Time { return time.Now().UTC() }
	statusPathExistsFunc      = statusPathExists
)

func fetchStatusDashboard() (statusDashboard, error) {
	output, err := statusCommandFunc("git", "status", "--porcelain=v2", "--branch")
	if err != nil {
		return statusDashboard{}, fmt.Errorf("git status: %w", err)
	}

	branchOutput, err := statusCommandFunc("git", "for-each-ref", "--format="+statusBranchFormat, "refs/heads", "refs/remotes")
	if err != nil {
		return statusDashboard{}, fmt.Errorf("git branch inventory: %w", err)
	}
	branches := parseStatusBranchRefs(branchOutput)

	worktreeOutput, err := statusCommandFunc("git", "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return statusDashboard{}, fmt.Errorf("git worktree list: %w", err)
	}
	worktrees := parseStatusWorktrees(worktreeOutput)
	attachStatusWorktreePaths(&branches, worktrees)

	root, err := statusCommandFunc("git", "rev-parse", "--show-toplevel")
	if err != nil {
		return statusDashboard{}, fmt.Errorf("git repository root: %w", err)
	}
	currentRoot := strings.TrimRight(root, "\r\n")
	defaultBranch := resolveStatusDefaultBranch(branches)
	if defaultBranch == "" {
		defaultBranch = statusDefaultBranchFunc()
	}

	repository := statusRepoLabelFunc("")
	repositoryURL := ""
	if repository != "" {
		repositoryURL, _ = statusRepoURLFunc("")
	}
	dashboard := statusDashboard{
		Repository:    repository,
		RepositoryURL: repositoryURL,
		DefaultBranch: defaultBranch,
		CurrentStatus: parseGitStatus(output),
		Branches:      branches,
	}
	dashboard.DefaultStatus, dashboard.DefaultCheckedOut, dashboard.DefaultStatusErr = fetchDefaultBranchStatus(defaultBranch, branches)

	now := statusNowFunc()
	issueOptions := issueListOptions{limit: statusListLimit, state: "open"}
	dashboard.Issues, dashboard.IssuesErr = statusIssueListFunc(issueOptions, now)

	prOptions := defaultListOptions()
	prOptions.limit = statusListLimit
	prOptions.state = "open"
	prResult, prErr := statusPullRequestListFunc(prOptions, now)
	dashboard.PullRequestsErr = prErr
	if prErr == nil {
		dashboard.PullRequests = prResult.Rendered
	}

	runOptions := runListOptions{limit: statusWorkflowRunLimit}
	runResult, runErr := statusWorkflowRunListFunc(runOptions, now)
	dashboard.WorkflowRunsErr = runErr
	if runErr == nil {
		dashboard.WorkflowRuns = runResult.Rendered
		dashboard.WorkflowRunsPerfect = isPerfectWorkflowRunStreak(runResult.Entries)
	}

	merged, mergedKnown := fetchMergedStatusBranches(defaultBranch)
	openHeads := openPullRequestHeads(prResult.Entries)
	pullRequestsKnown := prErr == nil && len(prResult.Entries) < prOptions.limit
	dashboard.Worktrees = assessStatusWorktrees(worktrees, currentRoot, defaultBranch, merged, openHeads, mergedKnown, pullRequestsKnown)
	return dashboard, nil
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

func parseStatusBranchRefs(output string) statusBranchInventory {
	inventory := statusBranchInventory{Local: make(map[string]statusBranchRef)}
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimRight(line, "\r") == "" {
			continue
		}
		fields := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(fields) != 5 && len(fields) != 6 {
			continue
		}
		ref := statusBranchRef{
			FullName:  fields[0],
			ShortName: fields[1],
			Upstream:  fields[2],
			Track:     fields[3],
			Symref:    fields[4],
		}
		if len(fields) == 6 {
			ref.WorktreePath = fields[5]
		}
		inventory.Refs = append(inventory.Refs, ref)
		classifyStatusBranchRef(&inventory, ref)
	}
	return inventory
}

func attachStatusWorktreePaths(inventory *statusBranchInventory, worktrees []statusWorktree) {
	for _, worktree := range worktrees {
		ref, ok := inventory.Local[worktree.Branch]
		if !ok {
			continue
		}
		ref.WorktreePath = worktree.Path
		inventory.Local[worktree.Branch] = ref
	}
}

func classifyStatusBranchRef(inventory *statusBranchInventory, ref statusBranchRef) {
	switch {
	case strings.HasPrefix(ref.FullName, "refs/heads/"):
		inventory.LocalCount++
		inventory.Local[ref.ShortName] = ref
		if strings.Contains(strings.ToLower(ref.Track), "[gone]") {
			inventory.DanglingCount++
		}
	case strings.HasPrefix(ref.FullName, "refs/remotes/") && ref.Symref == "":
		inventory.RemoteCount++
	}
}

func resolveStatusDefaultBranch(inventory statusBranchInventory) string {
	for _, ref := range inventory.Refs {
		if ref.FullName == "refs/remotes/origin/HEAD" {
			return branchNameFromRemoteHEAD(ref)
		}
	}
	for _, ref := range inventory.Refs {
		if strings.HasSuffix(ref.FullName, "/HEAD") && ref.Symref != "" {
			return branchNameFromRemoteHEAD(ref)
		}
	}
	return ""
}

func branchNameFromRemoteHEAD(ref statusBranchRef) string {
	remotePrefix := strings.TrimSuffix(ref.FullName, "/HEAD") + "/"
	if strings.HasPrefix(ref.Symref, remotePrefix) {
		return strings.TrimPrefix(ref.Symref, remotePrefix)
	}
	return ""
}

func fetchHostedDefaultBranch() string {
	stdout, _, err := ghExecFunc("repo", "view", "--json", "defaultBranchRef", "--jq", ".defaultBranchRef.name")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(stdout.String())
}

func parseStatusWorktrees(output string) []statusWorktree {
	var worktrees []statusWorktree
	var current statusWorktree
	active := false
	appendCurrent := func() {
		if !active {
			return
		}
		current.Primary = len(worktrees) == 0
		worktrees = append(worktrees, current)
		current = statusWorktree{}
		active = false
	}

	for _, field := range strings.Split(output, "\x00") {
		if field == "" {
			appendCurrent()
			continue
		}
		key, value, _ := strings.Cut(field, " ")
		if key == "worktree" && active {
			appendCurrent()
		}
		active = applyStatusWorktreeField(&current, key, value) || active
	}
	appendCurrent()
	return worktrees
}

func applyStatusWorktreeField(worktree *statusWorktree, key, value string) bool {
	switch key {
	case "worktree":
		worktree.Path = value
		return true
	case "HEAD":
		worktree.Head = value
	case "branch":
		worktree.Branch = strings.TrimPrefix(value, "refs/heads/")
	case "detached":
		worktree.Detached = true
	case "locked":
		worktree.Locked = true
	case "prunable":
		worktree.Prunable = true
		worktree.PrunableReason = value
	}
	return false
}

func fetchDefaultBranchStatus(branch string, inventory statusBranchInventory) (statusSummary, bool, error) {
	if branch == "" {
		return statusSummary{}, false, errors.New("default branch unavailable")
	}
	ref, ok := inventory.Local[branch]
	if !ok {
		return statusSummary{Branch: branch}, false, fmt.Errorf("local branch %s not found", branch)
	}
	if ref.WorktreePath != "" {
		output, err := statusCommandFunc("git", "-C", ref.WorktreePath, "status", "--porcelain=v2", "--branch")
		if err != nil {
			return statusSummary{Branch: branch, Upstream: ref.Upstream}, true, err
		}
		return parseGitStatus(output), true, nil
	}

	summary := statusSummary{Branch: branch, Upstream: ref.Upstream}
	if ref.Upstream == "" {
		return summary, false, nil
	}
	output, err := statusCommandFunc("git", "rev-list", "--left-right", "--count", branch+"..."+ref.Upstream)
	if err != nil {
		return summary, false, err
	}
	fields := strings.Fields(output)
	if len(fields) != 2 {
		return summary, false, fmt.Errorf("unexpected rev-list output: %q", strings.TrimSpace(output))
	}
	summary.Ahead = parseSignedCount(fields[0])
	summary.Behind = parseSignedCount(fields[1])
	return summary, false, nil
}

func fetchMergedStatusBranches(defaultBranch string) (map[string]bool, bool) {
	if defaultBranch == "" {
		return nil, false
	}
	output, err := statusCommandFunc("git", "for-each-ref", "--merged=refs/heads/"+defaultBranch, "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, false
	}
	merged := make(map[string]bool)
	for _, branch := range strings.Fields(output) {
		merged[branch] = true
	}
	return merged, true
}

func openPullRequestHeads(entries []pullRequest) map[string]bool {
	heads := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.HeadRefName != "" {
			heads[entry.HeadRefName] = true
		}
	}
	return heads
}

func assessStatusWorktrees(worktrees []statusWorktree, currentRoot, defaultBranch string, merged, openHeads map[string]bool, mergedKnown, pullRequestsKnown bool) []statusWorktree {
	for i := range worktrees {
		worktree := &worktrees[i]
		worktree.Current = sameStatusPath(worktree.Path, currentRoot)
		assessStatusWorktreeCleanliness(worktree, defaultBranch)
		assessDetachedWorktreeMerge(worktree, defaultBranch)
		worktree.CleanupReason = statusWorktreeCandidateReason(*worktree, defaultBranch, merged, openHeads, mergedKnown, pullRequestsKnown)
		worktree.CleanupCandidate = worktree.CleanupReason != ""
	}
	return worktrees
}

func assessDetachedWorktreeMerge(worktree *statusWorktree, defaultBranch string) {
	if !worktree.Prunable || !worktree.Detached || worktree.Head == "" || defaultBranch == "" {
		return
	}
	_, err := statusCommandFunc("git", "merge-base", "--is-ancestor", worktree.Head, "refs/heads/"+defaultBranch)
	worktree.DetachedMerged = err == nil
}

func assessStatusWorktreeCleanliness(worktree *statusWorktree, defaultBranch string) {
	if worktree.Prunable || worktree.Primary || worktree.Current || worktree.Locked || worktree.Branch == defaultBranch {
		return
	}
	worktree.Exists = statusPathExistsFunc(worktree.Path)
	if !worktree.Exists {
		return
	}
	output, err := statusCommandFunc("git", "-C", worktree.Path, "status", "--porcelain")
	if err != nil {
		return
	}
	worktree.CleanKnown = true
	worktree.Clean = strings.TrimSpace(output) == ""
}

func statusWorktreeCandidateReason(worktree statusWorktree, defaultBranch string, merged, openHeads map[string]bool, mergedKnown, pullRequestsKnown bool) string {
	if statusWorktreeProtected(worktree, defaultBranch) {
		return ""
	}
	if worktree.Prunable {
		if !pullRequestsKnown || (worktree.Detached && !worktree.DetachedMerged) {
			return ""
		}
		if !worktree.Detached && !statusBranchSafeForCleanup(worktree.Branch, merged, openHeads, mergedKnown, pullRequestsKnown) {
			return ""
		}
		if worktree.PrunableReason != "" {
			return "Git marks worktree prunable: " + worktree.PrunableReason
		}
		return "Git marks worktree prunable"
	}
	if !statusLinkedWorktreeSafeForCleanup(worktree, merged, openHeads, mergedKnown, pullRequestsKnown) {
		return ""
	}
	return "clean, merged branch with no open PR"
}

func statusWorktreeProtected(worktree statusWorktree, defaultBranch string) bool {
	return worktree.Primary || worktree.Current || worktree.Locked || worktree.Branch == defaultBranch
}

func statusLinkedWorktreeSafeForCleanup(worktree statusWorktree, merged, openHeads map[string]bool, mergedKnown, pullRequestsKnown bool) bool {
	if worktree.Branch == "" || !worktree.Exists || !worktree.CleanKnown || !worktree.Clean {
		return false
	}
	return statusBranchSafeForCleanup(worktree.Branch, merged, openHeads, mergedKnown, pullRequestsKnown)
}

func statusBranchSafeForCleanup(branch string, merged, openHeads map[string]bool, mergedKnown, pullRequestsKnown bool) bool {
	if !mergedKnown || !pullRequestsKnown {
		return false
	}
	return merged[branch] && !openHeads[branch]
}

func statusPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func sameStatusPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func parseGitStatus(output string) statusSummary {
	var summary statusSummary
	seen := make(map[string]bool)

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}

		parseGitStatusLine(line, seen, &summary)
	}

	return summary
}

func parseGitStatusLine(line string, seen map[string]bool, summary *statusSummary) {
	switch {
	case strings.HasPrefix(line, "# "):
		parseGitStatusHeader(line, summary)
	case strings.HasPrefix(line, "? "):
		if markSeen(seen, strings.TrimPrefix(line, "? ")) {
			summary.Untracked++
		}
	case strings.HasPrefix(line, "u "):
		if path := statusPath(line); markSeen(seen, path) {
			summary.Conflicted++
		}
	case strings.HasPrefix(line, "1 ") || strings.HasPrefix(line, "2 "):
		if path := statusPath(line); markSeen(seen, path) {
			applyStatusXY(statusXY(line), summary)
		}
	}
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

func renderStatus(stdout io.Writer, dashboard statusDashboard, colorEnabled bool) error {
	styler := newTableStyler(stdout, colorEnabled)
	renderStatusHeader(stdout, styler, dashboard)
	if err := renderStatusIssueSection(stdout, styler, dashboard); err != nil {
		return err
	}
	if err := renderStatusPullRequestSection(stdout, styler, dashboard); err != nil {
		return err
	}
	return renderStatusWorkflowRunSection(stdout, styler, dashboard)
}

func isPerfectWorkflowRunStreak(runs []workflowRun) bool {
	if len(runs) != statusWorkflowRunLimit {
		return false
	}
	for _, run := range runs {
		if run.Status != "completed" || run.Conclusion != "success" {
			return false
		}
	}
	return true
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

func plural(count int, singular, pluralText string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %s", count, pluralText)
}

type statusSeverity int

const (
	statusHealthy statusSeverity = iota
	statusAttention
	statusBlocked
	statusUnavailable
)

func renderStatusHeader(stdout io.Writer, styler tableStyler, dashboard statusDashboard) {
	rows := [][]tableCell{
		{styler.dim("Repository"), statusRepositoryCell(styler, dashboard.Repository, dashboard.RepositoryURL)},
		{styler.dim(statusDefaultBranchLabel(dashboard.DefaultBranch)), statusDefaultBranchCell(styler, dashboard)},
	}
	if dashboard.CurrentStatus.Branch != dashboard.DefaultBranch || dashboard.DefaultStatusErr != nil {
		rows = append(rows, []tableCell{styler.dim("Current"), statusCurrentBranchCell(styler, dashboard.CurrentStatus)})
	}
	rows = append(rows,
		[]tableCell{styler.dim("Branches"), statusBranchInventoryCell(styler, dashboard.Branches)},
		[]tableCell{styler.dim("Worktrees"), statusWorktreeInventoryCell(styler, dashboard.Worktrees)},
	)

	widths := computeColumnWidths([]tableCell{styler.plain(""), styler.plain("")}, rows)
	for _, row := range rows {
		writeRow(stdout, row, widths)
	}
	renderStatusCleanupCandidates(stdout, styler, dashboard.Worktrees)
}

func statusRepositoryCell(styler tableStyler, repository, repositoryURL string) tableCell {
	if repository == "" {
		return statusHeaderValue(styler, "unavailable", statusUnavailable)
	}
	return styler.linkCell(repository, repositoryURL, termenv.ANSIGreen)
}

func statusDefaultBranchLabel(branch string) string {
	if branch == "" {
		return "Default"
	}
	runes := []rune(branch)
	return strings.ToUpper(string(runes[0])) + string(runes[1:])
}

func statusDefaultBranchCell(styler tableStyler, dashboard statusDashboard) tableCell {
	if dashboard.DefaultStatusErr != nil {
		return statusHeaderValue(styler, conciseStatusError(dashboard.DefaultStatusErr), statusUnavailable)
	}
	return statusBranchCell(styler, dashboard.DefaultStatus, dashboard.DefaultCheckedOut)
}

func statusCurrentBranchCell(styler tableStyler, summary statusSummary) tableCell {
	branch := summary.Branch
	if branch == "" {
		branch = "detached HEAD"
	}
	cell := statusBranchCell(styler, summary, true)
	cell.text = branch + " · " + cell.text
	cell.styled = branch + " · " + cell.styled
	return cell
}

func statusBranchCell(styler tableStyler, summary statusSummary, checkedOut bool) tableCell {
	parts := []string{compactBranchStatusText(summary)}
	if checkedOut {
		parts = append(parts, strings.TrimSuffix(changeStatusText(summary), "."))
	} else {
		parts = append(parts, "not checked out")
	}
	return statusHeaderValue(styler, strings.Join(parts, " · "), statusBranchSeverity(summary))
}

func compactBranchStatusText(summary statusSummary) string {
	if summary.Upstream == "" {
		return "no upstream configured"
	}
	switch {
	case summary.Ahead == 0 && summary.Behind == 0:
		return "synced with " + summary.Upstream
	case summary.Ahead > 0 && summary.Behind == 0:
		return fmt.Sprintf("ahead of %s by %s", summary.Upstream, plural(summary.Ahead, "commit", "commits"))
	case summary.Ahead == 0 && summary.Behind > 0:
		return fmt.Sprintf("behind %s by %s", summary.Upstream, plural(summary.Behind, "commit", "commits"))
	default:
		return fmt.Sprintf("diverged from %s: %d ahead, %d behind", summary.Upstream, summary.Ahead, summary.Behind)
	}
}

func statusBranchSeverity(summary statusSummary) statusSeverity {
	if summary.Behind > 0 || summary.Conflicted > 0 {
		return statusBlocked
	}
	if summary.Ahead > 0 || summary.Upstream == "" || hasWorkingChanges(summary) {
		return statusAttention
	}
	return statusHealthy
}

func statusBranchInventoryCell(styler tableStyler, inventory statusBranchInventory) tableCell {
	text := fmt.Sprintf("%d local (%d dangling) · %d remote", inventory.LocalCount, inventory.DanglingCount, inventory.RemoteCount)
	severity := statusHealthy
	if inventory.DanglingCount > 0 {
		severity = statusAttention
	}
	return statusHeaderValue(styler, text, severity)
}

func statusWorktreeInventoryCell(styler tableStyler, worktrees []statusWorktree) tableCell {
	candidates := statusCleanupCandidateCount(worktrees)
	text := fmt.Sprintf("%d total · %d cleanup %s", len(worktrees), candidates, pluralWord(candidates, "candidate", "candidates"))
	severity := statusHealthy
	if candidates > 0 {
		severity = statusAttention
	}
	return statusHeaderValue(styler, text, severity)
}

func pluralWord(count int, singular, pluralText string) string {
	if count == 1 {
		return singular
	}
	return pluralText
}

func statusHeaderValue(styler tableStyler, text string, severity statusSeverity) tableCell {
	switch severity {
	case statusAttention:
		return styler.colored(text, termenv.ANSIYellow)
	case statusBlocked:
		return styler.colored(text, termenv.ANSIRed)
	case statusUnavailable:
		return styler.dim(text)
	default:
		return styler.colored(text, termenv.ANSIGreen)
	}
}

func statusCleanupCandidateCount(worktrees []statusWorktree) int {
	count := 0
	for _, worktree := range worktrees {
		if worktree.CleanupCandidate {
			count++
		}
	}
	return count
}

func renderStatusCleanupCandidates(stdout io.Writer, styler tableStyler, worktrees []statusWorktree) {
	candidates := make([]statusWorktree, 0)
	for _, worktree := range worktrees {
		if worktree.CleanupCandidate {
			candidates = append(candidates, worktree)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
	for _, candidate := range candidates {
		branch := candidate.Branch
		if branch == "" {
			branch = "detached"
		}
		text := fmt.Sprintf("  %s — %s (%s)", branch, candidate.Path, candidate.CleanupReason)
		fmt.Fprintln(stdout, styler.colored(text, termenv.ANSIYellow).styled)
	}
}

func renderStatusIssueSection(stdout io.Writer, styler tableStyler, dashboard statusDashboard) error {
	fmt.Fprintln(stdout)
	if dashboard.IssuesErr != nil {
		fmt.Fprintln(stdout, "Open issues")
		fmt.Fprintln(stdout, styler.dim("Unavailable: "+conciseStatusError(dashboard.IssuesErr)).styled)
		return nil
	}
	fmt.Fprintf(stdout, "Open issues (%s)\n", statusSectionCount(len(dashboard.Issues)))
	if len(dashboard.Issues) == 0 {
		writeBacklogPraise(stdout)
		return nil
	}
	if err := renderIssueRows(stdout, dashboard.Issues, styler.colorEnabled); err != nil {
		return err
	}
	if len(dashboard.Issues) >= statusListLimit {
		fmt.Fprintf(stdout, "\nShowing %d issues (limit reached). Use gh x issue list --limit to show more.\n", statusListLimit)
	}
	return nil
}

func renderStatusPullRequestSection(stdout io.Writer, styler tableStyler, dashboard statusDashboard) error {
	fmt.Fprintln(stdout)
	if dashboard.PullRequestsErr != nil {
		fmt.Fprintln(stdout, "Open pull requests")
		fmt.Fprintln(stdout, styler.dim("Unavailable: "+conciseStatusError(dashboard.PullRequestsErr)).styled)
		return nil
	}
	fmt.Fprintf(stdout, "Open pull requests (%s)\n", statusSectionCount(len(dashboard.PullRequests)))
	if len(dashboard.PullRequests) == 0 {
		writeBacklogPraise(stdout)
		return nil
	}
	if err := renderPullRequestRows(stdout, dashboard.PullRequests, styler.colorEnabled); err != nil {
		return err
	}
	if len(dashboard.PullRequests) >= statusListLimit {
		fmt.Fprintf(stdout, "\nShowing %d pull requests (limit reached). Use gh x pr list --limit to show more.\n", statusListLimit)
	}
	return nil
}

func renderStatusWorkflowRunSection(stdout io.Writer, styler tableStyler, dashboard statusDashboard) error {
	fmt.Fprintln(stdout)
	if dashboard.WorkflowRunsErr != nil {
		fmt.Fprintln(stdout, "Recent workflow runs")
		fmt.Fprintln(stdout, styler.dim("Unavailable: "+conciseStatusError(dashboard.WorkflowRunsErr)).styled)
		return nil
	}
	fmt.Fprintf(stdout, "Recent workflow runs (%d)\n", len(dashboard.WorkflowRuns))
	if len(dashboard.WorkflowRuns) == 0 {
		fmt.Fprintln(stdout, "No workflow runs found.")
		return nil
	}
	renderWorkflowRunRows(stdout, dashboard.WorkflowRuns, styler.colorEnabled)
	if dashboard.WorkflowRunsPerfect {
		fmt.Fprintln(stdout)
		writeWorkflowPerfectionPraise(stdout)
	}
	return nil
}

func statusSectionCount(count int) string {
	if count >= statusListLimit {
		return fmt.Sprintf("%d+", count)
	}
	return strconv.Itoa(count)
}

func conciseStatusError(err error) string {
	text := strings.Join(strings.Fields(err.Error()), " ")
	return trimTitle(text, 120)
}

func writeStatusUsage(w io.Writer) {
	fmt.Fprint(w, statusUsage)
}

const statusUsage = `Usage:
  gh x status

Show repository health, branches, worktrees, open issues, open pull requests,
and the five most recent workflow runs.
`
