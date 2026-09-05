package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

type actor struct{ Login string }
type reviewComment struct {
	Body      string
	CreatedAt time.Time
	Author    actor
}
type review struct {
	Body, State string
	SubmittedAt time.Time
	Author      actor
	Commit      struct{ OID string }
}
type pageInfo struct{ HasNextPage, HasPreviousPage bool }
type reviewState struct {
	HeadRefOID string
	Comments   struct {
		Nodes    []reviewComment
		PageInfo pageInfo
	}
	Reviews struct {
		Nodes    []review
		PageInfo pageInfo
	}
	ReviewThreads struct {
		Nodes    []struct{ IsResolved bool }
		PageInfo pageInfo
	}
}

const reviewQuery = `query($owner:String!,$repo:String!,$number:Int!){repository(owner:$owner,name:$repo){pullRequest(number:$number){headRefOid comments(last:100){nodes{body createdAt author{login}} pageInfo{hasPreviousPage}} reviews(last:100){nodes{body state submittedAt author{login} commit{oid}} pageInfo{hasPreviousPage}} reviewThreads(first:100){nodes{isResolved} pageInfo{hasNextPage}}}}}`

const cubicReviewCheckName = "cubic · AI code reviewer"

var zeroCubicIssues = regexp.MustCompile(`(?i)\b0 issues found\b|\bno issues found\b`)
var reviewedCommit = regexp.MustCompile("(?i)(?:\\*\\*)?Reviewed commit:(?:\\*\\*)?\\s*`?([0-9a-f]{10,40})\\b`?")

func fetchReviewState(gh command, cfg config, number string) (reviewState, error) {
	var response struct {
		Data struct {
			Repository struct{ PullRequest reviewState }
		}
	}
	owner, repo, _ := strings.Cut(cfg.repo, "/")
	err := readJSON(gh, &response, "api", "graphql", "-f", "query="+reviewQuery, "-f", "owner="+owner, "-f", "repo="+repo, "-F", "number="+number)
	return response.Data.Repository.PullRequest, err
}

func codexActor(login string) bool {
	return login == "chatgpt-codex-connector" || login == "chatgpt-codex-connector[bot]"
}
func cubicActor(login string) bool { return login == "cubic-dev-ai" || login == "cubic-dev-ai[bot]" }

func reviewReady(state reviewState, head string) (bool, bool, error) {
	if state.HeadRefOID != head {
		return false, false, errors.New("review response head does not match expected head")
	}
	if state.Comments.PageInfo.HasPreviousPage || state.Reviews.PageInfo.HasPreviousPage || state.ReviewThreads.PageInfo.HasNextPage {
		return false, false, errors.New("review evidence truncated; manual review required")
	}
	if ambiguousCodexReview(state, head) {
		return false, true, errors.New("current-head Codex evidence lacks a timestamp")
	}
	for _, comment := range state.Comments.Nodes {
		if codexActor(comment.Author.Login) && strings.Contains(comment.Body, "<!-- codex-pull-request-review-summary -->") && strings.Contains(comment.Body, "`"+head[:7]+"`") && strings.Contains(comment.Body, "**Running**") {
			return false, true, nil
		}
	}
	clean, latest, requested := codexEvidence(state, head)
	for _, thread := range state.ReviewThreads.Nodes {
		if !thread.IsResolved {
			return false, requested, nil
		}
	}
	return !clean.IsZero() && clean.After(latest), requested, nil
}

func requestMarker(reviewer, head string) string {
	return "<!-- changelog-" + reviewer + "-review-head:" + head + " -->"
}

func wasRequested(state reviewState, reviewer, head string) bool {
	for _, comment := range state.Comments.Nodes {
		// GraphQL returns bare bot logins; REST includes the suffix.
		trusted := comment.Author.Login == "github-actions" || comment.Author.Login == "github-actions[bot]"
		if trusted && strings.Contains(comment.Body, requestMarker(reviewer, head)) {
			return true
		}
	}
	return false
}

func codexEvidence(state reviewState, head string) (time.Time, time.Time, bool) {
	clean, latest, requested := codexCommentEvidence(state, head)
	for _, item := range state.Reviews.Nodes {
		if !codexActor(item.Author.Login) || item.Commit.OID != head {
			continue
		}
		requested = true
		if item.State != "APPROVED" && item.SubmittedAt.After(latest) {
			latest = item.SubmittedAt
		}
		if item.State == "APPROVED" && item.SubmittedAt.After(clean) {
			clean = item.SubmittedAt
		}
	}
	return clean, latest, requested
}

func codexCommentEvidence(state reviewState, head string) (time.Time, time.Time, bool) {
	var clean, latest time.Time
	requested := wasRequested(state, "codex", head)
	for _, comment := range state.Comments.Nodes {
		if !codexActor(comment.Author.Login) {
			continue
		}
		if strings.Contains(comment.Body, "<!-- codex-pull-request-review-summary -->") && strings.Contains(comment.Body, "`"+head[:7]+"`") {
			requested = true
		}
		if !receiptMatches(comment, head) {
			continue
		}
		requested = true
		if cleanCodexComment(comment.Body) {
			clean = laterTime(clean, comment.CreatedAt)
		} else {
			latest = laterTime(latest, comment.CreatedAt)
		}
	}
	return clean, latest, requested
}

