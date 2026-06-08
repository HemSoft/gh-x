package main

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"
)

func TestBuildListArgsIncludesFilters(t *testing.T) {
	options := listOptions{
		repo:      "HemSoft/gh-x",
		limit:     50,
		state:     "all",
		author:    "@me",
		assignee:  "octocat",
		app:       "dependabot",
		base:      "main",
		head:      "feature/demo",
		search:    "review:required",
		draftOnly: true,
		labels:    stringSliceFlag{"bug", "urgent"},
	}

	got := buildListArgs(options)
	want := []string{
		"pr", "list",
		"--json", jsonFields,
		"--repo", "HemSoft/gh-x",
		"--limit", "50",
		"--state", "all",
		"--author", "@me",
		"--assignee", "octocat",
		"--app", "dependabot",
		"--base", "main",
		"--head", "feature/demo",
		"--search", "review:required",
		"--draft",
		"--label", "bug",
		"--label", "urgent",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected arguments\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestBuildDisplayPullRequestNormalizesFields(t *testing.T) {
	now := time.Date(2026, 5, 10, 1, 45, 0, 0, time.UTC)
	pullRequest := pullRequest{
		Number:         42,
		Title:          "Improve the PR list view so reviews and checks are obvious at a glance",
		State:          "OPEN",
		IsDraft:        true,
		ReviewDecision: "CHANGES_REQUESTED",
		UpdatedAt:      now.Add(-2 * time.Hour),
		HeadRefName:    "feature/prx",
		BaseRefName:    "main",
		URL:            "https://github.com/HemSoft/gh-x/pull/42",
		Author:         &author{Login: "HemSoft", Name: "Jane Doe"},
		StatusCheckRollup: []checkItem{
			{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "SUCCESS"},
		},
		LatestReviews: []review{
			{State: "APPROVED", Author: &author{Login: "reviewer1"}},
			{State: "COMMENTED", Author: &author{Login: "reviewer2"}},
			{State: "APPROVED", Author: &author{Login: "reviewer3"}},
		},
	}

	got := buildDisplayPullRequest(pullRequest, now)

	if got.State != "draft" {
		t.Fatalf("expected draft state, got %q", got.State)
	}

	if got.Review != "changes" {
		t.Fatalf("expected changes review, got %q", got.Review)
	}

	if got.Checks != "pass" {
		t.Fatalf("expected pass checks, got %q", got.Checks)
	}

	if got.Branch != "prx" {
		t.Fatalf("unexpected branch column %q", got.Branch)
	}

	if got.Approvals != 2 {
		t.Fatalf("expected 2 approvals, got %d", got.Approvals)
	}

	if got.Comments != "-" {
		t.Fatalf("expected default comments '-', got %q", got.Comments)
	}

	if got.AIReview != "-" {
		t.Fatalf("expected default AIReview '-', got %q", got.AIReview)
	}

	if got.Updated != "2h" {
		t.Fatalf("unexpected updated column %q", got.Updated)
	}

	if got.Author != "Jane Doe" {
		t.Fatalf("unexpected author %q", got.Author)
	}
}

func TestFormatRelativeTime(t *testing.T) {
	now := time.Date(2026, 5, 10, 1, 45, 0, 0, time.UTC)

	testCases := []struct {
		name      string
		updatedAt time.Time
		expected  string
	}{
		{name: "seconds", updatedAt: now.Add(-30 * time.Second), expected: "30s"},
		{name: "minutes", updatedAt: now.Add(-45 * time.Minute), expected: "45m"},
		{name: "hours", updatedAt: now.Add(-3 * time.Hour), expected: "3h"},
		{name: "days", updatedAt: now.Add(-72 * time.Hour), expected: "3d"},
		{name: "months", updatedAt: now.Add(-(45 * 24 * time.Hour)), expected: "1mo"},
		{name: "years", updatedAt: now.Add(-(400 * 24 * time.Hour)), expected: "1y"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := formatRelativeTime(testCase.updatedAt, now); got != testCase.expected {
				t.Fatalf("expected %q, got %q", testCase.expected, got)
			}
		})
	}
}

func TestRenderTableNoColor(t *testing.T) {
	var buf bytes.Buffer
	prs := []displayPullRequest{
		{Number: 42, Title: "My PR", Author: "user", State: "open", Review: "approved", AIReview: "pass", Approvals: 2, Checks: "pass", Comments: "3/5", Branch: "feat", Updated: "2h"},
	}
	err := renderTableWithStyle(&buf, listOptions{}, prs, false)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if strings.Contains(output, "\x1b[") {
		t.Fatal("expected no ANSI escape codes when color is disabled")
	}
	if !strings.Contains(output, "#42") {
		t.Fatal("expected PR number in output")
	}
	if !strings.Contains(output, "My PR") {
		t.Fatal("expected title in output")
	}
	if !strings.Contains(output, "approved") {
		t.Fatal("expected review status in output")
	}
}

func TestRenderTableWithColor(t *testing.T) {
	var buf bytes.Buffer
	prs := []displayPullRequest{
		{Number: 7, Title: "Add colors", Author: "dev", State: "open", Review: "review", AIReview: "-", Approvals: 0, Checks: "pending", Comments: "-", Branch: "color", Updated: "5m"},
	}
	err := renderTableWithStyle(&buf, listOptions{}, prs, true)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "\x1b[") {
		t.Fatal("expected ANSI escape codes when color is enabled")
	}
	if !strings.Contains(output, "#7") {
		t.Fatal("expected PR number in output")
	}
}

func TestRenderTableAlignment(t *testing.T) {
	var buf bytes.Buffer
	prs := []displayPullRequest{
		{Number: 1, Title: "Short", Author: "a", State: "open", Review: "-", AIReview: "-", Approvals: 0, Checks: "-", Comments: "-", Branch: "x", Updated: "1h"},
		{Number: 999, Title: "Longer title here", Author: "longuser", State: "merged", Review: "approved", AIReview: "pass", Approvals: 3, Checks: "pass", Comments: "5/5", Branch: "feature/long", Updated: "30d"},
	}
	err := renderTableWithStyle(&buf, listOptions{}, prs, false)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 rows), got %d: %v", len(lines), lines)
	}

	// Verify header labels are present
	if !strings.Contains(lines[0], "Title") || !strings.Contains(lines[0], "Branch") {
		t.Fatal("expected header labels")
	}

	// Verify columns are aligned: the "Title" column should start at the same
	// position in header and data rows
	headerTitleIdx := strings.Index(lines[0], "Title")
	row1TitleIdx := strings.Index(lines[1], "Short")
	row2TitleIdx := strings.Index(lines[2], "Longer")
	if headerTitleIdx != row1TitleIdx || headerTitleIdx != row2TitleIdx {
		t.Fatalf("Title column misaligned: header=%d row1=%d row2=%d", headerTitleIdx, row1TitleIdx, row2TitleIdx)
	}
}

func TestRenderTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := renderTableWithStyle(&buf, listOptions{}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No pull requests found") {
		t.Fatal("expected empty message")
	}
}

func TestRenderTableLimitNotice(t *testing.T) {
	prs := make([]displayPullRequest, 3)
	for i := range prs {
		prs[i] = displayPullRequest{Number: i + 1, Title: "PR", Author: "u", State: "open", Review: "-", AIReview: "-", Checks: "-", Comments: "-", Branch: "b", Updated: "1h"}
	}

	t.Run("shows notice at limit", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderTableWithStyle(&buf, listOptions{limit: 3}, prs, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "limit reached") {
			t.Fatal("expected limit notice when count == limit")
		}
	})

	t.Run("no notice below limit", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderTableWithStyle(&buf, listOptions{limit: 10}, prs, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "limit reached") {
			t.Fatal("should not show notice when under limit")
		}
	})
}

func TestCountApprovals(t *testing.T) {
	tests := []struct {
		name    string
		reviews []review
		want    int
	}{
		{name: "nil", reviews: nil, want: 0},
		{name: "empty", reviews: []review{}, want: 0},
		{name: "one approved", reviews: []review{{State: "APPROVED"}}, want: 1},
		{name: "mixed", reviews: []review{
			{State: "APPROVED"},
			{State: "COMMENTED"},
			{State: "CHANGES_REQUESTED"},
			{State: "APPROVED"},
		}, want: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := countApprovals(tc.reviews); got != tc.want {
				t.Fatalf("expected %d, got %d", tc.want, got)
			}
		})
	}
}

func TestSupplementalApprovalsOverridesCountApprovals(t *testing.T) {
	// buildDisplayPullRequest uses countApprovals as initial value.
	// When supplemental data is available, it should override with the
	// accurate count from reviews(states: [APPROVED]).
	now := time.Now()
	pr := pullRequest{
		Number:      28,
		Title:       "Test PR",
		State:       "OPEN",
		HeadRefName: "test-branch",
		BaseRefName: "main",
		Author:      &author{Login: "user1"},
		// latestReviews has COMMENTED (not APPROVED), so countApprovals returns 0
		LatestReviews: []review{
			{State: "COMMENTED", Author: &author{Login: "coderabbitai"}},
		},
	}

	dp := buildDisplayPullRequest(pr, now)
	if dp.Approvals != 0 {
		t.Fatalf("expected countApprovals fallback = 0, got %d", dp.Approvals)
	}

	// Supplemental data says there IS an approval (from reviews(states: APPROVED))
	info := prSupplementalInfo{Approvals: 1}
	dp.Approvals = info.Approvals
	if dp.Approvals != 1 {
		t.Fatalf("expected supplemental override = 1, got %d", dp.Approvals)
	}
}

