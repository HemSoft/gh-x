package main

import (
	"fmt"
	"strings"
	"time"
)

// supplementalNotice renders one actionable, secret-safe line that explains
// why unknown columns appear. Text stays single-line and capped so it cannot
// disrupt table or JSON output.
func supplementalNotice(reason error) string {
	if reason == nil {
		return ""
	}
	return "Supplemental data unavailable: " + conciseStatusError(reason)
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
// applying required-check downgrade logic. PRs whose enrichment is unknown
// fail closed with unknown columns; their rows never report clean or empty
// supplemental values.
func enrichPullRequests(prs []pullRequest, supplemental prSupplementalData, requiredByBranch map[string]map[string]bool, failedRuleBranches map[string]error, now time.Time) []displayPullRequest {
	rendered := make([]displayPullRequest, 0, len(prs))
	for _, pr := range prs {
		dp := buildDisplayPullRequest(pr, now)
		info, found := supplemental.Info[pr.Number]
		unavailable := supplemental.Unavailable[pr.Number] || !found
		applySupplementalInfo(&dp, info, unavailable)
		dp.Issues, dp.issueRefs = relationshipDisplay(
			info.ClosingIssues,
			unavailable || !info.ClosingIssuesAvailable,
		)
		applyAIReviewCheck(&dp, info, pr.StatusCheckRollup, unavailable)
		downgradeChecksIfMissing(&dp, requiredByBranch, failedRuleBranches, pr.BaseRefName, pr.StatusCheckRollup)
		rendered = append(rendered, dp)
	}
	return rendered
}

func applySupplementalInfo(dp *displayPullRequest, info prSupplementalInfo, unavailable bool) {
	if unavailable {
		dp.Comments = "?"
		dp.AIReview = "?"
		return
	}
	if info.ThreadsTruncated {
		// The resolved count comes from a partial page; rendering it against
		// the full total would report a precise-looking wrong value.
		dp.Comments = "?"
	} else {
		dp.Comments = formatComments(info.Threads)
	}
	dp.AIReview = info.AIReview
	if info.AIClean {
		dp.AIClean = &info.AIClean
	}
	dp.Approvals = info.Approvals
	if dp.AIReview == "" {
		dp.AIReview = "-"
	}
}

var knownAIReviewChecks = map[string]bool{
	"cubic · ai code reviewer": true,
}

func isKnownAIReviewCheck(check checkItem) bool {
	return check.Typename == "CheckRun" &&
		knownAIReviewChecks[strings.ToLower(strings.TrimSpace(check.Name))]
}

func applyAIReviewCheck(dp *displayPullRequest, info prSupplementalInfo, checks []checkItem, supplementalUnavailable bool) {
	if supplementalUnavailable {
		return
	}

	switch detectAIReviewCheck(checks) {
	case "fail":
		dp.AIReview = "fail"
		dp.AIClean = nil
	case "pass":
		if dp.AIReview == "-" && !info.HasUnresolvedAIThreads {
			dp.AIReview = "pass"
			dp.AIClean = boolPtr(true)
		}
	}
}

func detectAIReviewCheck(checks []checkItem) string {
	for _, check := range latestCheckItems(checks) {
		if !isKnownAIReviewCheck(check) {
			continue
		}

		switch strings.ToUpper(check.Conclusion) {
		case "SUCCESS":
			return "pass"
		case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED", "CANCELLED":
			return "fail"
		}
	}
	return "-"
}

// downgradeChecksIfMissing downgrades a pass or review Checks value to
// pending when repository required checks have not all reported: either the
// fetched rules list a context that is missing from the rollup, or the
// rules themselves could not be fetched, so the pass is unverified. A rules
// fetch failure confirms nothing about the rollup, so it downgrades only a
// pass; the review state keeps marking a pending recognized AI reviewer.
func downgradeChecksIfMissing(dp *displayPullRequest, requiredByBranch map[string]map[string]bool, failedRuleBranches map[string]error, base string, checkItems []checkItem) {
	if dp.Checks != "pass" && dp.Checks != "review" {
		return
	}
	if failedRuleBranches[base] != nil {
		if dp.Checks == "pass" {
			dp.Checks = "pending"
			dp.checksDowngraded = true
		}
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
			dp.checksDowngraded = true
			return
		}
	}
}

func buildDisplayPullRequest(pullRequest pullRequest, now time.Time) displayPullRequest {
	authorName := "-"
	if pullRequest.Author != nil && pullRequest.Author.Login != "" {
		authorName = formatAuthor(pullRequest.Author.Login, pullRequest.Author.Name)
	}

	return displayPullRequest{
		Number:    pullRequest.Number,
		Issues:    "-",
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
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE", "ACTION_REQUIRED", "CANCELLED":
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

	summary := summarizeCheckState(items)

	switch {
	case summary.hasFail:
		return "fail"
	case summary.hasPending:
		return "pending"
	case summary.hasPendingReview && summary.blocksReview:
		return "pending"
	case summary.hasPendingReview:
		return "review"
	default:
		return "pass"
	}
}

type checkStateSummary struct {
	hasFail          bool
	hasPending       bool
	hasPendingReview bool
	blocksReview     bool
}

func summarizeCheckState(items []checkItem) checkStateSummary {
	var summary checkStateSummary
	for _, item := range latestCheckItems(items) {
		f, p := classifyCheckItem(item)
		summary.hasFail = summary.hasFail || f
		if isKnownAIReviewCheck(item) {
			summary.hasPendingReview = summary.hasPendingReview || p
			continue
		}
		summary.hasPending = summary.hasPending || p
		summary.blocksReview = summary.blocksReview || (!f && !p && !checkItemPassed(item))
	}
	return summary
}

func checkItemPassed(item checkItem) bool {
	if item.Typename == "StatusContext" {
		return strings.EqualFold(item.State, "SUCCESS")
	}
	return strings.EqualFold(item.Conclusion, "SUCCESS")
}

// latestCheckItems collapses repeated check contexts from reruns so stale
// failures do not override a newer result for the same workflow and job.
func latestCheckItems(items []checkItem) []checkItem {
	latestIndexes := make(map[string]int)
	result := make([]checkItem, 0, len(items))
	for _, item := range items {
		key := checkItemIdentity(item)
		if key == "" {
			result = append(result, item)
			continue
		}

		index, exists := latestIndexes[key]
		if !exists {
			latestIndexes[key] = len(result)
			result = append(result, item)
			continue
		}

		currentTime := checkItemTimestamp(result[index])
		candidateTime := checkItemTimestamp(item)
		if currentTime.IsZero() || (!candidateTime.IsZero() && !candidateTime.Before(currentTime)) {
			result[index] = item
		}
	}
	return result
}

func checkItemIdentity(item checkItem) string {
	switch item.Typename {
	case "CheckRun":
		if item.Name == "" {
			return ""
		}
		return "check:" + strings.ToLower(item.WorkflowName) + ":" + strings.ToLower(item.Name)
	case "StatusContext":
		if item.Context == "" {
			return ""
		}
		return "status:" + strings.ToLower(item.Context)
	default:
		return ""
	}
}

func checkItemTimestamp(item checkItem) time.Time {
	if !item.StartedAt.IsZero() {
		return item.StartedAt
	}
	return item.CompletedAt
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
