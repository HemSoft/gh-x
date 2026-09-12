package main

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The Auto Release job is the sole automated request owner, serialized by its
// concurrency group. Read-only CI jobs never post or replace requests.
const connectedRequester = "HemSoft"
const reviewWindow = 10 * time.Minute

func trustedRequester(login string) bool {
	return login == connectedRequester || login == "github-actions" || login == "github-actions[bot]"
}

func latestRequest(state reviewState, head string) (reviewComment, error) {
	var latest reviewComment
	for _, c := range state.Comments.Nodes {
		if !trustedRequester(c.Author.Login) || !strings.Contains(c.Body, requestMarker("codex", head)) {
			continue
		}
		if c.CreatedAt.IsZero() {
			return latest, errors.New("codex request lacks a timestamp; inspect request history before retrying")
		}
		if c.CreatedAt.Equal(latest.CreatedAt) && c.URL != latest.URL {
			return latest, errors.New("ambiguous simultaneous Codex requests; inspect request history")
		}
		if c.CreatedAt.After(latest.CreatedAt) {
			latest = c
		}
	}
	return latest, nil
}

var accessRefusal = regexp.MustCompile(`(?i)^(?:to use codex here,?\s*|(?:please )?create a codex account|(?:please )?connect to github|permission denied|not authorized|access denied|(?:codex |you |this account )does not have access|code review is not enabled)`)
var quotaRefusal = regexp.MustCompile(`(?i)^(?:(?:codex )?(?:usage|rate) limit|quota (?:exceeded|exhausted)|(?:please )?upgrade (?:your plan|to a paid)|insufficient credits|you(?: have|'ve) (?:hit|reached|exceeded) (?:your |the )?(?:codex )?(?:usage|rate|review|code review)|(?:codex )?review (?:is unavailable|requires a paid plan))`)
var refusalPreamble = regexp.MustCompile(`(?i)^(?:(?:sorry|unfortunately|i'm sorry|i am sorry)[,:.!]?\s*|(?:i (?:couldn't|cannot|can't) (?:start|complete) (?:the |this )?review|(?:the |this )?review (?:cannot|can't|could not) (?:start|continue|complete))[,:.!]?\s*)+`)
var refusalConnector = regexp.MustCompile(`(?i)^(?:but|and|so|because|since|however)[,:.!]?\s+`)

func refusalCorrection(body string) string {
	text := strings.TrimSpace(body)
	// Review receipts/summaries and quoted code are evidence, not standalone
	// access responses, even when they discuss permissions or quota handling.
	if reviewedCommit.MatchString(text) || strings.Contains(text, "<!-- codex-pull-request-review-summary -->") {
		return ""
	}
	leading := refusalPreamble.ReplaceAllString(text, "")
	if leading != text {
		text = refusalConnector.ReplaceAllString(leading, "")
	}
	if accessRefusal.MatchString(text) {
		return "connect HemSoft to Codex, grant repository access, and enable code review"
	}
	if quotaRefusal.MatchString(text) {
		return "restore the connected account's Codex quota or plan access before an explicit retry"
	}
	return ""
}

func requestRefusal(state reviewState, request reviewComment) (reviewComment, string, error) {
	var response reviewComment
	var correction string
	if request.CreatedAt.IsZero() {
		return response, correction, nil
	}
	for _, c := range state.Comments.Nodes {
		if !codexActor(c.Author.Login) {
			continue
		}
		fix := refusalCorrection(c.Body)
		if fix == "" || (!c.CreatedAt.IsZero() && c.CreatedAt.Before(request.CreatedAt)) {
			continue
		}
		if c.CreatedAt.IsZero() || c.CreatedAt.Equal(request.CreatedAt) {
			return c, "", fmt.Errorf("ambiguous Codex response timestamp at %s; inspect request %s before retrying", c.URL, request.URL)
		}
		if !responseBelongsToRequest(state, request, c) {
			continue
		}
		if c.CreatedAt.After(response.CreatedAt) {
			response, correction = c, fix
		}
	}
	return response, correction, nil
}

func responseBelongsToRequest(state reviewState, request, response reviewComment) bool {
	for _, c := range state.Comments.Nodes {
		if !trustedRequester(c.Author.Login) {
			continue
		}
		first, _, _ := strings.Cut(strings.TrimSpace(c.Body), "\n")
		if first == "@codex review" && c.CreatedAt.After(request.CreatedAt) && !c.CreatedAt.After(response.CreatedAt) {
			return false
		}
	}
	return true
}

func reviewBlocked(cfg config, number string, request reviewComment, responseURL, reason string) error {
	if responseURL == "" {
		responseURL = "https://github.com/" + cfg.repo + "/pull/" + number
	}
	requester := request.Author.Login
	if requester == "" {
		requester = "unavailable"
	}
	return fmt.Errorf("PR #%s head %s requester %s: %s; response %s; request %s", number, cfg.head, requester, reason, responseURL, request.URL)
}

