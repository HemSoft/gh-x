package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type actor struct{ Login string }
type reviewComment struct {
	Body, URL string
	CreatedAt time.Time
	Author    actor
	Clean     bool
	Request   bool
}
type review struct {
	Body, State string
	SubmittedAt time.Time
	Author      actor
	Commit      struct{ OID string }
}
type pageInfo struct {
	HasNextPage, HasPreviousPage bool
	StartCursor                  string
}
type timelineItem struct {
	TypeName    string `json:"__typename"`
	Body, URL   string
	CreatedAt   time.Time
	Author      actor
	Commit      struct{ OID string }
	AfterCommit struct{ OID string }
}
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
	TimelineItems timelineConnection
}

type timelineConnection struct {
	Nodes    []timelineItem
	PageInfo pageInfo
}

const timelineFields = `nodes{__typename ... on IssueComment{body url createdAt author{login}} ... on PullRequestCommit{commit{oid}} ... on HeadRefForcePushedEvent{createdAt afterCommit{oid}}} pageInfo{hasPreviousPage startCursor}`
const reviewQuery = `query($owner:String!,$repo:String!,$number:Int!){repository(owner:$owner,name:$repo){pullRequest(number:$number){headRefOid commits(last:1){nodes{commit{committedDate}}} comments(last:100){nodes{body url createdAt author{login}} pageInfo{hasPreviousPage}} reviews(last:100){nodes{body state submittedAt author{login} commit{oid}} pageInfo{hasPreviousPage}} reviewThreads(first:100){nodes{isResolved} pageInfo{hasNextPage}} timelineItems(last:100,itemTypes:[PULL_REQUEST_COMMIT,ISSUE_COMMENT,HEAD_REF_FORCE_PUSHED_EVENT]){` + timelineFields + `}}}}`
const earlierTimelineQuery = `query($owner:String!,$repo:String!,$number:Int!,$before:String!){repository(owner:$owner,name:$repo){pullRequest(number:$number){timelineItems(last:100,before:$before,itemTypes:[PULL_REQUEST_COMMIT,ISSUE_COMMENT,HEAD_REF_FORCE_PUSHED_EVENT]){` + timelineFields + `}}}}`

var (
	reviewedCommit   = regexp.MustCompile("(?i)(?:\\*\\*)?Reviewed commit:(?:\\*\\*)?\\s*`?([0-9a-f]{10,40})\\b`?")
	codexActivityRow = regexp.MustCompile("(?m)^\\| [^|\\n]*\\*\\*(?:Code|Security) Review\\*\\* \\| [^|\\n]*<relative-time datetime=\"([^\"]+)\">[^|\\n]*\\| `([0-9a-fA-F]{7,40})` \\|")
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
	if err != nil {
		return state, err
	}
	if len(state.Commits.Nodes) == 1 {
		state.CommittedAt = state.Commits.Nodes[0].Commit.CommittedDate
	}
	if err := fetchEarlierTimeline(gh, number, owner, repo, &state.TimelineItems); err != nil {
		return state, err
	}
	return state, nil
}

func fetchEarlierTimeline(gh command, number, owner, repo string, timeline *timelineConnection) error {
	for page := 0; timeline.PageInfo.HasPreviousPage; page++ {
		if page == 100 || timeline.PageInfo.StartCursor == "" {
			return errors.New("pull request timeline pagination is incomplete")
		}
		var response struct {
			Data struct {
				Repository struct {
					PullRequest struct{ TimelineItems timelineConnection }
				}
			}
		}
		err := readJSON(gh, &response, "api", "graphql", "-f", "query="+earlierTimelineQuery, "-f", "owner="+owner, "-f", "repo="+repo, "-F", "number="+number, "-f", "before="+timeline.PageInfo.StartCursor)
		if err != nil {
			return fmt.Errorf("fetch earlier pull request timeline: %w", err)
		}
		earlier := response.Data.Repository.PullRequest.TimelineItems
		timeline.Nodes = append(earlier.Nodes, timeline.Nodes...)
		timeline.PageInfo = earlier.PageInfo
	}
	return nil
}

func codexActor(login string) bool {
	return login == "chatgpt-codex-connector" || login == "chatgpt-codex-connector[bot]"
}