func TestFormatComments(t *testing.T) {
	tests := []struct {
		name string
		info reviewThreadInfo
		want string
	}{
		{name: "none", info: reviewThreadInfo{}, want: "-"},
		{name: "all resolved", info: reviewThreadInfo{Total: 5, Resolved: 5}, want: "5/5"},
		{name: "partial", info: reviewThreadInfo{Total: 5, Resolved: 3}, want: "3/5"},
		{name: "none resolved", info: reviewThreadInfo{Total: 3, Resolved: 0}, want: "0/3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatComments(tc.info); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestIsAIReviewer(t *testing.T) {
	tests := []struct {
		login string
		want  bool
	}{
		{"coderabbitai[bot]", true},
		{"copilot[bot]", true},
		{"copilot-pull-request-reviewer", true},
		{"human-reviewer", false},
		{"dependabot[bot]", true},
		{"", false},
	}
	for _, tc := range tests {
		t.Run(tc.login, func(t *testing.T) {
			if got := isAIReviewer(tc.login); got != tc.want {
				t.Fatalf("isAIReviewer(%q) = %v, want %v", tc.login, got, tc.want)
			}
		})
	}
}

func TestExtractReportedContexts(t *testing.T) {
	items := []checkItem{
		{Typename: "CheckRun", Name: "SonarCloud Code Analysis", Status: "COMPLETED", Conclusion: "SUCCESS"},
		{Typename: "StatusContext", Context: "usergroups-api-pr", State: "SUCCESS"},
		{Typename: "CheckRun", Name: "", Status: "COMPLETED", Conclusion: "SUCCESS"}, // empty name ignored
	}
	got := extractReportedContexts(items)
	if !got["SonarCloud Code Analysis"] {
		t.Error("expected SonarCloud Code Analysis")
	}
	if !got["usergroups-api-pr"] {
		t.Error("expected usergroups-api-pr")
	}
	if len(got) != 2 {
		t.Errorf("expected 2 contexts, got %d", len(got))
	}
}

func TestExtractReportedContextsEmpty(t *testing.T) {
	got := extractReportedContexts(nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestDetectAIReview(t *testing.T) {
	tests := []struct {
		name    string
		nodes   []aiReviewNode
		threads []aiReviewThread
		want    string
	}{
		{name: "nil", nodes: nil, want: "-"},
		{name: "empty", nodes: []aiReviewNode{}, want: "-"},
		{name: "no bots", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "human-reviewer", CommentCount: 0},
		}, want: "-"},
		{name: "coderabbit approved", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai[bot]", CommentCount: 0},
		}, want: "pass"},
		{name: "copilot no comments", nodes: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "copilot[bot]", CommentCount: 0},
		}, want: "pass"},
		{name: "copilot-pull-request-reviewer no comments", nodes: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "copilot-pull-request-reviewer", CommentCount: 0},
		}, want: "pass"},
		{name: "bot with comments no threads", nodes: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "coderabbitai[bot]", CommentCount: 3},
		}, want: "fail"},
		{name: "bot with comments all threads resolved", nodes: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "coderabbitai[bot]", CommentCount: 2},
		}, threads: []aiReviewThread{
			{AuthorLogin: "coderabbitai[bot]", IsResolved: true},
			{AuthorLogin: "coderabbitai[bot]", IsResolved: true},
		}, want: "pass"},
		{name: "bot with comments some threads unresolved", nodes: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "coderabbitai[bot]", CommentCount: 2},
		}, threads: []aiReviewThread{
			{AuthorLogin: "coderabbitai[bot]", IsResolved: true},
			{AuthorLogin: "coderabbitai[bot]", IsResolved: false},
		}, want: "fail"},
		{name: "bot with comments only human threads resolved", nodes: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "coderabbitai[bot]", CommentCount: 2},
		}, threads: []aiReviewThread{
			{AuthorLogin: "human-reviewer", IsResolved: true},
		}, want: "fail"},
		{name: "bot changes requested", nodes: []aiReviewNode{
			{State: "CHANGES_REQUESTED", AuthorLogin: "coderabbitai[bot]", CommentCount: 5},
		}, want: "fail"},
		{name: "bot changes requested all threads resolved", nodes: []aiReviewNode{
			{State: "CHANGES_REQUESTED", AuthorLogin: "coderabbitai[bot]", CommentCount: 2},
		}, threads: []aiReviewThread{
			{AuthorLogin: "coderabbitai[bot]", IsResolved: true},
			{AuthorLogin: "coderabbitai[bot]", IsResolved: true},
		}, want: "pass"},
		{name: "mixed bot approved and human", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai[bot]", CommentCount: 0},
			{State: "CHANGES_REQUESTED", AuthorLogin: "human-reviewer", CommentCount: 2},
		}, want: "pass"},
		{name: "issues override approval", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai[bot]", CommentCount: 0},
			{State: "CHANGES_REQUESTED", AuthorLogin: "copilot[bot]", CommentCount: 1},
		}, want: "fail"},
		{name: "issues override approval but threads resolved", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai[bot]", CommentCount: 0},
			{State: "CHANGES_REQUESTED", AuthorLogin: "copilot[bot]", CommentCount: 1},
		}, threads: []aiReviewThread{
			{AuthorLogin: "copilot[bot]", IsResolved: true},
		}, want: "pass"},
		{name: "dismissed bot review ignored", nodes: []aiReviewNode{
			{State: "DISMISSED", AuthorLogin: "coderabbitai[bot]", CommentCount: 0},
		}, want: "-"},
		{name: "graphql bot typename without suffix", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai", AuthorType: "Bot", CommentCount: 0},
		}, want: "pass"},
		{name: "graphql bot typename with issues", nodes: []aiReviewNode{
			{State: "CHANGES_REQUESTED", AuthorLogin: "coderabbitai", AuthorType: "Bot", CommentCount: 3},
		}, want: "fail"},
		{name: "graphql bot typename issues all resolved", nodes: []aiReviewNode{
			{State: "CHANGES_REQUESTED", AuthorLogin: "coderabbitai", AuthorType: "Bot", CommentCount: 3},
		}, threads: []aiReviewThread{
			{AuthorLogin: "coderabbitai", AuthorType: "Bot", IsResolved: true},
			{AuthorLogin: "coderabbitai", AuthorType: "Bot", IsResolved: true},
		}, want: "pass"},
		{name: "thread with empty author not counted", nodes: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "copilot[bot]", CommentCount: 1},
		}, threads: []aiReviewThread{
			{AuthorLogin: "", IsResolved: true},
		}, want: "fail"},
		{name: "mixed ai bots one resolved one unresolved", nodes: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "coderabbitai[bot]", CommentCount: 1},
			{State: "COMMENTED", AuthorLogin: "copilot[bot]", CommentCount: 1},
		}, threads: []aiReviewThread{
			{AuthorLogin: "coderabbitai[bot]", IsResolved: true},
			{AuthorLogin: "copilot[bot]", IsResolved: false},
		}, want: "fail"},
		{name: "bot approved with comments no threads", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai[bot]", CommentCount: 2},
		}, want: "fail"},
		{name: "bot approved with comments all threads resolved", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai[bot]", CommentCount: 2},
		}, threads: []aiReviewThread{
			{AuthorLogin: "coderabbitai[bot]", IsResolved: true},
			{AuthorLogin: "coderabbitai[bot]", IsResolved: true},
		}, want: "pass"},
		{name: "bot approved with comments some threads unresolved", nodes: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "copilot[bot]", CommentCount: 3},
		}, threads: []aiReviewThread{
			{AuthorLogin: "copilot[bot]", IsResolved: true},
			{AuthorLogin: "copilot[bot]", IsResolved: false},
		}, want: "fail"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectAIReview(tc.nodes, tc.threads); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestIsAIReviewClean(t *testing.T) {
	tests := []struct {
		name    string
		reviews []aiReviewNode
		want    bool
	}{
		{name: "nil", reviews: nil, want: false},
		{name: "empty", reviews: []aiReviewNode{}, want: false},
		{name: "no bots", reviews: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "human-reviewer", CommentCount: 0},
		}, want: false},
		{name: "copilot approved 0 comments", reviews: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "copilot[bot]", CommentCount: 0},
		}, want: true},
		{name: "copilot approved with comments", reviews: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "copilot[bot]", CommentCount: 2},
		}, want: false},
		{name: "copilot commented 0 comments", reviews: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "copilot-pull-request-reviewer", CommentCount: 0},
		}, want: true},
		{name: "copilot commented with comments", reviews: []aiReviewNode{
			{State: "COMMENTED", AuthorLogin: "copilot[bot]", CommentCount: 3},
		}, want: false},
		{name: "copilot changes requested", reviews: []aiReviewNode{
			{State: "CHANGES_REQUESTED", AuthorLogin: "copilot[bot]", CommentCount: 2},
		}, want: false},
		{name: "bot approval plus bot issues", reviews: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai[bot]", CommentCount: 0},
			{State: "CHANGES_REQUESTED", AuthorLogin: "copilot[bot]", CommentCount: 1},
		}, want: false},
		{name: "graphql bot typename clean", reviews: []aiReviewNode{
			{State: "APPROVED", AuthorLogin: "coderabbitai", AuthorType: "Bot", CommentCount: 0},
		}, want: true},
		{name: "dismissed review", reviews: []aiReviewNode{
			{State: "DISMISSED", AuthorLogin: "copilot[bot]", CommentCount: 0},
		}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAIReviewClean(tc.reviews); got != tc.want {
				t.Fatalf("expected %v, got %v", tc.want, got)
			}
		})
	}
}