// The deadline is derived from GitHub's persisted creation time, never from a
// workflow start time. Repeated same-account triggers cannot extend it.
func requestStart(state reviewState, request reviewComment, head string) time.Time {
	start := request.CreatedAt
	for _, c := range state.Comments.Nodes {
		if c.Author.Login != request.Author.Login || !strings.Contains(c.Body, requestMarker("codex", head)) {
			continue
		}
		if !c.CreatedAt.IsZero() && c.CreatedAt.Before(start) {
			start = c.CreatedAt
		}
	}
	return start
}

func pendingReview(state reviewState, cfg config, number string, now time.Time) error {
	request, err := latestRequest(state, cfg.head)
	if err != nil {
		return err
	}
	response, correction, err := requestRefusal(state, request)
	if err != nil {
		return err
	}
	if correction != "" {
		return reviewBlocked(cfg, number, request, response.URL, "Codex refused this request; "+correction)
	}
	// PR-triggered CI can start before Auto Release posts the request. Anchor
	// this short setup window to the immutable head, not this job invocation.
	if request.CreatedAt.IsZero() && !state.CommittedAt.IsZero() && now.Before(state.CommittedAt.Add(reviewWindow)) {
		return nil
	}
	if request.Author.Login != connectedRequester {
		return reviewBlocked(cfg, number, request, request.URL, "no supported connected-account request; configure CODEX_REVIEW_TOKEN for HemSoft or run the documented connected-account recovery")
	}
	deadline := requestStart(state, request, cfg.head).Add(reviewWindow)
	if !now.Before(deadline) {
		return reviewBlocked(cfg, number, request, request.URL, "Codex review timed out at "+deadline.Format(time.RFC3339)+"; inspect the existing request, do not rerun to reset its deadline")
	}
	return nil
}

func currentRequestAllowsClean(state reviewState, cfg config, number string) (bool, error) {
	request, err := latestRequest(state, cfg.head)
	if err != nil {
		return false, err
	}
	clean, _, _ := codexEvidence(state, cfg.head)
	response, correction, err := requestRefusal(state, request)
	if err != nil {
		return false, err
	}
	// A later clean receipt for this immutable head supersedes an earlier bot
	// refusal. A new request or refusal after that receipt still blocks.
	if correction != "" && (response.CreatedAt.IsZero() || !clean.After(response.CreatedAt)) {
		return false, reviewBlocked(cfg, number, request, response.URL, "Codex refused this request; "+correction)
	}
	if !request.CreatedAt.IsZero() && !clean.After(request.CreatedAt) {
		return false, pendingReview(state, cfg, number, time.Now())
	}
	return true, nil
}

func verifyRequester(gh command, cfg config) error {
	var user struct{ Login, Type string }
	if err := readJSON(gh, &user, "api", "user"); err != nil {
		return errors.New("cannot verify connected requester; configure CODEX_REVIEW_TOKEN for HemSoft, with repository read and pull-request write access")
	}
	if user.Login != connectedRequester || user.Type != "User" {
		return fmt.Errorf("requester %s is unsupported; use the connected HemSoft user token, not GITHUB_TOKEN or an installation token", user.Login)
	}
	var repo struct{ Permissions struct{ Push bool } }
	if err := readJSON(gh, &repo, "api", "repos/"+cfg.repo); err != nil || !repo.Permissions.Push {
		return errors.New("HemSoft requester lacks verified repository write access")
	}
	return nil
}

func requestNeeded(state reviewState, cfg config, number string) (bool, error) {
	ready, requested, err := reviewReady(state, cfg.head)
	if err != nil {
		return false, err
	}
	if ready {
		clean, err := currentRequestAllowsClean(state, cfg, number)
		if err != nil || clean {
			return false, err
		}
	}
	request, err := latestRequest(state, cfg.head)
	if err != nil {
		return false, err
	}
	if request.Author.Login == connectedRequester {
		return false, pendingReview(state, cfg, number, time.Now())
	}
	_, correction, err := requestRefusal(state, request)
	if err != nil {
		return false, err
	}
	if requested && correction == "" {
		return false, reviewBlocked(cfg, number, request, request.URL, "existing Codex evidence requires assessment; no duplicate request posted")
	}
	return true, nil
}

func ensureRequest(gh command, cfg config, number string) error {
	state, err := fetchReviewState(gh, cfg, number)
	if err != nil {
		return err
	}
	needed, err := requestNeeded(state, cfg, number)
	if err != nil || !needed {
		return err
	}
	if err := verifyRequester(gh, cfg); err != nil {
		return reviewBlocked(cfg, number, reviewComment{}, "", err.Error())
	}
	if err := inspectEligibility(gh, cfg, number, true); err != nil {
		return err
	}
	body := "@codex review\n\n" + requestMarker("codex", cfg.head)
	_, err = gh("pr", "comment", number, "--repo", cfg.repo, "--body", body)
	return err
}
