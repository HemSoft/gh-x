package main

import (
	"fmt"
	"testing"
	"time"
)

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