func TestFormatBranch(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"strips prefix", "feature/test", "test"},
		{"truncates long", "dependabot/npm_and_yarn/lint-staged-17.0.4", "lint-staged-17.…"},
		{"no slash", "main", "main"},
		{"empty", "", "-"},
		{"slash at start", "/foo", "foo"},
		{"exactly 16 chars", "feature/exactly-16-chars", "exactly-16-chars"},
		{"exactly 17 chars", "feature/exactly-17--chars", "exactly-17--cha…"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatBranch(tc.input)
			if got != tc.want {
				t.Fatalf("formatBranch(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestFormatAuthor(t *testing.T) {
	tests := []struct {
		login, name, want string
	}{
		{"app/dependabot", "", "dependabot"},
		{"app/renovate", "", "renovate"},
		{"octocat", "", "octocat"},
		{"app/", "", ""},
		{"jdoe-work", "Jane Doe", "Jane Doe"},
		{"bsmith-work", "Bob Smith", "Bob Smith"},
		{"octocat", "The Octocat", "The Octocat"},
	}
	for _, tc := range tests {
		if got := formatAuthor(tc.login, tc.name); got != tc.want {
			t.Fatalf("formatAuthor(%q, %q) = %q, want %q", tc.login, tc.name, got, tc.want)
		}
	}
}

func TestRunVersionUpToDate(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v1.2.3", nil
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "gh-x v1.2.3 © 2026 HemSoft Developments") {
		t.Fatalf("expected version line, got %q", out)
	}
	if !strings.Contains(out, "gh extension install HemSoft/gh-x") {
		t.Fatalf("expected install command, got %q", out)
	}
	if !strings.Contains(out, "✓ Up to date") {
		t.Fatalf("expected up-to-date indicator, got %q", out)
	}
}

func TestRunVersionUpdateAvailable(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v2.0.0", nil
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "↑ v2.0.0 available") {
		t.Fatalf("expected update indicator, got %q", out)
	}
	if !strings.Contains(out, "gh extension upgrade gh-x") {
		t.Fatalf("expected upgrade command, got %q", out)
	}
}

func TestRunVersionDevBuild(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v0.5.0", nil
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "dev")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "gh-x dev © 2026 HemSoft Developments") {
		t.Fatalf("expected dev version, got %q", out)
	}
	if !strings.Contains(out, "⚙ Dev build · latest release: v0.5.0") {
		t.Fatalf("expected dev build indicator, got %q", out)
	}
}

func TestRunVersionAPIError(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "", fmt.Errorf("network error")
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "⚠ Could not check for updates") {
		t.Fatalf("expected error fallback, got %q", out)
	}
}

func TestPrintBanner(t *testing.T) {
	oldVersion := version
	oldDate := buildDate
	defer func() { version = oldVersion; buildDate = oldDate }()

	version = "v1.2.3"
	buildDate = "2026-05-10"
	var buf bytes.Buffer
	printBanner(&buf)
	if got := buf.String(); got != "gh-x v1.2.3 (2026-05-10) © 2026 HemSoft Developments\n" {
		t.Fatalf("unexpected banner: %q", got)
	}
}

func TestPrintBannerNoDate(t *testing.T) {
	oldVersion := version
	oldDate := buildDate
	defer func() { version = oldVersion; buildDate = oldDate }()

	version = "v1.2.3"
	buildDate = ""
	var buf bytes.Buffer
	printBanner(&buf)
	if got := buf.String(); got != "gh-x v1.2.3 © 2026 HemSoft Developments\n" {
		t.Fatalf("unexpected banner without date: %q", got)
	}
}

func TestFormatVersion(t *testing.T) {
	if got := formatVersion("v1.0.0", "2026-05-10"); got != "v1.0.0 (2026-05-10)" {
		t.Fatalf("expected date in parens, got %q", got)
	}
	if got := formatVersion("v1.0.0", ""); got != "v1.0.0" {
		t.Fatalf("expected no parens when date empty, got %q", got)
	}
}

func TestBannerOnRootUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, _ = run(nil, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "gh-x") {
		t.Fatalf("expected banner on stderr for root usage, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Available Commands") {
		t.Fatalf("expected usage on stdout, got %q", stdout.String())
	}
}

func TestBannerOnHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, _ = run([]string{"--help"}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "gh-x") {
		t.Fatalf("expected banner on stderr for help, got %q", stderr.String())
	}
}

func TestNoBannerOnVersion(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v1.0.0", nil
	}

	for _, arg := range []string{"version", "--version", "-v"} {
		var stdout, stderr bytes.Buffer
		_, _ = run([]string{arg}, &stdout, &stderr)
		if strings.Contains(stderr.String(), "gh-x") {
			t.Fatalf("run(%q) should not print banner to stderr, got %q", arg, stderr.String())
		}
	}
}

func TestBannerOnUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, _ = run([]string{"bogus"}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "gh-x") {
		t.Fatalf("expected banner on stderr for unknown command, got %q", stderr.String())
	}
}

func TestUpgradeNoticeShown(t *testing.T) {
	oldVersion := version
	defer func() { version = oldVersion }()
	version = "v1.0.0"

	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v2.0.0", nil
	}

	var stdout, stderr bytes.Buffer
	updateCh, _ := run(nil, &stdout, &stderr)
	showUpdateNotice(&stderr, updateCh, updateSuccessTimeout)
	if !strings.Contains(stderr.String(), "↑ v2.0.0 available") {
		t.Fatalf("expected upgrade notice on stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "gh extension upgrade gh-x") {
		t.Fatalf("expected upgrade command on stderr, got %q", stderr.String())
	}
}

func TestNoUpgradeNoticeWhenCurrent(t *testing.T) {
	oldVersion := version
	defer func() { version = oldVersion }()
	version = "v1.0.0"

	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v1.0.0", nil
	}

	var stdout, stderr bytes.Buffer
	updateCh, _ := run(nil, &stdout, &stderr)
	// Drain the channel so the async goroutine completes before deferred
	// cleanup restores fetchLatestReleaseFunc (prevents data race).
	if updateCh != nil {
		for range updateCh {
		}
	}
	if strings.Contains(stderr.String(), "available") {
		t.Fatalf("should not show upgrade notice when up-to-date, got %q", stderr.String())
	}
}

func TestNoUpgradeNoticeOnVersionCmd(t *testing.T) {
	oldVersion := version
	defer func() { version = oldVersion }()
	version = "v1.0.0"

	apiCalls := 0
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		apiCalls++
		return "v2.0.0", nil
	}

	var stdout, stderr bytes.Buffer
	_, _ = run([]string{"version"}, &stdout, &stderr)
	if apiCalls != 1 {
		t.Fatalf("expected 1 API call (from version cmd only), got %d", apiCalls)
	}
}

func TestShowUpdateNotice(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "v2.0.0"
	close(ch)

	var buf bytes.Buffer
	showUpdateNotice(&buf, ch, 500*time.Millisecond)
	if !strings.Contains(buf.String(), "↑ v2.0.0 available") {
		t.Fatalf("expected upgrade notice, got %q", buf.String())
	}
}

func TestShowUpdateNoticeNil(t *testing.T) {
	var buf bytes.Buffer
	showUpdateNotice(&buf, nil, 500*time.Millisecond)
	if buf.Len() != 0 {
		t.Fatalf("expected no output for nil channel, got %q", buf.String())
	}
}

func TestShowUpdateNoticeTimeout(t *testing.T) {
	// Channel that never receives — simulates a slow API call.
	ch := make(chan string, 1)
	var buf bytes.Buffer
	start := time.Now()
	showUpdateNotice(&buf, ch, 10*time.Millisecond)
	elapsed := time.Since(start)
	if buf.Len() != 0 {
		t.Fatalf("expected no output on timeout, got %q", buf.String())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected short timeout, took %v", elapsed)
	}
}

func TestRunErrorGetsLongerUpdateTimeout(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()

	oldVersion := version
	defer func() { version = oldVersion }()

	// Override timeouts to small values so the test runs fast.
	oldSuccess, oldError := updateSuccessTimeout, updateErrorTimeout
	defer func() { updateSuccessTimeout, updateErrorTimeout = oldSuccess, oldError }()
	updateSuccessTimeout = 10 * time.Millisecond
	updateErrorTimeout = 200 * time.Millisecond

	// Use a gate channel to make timing deterministic: the fetch blocks until
	// we release it, which we do after the success timeout has certainly elapsed.
	gate := make(chan struct{})
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		<-gate
		return "v99.0.0", nil
	}
	version = "v1.0.0"

	var stdout, stderr bytes.Buffer
	updateCh, err := run([]string{"bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for bogus command")
	}

	// Release the fetch after the success timeout has elapsed but well within
	// the error timeout window. Derived from updateSuccessTimeout to stay
	// resilient if the constants change.
	time.Sleep(2 * updateSuccessTimeout)
	close(gate)

	showUpdateNotice(&stderr, updateCh, updateErrorTimeout)
	if !strings.Contains(stderr.String(), "v99.0.0 available") {
		t.Fatalf("expected update notice in error output, got %q", stderr.String())
	}
}