func reviewReady(state reviewState, head string) (bool, bool, error) {
	if state.HeadRefOID != head {
		return false, false, errors.New("review response head does not match expected head")
	}
	if state.Comments.PageInfo.HasPreviousPage || state.Reviews.PageInfo.HasPreviousPage || state.ReviewThreads.PageInfo.HasNextPage || state.TimelineItems.PageInfo.HasPreviousPage {
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

func ordinaryReviewReady(state reviewState, head string) (bool, bool, error) {
	if state.HeadRefOID != head {
		return false, false, errors.New("review response head does not match expected head")
	}
	if state.Comments.PageInfo.HasPreviousPage {
		validated, err := hasCurrentHeadCodexSummary(state.Comments.Nodes, head)
		if err != nil {
			return false, false, err
		}
		if !validated {
			return false, false, errors.New("review evidence truncated; manual review required")
		}
	}
	if state.Reviews.PageInfo.HasPreviousPage || state.ReviewThreads.PageInfo.HasNextPage || state.TimelineItems.PageInfo.HasPreviousPage {
		return false, false, errors.New("review evidence truncated; manual review required")
	}
	clean, latest, requested, err := ordinaryCodexEvidence(state, head)
	if err != nil {
		return false, requested, err
	}
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
	candidates := make([]reviewComment, 0, len(state.Comments.Nodes)+len(state.Reviews.Nodes))
	for _, comment := range state.Comments.Nodes {
		candidate, matched, err := codexCommentActivity(comment, head)
		if err != nil {
			return reviewComment{}, err
		}
		if matched {
			candidates = append(candidates, candidate)
		}
	}
	for _, item := range state.Reviews.Nodes {
		if codexActor(item.Author.Login) && item.Commit.OID == head {
			candidates = append(candidates, reviewComment{Body: item.Body, CreatedAt: item.SubmittedAt, Author: item.Author, Clean: item.State == "APPROVED"})
		}
	}
	return latestCodexActivity(candidates)
}

func currentHeadOrdinaryCodexActivity(state reviewState, head string) (reviewComment, error) {
	candidates, err := ordinaryCodexCandidates(state, head)
	if err != nil {
		return reviewComment{}, err
	}
	return latestCodexActivity(candidates)
}

func ordinaryCodexEvidence(state reviewState, head string) (time.Time, time.Time, bool, error) {
	candidates, err := ordinaryCodexCandidates(state, head)
	if err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	if _, err := latestCodexActivity(candidates); err != nil {
		return time.Time{}, time.Time{}, false, err
	}
	var clean, latest time.Time
	for _, candidate := range candidates {
		if candidate.Clean {
			clean = laterTime(clean, candidate.CreatedAt)
		} else {
			latest = laterTime(latest, candidate.CreatedAt)
		}
	}
	return clean, latest, len(candidates) != 0, nil
}

func hasCurrentHeadCodexSummary(comments []reviewComment, head string) (bool, error) {
	for _, comment := range comments {
		if !strings.Contains(comment.Body, "<!-- codex-pull-request-review-summary -->") {
			continue
		}
		_, matched, err := codexCommentActivity(comment, head)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func ordinaryCodexCandidates(state reviewState, head string) ([]reviewComment, error) {
	candidates := exactCodexReviewCandidates(state.Reviews.Nodes, head)
	hasExactReview := len(candidates) != 0
	collision := false
	for _, comment := range state.Comments.Nodes {
		candidate, matched, collides, err := ordinaryCommentCandidate(state, comment, head)
		if err != nil {
			return nil, err
		}
		collision = collision || collides
		if matched {
			candidates = append(candidates, candidate)
		}
	}
	if collision && !hasExactReview {
		return nil, errors.New("abbreviated Codex receipt matches distinct pull request heads; exact review evidence required")
	}
	return candidates, nil
}

func ordinaryCommentCandidate(state reviewState, comment reviewComment, head string) (reviewComment, bool, bool, error) {
	if strings.Contains(comment.Body, "<!-- codex-pull-request-review-summary -->") {
		candidate, matched, err := codexCommentActivity(comment, head)
		if err != nil || !matched {
			return reviewComment{}, false, false, err
		}
		if candidate.Clean {
			candidate.Clean, err = completedSummaryIsClean(state, head, candidate.CreatedAt)
			if err != nil {
				return reviewComment{}, false, false, err
			}
		}
		collides := latestHeadBoundary(state.TimelineItems.Nodes, head) < 0 || timelineHasReceiptPrefixCollision(state.TimelineItems.Nodes, head, codexSummaryPrefix(comment.Body))
		return candidate, !collides, collides, nil
	}
	if !receiptMatches(comment, head) {
		return reviewComment{}, false, false, nil
	}
	if timelineHasReceiptPrefixCollision(state.TimelineItems.Nodes, head, receiptCommitPrefix(comment)) {
		return reviewComment{}, false, true, nil
	}
	bound, err := timelineBindsComment(state, comment, head)
	comment.Clean = cleanCodexComment(comment.Body)
	return comment, bound, false, err
}

func completedSummaryIsClean(state reviewState, head string, completedAt time.Time) (bool, error) {
	var latest review
	for _, item := range state.Reviews.Nodes {
		if !codexActor(item.Author.Login) || item.Commit.OID != head {
			continue
		}
		if item.SubmittedAt.IsZero() {
			return false, errors.New("current-head Codex activity lacks a timestamp")
		}
		if latest.SubmittedAt.IsZero() || item.SubmittedAt.After(latest.SubmittedAt) {
			latest = item
		}
	}
	if latest.SubmittedAt.IsZero() || latest.State == "APPROVED" {
		return true, nil
	}
	request, err := latestBoundOrdinaryRequest(state, head, latest.SubmittedAt)
	if err != nil {
		return false, err
	}
	return !request.CreatedAt.IsZero() && request.CreatedAt.After(latest.SubmittedAt) && !request.CreatedAt.After(completedAt), nil
}

func exactCodexReviewCandidates(reviews []review, head string) []reviewComment {
	candidates := make([]reviewComment, 0, len(reviews))
	for _, item := range reviews {
		if codexActor(item.Author.Login) && item.Commit.OID == head {
			candidates = append(candidates, reviewComment{Body: item.Body, CreatedAt: item.SubmittedAt, Author: item.Author, Clean: item.State == "APPROVED"})
		}
	}
	return candidates
}

func timelineHasReceiptPrefixCollision(items []timelineItem, head, prefix string) bool {
	for _, item := range items {
		oid := strings.ToLower(timelineHeadOID(item))
		if oid != "" && oid != strings.ToLower(head) && strings.HasPrefix(oid, prefix) {
			return true
		}
	}
	return false
}

func timelineBindsComment(state reviewState, comment reviewComment, head string) (bool, error) {
	if state.TimelineItems.PageInfo.HasPreviousPage {
		return false, errors.New("pull request timeline is truncated; cannot bind Codex evidence to current head")
	}
	if comment.URL == "" {
		return false, errors.New("Codex receipt lacks a timeline identity")
	}
	var timelineHead string
	for _, item := range state.TimelineItems.Nodes {
		if oid := timelineHeadOID(item); oid != "" {
			timelineHead = oid
		}
		if item.TypeName != "IssueComment" || item.URL != comment.URL {
			continue
		}
		if timelineHead == "" {
			return false, errors.New("Codex receipt cannot be bound to a head timeline event")
		}
		return timelineHead == head, nil
	}
	return false, nil
}

func timelineHeadOID(item timelineItem) string {
	if item.TypeName == "PullRequestCommit" {
		return item.Commit.OID
	}
	if item.TypeName == "HeadRefForcePushedEvent" {
		return item.AfterCommit.OID
	}
	return ""
}

func codexCommentActivity(comment reviewComment, head string) (reviewComment, bool, error) {
	if !codexActor(comment.Author.Login) {
		return reviewComment{}, false, nil
	}
	summary := strings.Contains(comment.Body, "<!-- codex-pull-request-review-summary -->")
	if !summary && !receiptMatches(comment, head) {
		return reviewComment{}, false, nil
	}
	if summary {
		matches := codexActivityRow.FindAllStringSubmatch(comment.Body, -1)
		if len(matches) != 1 {
			return reviewComment{}, false, errors.New("current-head Codex summary has missing or ambiguous activity time")
		}
		if !strings.HasPrefix(strings.ToLower(head), strings.ToLower(matches[0][2])) {
			return reviewComment{}, false, nil
		}
		stamp, err := time.Parse(time.RFC3339Nano, matches[0][1])
		if err != nil {
			return reviewComment{}, false, errors.New("current-head Codex activity has an invalid timestamp")
		}
		comment.CreatedAt = stamp
		comment.Clean = strings.Contains(matches[0][0], "**Completed**")
	}
	if comment.CreatedAt.IsZero() {
		return reviewComment{}, false, errors.New("current-head Codex activity lacks a timestamp")
	}
	comment.Clean = comment.Clean || cleanCodexComment(comment.Body)
	return comment, true, nil
}

func codexSummaryPrefix(body string) string {
	matches := codexActivityRow.FindAllStringSubmatch(body, -1)
	if len(matches) != 1 {
		return ""
	}
	return strings.ToLower(matches[0][2])
}

func latestCodexActivity(candidates []reviewComment) (reviewComment, error) {
	var latest reviewComment
	for _, candidate := range candidates {
		if candidate.CreatedAt.IsZero() {
			return reviewComment{}, errors.New("current-head Codex activity lacks a timestamp")
		}
		if candidate.CreatedAt.Equal(latest.CreatedAt) && (candidate.URL != latest.URL || candidate.Body != latest.Body || candidate.Author.Login != latest.Author.Login || candidate.Clean != latest.Clean) {
			return reviewComment{}, errors.New("distinct current-head Codex activity shares a timestamp; manual review required")
		}
		if candidate.CreatedAt.After(latest.CreatedAt) {
			latest = candidate
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

func receiptCommitPrefix(comment reviewComment) string {
	match := reviewedCommit.FindStringSubmatch(comment.Body)
	if len(match) != 2 {
		return ""
	}
	return strings.ToLower(match[1])
}

func receiptMatches(comment reviewComment, head string) bool {
	prefix := receiptCommitPrefix(comment)
	return codexActor(comment.Author.Login) && prefix != "" && strings.HasPrefix(strings.ToLower(head), prefix)
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
