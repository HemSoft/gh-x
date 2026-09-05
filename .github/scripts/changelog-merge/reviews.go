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

var zeroCubicIssues = regexp.MustCompile(`(?i)\b0 issues found\b|\bno issues found\b`)
var reviewedCommit = regexp.MustCompile("\\*\\*Reviewed commit:\\*\\* `([0-9a-f]{10,40})`")

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
	return !clean.IsZero() && !clean.Before(latest), requested, nil
}

func codexEvidence(state reviewState, head string) (time.Time, time.Time, bool) {
	var clean, latest time.Time
	requested := false
	for _, comment := range state.Comments.Nodes {
		if comment.Author.Login == "github-actions[bot]" && strings.Contains(comment.Body, "<!-- changelog-review-head:"+head+" -->") {
			requested = true
		}
		if !codexActor(comment.Author.Login) {
			continue
		}
		if strings.Contains(comment.Body, "<!-- codex-pull-request-review-summary -->") && strings.Contains(comment.Body, "`"+head[:7]+"`") {
			requested = true
		}
		match := reviewedCommit.FindStringSubmatch(comment.Body)
		if len(match) == 2 && strings.HasPrefix(head, match[1]) {
			requested = true
			if strings.Contains(comment.Body, "Codex Review: Didn't find any major issues.") && comment.CreatedAt.After(clean) {
				clean = comment.CreatedAt
			}
		}
	}
	for _, item := range state.Reviews.Nodes {
		if !codexActor(item.Author.Login) || item.Commit.OID != head {
			continue
		}
		requested = true
		if item.SubmittedAt.After(latest) {
			latest = item.SubmittedAt
		}
		if item.State == "APPROVED" && item.SubmittedAt.After(clean) {
			clean = item.SubmittedAt
		}
	}
	return clean, latest, requested
}

type checkRun struct {
	Name, Status, Conclusion string
	HeadSHA                  string `json:"head_sha"`
	App                      struct{ Slug string }
	Output                   struct{ Summary, Title string }
}

func inspectCubic(gh command, cfg config, state reviewState) (bool, error) {
	var response struct {
		TotalCount int        `json:"total_count"`
		CheckRuns  []checkRun `json:"check_runs"`
	}
	if err := readJSON(gh, &response, "api", "repos/"+cfg.repo+"/commits/"+cfg.head+"/check-runs?per_page=100&filter=latest"); err != nil {
		return false, err
	}
	if response.TotalCount > len(response.CheckRuns) {
		return false, errors.New("check evidence truncated; manual review required")
	}
	found := false
	for _, check := range response.CheckRuns {
		if check.App.Slug != "cubic-dev-ai" {
			continue
		}
		found = true
		ready, err := cubicReady(check, state, cfg.head)
		if err != nil || !ready {
			return ready, err
		}
	}
	// This repository has Cubic configured. Absence is pending, not an explicit skip.
	return found, nil
}

func cubicReady(check checkRun, state reviewState, head string) (bool, error) {
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
	for i := len(state.Reviews.Nodes) - 1; i >= 0; i-- {
		item := state.Reviews.Nodes[i]
		if cubicActor(item.Author.Login) && item.Commit.OID == head {
			return strings.Contains(item.Body, "All reported issues were addressed") || strings.Contains(item.Body, "No issues found"), nil
		}
	}
	return false, nil
}