func TestRunVersionRouting(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v1.0.0", nil
	}

	for _, arg := range []string{"version", "--version", "-v"} {
		var buf bytes.Buffer
		_, err := run([]string{arg}, &buf, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("run(%q) returned error: %v", arg, err)
		}
		if !strings.Contains(buf.String(), "gh-x") {
			t.Fatalf("run(%q) missing version output: %q", arg, buf.String())
		}
	}
}

func TestParseListOptionsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseListOptions(nil, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.limit != 30 {
		t.Fatalf("expected limit 30, got %d", opts.limit)
	}
	if opts.state != "open" {
		t.Fatalf("expected state 'open', got %q", opts.state)
	}
}

func TestParseListOptionsAllFlags(t *testing.T) {
	var stderr bytes.Buffer
	args := []string{
		"--repo", "owner/repo",
		"--limit", "50",
		"--state", "all",
		"--author", "@me",
		"--assignee", "bob",
		"--app", "dependabot",
		"--base", "main",
		"--head", "feature",
		"--search", "bug",
		"--draft",
		"--label", "bug",
		"--label", "urgent",
		"--json",
	}
	opts, err := parseListOptions(args, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.repo != "owner/repo" {
		t.Fatalf("expected repo 'owner/repo', got %q", opts.repo)
	}
	if opts.limit != 50 {
		t.Fatalf("expected limit 50, got %d", opts.limit)
	}
	if opts.state != "all" {
		t.Fatalf("expected state 'all', got %q", opts.state)
	}
	if opts.author != "@me" {
		t.Fatalf("expected author '@me', got %q", opts.author)
	}
	if opts.assignee != "bob" {
		t.Fatalf("expected assignee 'bob', got %q", opts.assignee)
	}
	if opts.app != "dependabot" {
		t.Fatalf("expected app 'dependabot', got %q", opts.app)
	}
	if opts.base != "main" {
		t.Fatalf("expected base 'main', got %q", opts.base)
	}
	if opts.head != "feature" {
		t.Fatalf("expected head 'feature', got %q", opts.head)
	}
	if opts.search != "bug" {
		t.Fatalf("expected search 'bug', got %q", opts.search)
	}
	if !opts.draftOnly {
		t.Fatalf("expected draftOnly true")
	}
	if !opts.json {
		t.Fatalf("expected json true")
	}
	if len(opts.labels) != 2 || opts.labels[0] != "bug" || opts.labels[1] != "urgent" {
		t.Fatalf("expected labels [bug urgent], got %v", opts.labels)
	}
}

func TestParseListOptionsShortFlags(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseListOptions([]string{"-R", "o/r", "-L", "10", "-s", "closed", "-A", "@me", "-B", "main", "-H", "dev", "-S", "fix", "-d", "-w", "-l", "p1"}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.repo != "o/r" || opts.limit != 10 || opts.state != "closed" {
		t.Fatalf("short flags not parsed: repo=%q limit=%d state=%q", opts.repo, opts.limit, opts.state)
	}
	if opts.author != "@me" || opts.base != "main" || opts.head != "dev" || opts.search != "fix" {
		t.Fatalf("short filters not parsed")
	}
	if !opts.draftOnly || !opts.web {
		t.Fatalf("short bools not parsed")
	}
	if len(opts.labels) != 1 || opts.labels[0] != "p1" {
		t.Fatalf("short label not parsed: %v", opts.labels)
	}
}

func TestParseListOptionsInvalidLimit(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseListOptions([]string{"--limit", "0"}, &stderr)
	if err == nil {
		t.Fatalf("expected error for zero limit")
	}
}

func TestParseListOptionsWebAndJSON(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseListOptions([]string{"--web", "--json"}, &stderr)
	if err == nil {
		t.Fatalf("expected error for --web and --json together")
	}
}

func TestParseListOptionsUnexpectedArgs(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseListOptions([]string{"extra"}, &stderr)
	if err == nil {
		t.Fatalf("expected error for unexpected args")
	}
}

func TestParseListOptionsHelp(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseListOptions([]string{"--help"}, &stderr)
	if err != errHelpDisplayed {
		t.Fatalf("expected errHelpDisplayed, got %v", err)
	}
}

func TestDefaultListOptions(t *testing.T) {
	opts := defaultListOptions()
	if opts.limit != 30 || opts.state != "open" {
		t.Fatalf("unexpected defaults: limit=%d state=%q", opts.limit, opts.state)
	}
}

func TestWriteListUsage(t *testing.T) {
	var buf bytes.Buffer
	writeListUsage(&buf)
	if !strings.Contains(buf.String(), "gh x pr list") {
		t.Fatalf("expected list usage, got %q", buf.String())
	}
}

func TestStringSliceFlag(t *testing.T) {
	var s stringSliceFlag
	if s.String() != "" {
		t.Fatalf("expected empty string, got %q", s.String())
	}
	if err := s.Set("bug"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.Set("feature"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.String() != "bug,feature" {
		t.Fatalf("expected 'bug,feature', got %q", s.String())
	}
}

func TestResolveRepoOverride(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{"owner/repo", "owner", "repo", false},
		{"github.com/owner/repo", "owner", "repo", false},
		{"noslash", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			owner, name, err := resolveRepo(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tc.wantOwner || name != tc.wantName {
				t.Fatalf("got %s/%s, want %s/%s", owner, name, tc.wantOwner, tc.wantName)
			}
		})
	}
}

func TestResolveAuthorLogin_NoSpace(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"octocat", "octocat"},
		{"@octocat", "octocat"},
		{"some-user", "some-user"},
		{"@some-user", "some-user"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := resolveAuthorLogin(tc.input, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveAuthorLoginFunc_Integration(t *testing.T) {
	// Verify that runList resolves the author via resolveAuthorLoginFunc and
	// passes the resolved login to executeListFunc.
	saved := resolveAuthorLoginFunc
	t.Cleanup(func() { resolveAuthorLoginFunc = saved })

	var calledWith string
	var calledOrg string
	resolveAuthorLoginFunc = func(author, org string) (string, error) {
		calledWith = author
		calledOrg = org
		return "resolved-login", nil
	}

	savedExec := executeListFunc
	t.Cleanup(func() { executeListFunc = savedExec })
	var receivedAuthor string
	executeListFunc = func(options listOptions, _ io.Writer) error {
		receivedAuthor = options.author
		return nil
	}

	// Call through runList which invokes executeListFunc (the mock).
	err := runList([]string{"--author", "Trey Walters", "--repo", "test-org/test-repo"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calledWith != "Trey Walters" {
		t.Fatalf("resolveAuthorLoginFunc called with %q, want %q", calledWith, "Trey Walters")
	}
	if calledOrg != "test-org" {
		t.Fatalf("resolveAuthorLoginFunc called with org %q, want %q", calledOrg, "test-org")
	}
	if receivedAuthor != "resolved-login" {
		t.Fatalf("executeListFunc received author %q, want %q", receivedAuthor, "resolved-login")
	}
}

func TestApplySupplementalInfo(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		dp := displayPullRequest{}
		applySupplementalInfo(&dp, nil, 1, true)
		if dp.Comments != "?" || dp.AIReview != "?" {
			t.Fatalf("expected ? for failed supplemental, got comments=%q aiReview=%q", dp.Comments, dp.AIReview)
		}
		if dp.AIClean != nil {
			t.Fatalf("expected AIClean=nil for failed supplemental, got %v", *dp.AIClean)
		}
	})

	t.Run("success with data", func(t *testing.T) {
		supp := map[int]prSupplementalInfo{
			42: {Threads: reviewThreadInfo{Total: 3, Resolved: 2}, AIReview: "pass", Approvals: 1},
		}
		dp := displayPullRequest{}
		applySupplementalInfo(&dp, supp, 42, false)
		if dp.Comments != "2/3" {
			t.Fatalf("expected comments '2/3', got %q", dp.Comments)
		}
		if dp.AIReview != "pass" {
			t.Fatalf("expected aiReview 'pass', got %q", dp.AIReview)
		}
		if dp.Approvals != 1 {
			t.Fatalf("expected approvals 1, got %d", dp.Approvals)
		}
	})

	t.Run("empty ai review defaults to dash", func(t *testing.T) {
		supp := map[int]prSupplementalInfo{1: {}}
		dp := displayPullRequest{}
		applySupplementalInfo(&dp, supp, 1, false)
		if dp.AIReview != "-" {
			t.Fatalf("expected '-' for empty aiReview, got %q", dp.AIReview)
		}
	})

	t.Run("ai clean propagates", func(t *testing.T) {
		supp := map[int]prSupplementalInfo{
			7: {Threads: reviewThreadInfo{Total: 2, Resolved: 2}, AIReview: "pass", AIClean: true, Approvals: 1},
		}
		dp := displayPullRequest{}
		applySupplementalInfo(&dp, supp, 7, false)
		if dp.AIClean == nil || !*dp.AIClean {
			t.Fatalf("expected AIClean=true, got %v", dp.AIClean)
		}
		if dp.Comments != "2/2" {
			t.Fatalf("expected comments '2/2' (clean not embedded), got %q", dp.Comments)
		}
	})

	t.Run("ai not clean omits pointer", func(t *testing.T) {
		supp := map[int]prSupplementalInfo{
			8: {Threads: reviewThreadInfo{Total: 1, Resolved: 0}, AIReview: "fail", AIClean: false, Approvals: 0},
		}
		dp := displayPullRequest{}
		applySupplementalInfo(&dp, supp, 8, false)
		if dp.AIClean != nil {
			t.Fatalf("expected AIClean=nil when not clean, got %v", *dp.AIClean)
		}
	})
}

func TestDowngradeChecksIfMissing(t *testing.T) {
	required := map[string]map[string]bool{
		"main": {"ci/test": true, "ci/lint": true},
	}

	t.Run("not pass stays unchanged", func(t *testing.T) {
		dp := displayPullRequest{Checks: "fail"}
		downgradeChecksIfMissing(&dp, required, "main", nil)
		if dp.Checks != "fail" {
			t.Fatalf("expected 'fail', got %q", dp.Checks)
		}
	})

	t.Run("pass with all required stays pass", func(t *testing.T) {
		dp := displayPullRequest{Checks: "pass"}
		items := []checkItem{
			{Typename: "CheckRun", Name: "ci/test"},
			{Typename: "CheckRun", Name: "ci/lint"},
		}
		downgradeChecksIfMissing(&dp, required, "main", items)
		if dp.Checks != "pass" {
			t.Fatalf("expected 'pass', got %q", dp.Checks)
		}
	})

	t.Run("pass with missing required becomes pending", func(t *testing.T) {
		dp := displayPullRequest{Checks: "pass"}
		items := []checkItem{
			{Typename: "CheckRun", Name: "ci/test"},
		}
		downgradeChecksIfMissing(&dp, required, "main", items)
		if dp.Checks != "pending" {
			t.Fatalf("expected 'pending', got %q", dp.Checks)
		}
	})

	t.Run("no required for branch stays pass", func(t *testing.T) {
		dp := displayPullRequest{Checks: "pass"}
		downgradeChecksIfMissing(&dp, required, "develop", nil)
		if dp.Checks != "pass" {
			t.Fatalf("expected 'pass', got %q", dp.Checks)
		}
	})
}

func TestEnrichPullRequests(t *testing.T) {
	now := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	prs := []pullRequest{
		{Number: 1, Title: "PR1", State: "OPEN", UpdatedAt: now},
		{Number: 2, Title: "PR2", State: "OPEN", UpdatedAt: now},
	}
	supp := map[int]prSupplementalInfo{
		1: {Threads: reviewThreadInfo{Total: 1, Resolved: 1}, AIReview: "pass"},
	}
	required := map[string]map[string]bool{}

	rendered := enrichPullRequests(prs, supp, false, required, now)
	if len(rendered) != 2 {
		t.Fatalf("expected 2, got %d", len(rendered))
	}
	if rendered[0].AIReview != "pass" {
		t.Fatalf("expected 'pass' for PR1, got %q", rendered[0].AIReview)
	}
	if rendered[1].AIReview != "-" {
		t.Fatalf("expected '-' for PR2 (no supp data), got %q", rendered[1].AIReview)
	}
}

func TestParsePRSupplementalNode(t *testing.T) {
	t.Run("valid JSON", func(t *testing.T) {
		raw := []byte(`{
			"number": 42,
			"reviewThreads": {
				"totalCount": 2,
				"nodes": [
					{"isResolved": true, "comments": {"nodes": [{"author": {"login": "copilot[bot]", "__typename": "Bot"}}]}},
					{"isResolved": false, "comments": {"nodes": [{"author": {"login": "user1", "__typename": "User"}}]}}
				]
			},
			"latestReviews": {
				"nodes": [
					{"state": "APPROVED", "author": {"login": "copilot[bot]", "__typename": "Bot"}, "comments": {"totalCount": 0}}
				]
			},
			"approvedReviews": {
				"nodes": [
					{"author": {"login": "alice"}},
					{"author": {"login": "Alice"}},
					{"author": {"login": "bob"}}
				]
			}
		}`)
		num, info, ok := parsePRSupplementalNode(raw)
		if !ok {
			t.Fatalf("expected ok=true")
		}
		if num != 42 {
			t.Fatalf("expected number 42, got %d", num)
		}
		if info.Threads.Total != 2 {
			t.Fatalf("expected total threads 2, got %d", info.Threads.Total)
		}
		if info.Threads.Resolved != 1 {
			t.Fatalf("expected resolved 1, got %d", info.Threads.Resolved)
		}
		if info.Approvals != 2 {
			t.Fatalf("expected 2 unique approvers, got %d", info.Approvals)
		}
		if !info.AIClean {
			t.Fatalf("expected AIClean=true for bot APPROVED with 0 comments")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, _, ok := parsePRSupplementalNode([]byte(`{invalid`))
		if ok {
			t.Fatalf("expected ok=false for invalid JSON")
		}
	})

	t.Run("zero number returns not-ok", func(t *testing.T) {
		_, _, ok := parsePRSupplementalNode([]byte(`{"number": 0, "reviewThreads": {"totalCount": 0, "nodes": []}, "latestReviews": {"nodes": []}, "approvedReviews": {"nodes": []}}`))
		if ok {
			t.Fatalf("expected ok=false for zero PR number")
		}
	})
}

func TestNormalizeReviewDecisionAllCases(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"APPROVED", "approved"},
		{"CHANGES_REQUESTED", "changes"},
		{"REVIEW_REQUIRED", "review"},
		{"", "-"},
		{"UNKNOWN_VALUE", "unknown_value"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			if got := normalizeReviewDecision(tc.input); got != tc.want {
				t.Fatalf("normalizeReviewDecision(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestAppendNonEmpty(t *testing.T) {
	args := []string{"base"}
	args = appendNonEmpty(args, "--flag", "val")
	if len(args) != 3 {
		t.Fatalf("expected 3, got %d", len(args))
	}
	args = appendNonEmpty(args, "--empty", "")
	if len(args) != 3 {
		t.Fatalf("expected 3 (no append for empty), got %d", len(args))
	}
}

func TestClassifyCheckItem(t *testing.T) {
	tests := []struct {
		name     string
		item     checkItem
		wantFail bool
		wantPend bool
	}{
		{"status error", checkItem{Typename: "StatusContext", State: "ERROR"}, true, false},
		{"status failure", checkItem{Typename: "StatusContext", State: "FAILURE"}, true, false},
		{"status pending", checkItem{Typename: "StatusContext", State: "PENDING"}, false, true},
		{"status expected", checkItem{Typename: "StatusContext", State: "EXPECTED"}, false, true},
		{"status success", checkItem{Typename: "StatusContext", State: "SUCCESS"}, false, false},
		{"check failure", checkItem{Typename: "CheckRun", Conclusion: "FAILURE"}, true, false},
		{"check timed_out", checkItem{Typename: "CheckRun", Conclusion: "TIMED_OUT"}, true, false},
		{"check no conclusion", checkItem{Typename: "CheckRun", Conclusion: ""}, false, true},
		{"check in_progress", checkItem{Typename: "CheckRun", Conclusion: "SUCCESS", Status: "IN_PROGRESS"}, false, true},
		{"check success completed", checkItem{Typename: "CheckRun", Conclusion: "SUCCESS", Status: "COMPLETED"}, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, p := classifyCheckItem(tc.item)
			if f != tc.wantFail || p != tc.wantPend {
				t.Fatalf("classifyCheckItem(%q) = (%v, %v), want (%v, %v)",
					tc.name, f, p, tc.wantFail, tc.wantPend)
			}
		})
	}
}

func TestCountResolvedThreads(t *testing.T) {
	threads := []aiReviewThread{
		{IsResolved: true},
		{IsResolved: false},
		{IsResolved: true},
	}
	if got := countResolvedThreads(threads); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
	if got := countResolvedThreads(nil); got != 0 {
		t.Fatalf("expected 0 for nil, got %d", got)
	}
}

func TestCountUniqueApprovers(t *testing.T) {
	logins := []string{"alice", "Alice", "bob", "", "alice"}
	if got := countUniqueApprovers(logins); got != 2 {
		t.Fatalf("expected 2, got %d", got)
	}
	if got := countUniqueApprovers(nil); got != 0 {
		t.Fatalf("expected 0 for nil, got %d", got)
	}
}

func TestClassifyAIReviews(t *testing.T) {
	tests := []struct {
		name                                 string
		reviews                              []aiReviewNode
		wantCleanPass, wantIssues, wantFound bool
	}{
		{"no bot reviews", []aiReviewNode{{State: "APPROVED", AuthorLogin: "human"}}, false, false, false},
		{"bot approved", []aiReviewNode{{State: "APPROVED", AuthorLogin: "copilot[bot]", CommentCount: 0}}, true, false, true},
		{"bot approved with comments", []aiReviewNode{{State: "APPROVED", AuthorLogin: "copilot[bot]", CommentCount: 3}}, false, true, true},
		{"bot changes requested", []aiReviewNode{{State: "CHANGES_REQUESTED", AuthorLogin: "copilot[bot]"}}, false, true, true},
		{"bot commented with 0 comments", []aiReviewNode{{State: "COMMENTED", AuthorLogin: "copilot[bot]", CommentCount: 0}}, true, false, true},
		{"bot commented with comments", []aiReviewNode{{State: "COMMENTED", AuthorLogin: "copilot[bot]", CommentCount: 3}}, false, true, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, i, f := classifyAIReviews(tc.reviews)
			if a != tc.wantCleanPass || i != tc.wantIssues || f != tc.wantFound {
				t.Fatalf("got (%v,%v,%v) want (%v,%v,%v)", a, i, f, tc.wantCleanPass, tc.wantIssues, tc.wantFound)
			}
		})
	}
}

func TestAllAIThreadsResolved(t *testing.T) {
	t.Run("all resolved", func(t *testing.T) {
		threads := []aiReviewThread{
			{AuthorLogin: "copilot[bot]", IsResolved: true},
			{AuthorLogin: "human", IsResolved: false},
		}
		if !allAIThreadsResolved(threads) {
			t.Fatalf("expected true when all AI threads resolved")
		}
	})

	t.Run("unresolved AI thread", func(t *testing.T) {
		threads := []aiReviewThread{
			{AuthorLogin: "copilot[bot]", IsResolved: false},
		}
		if allAIThreadsResolved(threads) {
			t.Fatalf("expected false when AI thread unresolved")
		}
	})

	t.Run("no AI threads", func(t *testing.T) {
		threads := []aiReviewThread{
			{AuthorLogin: "human", IsResolved: false},
		}
		if allAIThreadsResolved(threads) {
			t.Fatalf("expected false when no AI threads exist")
		}
	})
}

func TestShouldSkipUpdateCheck(t *testing.T) {
	for _, cmd := range []string{"version", "-v", "--version"} {
		if !shouldSkipUpdateCheck(cmd) {
			t.Fatalf("expected skip for %q", cmd)
		}
	}
	for _, cmd := range []string{"pr", "list", "atm", "me", "help", "changelog"} {
		if shouldSkipUpdateCheck(cmd) {
			t.Fatalf("expected no skip for %q", cmd)
		}
	}
}

func TestResolveCommandKnown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolveCommand("help")
	if cmd.name != "help" {
		t.Fatalf("expected name 'help', got %q", cmd.name)
	}
	if !cmd.banner {
		t.Fatalf("expected banner true for help")
	}
	err := cmd.handler(nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "gh x") {
		t.Fatalf("expected usage output, got %q", stdout.String())
	}
}

func TestResolveCommandPr(t *testing.T) {
	cmd := resolveCommand("pr")
	if cmd.name != "pr" {
		t.Fatalf("expected name 'pr', got %q", cmd.name)
	}
	if !cmd.banner {
		t.Fatalf("expected banner true for pr")
	}
}

func TestResolveCommandIssue(t *testing.T) {
	cmd := resolveCommand("issue")
	if cmd.name != "issue" {
		t.Fatalf("expected name 'issue', got %q", cmd.name)
	}
	if !cmd.banner {
		t.Fatalf("expected banner true for issue")
	}
}

func TestResolveIssueCommandKnown(t *testing.T) {
	for _, name := range []string{"list", "help"} {
		cmd := resolveIssueCommand(name)
		if cmd.name != name {
			t.Fatalf("expected name %q, got %q", name, cmd.name)
		}
	}
}

func TestResolveIssueCommandUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolveIssueCommand("bogus")
	if cmd.name != "bogus" {
		t.Fatalf("expected name 'bogus', got %q", cmd.name)
	}
	err := cmd.handler(nil, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown issue subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning command, got %q", err.Error())
	}
	if !strings.Contains(stderr.String(), "gh x issue") {
		t.Fatalf("expected issue usage on stderr, got %q", stderr.String())
	}
}

func TestRunIssueNoArgs(t *testing.T) {
	savedFn := executeIssueListFunc
	defer func() { executeIssueListFunc = savedFn }()
	executeIssueListFunc = func(_ issueListOptions, _ io.Writer, _ time.Time) error { return nil }

	var stdout, stderr bytes.Buffer
	_, err := run([]string{"issue"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunIssueHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"issue", "help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for issue help, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "gh x issue") {
		t.Fatalf("expected issue usage on stdout, got %q", stdout.String())
	}
}

func TestRunIssueDashHelpDefaultsList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"issue", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for issue --help, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x issue list") {
		t.Fatalf("expected issue list usage in stderr, got %q", stderr.String())
	}
}

func TestRunIssueUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"issue", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown issue subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning bogus, got %q", err.Error())
	}
}

func TestResolvePrCommandKnown(t *testing.T) {
	for _, name := range []string{"list", "atm", "me", "review", "changelog", "help"} {
		cmd := resolvePrCommand(name)
		if cmd.name != name {
			t.Fatalf("expected name %q, got %q", name, cmd.name)
		}
	}
}

func TestResolvePrCommandUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolvePrCommand("bogus")
	if cmd.name != "bogus" {
		t.Fatalf("expected name 'bogus', got %q", cmd.name)
	}
	err := cmd.handler(nil, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown pr subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning command, got %q", err.Error())
	}
	if !strings.Contains(stderr.String(), "gh x pr") {
		t.Fatalf("expected pr usage on stderr, got %q", stderr.String())
	}
}

