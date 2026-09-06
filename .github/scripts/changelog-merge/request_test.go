package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func reviewFixture(t *testing.T, state *reviewState) command {
	t.Helper()
	return func(args ...string) ([]byte, error) {
		switch {
		case args[0] == "api" && args[1] == "graphql":
			return encode(t, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": *state}}}), nil
		case strings.Contains(strings.Join(args, " "), "/files?"):
			return encode(t, []changedFile{{"CHANGELOG.md", "modified"}}), nil
		case args[0] == "api" && args[1] == "repos/HemSoft/gh-x/pulls/12":
			return encode(t, validPR()), nil
		default:
			t.Fatalf("unexpected API call or optional reviewer request: %v", args)
			return nil, nil
		}
	}
}

func TestWorkflowHasOneRequestOwnerAndReusesVerification(t *testing.T) {
	var release struct {
		Jobs map[string]struct {
			Concurrency struct {
				Group  string
				Cancel bool `yaml:"cancel-in-progress"`
			}
			Steps []struct {
				Run string
				Env map[string]string
			}
		}
	}
	data, err := os.ReadFile("../../workflows/auto-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &release); err != nil {
		t.Fatal(err)
	}
	job := release.Jobs["release"]
	if job.Concurrency.Group != "auto-release" || job.Concurrency.Cancel {
		t.Fatal("automated request owner must serialize rather than cancel")
	}
	requests := 0
	for _, step := range job.Steps {
		if !strings.Contains(step.Run, "changelog-merge request") {
			continue
		}
		requests++
		if step.Env["CODEX_REVIEW_TOKEN"] != "${{ secrets.CODEX_REVIEW_TOKEN }}" || !strings.Contains(step.Run, `GH_TOKEN="$CODEX_REVIEW_TOKEN"`) || !strings.Contains(step.Run, `-z "$CODEX_REVIEW_TOKEN"`) {
			t.Fatal("request must use the explicit connected-user secret and guard missing setup")
		}
		watch := strings.Index(step.Run, `gh run watch "$ci_run"`)
		merge := strings.Index(step.Run, "changelog-merge enable")
		if watch < 0 || watch > merge || !strings.Contains(step.Run, ".workflow_run_id") || !strings.Contains(step.Run, `"$ci_head" != "$head_sha"`) {
			t.Fatal("must await the exact dispatched run and verify its head before reading the merge gate")
		}
	}
	if requests != 1 {
		t.Fatalf("expected one request owner, got %d", requests)
	}
	data, err = os.ReadFile("../../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "changelog-merge request") || !strings.Contains(string(data), "cancel-in-progress: true") || !strings.Contains(string(data), "inputs.changelog_head || github.event.pull_request.head.sha || github.sha") {
		t.Fatal("verification must be read-only and replace duplicate same-head waits")
	}
}

func markedRequest(login string, at time.Time) reviewComment {
	return reviewComment{Body: "@codex review\n" + requestMarker("codex", testHead), Author: actor{login}, CreatedAt: at, URL: "https://github.com/HemSoft/gh-x/pull/12#issuecomment-request"}
}

func TestRefusalThenConnectedSuccess(t *testing.T) {
	state := cleanState()
	clean := state.Comments.Nodes[0]
	bot := markedRequest("github-actions", clean.CreatedAt.Add(-3*time.Minute))
	refusal := reviewComment{Body: "To use Codex here, create a Codex account and connect to github.", Author: actor{"chatgpt-codex-connector"}, CreatedAt: bot.CreatedAt.Add(time.Second), URL: "https://github.com/HemSoft/gh-x/pull/12#issuecomment-refusal"}
	human := markedRequest("HemSoft", clean.CreatedAt.Add(-time.Minute))
	state.Comments.Nodes = []reviewComment{bot, refusal, human, clean}
	for i := 0; i < 2; i++ {
		if err := waitForReview(context.Background(), reviewFixture(t, &state), testConfig, "12"); err != nil {
			t.Fatalf("verification rerun must reuse the clean connected result: %v", err)
		}
	}
}

func TestRefusalsReportActionableContext(t *testing.T) {
	for _, message := range []string{"create a Codex account and connect to github", "Codex usage limit reached", "upgrade to a paid plan", "Code review is not enabled", "permission denied"} {
		t.Run(message, func(t *testing.T) {
			state := cleanState()
			request := markedRequest("HemSoft", time.Now().Add(-time.Minute))
			response := reviewComment{Body: message, Author: actor{"chatgpt-codex-connector"}, CreatedAt: time.Now(), URL: "https://github.com/HemSoft/gh-x/pull/12#issuecomment-refusal"}
			state.Comments.Nodes = []reviewComment{request, response}
			err := pendingReview(state, testConfig, "12", time.Now())
			if err == nil {
				t.Fatal("explicit refusal must block")
			}
			for _, text := range []string{"PR #12", testHead, "HemSoft", response.URL, request.URL, "refused"} {
				if !strings.Contains(err.Error(), text) {
					t.Fatalf("missing %s in %v", text, err)
				}
			}
		})
	}
}

func TestRequestDeadlineSurvivesDuplicateJobsAndTriggers(t *testing.T) {
	state := cleanState()
	start := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	first := markedRequest("HemSoft", start)
	duplicate := markedRequest("HemSoft", start.Add(9*time.Minute))
	state.Comments.Nodes = []reviewComment{first, duplicate}
	if err := pendingReview(state, testConfig, "12", start.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, elapsed := range []time.Duration{10 * time.Minute, 40 * time.Minute} {
		if err := pendingReview(state, testConfig, "12", start.Add(elapsed)); err == nil || !strings.Contains(err.Error(), "timed out at 2026-09-05T12:10:00Z") {
			t.Fatalf("duplicate job must retain original terminal timeout: %v", err)
		}
	}
}

func TestSetupWindowDoesNotRestart(t *testing.T) {
	state := cleanState()
	state.Comments.Nodes = nil
	state.CommittedAt = time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	if err := pendingReview(state, testConfig, "12", state.CommittedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := pendingReview(state, testConfig, "12", state.CommittedAt.Add(time.Hour)); err == nil {
		t.Fatal("a rerun must not create a new setup window")
	}
}

func TestVerifierBudgetIncludesLateRequest(t *testing.T) {
	processStart := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	requestPosted := processStart.Add(9 * time.Minute)
	requestDeadline := requestPosted.Add(10 * time.Minute)
	processDeadline := processStart.Add(executionTimeout([]string{"review"}))
	if !processDeadline.After(requestDeadline) {
		t.Fatalf("setup consumed the review window: process expires %s, request expires %s", processDeadline, requestDeadline)
	}
}

func TestPassiveReviewCannotClearAnUnknownTimestamp(t *testing.T) {
	state := cleanState()
	finding := review{State: "CHANGES_REQUESTED", Author: actor{"cubic-dev-ai"}}
	finding.Commit.OID = testHead
	approval := finding
	approval.State, approval.SubmittedAt = "APPROVED", time.Now()
	state.Reviews.Nodes = []review{finding, approval}
	ready, _, _ := reviewReady(state, testHead)
	if ready {
		t.Fatal("missing finding timestamp must remain outstanding")
	}
}

func TestReviewTextIsNotAnAccessRefusal(t *testing.T) {
	for _, body := range []string{"Codex Review: Didn't find any major issues.\nReviewed commit: " + testHead + "\nQuota accounting looks correct.", "The quota variable is unused.", "Consider handling permission denied in this function."} {
		if got := refusalCorrection(body); got != "" {
			t.Fatalf("ordinary review misclassified: %s", body)
		}
	}
}

func TestRefusalAllowsExplanatoryPrefix(t *testing.T) {
	for _, body := range []string{"Sorry, you've reached your Codex usage limit.", "I couldn't start the review. To use Codex here, create a Codex account and connect to github.", "The review cannot continue: quota exceeded.", "Sorry, but you've reached your Codex usage limit.", "I can't start the review because permission denied."} {
		if refusalCorrection(body) == "" {
			t.Fatalf("missed explicit refusal with explanatory prose: %s", body)
		}
	}
}

func TestRefusalMustFollowItsOwnRequest(t *testing.T) {
	state := cleanState()
	request := markedRequest("HemSoft", time.Now().Add(-time.Minute))
	for _, at := range []time.Time{{}, request.CreatedAt} {
		state.Comments.Nodes = []reviewComment{request, {Body: "quota exceeded", Author: actor{"chatgpt-codex-connector"}, CreatedAt: at, URL: "response-url"}}
		if _, _, err := requestRefusal(state, request); err == nil {
			t.Fatal("missing or tied response timestamps must be ambiguous")
		}
	}
	otherHead := markedRequest("HemSoft", request.CreatedAt.Add(time.Second))
	otherHead.Body = strings.ReplaceAll(otherHead.Body, testHead, strings.Repeat("f", 40))
	state.Comments.Nodes = []reviewComment{request, otherHead, {Body: "quota exceeded", Author: actor{"chatgpt-codex-connector"}, CreatedAt: otherHead.CreatedAt.Add(time.Second)}}
	if _, correction, err := requestRefusal(state, request); correction != "" || err != nil {
		t.Fatalf("response to another request must not be attributed to this head: %s,%v", correction, err)
	}
}

func TestRequesterAccessUsesSelectedRepository(t *testing.T) {
	cfg := testConfig
	cfg.repo = "HemSoft/another-repo"
	gh := func(args ...string) ([]byte, error) {
		if args[1] == "user" {
			return []byte(`{"login":"HemSoft","type":"User"}`), nil
		}
		if args[1] != "repos/"+cfg.repo {
			t.Fatalf("checked wrong repository: %v", args)
		}
		return []byte(`{"permissions":{"push":true}}`), nil
	}
	if err := verifyRequester(gh, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestRefusalDoesNotOverrideLaterHeadReceipt(t *testing.T) {
	state := cleanState()
	clean := state.Comments.Nodes[0]
	request := markedRequest("github-actions", clean.CreatedAt.Add(-time.Minute))
	state.Comments.Nodes = append(state.Comments.Nodes, request, reviewComment{Body: "create a Codex account", Author: actor{"chatgpt-codex-connector"}, CreatedAt: request.CreatedAt.Add(time.Second)})
	ready, err := pollReview(reviewFixture(t, &state), testConfig, "12")
	if !ready || err != nil {
		t.Fatalf("PR 78 legacy unmarked human recovery must accept its explicit head receipt: %v,%v", ready, err)
	}
	state.Comments.Nodes = append(state.Comments.Nodes, markedRequest("HemSoft", time.Now()))
	ready, err = pollReview(reviewFixture(t, &state), testConfig, "12")
	if ready || err != nil {
		t.Fatalf("new current-head request supersedes old clean receipt: %v,%v", ready, err)
	}
}

func TestSerializedRequestInvocationsPostOnce(t *testing.T) {
	state := cleanState()
	state.Comments.Nodes = nil
	read := reviewFixture(t, &state)
	writes := 0
	gh := func(args ...string) ([]byte, error) {
		switch {
		case args[0] == "api" && args[1] == "user":
			return []byte(`{"login":"HemSoft","type":"User"}`), nil
		case args[0] == "api" && args[1] == "repos/HemSoft/gh-x":
			return []byte(`{"permissions":{"push":true}}`), nil
		case args[0] == "pr":
			writes++
			if args[1] != "comment" || !strings.Contains(strings.Join(args, " "), requestMarker("codex", testHead)) {
				t.Fatalf("unexpected mutation %v", args)
			}
			state.Comments.Nodes = append(state.Comments.Nodes, markedRequest("HemSoft", time.Now()))
			return []byte(state.Comments.Nodes[0].URL), nil
		default:
			return read(args...)
		}
	}
	// Auto Release's non-canceling concurrency group serializes these callers;
	// all deduplication state here comes back from GitHub, not process memory.
	for i := 0; i < 3; i++ {
		if err := ensureRequest(gh, testConfig, "12"); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 {
		t.Fatalf("expected one persisted trigger, got %d", writes)
	}
}

func TestUnsupportedRequesterNeverPosts(t *testing.T) {
	for _, identity := range []string{`{"login":"github-actions[bot]","type":"Bot"}`, `{"login":"other","type":"User"}`, `{"login":"HemSoft","type":"Bot"}`, `null`} {
		state := cleanState()
		state.Comments.Nodes = nil
		read := reviewFixture(t, &state)
		gh := func(args ...string) ([]byte, error) {
			if args[0] == "api" && args[1] == "user" {
				return []byte(identity), nil
			}
			return read(args...)
		}
		if err := ensureRequest(gh, testConfig, "12"); err == nil {
			t.Fatalf("unsupported identity accepted: %s", identity)
		}
	}
	gh := func(...string) ([]byte, error) { return nil, errors.New("token-secret must not appear") }
	if err := verifyRequester(gh, testConfig); err == nil || strings.Contains(err.Error(), "token-secret") {
		t.Fatalf("authentication error must be actionable and redacted: %v", err)
	}
}

func TestTerminalRequestNeverRetriggers(t *testing.T) {
	state := cleanState()
	state.Comments.Nodes = []reviewComment{markedRequest("HemSoft", time.Now().Add(-time.Hour))}
	for i := 0; i < 2; i++ {
		if err := ensureRequest(reviewFixture(t, &state), testConfig, "12"); err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("terminal timeout must not retrigger: %v", err)
		}
	}
	state.Comments.Nodes = append(state.Comments.Nodes, reviewComment{Body: "quota exceeded", Author: actor{"chatgpt-codex-connector"}, CreatedAt: time.Now()})
	if err := ensureRequest(reviewFixture(t, &state), testConfig, "12"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("terminal refusal must not retrigger: %v", err)
	}
}

func TestCodexAlonePassesWithoutOptionalProducts(t *testing.T) {
	state := cleanState()
	ready, err := pollReview(reviewFixture(t, &state), testConfig, "12")
	if err != nil || !ready {
		t.Fatalf("clean Codex-only review must pass: %v, %v", ready, err)
	}
}

func TestRefusalIsImmediate(t *testing.T) {
	state := cleanState()
	state.Comments.Nodes = []reviewComment{
		{Body: "@codex review\n" + requestMarker("codex", testHead), Author: actor{"github-actions"}, CreatedAt: time.Now().Add(-time.Minute)},
		{Body: "To use Codex here, create a Codex account and connect to github.", Author: actor{"chatgpt-codex-connector"}, CreatedAt: time.Now()},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := waitForReview(ctx, reviewFixture(t, &state), testConfig, "12")
	if err == nil || !strings.Contains(err.Error(), "refused") || ctx.Err() != nil {
		t.Fatalf("must report refusal before waiting: %v", err)
	}
}