type checkRun struct {
	StartedAt                time.Time `json:"started_at"`
	Name, Status, Conclusion string
	HeadSHA                  string `json:"head_sha"`
	App                      struct{ Slug string }
	Output                   struct{ Summary, Title string }
}

func inspectCubic(gh command, cfg config, state reviewState) (bool, bool, error) {
	var response struct {
		TotalCount int        `json:"total_count"`
		CheckRuns  []checkRun `json:"check_runs"`
	}
	if err := readJSON(gh, &response, "api", "repos/"+cfg.repo+"/commits/"+cfg.head+"/check-runs?per_page=100&filter=latest"); err != nil {
		return false, false, err
	}
	if response.TotalCount > len(response.CheckRuns) {
		return false, false, errors.New("check evidence truncated; manual review required")
	}
	checks, err := latestChecks(cubicReviewChecks(response.CheckRuns), "cubic-dev-ai")
	if err != nil {
		// An unorderable rerun is pending; do not post a duplicate request.
		return false, true, nil
	}
	found := false
	for _, check := range checks {
		found = true
		ready, err := cubicReady(check, cfg.head)
		if err != nil || !ready {
			return ready, true, err
		}
	}
	// This repository has Cubic configured. Absence is pending, not an explicit skip.
	return found, found || wasRequested(state, "cubic", cfg.head) || hasCubicReview(state, cfg.head), nil
}

func cubicReviewChecks(checks []checkRun) []checkRun {
	var reviews []checkRun
	for _, check := range checks {
		if strings.EqualFold(strings.TrimSpace(check.Name), cubicReviewCheckName) {
			check.Name = cubicReviewCheckName
			reviews = append(reviews, check)
		}
	}
	return reviews
}

func hasCubicReview(state reviewState, head string) bool {
	for _, item := range state.Reviews.Nodes {
		if cubicActor(item.Author.Login) && item.Commit.OID == head {
			return true
		}
	}
	return false
}

func cubicReady(check checkRun, head string) (bool, error) {
	if check.HeadSHA != head {
		return false, errors.New("cubic check does not match expected head")
	}
	if check.Status != "completed" {
		return false, nil
	}
	text := strings.ToLower(check.Output.Title + " " + check.Output.Summary)
	if check.Conclusion == "skipped" || (check.Conclusion == "success" && strings.Contains(text, "review skipped")) {
		fmt.Fprintln(os.Stdout, "Cubic review unavailable/skipped; requiring Codex and all conversations resolved")
		return true, nil
	}
	if check.Conclusion != "success" {
		return false, errors.New("cubic check did not succeed")
	}
	if zeroCubicIssues.MatchString(text) {
		return true, nil
	}
	return false, nil
}

func ambiguousCodexReview(state reviewState, head string) bool {
	for _, item := range state.Reviews.Nodes {
		if codexActor(item.Author.Login) && item.Commit.OID == head && item.SubmittedAt.IsZero() {
			return true
		}
	}
	for _, comment := range state.Comments.Nodes {
		if receiptMatches(comment, head) && comment.CreatedAt.IsZero() {
			return true
		}
	}
	return false
}

func receiptMatches(comment reviewComment, head string) bool {
	if !codexActor(comment.Author.Login) {
		return false
	}
	match := reviewedCommit.FindStringSubmatch(comment.Body)
	return len(match) == 2 && strings.HasPrefix(head, strings.ToLower(match[1]))
}

func cleanCodexComment(body string) bool {
	first, _, _ := strings.Cut(strings.TrimSpace(body), "\n")
	first = strings.ToLower(strings.TrimLeft(strings.ReplaceAll(first, "**", ""), "# "))
	return strings.HasPrefix(first, "codex review: didn't find any major issues") || strings.HasPrefix(first, "codex review: did not find any major issues")
}

func laterTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// API filtering can retain reruns from different check suites for one context.
func latestChecks(checks []checkRun, app string) ([]checkRun, error) {
	latest := map[string]checkRun{}
	for _, check := range checks {
		if check.App.Slug != app {
			continue
		}
		current, exists := latest[check.Name]
		if !exists {
			latest[check.Name] = check
			continue
		}
		newer, err := newerCheck(check, current)
		if err != nil {
			return nil, err
		}
		if newer {
			latest[check.Name] = check
		}
	}
	result := make([]checkRun, 0, len(latest))
	for _, check := range latest {
		result = append(result, check)
	}
	return result, nil
}

func newerCheck(candidate, current checkRun) (bool, error) {
	a, b := candidate.StartedAt, current.StartedAt
	if a.IsZero() || b.IsZero() || a.Equal(b) {
		return false, errors.New("cannot order repeated review checks; manual review required")
	}
	return a.After(b), nil
}