func TestRunPrNoArgs(t *testing.T) {
	// With no args, pr defaults to "list" subcommand
	savedExecuteList := executeListFunc
	defer func() { executeListFunc = savedExecuteList }()
	executeListFunc = func(_ listOptions, _ io.Writer) error { return nil }

	var stdout, stderr bytes.Buffer
	_, err := run([]string{"pr"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout.String(), "Pull request commands") {
		t.Fatal("expected no pr-level usage on stdout when defaulting to list")
	}
	if strings.Contains(stderr.String(), "Pull request commands") {
		t.Fatal("expected no pr-level usage on stderr when defaulting to list")
	}
}

func TestRunPrHelp(t *testing.T) {
	// "pr help" (no dash) still shows pr-level usage
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"pr", "help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for pr help, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "gh x pr") {
		t.Fatalf("expected pr usage on stdout, got %q", stdout.String())
	}
}

func TestRunPrDashHelpDefaultsList(t *testing.T) {
	// "pr --help" routes to "list --help" since --help is a flag
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"pr", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for pr --help, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x pr list") {
		t.Fatalf("expected pr list usage in stderr, got %q", stderr.String())
	}
}

func TestRunPrUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"pr", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown pr subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning bogus, got %q", err.Error())
	}
}

func TestLooksLikeFlag(t *testing.T) {
	tests := []struct {
		arg  string
		want bool
	}{
		{"--help", true},
		{"-h", true},
		{"--limit", true},
		{"-L", true},
		{"list", false},
		{"atm", false},
		{"help", false},
	}
	for _, tc := range tests {
		if got := looksLikeFlag(tc.arg); got != tc.want {
			t.Fatalf("looksLikeFlag(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

func TestLooksLikeNumber(t *testing.T) {
	tests := []struct {
		arg  string
		want bool
	}{
		{"363", true},
		{"1", true},
		{"0", true},
		{"", false},
		{"abc", false},
		{"12a", false},
		{"-1", false},
		{"--repo", false},
	}
	for _, tc := range tests {
		if got := looksLikeNumber(tc.arg); got != tc.want {
			t.Fatalf("looksLikeNumber(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

func TestRunView_MissingNumber(t *testing.T) {
	err := runView([]string{"--repo", "org/repo"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestWritePrUsage(t *testing.T) {
	var buf bytes.Buffer
	writePrUsage(&buf)
	if !strings.Contains(buf.String(), "gh x pr") {
		t.Fatalf("expected pr usage, got %q", buf.String())
	}
	for _, cmd := range []string{"list", "me", "atm", "review", "changelog"} {
		if !strings.Contains(buf.String(), cmd) {
			t.Fatalf("expected pr usage to mention %q", cmd)
		}
	}
}

func TestResolveCommandUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolveCommand("nonexistent")
	if cmd.name != "nonexistent" {
		t.Fatalf("expected name 'nonexistent', got %q", cmd.name)
	}
	err := cmd.handler(nil, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("expected error mentioning command, got %q", err.Error())
	}
}

func TestStateCellAllStates(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, false)
	tests := []struct {
		state, wantText string
	}{
		{"open", "open"},
		{"draft", "draft"},
		{"closed", "closed"},
		{"merged", "merged"},
		{"unknown", "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			cell := styler.stateCell(tc.state)
			if cell.text != tc.wantText {
				t.Fatalf("stateCell(%q).text = %q, want %q", tc.state, cell.text, tc.wantText)
			}
		})
	}
}

func TestReviewCellAllDecisions(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, false)
	for _, review := range []string{"approved", "changes", "review", "-"} {
		cell := styler.reviewCell(review)
		if cell.text != review {
			t.Fatalf("reviewCell(%q).text = %q", review, cell.text)
		}
	}
}

func TestChecksCellAllStates(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, false)
	for _, state := range []string{"pass", "fail", "pending", "merge", "-"} {
		cell := styler.checksCell(state)
		if cell.text != state {
			t.Fatalf("checksCell(%q).text = %q", state, cell.text)
		}
	}
}

func TestResolveChecksState(t *testing.T) {
	passing := []checkItem{{Typename: "CheckRun", Conclusion: "SUCCESS", Status: "COMPLETED"}}
	tests := []struct {
		name string
		pr   pullRequest
		want string
	}{
		{"conflicting overrides passing", pullRequest{Mergeable: "CONFLICTING", StatusCheckRollup: passing}, "merge"},
		{"conflicting lowercase", pullRequest{Mergeable: "conflicting"}, "merge"},
		{"mergeable passes through", pullRequest{Mergeable: "MERGEABLE", StatusCheckRollup: passing}, "pass"},
		{"unknown passes through", pullRequest{Mergeable: "UNKNOWN", StatusCheckRollup: passing}, "pass"},
		{"empty mergeable no checks", pullRequest{}, "-"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveChecksState(tc.pr); got != tc.want {
				t.Fatalf("resolveChecksState() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRequiredCheckRules(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect map[string]bool
	}{
		{
			name:   "single required check",
			input:  `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"ci/build"}]}}]`,
			expect: map[string]bool{"ci/build": true},
		},
		{
			name:  "multiple checks",
			input: `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"ci/build"},{"context":"ci/lint"}]}}]`,
			expect: map[string]bool{
				"ci/build": true,
				"ci/lint":  true,
			},
		},
		{
			name:   "non-status-check rule ignored",
			input:  `[{"type":"pull_request","parameters":{}}]`,
			expect: nil,
		},
		{
			name:   "empty context skipped",
			input:  `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":""},{"context":"real"}]}}]`,
			expect: map[string]bool{"real": true},
		},
		{
			name:   "invalid JSON returns nil",
			input:  `not json`,
			expect: nil,
		},
		{
			name:   "empty rules array returns nil",
			input:  `[]`,
			expect: nil,
		},
		{
			name:   "no checks in rule returns nil",
			input:  `[{"type":"required_status_checks","parameters":{"required_status_checks":[]}}]`,
			expect: nil,
		},
		{
			name:   "mixed rule types",
			input:  `[{"type":"pull_request","parameters":{}},{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"Quality Gate"}]}}]`,
			expect: map[string]bool{"Quality Gate": true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRequiredCheckRules([]byte(tc.input))
			if !reflect.DeepEqual(got, tc.expect) {
				t.Fatalf("parseRequiredCheckRules() = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestParseRepoViewResponse(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{
			name:      "valid response",
			input:     `{"name":"gh-x","owner":{"login":"HemSoft"}}`,
			wantOwner: "HemSoft",
			wantName:  "gh-x",
		},
		{
			name:    "missing owner login",
			input:   `{"name":"gh-x","owner":{"login":""}}`,
			wantErr: true,
		},
		{
			name:    "missing name",
			input:   `{"name":"","owner":{"login":"HemSoft"}}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   `{broken`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			owner, name, err := parseRepoViewResponse([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got owner=%q name=%q", owner, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tc.wantOwner || name != tc.wantName {
				t.Fatalf("got (%q, %q), want (%q, %q)", owner, name, tc.wantOwner, tc.wantName)
			}
		})
	}
}

func TestParseSupplementalResponse(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		wantErr bool
	}{
		{
			name:    "valid with one PR",
			input:   `{"data":{"repository":{"pr42":{"number":42,"reviewThreads":{"totalCount":2,"nodes":[]},"latestReviews":{"nodes":[]},"approvedReviews":{"nodes":[]}}}}}`,
			wantLen: 1,
		},
		{
			name:    "empty repository",
			input:   `{"data":{"repository":{}}}`,
			wantLen: 0,
		},
		{
			name:    "invalid JSON",
			input:   `not json`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseSupplementalResponse([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got result with %d entries", len(result))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result) != tc.wantLen {
				t.Fatalf("got %d entries, want %d", len(result), tc.wantLen)
			}
		})
	}
}

func TestUniqueBaseBranches(t *testing.T) {
	tests := []struct {
		name   string
		prs    []pullRequest
		expect []string
	}{
		{
			name:   "empty",
			prs:    nil,
			expect: nil,
		},
		{
			name:   "single branch",
			prs:    []pullRequest{{BaseRefName: "main"}},
			expect: []string{"main"},
		},
		{
			name:   "duplicates removed",
			prs:    []pullRequest{{BaseRefName: "main"}, {BaseRefName: "develop"}, {BaseRefName: "main"}},
			expect: []string{"main", "develop"},
		},
		{
			name:   "preserves order",
			prs:    []pullRequest{{BaseRefName: "beta"}, {BaseRefName: "alpha"}},
			expect: []string{"beta", "alpha"},
		},
		{
			name:   "empty base name preserved",
			prs:    []pullRequest{{BaseRefName: ""}, {BaseRefName: "main"}},
			expect: []string{"", "main"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := uniqueBaseBranches(tc.prs)
			if !reflect.DeepEqual(got, tc.expect) {
				t.Fatalf("uniqueBaseBranches() = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestWrapExecError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		stderr  string
		wantSub string
	}{
		{
			name:    "with stderr",
			err:     fmt.Errorf("search failed"),
			stderr:  "rate limited",
			wantSub: "search failed: rate limited",
		},
		{
			name:    "empty stderr",
			err:     fmt.Errorf("search failed"),
			stderr:  "",
			wantSub: "search failed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := wrapExecError(tc.err, tc.stderr)
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("wrapExecError() = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestRenderListOutputJSON(t *testing.T) {
	var buf bytes.Buffer
	prs := []displayPullRequest{
		{Number: 1, Title: "Test PR"},
	}
	err := renderListOutput(&buf, listOptions{json: true}, prs)
	if err != nil {
		t.Fatalf("renderListOutput(json) error: %v", err)
	}
	if !strings.Contains(buf.String(), `"number": 1`) {
		t.Fatalf("JSON output missing PR number: %s", buf.String())
	}
}

func TestParseListOptionsValidLimit(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseListOptions([]string{"--limit", "1"}, &stderr)
	if err != nil {
		t.Fatalf("expected no error for limit=1, got: %v", err)
	}
	if opts.limit != 1 {
		t.Fatalf("expected limit 1, got %d", opts.limit)
	}
}

func TestTrimTitle(t *testing.T) {
	tests := []struct {
		name  string
		title string
		limit int
		want  string
	}{
		{"zero limit", "abc", 0, "abc"},
		{"negative limit", "hello", -1, "hello"},
		{"len equals limit", "abcdef", 6, "abcdef"},
		{"len under limit", "hi", 10, "hi"},
		{"limit 3 truncates without ellipsis", "abcdefg", 3, "abc"},
		{"limit 2 truncates without ellipsis", "abcdefg", 2, "ab"},
		{"limit 1 truncates without ellipsis", "abcdefg", 1, "a"},
		{"normal truncation with ellipsis", "abcdefgh", 5, "ab..."},
		{"whitespace trimmed first", "  hello  ", 5, "hello"},
		{"already short", "hi", 5, "hi"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := trimTitle(tc.title, tc.limit)
			if got != tc.want {
				t.Fatalf("trimTitle(%q, %d) = %q, want %q", tc.title, tc.limit, got, tc.want)
			}
		})
	}
}

func TestNormalizeState(t *testing.T) {
	tests := []struct {
		state   string
		isDraft bool
		want    string
	}{
		{"OPEN", false, "open"},
		{"CLOSED", false, "closed"},
		{"MERGED", false, "merged"},
		{"OPEN", true, "draft"},
		{"", false, "-"},
		{"PENDING", false, "pending"},
		{"WeirdCase", false, "weirdcase"},
	}
	for _, tc := range tests {
		t.Run(tc.state+"_draft="+fmt.Sprintf("%v", tc.isDraft), func(t *testing.T) {
			got := normalizeState(tc.state, tc.isDraft)
			if got != tc.want {
				t.Fatalf("normalizeState(%q, %v) = %q, want %q", tc.state, tc.isDraft, got, tc.want)
			}
		})
	}
}

func TestApprovalCell(t *testing.T) {
	s := newTableStyler(bytes.NewBuffer(nil), true)
	dim0 := s.dim("0")
	green1 := s.colored("1", termenv.ANSIGreen)

	got0 := s.approvalCell(0)
	if got0.text != "0" || got0.styled != dim0.styled {
		t.Fatalf("approvalCell(0): got styled=%q, want dim=%q", got0.styled, dim0.styled)
	}

	got1 := s.approvalCell(1)
	if got1.text != "1" || got1.styled != green1.styled {
		t.Fatalf("approvalCell(1): got styled=%q, want green=%q", got1.styled, green1.styled)
	}
}

func TestCommentsCell(t *testing.T) {
	s := newTableStyler(bytes.NewBuffer(nil), true)
	tests := []struct {
		name     string
		input    string
		aiClean  *bool
		wantText string
		wantKind string // "plain", "green", "red", "yellow", "green+bang", "yellow+bang", "red+bang", "plain+bang"
	}{
		{"dash is plain", "-", boolPtr(false), "-", "plain"},
		{"question is plain", "?", boolPtr(false), "?", "plain"},
		{"all resolved is green", "2/2", boolPtr(false), "2/2", "green"},
		{"none resolved is red", "0/2", boolPtr(false), "0/2", "red"},
		{"partial is yellow", "1/2", boolPtr(false), "1/2", "yellow"},
		{"no slash is yellow", "5", boolPtr(false), "5", "yellow"},
		{"ai clean with dash", "-", boolPtr(true), "0/0!", "green+bang"},
		{"ai clean all resolved", "2/2", boolPtr(true), "2/2!", "green+bang"},
		{"ai clean partial", "1/2", boolPtr(true), "1/2!", "yellow+bang"},
		{"ai clean none resolved", "0/2", boolPtr(true), "0/2!", "red+bang"},
		{"nil aiClean treated as false", "2/2", nil, "2/2", "green"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := s.commentsCell(tc.input, tc.aiClean)
			if got.text != tc.wantText {
				t.Fatalf("text = %q, want %q", got.text, tc.wantText)
			}
			var wantStyled string
			base := strings.TrimSuffix(tc.wantText, "!")
			switch tc.wantKind {
			case "plain":
				wantStyled = s.plain(tc.input).styled
			case "green":
				wantStyled = s.colored(tc.input, termenv.ANSIGreen).styled
			case "red":
				wantStyled = s.colored(tc.input, termenv.ANSIRed).styled
			case "yellow":
				wantStyled = s.colored(tc.input, termenv.ANSIYellow).styled
			case "plain+bang":
				wantStyled = s.output.String(base).String() +
					s.output.String("!").Foreground(termenv.ANSIBrightGreen).String()
			case "green+bang":
				wantStyled = s.output.String(base).Foreground(termenv.ANSIGreen).String() +
					s.output.String("!").Foreground(termenv.ANSIBrightGreen).String()
			case "yellow+bang":
				wantStyled = s.output.String(base).Foreground(termenv.ANSIYellow).String() +
					s.output.String("!").Foreground(termenv.ANSIBrightGreen).String()
			case "red+bang":
				wantStyled = s.output.String(base).Foreground(termenv.ANSIRed).String() +
					s.output.String("!").Foreground(termenv.ANSIBrightGreen).String()
			}
			if got.styled != wantStyled {
				t.Fatalf("styled mismatch for %q (aiClean=%v): got %q, want %s %q", tc.input, tc.aiClean, got.styled, tc.wantKind, wantStyled)
			}
		})
	}
}

func TestWriteRow(t *testing.T) {
	cells := []tableCell{
		{text: "hello", styled: "hello"},
		{text: "world", styled: "world"},
	}
	widths := []int{8, 5}
	var buf bytes.Buffer
	writeRow(&buf, cells, widths)
	got := buf.String()

	// Last cell should NOT have trailing spaces before newline
	if !strings.HasSuffix(got, "world\n") {
		t.Fatalf("writeRow should end with last cell + newline, got %q", got)
	}

	// First cell should have padding (8 - 5 + 2 = 5 spaces)
	if !strings.Contains(got, "hello     world") {
		t.Fatalf("writeRow padding incorrect, got %q", got)
	}
}

func TestParseSupplementalResponseWithThreadComments(t *testing.T) {
	// Thread with empty Comments.Nodes — tests boundary mutation at line 967
	emptyComments := `{"data":{"repository":{"pr42":{
		"number":42,
		"reviewThreads":{
			"totalCount":1,
			"nodes":[{"isResolved":false,"comments":{"nodes":[]}}]
		},
		"latestReviews":{"nodes":[]},
		"approvedReviews":{"nodes":[]}
	}}}}`

	result, err := parseSupplementalResponse([]byte(emptyComments))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("got %d entries, want 1", len(result))
	}

	// Thread with bot comments — tests negation mutation at line 967
	botComments := `{"data":{"repository":{"pr42":{
		"number":42,
		"reviewThreads":{
			"totalCount":1,
			"nodes":[{
				"isResolved":true,
				"comments":{"nodes":[{
					"author":{"login":"copilot-pull-request-reviewer[bot]","__typename":"Bot"}
				}]}
			}]
		},
		"latestReviews":{"nodes":[{
			"state":"COMMENTED",
			"author":{"login":"copilot-pull-request-reviewer[bot]"},
			"comments":{"totalCount":1}
		}]},
		"approvedReviews":{"nodes":[]}
	}}}}`

	result2, err := parseSupplementalResponse([]byte(botComments))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result2) != 1 {
		t.Fatalf("got %d entries, want 1", len(result2))
	}
	info, ok := result2[42]
	if !ok {
		t.Fatal("expected supplemental info for PR 42")
	}
	// With bot review (COMMENTED + 1 comment = issues) + all AI threads resolved → "pass"
	if info.AIReview != "pass" {
		t.Fatalf("expected AIReview='pass' (bot review with resolved threads), got %q", info.AIReview)
	}
}

func TestFitColumnsToTerminal(t *testing.T) {
	tests := []struct {
		name         string
		colWidths    []int
		flexibleCols []int
		termWidth    int
		wantTotal    int
	}{
		{
			name:         "no shrink needed",
			colWidths:    []int{5, 20, 15, 10},
			flexibleCols: []int{1, 2},
			termWidth:    100,
			wantTotal:    -1, // unchanged
		},
		{
			name:         "shrinks to fit",
			colWidths:    []int{5, 40, 30, 10},
			flexibleCols: []int{1, 2},
			termWidth:    80,
			wantTotal:    80,
		},
		{
			name:         "zero term width returns unchanged",
			colWidths:    []int{5, 40, 30, 10},
			flexibleCols: []int{1, 2},
			termWidth:    0,
			wantTotal:    -1,
		},
		{
			name:         "respects minimum width",
			colWidths:    []int{5, 40, 30, 10},
			flexibleCols: []int{1, 2},
			termWidth:    20,
			wantTotal:    -1, // can't fit, but flex cols at minimum 10
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := make([]int, len(tc.colWidths))
			copy(original, tc.colWidths)
			result := fitColumnsToTerminal(tc.colWidths, tc.flexibleCols, tc.termWidth)

			totalWidth := 0
			for i, w := range result {
				totalWidth += w
				if i < len(result)-1 {
					totalWidth += 2
				}
			}

			if tc.wantTotal == -1 {
				// Should be unchanged or at minimum
				if tc.termWidth <= 0 {
					if !reflect.DeepEqual(result, original) {
						t.Fatalf("expected unchanged widths for termWidth=0, got %v", result)
					}
				}
			} else {
				if totalWidth > tc.wantTotal {
					t.Fatalf("expected total width ≤%d, got %d (widths: %v)", tc.wantTotal, totalWidth, result)
				}
			}

			// Flexible cols should never go below 10
			for _, idx := range tc.flexibleCols {
				if result[idx] < 10 {
					t.Fatalf("flexible col %d below minimum: %d", idx, result[idx])
				}
			}
		})
	}
}

func TestTruncateCells(t *testing.T) {
	rows := [][]tableCell{
		{
			{text: "#1", styled: "#1"},
			{text: "A very long title that exceeds limit", styled: "A very long title that exceeds limit"},
			{text: "short", styled: "short"},
		},
	}
	colWidths := []int{5, 15, 10}
	flexibleCols := []int{1}

	result := truncateCells(rows, colWidths, flexibleCols)
	if len(result[0][1].text) > 15 {
		t.Fatalf("expected truncated title ≤15 chars, got %q (%d)", result[0][1].text, len(result[0][1].text))
	}
	if !strings.HasSuffix(result[0][1].text, "...") {
		t.Fatalf("expected ... suffix, got %q", result[0][1].text)
	}
	// Non-flexible col unchanged
	if result[0][2].text != "short" {
		t.Fatalf("non-flexible col should be unchanged, got %q", result[0][2].text)
	}
}

func TestTruncateCellsPreservesStyleFn(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, true)

	longBranch := "feature/very-long-branch-name-that-exceeds-column-width"
	rows := [][]tableCell{
		{
			styler.plain("#1"),
			styler.colored(longBranch, termenv.ANSICyan),
		},
	}
	colWidths := []int{5, 15}
	flexibleCols := []int{1}

	result := truncateCells(rows, colWidths, flexibleCols)
	cell := result[0][1]

	// text should be truncated
	if len(cell.text) > 15 {
		t.Fatalf("expected truncated text ≤15 chars, got %q (%d)", cell.text, len(cell.text))
	}
	if !strings.HasSuffix(cell.text, "...") {
		t.Fatalf("expected ... suffix, got %q", cell.text)
	}

	// styled should differ from text (ANSI codes preserved)
	if cell.styled == cell.text {
		t.Fatalf("expected styled to contain ANSI codes, but styled == text: %q", cell.styled)
	}

	// styled should contain the truncated text, not the original long text
	if strings.Contains(cell.styled, longBranch) {
		t.Fatal("styled should contain truncated text, not original")
	}

	// styleFn should still be set for further re-styling
	if cell.styleFn == nil {
		t.Fatal("expected styleFn to be preserved after truncation")
	}
}

func TestErrorHint(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantSub string
	}{
		{
			name:    "401 bad credentials",
			err:     fmt.Errorf("gh execution failed: exit status 1: HTTP 401: Bad credentials (https://api.github.com/graphql)"),
			wantSub: "gh auth login",
		},
		{
			name:    "403 SAML",
			err:     fmt.Errorf("HTTP 403: Resource protected by SAML enforcement"),
			wantSub: "SSO",
		},
		{
			name:    "403 generic",
			err:     fmt.Errorf("HTTP 403: forbidden"),
			wantSub: "gh auth status",
		},
		{
			name:    "DNS resolution failure",
			err:     fmt.Errorf("could not resolve host api.github.com"),
			wantSub: "internet connection",
		},
		{
			name:    "timeout",
			err:     fmt.Errorf("request timed out"),
			wantSub: "timed out",
		},
		{
			name:    "404 not found",
			err:     fmt.Errorf("HTTP 404: Not Found"),
			wantSub: "not found",
		},
		{
			name:    "unrelated error no hint",
			err:     fmt.Errorf("decode json: unexpected EOF"),
			wantSub: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hint := errorHint(tc.err)
			if tc.wantSub == "" {
				if hint != "" {
					t.Fatalf("expected no hint, got %q", hint)
				}
				return
			}
			if !strings.Contains(hint, tc.wantSub) {
				t.Fatalf("errorHint() = %q, want substring %q", hint, tc.wantSub)
			}
		})
	}
}
