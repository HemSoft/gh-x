package main

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestParseReviewOptionsPostAndApproveFlags(t *testing.T) {
	options, err := parseReviewOptions([]string{"42", "--post", "--allow-approve", "--reviewer", "PR Reviewer V1.4 Skill"}, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !options.post {
		t.Fatal("expected post enabled")
	}
	if !options.allowApprove {
		t.Fatal("expected approve enabled")
	}
	if !options.postsReview() {
		t.Fatal("expected postsReview true")
	}
	if options.reviewer != "PR Reviewer V1.4 Skill" {
		t.Fatalf("unexpected reviewer %q", options.reviewer)
	}
}

func TestBuildPullRequestReviewRequestRequestsChangesWithInlineComments(t *testing.T) {
	reviewedAt := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	result := structuredReviewOutput{
		Summary: "One blocking issue.",
		Critical: []reviewFinding{{
			Path:       "main.go",
			Line:       10,
			Title:      "Nil dereference",
			Body:       "This path can panic before validation.",
			Suggestion: "Guard the nil value before dereferencing.",
		}},
		Recommendations: []string{"Add a regression test."},
		ResidualRisk:    "Runtime path was not executed.",
	}

	request := buildPullRequestReviewRequest(
		prReviewOptions{agent: "codex", model: "gpt-5.5", mode: "strict", reviewer: "PR Reviewer V1.4 Skill"},
		reviewPullRequest{Number: 42, Title: "Add feature", BaseRefName: "main", HeadRefName: "feature/review"},
		result,
		map[string]map[int]bool{"main.go": {10: true}},
		reviewedAt,
	)

	if request.Event != "REQUEST_CHANGES" {
		t.Fatalf("expected REQUEST_CHANGES, got %q", request.Event)
	}
	if len(request.Comments) != 1 {
		t.Fatalf("expected one inline comment, got %#v", request.Comments)
	}
	if request.Comments[0].Path != "main.go" || request.Comments[0].Line != 10 || request.Comments[0].Side != "RIGHT" {
		t.Fatalf("unexpected inline comment: %#v", request.Comments[0])
	}
	for _, want := range []string{"# PR Review Report", "**Reviewer:** PR Reviewer V1.4 Skill", "## Critical Issues", "`main.go:10`"} {
		if !strings.Contains(request.Body, want) {
			t.Fatalf("expected review body to contain %q, got %q", want, request.Body)
		}
	}
	if !strings.Contains(request.Comments[0].Body, "**[CRITICAL]**") {
		t.Fatalf("expected critical inline prefix, got %q", request.Comments[0].Body)
	}
}

func TestBuildPullRequestReviewRequestApprovesOnlyStrictCleanReview(t *testing.T) {
	reviewedAt := time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	pr := reviewPullRequest{Number: 42, Title: "Add feature", BaseRefName: "main", HeadRefName: "feature/review"}
	result := structuredReviewOutput{Summary: "No findings.", ApprovalEligible: true}

	request := buildPullRequestReviewRequest(
		prReviewOptions{agent: "codex", model: "gpt-5.5", mode: "strict", reviewer: "gh-x PR Reviewer", allowApprove: true},
		pr,
		result,
		nil,
		reviewedAt,
	)
	if request.Event != "APPROVE" {
		t.Fatalf("expected APPROVE, got %q", request.Event)
	}

	result.Nitpicks = []reviewFinding{{Path: "main.go", Line: 10, Title: "Polish"}}
	request = buildPullRequestReviewRequest(
		prReviewOptions{agent: "codex", model: "gpt-5.5", mode: "strict", reviewer: "gh-x PR Reviewer", allowApprove: true},
		pr,
		result,
		map[string]map[int]bool{"main.go": {10: true}},
		reviewedAt,
	)
	if request.Event != "COMMENT" {
		t.Fatalf("expected COMMENT when nitpicks exist, got %q", request.Event)
	}

	result.Nitpicks = nil
	request = buildPullRequestReviewRequest(
		prReviewOptions{agent: "codex", model: "gpt-5.5", mode: "medium", reviewer: "gh-x PR Reviewer", allowApprove: true},
		pr,
		result,
		nil,
		reviewedAt,
	)
	if request.Event != "COMMENT" {
		t.Fatalf("expected COMMENT outside strict mode, got %q", request.Event)
	}

	result.ResidualRisk = "Tests were not run."
	request = buildPullRequestReviewRequest(
		prReviewOptions{agent: "codex", model: "gpt-5.5", mode: "strict", reviewer: "gh-x PR Reviewer", allowApprove: true},
		pr,
		result,
		nil,
		reviewedAt,
	)
	if request.Event != "COMMENT" {
		t.Fatalf("expected COMMENT when residual risk remains, got %q", request.Event)
	}
}

func TestCommentableLinesForPatchIncludesRightSideDiffLines(t *testing.T) {
	lines := commentableLinesForPatch(strings.Join([]string{
		"@@ -1,3 +1,4 @@",
		" package main",
		"-old := true",
		"+new := true",
		" keep := true",
		"+added := true",
	}, "\n"))

	for _, line := range []int{1, 2, 3, 4} {
		if !lines[line] {
			t.Fatalf("expected line %d to be commentable, got %#v", line, lines)
		}
	}
}

func TestExecuteReviewPostBuildsAndSubmitsReview(t *testing.T) {
	savedFetch := fetchReviewPullRequestFunc
	savedCapture := runReviewAgentCaptureFunc
	savedLines := fetchReviewCommentLinesFunc
	savedSubmit := submitPullRequestReviewFunc
	savedNow := reviewNowFunc
	defer func() {
		fetchReviewPullRequestFunc = savedFetch
		runReviewAgentCaptureFunc = savedCapture
		fetchReviewCommentLinesFunc = savedLines
		submitPullRequestReviewFunc = savedSubmit
		reviewNowFunc = savedNow
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
	runReviewAgentCaptureFunc = func(invocation reviewAgentInvocation, _ io.Writer) (string, error) {
		if !strings.Contains(invocation.Prompt, "Return only a JSON object") {
			t.Fatalf("expected structured prompt, got %q", invocation.Prompt)
		}
		return `{"summary":"One issue.","critical":[],"medium":[{"path":"main.go","line":10,"title":"Missing test","body":"The behavior changed without coverage.","suggestion":"Add a regression test."}],"nitpicks":[],"recommendations":[],"residual_risk":"Tests not run.","approval_eligible":false}`, nil
	}
	fetchReviewCommentLinesFunc = func(_ prReviewOptions, _ reviewPullRequest) (map[string]map[int]bool, error) {
		return map[string]map[int]bool{"main.go": {10: true}}, nil
	}
	reviewNowFunc = func() time.Time {
		return time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
	}

	var submitted pullRequestReviewRequest
	submitPullRequestReviewFunc = func(_ prReviewOptions, _ reviewPullRequest, request pullRequestReviewRequest) error {
		submitted = request
		return nil
	}

	var stdout, stderr bytes.Buffer
	err := executeReview(prReviewOptions{agent: "codex", model: "gpt-5.5", effort: "high", mode: "strict", reviewer: "gh-x PR Reviewer", post: true}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if submitted.Event != "COMMENT" {
		t.Fatalf("expected COMMENT, got %q", submitted.Event)
	}
	if len(submitted.Comments) != 1 {
		t.Fatalf("expected one inline comment, got %#v", submitted.Comments)
	}
	if !strings.Contains(stdout.String(), "Posted COMMENT review for PR #42 with 1 inline comment") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Running codex review for PR #42") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}
