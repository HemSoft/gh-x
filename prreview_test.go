package main

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseReviewOptionsUsesEnvDefaults(t *testing.T) {
	t.Setenv("GH_X_PR_REVIEW_AGENT", "claude")
	t.Setenv("GH_X_PR_REVIEW_MODEL", "sonnet")
	t.Setenv("GH_X_PR_REVIEW_EFFORT", "medium")
	t.Setenv("GH_X_PR_REVIEW_MODE", "fast-lane")

	options, err := parseReviewOptions([]string{"42", "--repo", "owner/repo", "--dry-run"}, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if options.target != "42" {
		t.Fatalf("expected target 42, got %q", options.target)
	}
	if options.repo != "owner/repo" {
		t.Fatalf("expected repo owner/repo, got %q", options.repo)
	}
	if options.agent != "claude" {
		t.Fatalf("expected env agent claude, got %q", options.agent)
	}
	if options.model != "sonnet" {
		t.Fatalf("expected env model sonnet, got %q", options.model)
	}
	if options.effort != "medium" {
		t.Fatalf("expected env effort medium, got %q", options.effort)
	}
	if options.mode != "fast-lane" {
		t.Fatalf("expected env mode fast-lane, got %q", options.mode)
	}
	if !options.dryRun {
		t.Fatal("expected dry-run")
	}
}

func TestParseReviewOptionsDefaultsToStrictHighCodex(t *testing.T) {
	options, err := parseReviewOptions([]string{"42"}, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if options.agent != "codex" {
		t.Fatalf("expected default agent codex, got %q", options.agent)
	}
	if options.model != "gpt-5.5" {
		t.Fatalf("expected default model gpt-5.5, got %q", options.model)
	}
	if options.effort != "high" {
		t.Fatalf("expected default effort high, got %q", options.effort)
	}
	if options.mode != "strict" {
		t.Fatalf("expected default mode strict, got %q", options.mode)
	}
}

func TestParseReviewOptionsAllowsFlagsBeforeTarget(t *testing.T) {
	options, err := parseReviewOptions([]string{"--agent", "codex", "--mode", "medium", "--repo", "owner/repo", "42"}, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if options.target != "42" {
		t.Fatalf("expected target 42, got %q", options.target)
	}
	if options.agent != "codex" {
		t.Fatalf("expected agent codex, got %q", options.agent)
	}
	if options.repo != "owner/repo" {
		t.Fatalf("expected repo owner/repo, got %q", options.repo)
	}
	if options.mode != "medium" {
		t.Fatalf("expected mode medium, got %q", options.mode)
	}
}

func TestFetchReviewPullRequestUsesGhPrView(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	var gotArgs []string
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		gotArgs = append([]string(nil), args...)
		var stdout bytes.Buffer
		stdout.WriteString(`{"number":42,"title":"Add review","body":"Body","baseRefName":"main","headRefName":"feature/review","url":"https://github.com/owner/repo/pull/42","author":{"login":"octocat","name":"Octo Cat"}}`)
		return stdout, bytes.Buffer{}, nil
	}

	pr, err := fetchReviewPullRequest(prReviewOptions{target: "42", repo: "owner/repo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"pr", "view", "42", "--json", reviewPRFields, "--repo", "owner/repo"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("unexpected gh args\nwant: %#v\ngot:  %#v", wantArgs, gotArgs)
	}
	if pr.Number != 42 || pr.BaseRefName != "main" || pr.Author.Login != "octocat" {
		t.Fatalf("unexpected PR: %#v", pr)
	}
}

func TestBuildReviewInvocationCodexReadOnly(t *testing.T) {
	invocation, err := buildReviewInvocation(
		prReviewOptions{agent: "codex", model: "gpt-5.5", effort: "high"},
		reviewPullRequest{Number: 42},
		"review prompt",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"exec", "--sandbox", "read-only", "--model", "gpt-5.5", "-c", `model_reasoning_effort="high"`, "-"}
	if invocation.Name != "codex" {
		t.Fatalf("expected codex command, got %q", invocation.Name)
	}
	if !reflect.DeepEqual(invocation.Args, wantArgs) {
		t.Fatalf("unexpected args\nwant: %#v\ngot:  %#v", wantArgs, invocation.Args)
	}
	if !invocation.PromptOnStdin {
		t.Fatal("expected prompt on stdin")
	}
}

func TestBuildReviewInvocationCustomReplacesPromptAsSingleArgument(t *testing.T) {
	prompt := "line one\nkeep literal {number}"
	invocation, err := buildReviewInvocation(
		prReviewOptions{agent: "custom", command: `tool review --pr {number} --mode {mode} --prompt "{prompt}"`, mode: "medium"},
		reviewPullRequest{Number: 42},
		prompt,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"review", "--pr", "42", "--mode", "medium", "--prompt", prompt}
	if invocation.Name != "tool" {
		t.Fatalf("expected tool command, got %q", invocation.Name)
	}
	if !reflect.DeepEqual(invocation.Args, wantArgs) {
		t.Fatalf("unexpected args\nwant: %#v\ngot:  %#v", wantArgs, invocation.Args)
	}
	if invocation.PromptOnStdin {
		t.Fatal("did not expect stdin prompt when {prompt} is used")
	}
}

func TestExecuteReviewDryRunDoesNotRunAgent(t *testing.T) {
	savedFetch := fetchReviewPullRequestFunc
	savedRun := runReviewAgentFunc
	defer func() {
		fetchReviewPullRequestFunc = savedFetch
		runReviewAgentFunc = savedRun
	}()

	fetchReviewPullRequestFunc = func(_ prReviewOptions) (reviewPullRequest, error) {
		return reviewPullRequest{
			Number:      42,
			Title:       "Add review",
			BaseRefName: "main",
			HeadRefName: "feature/review",
			URL:         "https://github.com/owner/repo/pull/42",
			Author:      &author{Login: "octocat"},
		}, nil
	}
	runReviewAgentFunc = func(_ reviewAgentInvocation, _ io.Writer, _ io.Writer) error {
		return fmt.Errorf("agent should not run")
	}

	var stdout, stderr bytes.Buffer
	err := executeReview(prReviewOptions{agent: "codex", model: "gpt-5.5", effort: "high", mode: "strict", dryRun: true}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	for _, want := range []string{"Agent: codex", "Mode: strict", "Effort: high", "Command: codex exec", `model_reasoning_effort=\"high\"`, "Review GitHub pull request #42", "Review mode: strict", "gh pr diff 42"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected dry-run output to contain %q, got %q", want, output)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
}
