package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

type reviewThreadInfo struct {
	Total    int
	Resolved int
}

type prSupplementalInfo struct {
	Threads                reviewThreadInfo
	ClosingIssues          []linkedReference
	ClosingIssuesAvailable bool
	AIReview               string
	AIClean                bool
	HasUnresolvedAIThreads bool
	Approvals              int
}

// aiReviewNode holds the fields needed to detect bot reviewer status.
type aiReviewNode struct {
	State        string
	AuthorLogin  string
	AuthorType   string
	CommentCount int
	OccurredAt   time.Time
	CommitOID    string
}

// aiReviewThread holds thread resolution state and authorship for AI review detection.
type aiReviewThread struct {
	AuthorLogin string
	AuthorType  string
	IsResolved  bool
}

// aiReviewComment holds PR conversation comments that may contain a
// current-head Codex review summary.
type aiReviewComment struct {
	Body        string
	AuthorLogin string
	AuthorType  string
	OccurredAt  time.Time
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
	normalized := strings.ToLower(strings.TrimSpace(login))
	return strings.HasSuffix(normalized, "[bot]") || knownAIReviewers[normalized]
}

// currentHeadReviewNodes keeps only review evidence tied to the current head.
// An empty head preserves compatibility for callers that lack commit metadata.
func currentHeadReviewNodes(reviews []aiReviewNode, headRefOID string) []aiReviewNode {
	if headRefOID == "" {
		return reviews
	}

	current := make([]aiReviewNode, 0, len(reviews))
	for _, review := range reviews {
		if strings.EqualFold(review.CommitOID, headRefOID) {
			current = append(current, review)
		}
	}
	return current
}

func codexReviewNode(comment aiReviewComment, headRefOID string) (aiReviewNode, bool) {
	login := strings.TrimSuffix(strings.ToLower(comment.AuthorLogin), "[bot]")
	if login != "chatgpt-codex-connector" {
		return aiReviewNode{}, false
	}

	body := strings.ToLower(comment.Body)
	if !strings.Contains(body, "codex review:") || headRefOID == "" {
		return aiReviewNode{}, false
	}

	shaLength := min(10, len(headRefOID))
	if !strings.Contains(body, strings.ToLower(headRefOID[:shaLength])) {
		return aiReviewNode{}, false
	}

	commentCount := 1
	if strings.Contains(body, "didn't find any major issues") ||
		strings.Contains(body, "did not find any major issues") {
		commentCount = 0
	}

	return aiReviewNode{
		State:        "COMMENTED",
		AuthorLogin:  comment.AuthorLogin,
		AuthorType:   comment.AuthorType,
		CommentCount: commentCount,
		OccurredAt:   comment.OccurredAt,
		CommitOID:    headRefOID,
	}, true
}

func sortAIReviewsChronologically(reviews []aiReviewNode) {
	sort.SliceStable(reviews, func(i, j int) bool {
		left, right := reviews[i].OccurredAt, reviews[j].OccurredAt
		if left.IsZero() {
			return !right.IsZero()
		}
		if right.IsZero() {
			return false
		}
		return left.Before(right)
	})
}

// latestAIReviewIsClean returns true when the most recent bot-authored review
// is a clean pass (APPROVED or COMMENTED state with zero review comments).
// Reviews are expected in submission order, as returned by the GitHub API.
func latestAIReviewIsClean(reviews []aiReviewNode) bool {
	hasBotReview := false
	clean := false
	for _, r := range reviews {
		if !isAIReviewer(r.AuthorLogin) && r.AuthorType != "Bot" {
			continue
		}
		hasBotReview = true
		switch strings.ToUpper(r.State) {
		case "APPROVED", "COMMENTED":
			clean = r.CommentCount == 0
		default:
			clean = false
		}
	}
	return hasBotReview && clean
}

