package main

import (
	"errors"
	"regexp"
	"strings"
	"time"
)

type actor struct{ Login string }
type reviewComment struct {
	Body, URL string
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
	HeadRefOID  string
	CommittedAt time.Time
	Commits     struct {
		Nodes []struct {
			Commit struct{ CommittedDate time.Time }
		}
	}
	Comments struct {
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

const reviewQuery = `query($owner:String!,$repo:String!,$number:Int!){repository(owner:$owner,name:$repo){pullRequest(number:$number){headRefOid commits(last:1){nodes{commit{committedDate}}} comments(last:100){nodes{body url createdAt author{login}} pageInfo{hasPreviousPage}} reviews(last:100){nodes{body state submittedAt author{login} commit{oid}} pageInfo{hasPreviousPage}} reviewThreads(first:100){nodes{isResolved} pageInfo{hasNextPage}}}}}`

var (
	reviewedCommit        = regexp.MustCompile("(?i)(?:\\*\\*)?Reviewed commit:(?:\\*\\*)?\\s*`?([0-9a-f]{10,40})\\b`?")
	codexActivityDatetime = regexp.MustCompile(`datetime="([^"]+)"`)
)

func fetchReviewState(gh command, cfg config, number string) (reviewState, error) {
	var response struct {
		Data struct {
			Repository struct{ PullRequest reviewState }
		}
	}
	owner, repo, _ := strings.Cut(cfg.repo, "/")
	err := readJSON(gh, &response, "api", "graphql", "-f", "query="+reviewQuery, "-f", "owner="+owner, "-f", "repo="+repo, "-F", "number="+number)
	state := response.Data.Repository.PullRequest
	if len(state.Commits.Nodes) == 1 {
		state.CommittedAt = state.Commits.Nodes[0].Commit.CommittedDate
	}
	return state, err
}

func codexActor(login string) bool {
	return login == "chatgpt-codex-connector" || login == "chatgpt-codex-connector[bot]"
}

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
	if outstandingReviewDecision(state, head) {
		return false, requested, nil
	}
	for _, thread := range state.ReviewThreads.Nodes {
		if !thread.IsResolved {
			return false, requested, nil
		}
	}
	return !clean.IsZero() && clean.After(latest), requested, nil
}

// Passive reviewers never add a completion gate, but their actual requests for
// changes still require assessment. A later approval/dismissal clears one.
func outstandingReviewDecision(state reviewState, head string) bool {
	for _, finding := range state.Reviews.Nodes {
		if finding.Commit.OID == head && finding.State == "CHANGES_REQUESTED" && !clearedReviewDecision(state, finding) {
			return true
		}
	}
	return false
}

func clearedReviewDecision(state reviewState, finding review) bool {
	if finding.SubmittedAt.IsZero() {
		return false
	}
	for _, later := range state.Reviews.Nodes {
		if later.Author.Login != finding.Author.Login || later.Commit.OID != finding.Commit.OID || !later.SubmittedAt.After(finding.SubmittedAt) {
			continue
		}
		if later.State == "APPROVED" || later.State == "DISMISSED" {
			return true
		}
	}
	return false
}

func requestMarker(reviewer, head string) string {
	return "<!-- changelog-" + reviewer + "-review-head:" + head + " -->"
}

func wasRequested(state reviewState, reviewer, head string) bool {
	for _, comment := range state.Comments.Nodes {
		// GraphQL returns bare bot logins; REST includes the suffix.
		trusted := trustedRequester(comment.Author.Login)
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

func currentHeadCodexActivity(state reviewState, head string) (reviewComment, error) {
	var latest reviewComment
	for _, comment := range state.Comments.Nodes {
		if !codexActor(comment.Author.Login) {
			continue
		}
		summary := strings.Contains(comment.Body, "<!-- codex-pull-request-review-summary -->") && strings.Contains(comment.Body, "`"+head[:7]+"`")
		if !summary && !receiptMatches(comment, head) {
			continue
		}
		stamp := comment.CreatedAt
		if summary {
			matches := codexActivityDatetime.FindAllStringSubmatch(comment.Body, -1)
			if len(matches) == 0 {
				return latest, errors.New("current-head Codex activity lacks a timestamp")
			}
			parsed, err := time.Parse(time.RFC3339Nano, matches[len(matches)-1][1])
			if err != nil {
				return latest, errors.New("current-head Codex activity has an invalid timestamp")
			}
			stamp = parsed
		}
		if stamp.IsZero() {
			return latest, errors.New("current-head Codex activity lacks a timestamp")
		}
		if stamp.After(latest.CreatedAt) {
			latest = comment
			latest.CreatedAt = stamp
		}
	}
	for _, item := range state.Reviews.Nodes {
		if !codexActor(item.Author.Login) || item.Commit.OID != head {
			continue
		}
		if item.SubmittedAt.IsZero() {
			return latest, errors.New("current-head Codex review lacks a timestamp")
		}
		if item.SubmittedAt.After(latest.CreatedAt) {
			latest = reviewComment{Body: item.Body, CreatedAt: item.SubmittedAt, Author: item.Author}
		}
	}
	return latest, nil
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
