package main

import (
	"reflect"
	"testing"
	"time"
)

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

	t.Run("review with missing required becomes pending", func(t *testing.T) {
		dp := displayPullRequest{Checks: "review"}
		items := []checkItem{
			{Typename: "CheckRun", Name: "ci/test"},
			{Typename: "CheckRun", Name: "cubic · AI code reviewer"},
		}
		downgradeChecksIfMissing(&dp, required, "main", items)
		if dp.Checks != "pending" {
			t.Fatalf("expected 'pending', got %q", dp.Checks)
		}
	})

	t.Run("review with all required stays review", func(t *testing.T) {
		dp := displayPullRequest{Checks: "review"}
		items := []checkItem{
			{Typename: "CheckRun", Name: "ci/test"},
			{Typename: "CheckRun", Name: "ci/lint"},
			{Typename: "CheckRun", Name: "cubic · AI code reviewer"},
		}
		downgradeChecksIfMissing(&dp, required, "main", items)
		if dp.Checks != "review" {
			t.Fatalf("expected 'review', got %q", dp.Checks)
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

func TestEnrichPullRequestsUsesSuccessfulCubicCheckAsCurrentHeadAIReview(t *testing.T) {
	now := time.Date(2026, 8, 23, 19, 42, 17, 0, time.UTC)
	prs := []pullRequest{{
		Number:    25,
		Title:     "Route enterprise monitors by host",
		State:     "OPEN",
		UpdatedAt: now,
		StatusCheckRollup: []checkItem{{
			Typename:    "CheckRun",
			Name:        "cubic · AI code reviewer",
			Status:      "COMPLETED",
			Conclusion:  "SUCCESS",
			CompletedAt: now,
		}},
	}}
	supp := map[int]prSupplementalInfo{
		25: {Threads: reviewThreadInfo{Total: 11, Resolved: 11}, AIReview: "-"},
	}

	rendered := enrichPullRequests(prs, supp, false, nil, now)
	if rendered[0].AIReview != "pass" {
		t.Fatalf("AIReview = %q, want pass from current-head Cubic check", rendered[0].AIReview)
	}
	if rendered[0].AIClean == nil || !*rendered[0].AIClean {
		t.Fatal("expected successful current-head Cubic check to set AIClean")
	}
}

func TestEnrichPullRequestsDoesNotTrustCubicCheckWithoutSupplementalEntry(t *testing.T) {
	now := time.Date(2026, 8, 23, 21, 32, 21, 0, time.UTC)
	prs := []pullRequest{{
		Number:    25,
		Title:     "Route enterprise monitors by host",
		State:     "OPEN",
		UpdatedAt: now,
		StatusCheckRollup: []checkItem{{
			Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "SUCCESS",
		}},
	}}

	rendered := enrichPullRequests(prs, map[int]prSupplementalInfo{}, false, nil, now)
	if rendered[0].AIReview != "-" {
		t.Fatalf("AIReview = %q, want - when supplemental PR data is missing", rendered[0].AIReview)
	}
	if rendered[0].AIClean != nil {
		t.Fatalf("expected missing supplemental PR data to leave AIClean unset, got %v", *rendered[0].AIClean)
	}
}

func TestDetectAIReviewCheck(t *testing.T) {
	now := time.Date(2026, 8, 23, 19, 42, 17, 0, time.UTC)
	tests := []struct {
		name   string
		checks []checkItem
		want   string
	}{
		{name: "none", want: "-"},
		{
			name: "successful Cubic review",
			checks: []checkItem{{
				Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "SUCCESS",
			}},
			want: "pass",
		},
		{
			name: "failed Cubic review",
			checks: []checkItem{{
				Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "FAILURE",
			}},
			want: "fail",
		},
		{
			name: "cancelled Cubic review",
			checks: []checkItem{{
				Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "CANCELLED",
			}},
			want: "fail",
		},
		{
			name: "pending Cubic review",
			checks: []checkItem{{
				Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "IN_PROGRESS",
			}},
			want: "-",
		},
		{
			name: "unrelated successful check",
			checks: []checkItem{{
				Typename: "CheckRun", Name: "Build & Test", Status: "COMPLETED", Conclusion: "SUCCESS",
			}},
			want: "-",
		},
		{
			name: "latest rerun wins",
			checks: []checkItem{
				{Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "FAILURE", CompletedAt: now.Add(-time.Minute)},
				{Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "SUCCESS", CompletedAt: now},
			},
			want: "pass",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectAIReviewCheck(tc.checks); got != tc.want {
				t.Fatalf("detectAIReviewCheck() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyAIReviewCheckFailsClosed(t *testing.T) {
	success := []checkItem{{
		Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "SUCCESS",
	}}
	failure := []checkItem{{
		Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "FAILURE",
	}}
	cancelled := []checkItem{{
		Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "CANCELLED",
	}}
	tests := []struct {
		name               string
		display            displayPullRequest
		info               prSupplementalInfo
		checks             []checkItem
		supplementalFailed bool
		wantReview         string
		wantClean          bool
	}{
		{
			name: "all threads resolved", display: displayPullRequest{AIReview: "-"},
			info: prSupplementalInfo{Threads: reviewThreadInfo{Total: 2, Resolved: 2}}, checks: success, wantReview: "pass", wantClean: true,
		},
		{
			name: "unresolved human thread", display: displayPullRequest{AIReview: "-"},
			info: prSupplementalInfo{Threads: reviewThreadInfo{Total: 2, Resolved: 1}}, checks: success, wantReview: "pass", wantClean: true,
		},
		{
			name: "unresolved AI thread", display: displayPullRequest{AIReview: "-"},
			info: prSupplementalInfo{Threads: reviewThreadInfo{Total: 2, Resolved: 1}, HasUnresolvedAIThreads: true}, checks: success, wantReview: "-",
		},
		{
			name: "current review failure", display: displayPullRequest{AIReview: "fail"},
			info: prSupplementalInfo{Threads: reviewThreadInfo{}}, checks: success, wantReview: "fail",
		},
		{
			name: "failed check clears prior clean state", display: displayPullRequest{AIReview: "pass", AIClean: boolPtr(true)},
			info: prSupplementalInfo{Threads: reviewThreadInfo{}}, checks: failure, wantReview: "fail",
		},
		{
			name: "cancelled check clears prior clean state", display: displayPullRequest{AIReview: "pass", AIClean: boolPtr(true)},
			info: prSupplementalInfo{Threads: reviewThreadInfo{}}, checks: cancelled, wantReview: "fail",
		},
		{
			name: "incomplete supplemental data", display: displayPullRequest{AIReview: "?"},
			info: prSupplementalInfo{Threads: reviewThreadInfo{}}, checks: success, supplementalFailed: true, wantReview: "?",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dp := tc.display
			applyAIReviewCheck(&dp, tc.info, tc.checks, tc.supplementalFailed)
			if dp.AIReview != tc.wantReview {
				t.Fatalf("AIReview = %q, want %q", dp.AIReview, tc.wantReview)
			}
			gotClean := dp.AIClean != nil && *dp.AIClean
			if gotClean != tc.wantClean {
				t.Fatalf("AIClean = %v, want %v", gotClean, tc.wantClean)
			}
		})
	}
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
		{"check cancelled", checkItem{Typename: "CheckRun", Conclusion: "CANCELLED"}, true, false},
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

func TestNormalizeCheckStateUsesLatestNamedCheckRun(t *testing.T) {
	oldRun := time.Date(2026, 8, 3, 6, 47, 0, 0, time.UTC)
	newRun := time.Date(2026, 8, 3, 19, 7, 0, 0, time.UTC)
	checkRun := func(workflow, conclusion, status string, startedAt time.Time) checkItem {
		return checkItem{
			Typename:     "CheckRun",
			Name:         "agent",
			WorkflowName: workflow,
			Status:       status,
			Conclusion:   conclusion,
			StartedAt:    startedAt,
		}
	}

	tests := []struct {
		name  string
		items []checkItem
		want  string
	}{
		{
			name: "new success replaces old failure",
			items: []checkItem{
				checkRun("CI Review", "FAILURE", "COMPLETED", oldRun),
				checkRun("CI Review", "SUCCESS", "COMPLETED", newRun),
			},
			want: "pass",
		},
		{
			name: "api order does not matter",
			items: []checkItem{
				checkRun("CI Review", "SUCCESS", "COMPLETED", newRun),
				checkRun("CI Review", "FAILURE", "COMPLETED", oldRun),
			},
			want: "pass",
		},
		{
			name: "new failure replaces old success",
			items: []checkItem{
				checkRun("CI Review", "SUCCESS", "COMPLETED", oldRun),
				checkRun("CI Review", "FAILURE", "COMPLETED", newRun),
			},
			want: "fail",
		},
		{
			name: "new pending replaces old success",
			items: []checkItem{
				checkRun("CI Review", "SUCCESS", "COMPLETED", oldRun),
				checkRun("CI Review", "", "IN_PROGRESS", newRun),
			},
			want: "pending",
		},
		{
			name: "same job name in different workflows remains distinct",
			items: []checkItem{
				checkRun("CI Review", "SUCCESS", "COMPLETED", newRun),
				checkRun("Other Workflow", "FAILURE", "COMPLETED", oldRun),
			},
			want: "fail",
		},
		{
			name: "new status context replaces old state without timestamps",
			items: []checkItem{
				{Typename: "StatusContext", Context: "deploy", State: "FAILURE"},
				{Typename: "StatusContext", Context: "deploy", State: "SUCCESS"},
			},
			want: "pass",
		},
		{
			name: "check run without identity remains distinct",
			items: []checkItem{
				checkRun("CI", "SUCCESS", "COMPLETED", newRun),
				{Typename: "CheckRun", Status: "COMPLETED", Conclusion: "FAILURE"},
			},
			want: "fail",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCheckState(tc.items); got != tc.want {
				t.Fatalf("normalizeCheckState() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeCheckStateDistinguishesReviewerOnlyWaits(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	passingCI := checkItem{Typename: "CheckRun", Name: "CI", Status: "COMPLETED", Conclusion: "SUCCESS"}
	pendingReview := checkItem{Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "IN_PROGRESS"}
	tests := []struct {
		name  string
		items []checkItem
		want  string
	}{
		{
			name:  "only recognized reviewer is pending",
			items: []checkItem{passingCI, pendingReview},
			want:  "review",
		},
		{
			name: "queued recognized reviewer",
			items: []checkItem{
				passingCI,
				{Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "QUEUED"},
			},
			want: "review",
		},
		{
			name: "pending non-review takes precedence",
			items: []checkItem{
				{Typename: "CheckRun", Name: "CI", Status: "IN_PROGRESS"},
				pendingReview,
			},
			want: "pending",
		},
		{
			name: "failed reviewer takes precedence",
			items: []checkItem{
				passingCI,
				{Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "FAILURE"},
			},
			want: "fail",
		},
		{
			name: "cancelled non-review takes precedence",
			items: []checkItem{
				{Typename: "CheckRun", Name: "CI", Status: "COMPLETED", Conclusion: "CANCELLED"},
				pendingReview,
			},
			want: "fail",
		},
		{
			name: "skipped non-review blocks reviewer-only state",
			items: []checkItem{
				{Typename: "CheckRun", Name: "Optional CI", Status: "COMPLETED", Conclusion: "SKIPPED"},
				pendingReview,
			},
			want: "pending",
		},
		{
			name: "unrecognized review-like name remains pending",
			items: []checkItem{
				passingCI,
				{Typename: "CheckRun", Name: "AI review preview", Status: "IN_PROGRESS"},
			},
			want: "pending",
		},
		{
			name: "status context with recognized name remains pending",
			items: []checkItem{
				passingCI,
				{Typename: "StatusContext", Context: "cubic · AI code reviewer", State: "EXPECTED"},
			},
			want: "pending",
		},
		{
			name: "latest reviewer rerun wins",
			items: []checkItem{
				passingCI,
				{Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "COMPLETED", Conclusion: "FAILURE", StartedAt: now.Add(-time.Minute)},
				{Typename: "CheckRun", Name: "cubic · AI code reviewer", Status: "IN_PROGRESS", StartedAt: now},
			},
			want: "review",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCheckState(tc.items); got != tc.want {
				t.Fatalf("normalizeCheckState() = %q, want %q", got, tc.want)
			}
		})
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

func TestParseRequiredCheckRulesResult(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect map[string]bool
		valid  bool
	}{
		{
			name:   "valid required check",
			input:  `[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"ci/build"}]}}]`,
			expect: map[string]bool{"ci/build": true},
			valid:  true,
		},
		{name: "valid empty array", input: `[]`, valid: true},
		{name: "top-level null", input: `null`},
		{name: "top-level object", input: `{}`},
		{name: "null array entry", input: `[null]`},
		{name: "scalar array entry", input: `[1]`},
		{
			name:  "invalid nested checks type",
			input: `[{"type":"required_status_checks","parameters":{"required_status_checks":"invalid"}}]`,
		},
		{name: "malformed JSON", input: `[`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := parseRequiredCheckRulesResult([]byte(tc.input))
			if valid != tc.valid {
				t.Fatalf("parseRequiredCheckRulesResult() valid = %v, want %v", valid, tc.valid)
			}
			if !reflect.DeepEqual(got, tc.expect) {
				t.Fatalf("parseRequiredCheckRulesResult() = %v, want %v", got, tc.expect)
			}
		})
	}
}