// isAIReviewClean returns true when AI review is effectively clear for the bang
// marker: no unresolved AI threads, and either the latest bot review left zero
// comments or every AI-authored thread from earlier findings has been resolved
// (detectAIReview reports "pass").
func isAIReviewClean(reviews []aiReviewNode, threads []aiReviewThread) bool {
	if hasUnresolvedAIThreads(threads) {
		return false
	}
	if latestAIReviewIsClean(reviews) {
		return true
	}
	return detectAIReview(reviews, threads) == "pass" && allAIThreadsResolved(threads)
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

func hasUnresolvedAIThreads(threads []aiReviewThread) bool {
	for _, t := range threads {
		if (isAIReviewer(t.AuthorLogin) || t.AuthorType == "Bot") && !t.IsResolved {
			return true
		}
	}
	return false
}

// detectAIReview determines the AI review status from the latest bot review and
// thread resolution. A later clean review supersedes older findings, but any
// unresolved AI-authored thread still forces a failure.
func detectAIReview(reviews []aiReviewNode, threads []aiReviewThread) string {
	latest, ok := latestAIReview(reviews)
	if !ok {
		return "-"
	}
	if hasUnresolvedAIThreads(threads) {
		return "fail"
	}

	switch strings.ToUpper(latest.State) {
	case "APPROVED", "COMMENTED":
		if latest.CommentCount == 0 || allAIThreadsResolved(threads) {
			return "pass"
		}
		return "fail"
	case "CHANGES_REQUESTED":
		if allAIThreadsResolved(threads) {
			return "pass"
		}
		return "fail"
	default:
		return "-"
	}
}

func latestAIReview(reviews []aiReviewNode) (aiReviewNode, bool) {
	for i := len(reviews) - 1; i >= 0; i-- {
		review := reviews[i]
		if isAIReviewer(review.AuthorLogin) || review.AuthorType == "Bot" {
			return review, true
		}
	}
	return aiReviewNode{}, false
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
		Number                  int                        `json:"number"`
		HeadRefOID              string                     `json:"headRefOid"`
		ClosingIssuesReferences *linkedReferenceConnection `json:"closingIssuesReferences"`
		Comments                struct {
			TotalCount int `json:"totalCount"`
			Nodes      []struct {
				Body      string    `json:"body"`
				CreatedAt time.Time `json:"createdAt"`
				Author    struct {
					Login    string `json:"login"`
					Typename string `json:"__typename"`
				} `json:"author"`
			} `json:"nodes"`
		} `json:"comments"`
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
		Reviews struct {
			TotalCount int `json:"totalCount"`
			Nodes      []struct {
				State       string    `json:"state"`
				SubmittedAt time.Time `json:"submittedAt"`
				Commit      struct {
					OID string `json:"oid"`
				} `json:"commit"`
				Author struct {
					Login    string `json:"login"`
					Typename string `json:"__typename"`
				} `json:"author"`
				Comments struct {
					TotalCount int `json:"totalCount"`
				} `json:"comments"`
			} `json:"nodes"`
		} `json:"reviews"`
		ApprovedReviews struct {
			Nodes []struct {
				Author struct {
					Login    string `json:"login"`
					Typename string `json:"__typename"`
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
	for _, r := range prData.Reviews.Nodes {
		aiNodes = append(aiNodes, aiReviewNode{
			State:        r.State,
			AuthorLogin:  r.Author.Login,
			AuthorType:   r.Author.Typename,
			CommentCount: r.Comments.TotalCount,
			OccurredAt:   r.SubmittedAt,
			CommitOID:    r.Commit.OID,
		})
	}
	hasCurrentHeadCodexReview := false
	for _, comment := range prData.Comments.Nodes {
		if node, ok := codexReviewNode(aiReviewComment{
			Body:        comment.Body,
			AuthorLogin: comment.Author.Login,
			AuthorType:  comment.Author.Typename,
			OccurredAt:  comment.CreatedAt,
		}, prData.HeadRefOID); ok {
			aiNodes = append(aiNodes, node)
			hasCurrentHeadCodexReview = true
		}
	}
	sortAIReviewsChronologically(aiNodes)

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

	commentsTruncated := prData.Comments.TotalCount > len(prData.Comments.Nodes)
	threadsTruncated := prData.ReviewThreads.TotalCount > len(prData.ReviewThreads.Nodes)
	reviewsTruncated := prData.Reviews.TotalCount > len(prData.Reviews.Nodes)
	commentsIncomplete := commentsTruncated && !hasCurrentHeadCodexReview
	aiReview, aiClean := summarizeSupplementalReviews(
		aiNodes,
		aiThreads,
		prData.HeadRefOID,
		anyConnectionTruncated(commentsIncomplete, threadsTruncated, reviewsTruncated),
	)

	return prData.Number, prSupplementalInfo{
		Threads: reviewThreadInfo{
			Total:    prData.ReviewThreads.TotalCount,
			Resolved: countResolvedThreads(aiThreads),
		},
		ClosingIssues:          closingIssueNodes(prData.ClosingIssuesReferences),
		ClosingIssuesAvailable: prData.ClosingIssuesReferences.complete(),
		AIReview:               aiReview,
		AIClean:                aiClean,
		HasUnresolvedAIThreads: hasUnresolvedAIThreads(aiThreads),
		Approvals:              countUniqueApprovers(approverLogins),
	}, true
}

func closingIssueNodes(connection *linkedReferenceConnection) []linkedReference {
	if !connection.complete() {
		return nil
	}
	return connection.Nodes
}

func anyConnectionTruncated(truncated ...bool) bool {
	for _, value := range truncated {
		if value {
			return true
		}
	}
	return false
}

func summarizeSupplementalReviews(
	aiNodes []aiReviewNode,
	aiThreads []aiReviewThread,
	headRefOID string,
	aiIncomplete bool,
) (aiReview string, aiClean bool) {
	currentReviews := currentHeadReviewNodes(aiNodes, headRefOID)
	aiReview = detectAIReview(currentReviews, aiThreads)
	aiClean = isAIReviewClean(currentReviews, aiThreads)
	if aiIncomplete {
		aiReview = "?"
		aiClean = false
	}
	return
}

func boolPtr(b bool) *bool { return &b }
