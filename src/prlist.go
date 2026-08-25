package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const jsonFields = "number,title,author,state,isDraft,reviewDecision,statusCheckRollup,updatedAt,headRefName,baseRefName,url,latestReviews,mergeable"

// executeListFunc is swapped in tests to avoid real API calls.
var executeListFunc = executeList

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
	Typename     string    `json:"__typename"`
	Name         string    `json:"name"`    // CheckRun name
	Context      string    `json:"context"` // StatusContext context
	WorkflowName string    `json:"workflowName"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	State        string    `json:"state"`
	StartedAt    time.Time `json:"startedAt"`
	CompletedAt  time.Time `json:"completedAt"`
}

type displayPullRequest struct {
	Number    int    `json:"number"`
	Issues    string `json:"issues,omitempty"`
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

	checksDowngraded bool // unexported; required-check rules downgraded a pass
	issueRefs        []linkedReference
	updatedAt        time.Time // unexported; used for sorting
}

type pullRequestListResult struct {
	Entries                []pullRequest
	Rendered               []displayPullRequest
	SupplementalFailed     bool
	RequiredChecksFailed   bool
	FailedRequiredCheckPRs map[int]bool
	AuxiliaryErr           error
}

func defaultListOptions() listOptions {
	return listOptions{
		limit: 30,
		state: "open",
	}
}

func executeList(options listOptions, stdout io.Writer) error {
	result, err := fetchPullRequestList(options, time.Now().UTC())
	if err != nil {
		return err
	}
	if options.web {
		return nil
	}
	return renderListOutput(stdout, options, result.Rendered)
}

func fetchPullRequestList(options listOptions, now time.Time) (pullRequestListResult, error) {
	arguments := buildListArgs(options)
	commandOutput, commandError, err := ghExecFunc(arguments...)
	if err != nil {
		return pullRequestListResult{}, wrapExecError(err, commandError.String())
	}
	if options.web {
		return pullRequestListResult{}, nil
	}
	var pullRequests []pullRequest
	if err := json.Unmarshal(commandOutput.Bytes(), &pullRequests); err != nil {
		return pullRequestListResult{}, fmt.Errorf("decode gh pr list output: %w", err)
	}
	supplemental, supplementalFailed, repoOwner, repoName := fetchSupplementalData(options.repo, pullRequests)
	requiredByBranch, failedRequiredBranches := fetchRequiredChecks(repoOwner, repoName, pullRequests)
	failedRequiredPRs := make(map[int]bool)
	for _, pr := range pullRequests {
		if failedRequiredBranches[pr.BaseRefName] {
			failedRequiredPRs[pr.Number] = true
		}
	}
	requiredChecksFailed := len(failedRequiredBranches) > 0
	rendered := enrichPullRequests(pullRequests, supplemental, supplementalFailed, requiredByBranch, now)
	return pullRequestListResult{
		Entries:                pullRequests,
		Rendered:               rendered,
		SupplementalFailed:     supplementalFailed,
		RequiredChecksFailed:   requiredChecksFailed,
		FailedRequiredCheckPRs: failedRequiredPRs,
		AuxiliaryErr:           auxiliaryRefreshError(supplementalFailed, requiredChecksFailed),
	}, nil
}

func wrapExecError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr != "" {
		return fmt.Errorf("%w: %s", err, stderr)
	}
	return err
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
	commandOutput, commandError, err := ghExecFunc(ghArgs...)
	if err != nil {
		return wrapExecError(err, commandError.String())
	}

	var pr pullRequest
	if err := json.Unmarshal(commandOutput.Bytes(), &pr); err != nil {
		return fmt.Errorf("decode gh pr view output: %w", err)
	}

	prs := []pullRequest{pr}
	supplemental, supplementalFailed, repoOwner, repoName := fetchSupplementalData(repo, prs)
	requiredByBranch, _ := fetchRequiredChecks(repoOwner, repoName, prs)
	rendered := enrichPullRequests(prs, supplemental, supplementalFailed, requiredByBranch, time.Now().UTC())

	// Render as a single-row table with no limit footer
	opts := defaultListOptions()
	opts.repo = repo
	opts.limit = 0 // suppress "limit reached" footer
	return renderTable(stdout, opts, rendered)
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
