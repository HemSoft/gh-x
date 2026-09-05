package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const testHead = "0123456789abcdef0123456789abcdef01234567"

var testConfig = config{"HemSoft/gh-x", "chore/changelog-1.2.3", testHead}

func validPR() pullRequest {
	var pr pullRequest
	pr.Number = 12
	pr.State = "open"
	pr.ChangedFiles = 1
	pr.User.Login = "github-actions[bot]"
	pr.User.Type = "Bot"
	pr.Head.Ref = testConfig.branch
	pr.Head.SHA = testHead
	pr.Head.Repo.FullName = testConfig.repo
	pr.Base.Ref = "main"
	pr.Base.Repo.FullName = testConfig.repo
	return pr
}

func TestEligibility(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*pullRequest, *[]changedFile)
		wantError bool
	}{
		{"eligible", func(*pullRequest, *[]changedFile) {}, false},
		{"human author", func(p *pullRequest, _ *[]changedFile) { p.User.Login = "HemSoft" }, true},
		{"spoofed bot type", func(p *pullRequest, _ *[]changedFile) { p.User.Type = "User" }, true},
		{"fork", func(p *pullRequest, _ *[]changedFile) { p.Head.Repo.FullName = "fork/gh-x" }, true},
		{"other base", func(p *pullRequest, _ *[]changedFile) { p.Base.Ref = "develop" }, true},
		{"draft", func(p *pullRequest, _ *[]changedFile) { p.Draft = true }, true},
		{"closed", func(p *pullRequest, _ *[]changedFile) { p.State = "closed" }, true},
		{"changed head", func(p *pullRequest, _ *[]changedFile) { p.Head.SHA = strings.Repeat("f", 40) }, true},
		{"other branch", func(p *pullRequest, _ *[]changedFile) { p.Head.Ref = "feature/foo" }, true},
		{"widened diff", func(p *pullRequest, _ *[]changedFile) { p.ChangedFiles = 2 }, true},
		{"workflow edit", func(_ *pullRequest, f *[]changedFile) { (*f)[0].Filename = ".github/workflows/ci.yml" }, true},
		{"rename", func(_ *pullRequest, f *[]changedFile) { (*f)[0].Status = "renamed" }, true},
		{"empty response", func(_ *pullRequest, f *[]changedFile) { *f = nil }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := validPR()
			files := []changedFile{{"CHANGELOG.md", "modified"}}
			tt.mutate(&pr, &files)
			if err := eligible(testConfig, pr, files); (err != nil) != tt.wantError {
				t.Fatalf("eligible error=%v, wantError=%v", err, tt.wantError)
			}
		})
	}
}

func cleanState() reviewState {
	var state reviewState
	state.HeadRefOID = testHead
	state.Comments.Nodes = []reviewComment{{Body: "Codex Review: Didn't find any major issues.\n\n**Reviewed commit:** `" + testHead[:10] + "`", Author: actor{"chatgpt-codex-connector"}, CreatedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}}
	return state
}

func TestReviewEvidence(t *testing.T) {
	tests := []struct {
		name                        string
		mutate                      func(*reviewState)
		ready, requested, wantError bool
	}{
		{"clean receipt", func(*reviewState) {}, true, true, false},
		{"spoofed receipt", func(s *reviewState) { s.Comments.Nodes[0].Author.Login = "HemSoft" }, false, false, false},
		{"stale receipt", func(s *reviewState) {
			s.Comments.Nodes[0].Body = strings.ReplaceAll(s.Comments.Nodes[0].Body, testHead[:10], "aaaaaaaaaa")
		}, false, false, false},
		{"head changed", func(s *reviewState) { s.HeadRefOID = "other" }, false, false, true},
		{"comments truncated", func(s *reviewState) { s.Comments.PageInfo.HasPreviousPage = true }, false, false, true},
		{"reviews truncated", func(s *reviewState) { s.Reviews.PageInfo.HasPreviousPage = true }, false, false, true},
		{"threads truncated", func(s *reviewState) { s.ReviewThreads.PageInfo.HasNextPage = true }, false, false, true},
		{"unresolved conversation", func(s *reviewState) {
			s.ReviewThreads.Nodes = append(s.ReviewThreads.Nodes, struct{ IsResolved bool }{false})
		}, false, true, false},
		{"resolved conversation", func(s *reviewState) {
			s.ReviewThreads.Nodes = append(s.ReviewThreads.Nodes, struct{ IsResolved bool }{true})
		}, true, true, false},
		{"new review running", func(s *reviewState) {
			s.Comments.Nodes = append(s.Comments.Nodes, reviewComment{Author: actor{"chatgpt-codex-connector"}, Body: "<!-- codex-pull-request-review-summary --> `" + testHead[:7] + "` **Running**"})
		}, false, true, false},
		{"tied independent evidence", func(s *reviewState) {
			r := review{Author: actor{"chatgpt-codex-connector"}, State: "COMMENTED", SubmittedAt: s.Comments.Nodes[0].CreatedAt}
			r.Commit.OID = testHead
			s.Reviews.Nodes = []review{r}
		}, false, true, false},
		{"lone approval", func(s *reviewState) {
			r := review{Author: actor{"chatgpt-codex-connector"}, State: "APPROVED", SubmittedAt: s.Comments.Nodes[0].CreatedAt}
			r.Commit.OID = testHead
			s.Comments.Nodes = nil
			s.Reviews.Nodes = []review{r}
		}, true, true, false},
		{"missing review timestamp", func(s *reviewState) {
			r := review{Author: actor{"chatgpt-codex-connector"}, State: "COMMENTED"}
			r.Commit.OID = testHead
			s.Reviews.Nodes = []review{r}
		}, false, true, true},
		{"unquoted receipt", func(s *reviewState) { s.Comments.Nodes[0].Body = strings.ReplaceAll(s.Comments.Nodes[0].Body, "`", "") }, true, true, false},
		{"newer finding", func(s *reviewState) {
			r := review{Author: actor{"chatgpt-codex-connector"}, State: "COMMENTED", SubmittedAt: s.Comments.Nodes[0].CreatedAt.Add(time.Minute)}
			r.Commit.OID = testHead
			s.Reviews.Nodes = append(s.Reviews.Nodes, r)
		}, false, true, false},
		{"spoofed request", func(s *reviewState) {
			s.Comments.Nodes = []reviewComment{{Author: actor{"stranger"}, Body: "<!-- changelog-codex-review-head:" + testHead + " -->"}}
		}, false, false, false},
		{"existing request", func(s *reviewState) {
			s.Comments.Nodes = []reviewComment{{Author: actor{"github-actions[bot]"}, Body: "@codex review\n<!-- changelog-codex-review-head:" + testHead + " -->"}}
		}, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := cleanState()
			tt.mutate(&s)
			ready, requested, err := reviewReady(s, testHead)
			if ready != tt.ready || requested != tt.requested || (err != nil) != tt.wantError {
				t.Fatalf("got (%v,%v,%v), want (%v,%v,error=%v)", ready, requested, err, tt.ready, tt.requested, tt.wantError)
			}
		})
	}
}

func TestCubicEvidence(t *testing.T) {
	tests := []struct {
		name, status, conclusion, summary string
		ready, wantError                  bool
	}{
		{"clean", "completed", "success", "0 issues found", true, false},
		{"ten issues are not zero", "completed", "success", "10 issues found", false, false},
		{"findings", "completed", "success", "1 issue found", false, false},
		{"pending", "in_progress", "", "Reviewing", false, false},
		{"failed", "completed", "failure", "failed", false, true},
		{"skipped", "completed", "skipped", "Review skipped", true, false},
		{"successful skip", "completed", "success", "Review skipped", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := checkRun{Status: tt.status, Conclusion: tt.conclusion, HeadSHA: testHead}
			check.Output.Summary = tt.summary
			ready, err := cubicReady(check, testHead)
			if ready != tt.ready || (err != nil) != tt.wantError {
				t.Fatalf("got %v,%v", ready, err)
			}
		})
	}
}

func encode(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestEnableWaitsForReviewGateAndPinsMerge(t *testing.T) {
	var mergeArgs []string
	reads := 0
	gh := func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "pr" && args[1] == "list":
			return []byte(`[{"number":12}]`), nil
		case strings.Contains(joined, "/files?"):
			return encode(t, []changedFile{{"CHANGELOG.md", "modified"}}), nil
		case args[0] == "api" && args[1] == "graphql":
			return encode(t, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": cleanState()}}}), nil
		case strings.Contains(joined, "check-runs?per_page"):
			check := checkRun{Status: "completed", Conclusion: "success", HeadSHA: testHead}
			check.Name = cubicReviewCheckName
			check.App.Slug = "cubic-dev-ai"
			check.Output.Summary = "0 issues found"
			return encode(t, map[string]any{"check_runs": []checkRun{check}}), nil
		case strings.Contains(joined, "check-runs?"):
			check := checkRun{Name: "Changelog AI Review", Status: "completed", Conclusion: "success", HeadSHA: testHead}
			check.App.Slug = "github-actions"
			return encode(t, map[string]any{"check_runs": []checkRun{check}}), nil
		case args[0] == "pr" && args[1] == "merge":
			mergeArgs = append([]string{}, args...)
			return nil, nil
		default:
			reads++
			pr := validPR()
			if len(mergeArgs) > 0 {
				pr.Merged = true
				pr.State = "closed"
			}
			return encode(t, pr), nil
		}
	}
	if err := run(context.Background(), testConfig, []string{"enable"}, gh); err != nil {
		t.Fatal(err)
	}
	want := []string{"pr", "merge", "12", "--repo", "HemSoft/gh-x", "--auto", "--squash", "--match-head-commit", testHead}
	if !reflect.DeepEqual(mergeArgs, want) || reads != 5 {
		t.Fatalf("merge=%v, reads=%d", mergeArgs, reads)
	}
}

func TestAbsentOrFailedReviewGateCannotEnableMerge(t *testing.T) {
	for _, conclusion := range []string{"absent", "failure", "skipped"} {
		t.Run(conclusion, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			gh := func(args ...string) ([]byte, error) {
				if conclusion == "absent" {
					return []byte(`{"check_runs":[]}`), nil
				}
				check := checkRun{Name: "Changelog AI Review", Status: "completed", Conclusion: conclusion, HeadSHA: testHead}
				check.App.Slug = "github-actions"
				return encode(t, map[string]any{"check_runs": []checkRun{check}}), nil
			}
			if err := waitForReviewGate(ctx, gh, testConfig); err == nil {
				t.Fatal("missing review gate must block")
			}
		})
	}
}

func TestAPIErrorFailsClosed(t *testing.T) {
	gh := func(...string) ([]byte, error) { return nil, errors.New("GitHub unavailable") }
	if err := run(context.Background(), testConfig, []string{"enable"}, gh); err == nil {
		t.Fatal("API failure must block")
	}
}

func TestWorkflowReviewGateUsesTrustedCode(t *testing.T) {
	contents, err := os.ReadFile("../../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct {
			Needs       []string
			Permissions map[string]string
			Steps       []struct {
				With map[string]string
				Run  string
			}
		}
	}
	if err := yaml.Unmarshal(contents, &workflow); err != nil {
		t.Fatal(err)
	}
	job := workflow.Jobs["changelog-review"]
	if job.Steps[0].With["ref"] != "${{ github.event.repository.default_branch }}" || job.Steps[0].With["persist-credentials"] != "false" {
		t.Fatal("privileged review must use trusted default branch without persisted credentials")
	}
	if job.Permissions["pull-requests"] != "read" || job.Permissions["contents"] != "read" {
		t.Fatal("review permissions changed")
	}
	if !strings.Contains(strings.Join(workflow.Jobs["gate"].Needs, ","), "changelog-review") {
		t.Fatal("Quality Gate must depend on changelog review")
	}
	if !strings.Contains(workflow.Jobs["gate"].Steps[0].Run, "needs.changelog-review.result") {
		t.Fatal("Quality Gate must enforce changelog review result")
	}
}

func TestMissingCubicCheckIsPending(t *testing.T) {
	gh := func(...string) ([]byte, error) { return []byte(`{"total_count":0,"check_runs":[]}`), nil }
	ready, _, err := inspectCubic(gh, testConfig, cleanState())
	if err != nil || ready {
		t.Fatalf("missing configured Cubic check must wait, got %v,%v", ready, err)
	}
}

func TestRequestsUseTrustedGraphQLIdentity(t *testing.T) {
	for _, login := range []string{"github-actions", "github-actions[bot]", "stranger"} {
		s := cleanState()
		s.Comments.Nodes = []reviewComment{{Author: actor{login}, Body: requestMarker("cubic", testHead)}}
		if got := wasRequested(s, "cubic", testHead); got != (login != "stranger") {
			t.Fatalf("trusted request for %s: %v", login, got)
		}
		if wasRequested(s, "codex", testHead) {
			t.Fatal("Cubic marker must not suppress Codex")
		}
	}
}

func TestPollRequestsMissingCubicOnlyOnce(t *testing.T) {
	comments := 0
	gh := func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "pr" && args[1] == "comment":
			comments++
			if !strings.Contains(joined, "@cubic-dev-ai review this PR") || !strings.Contains(joined, requestMarker("cubic", testHead)) {
				t.Fatalf("unexpected request: %v", args)
			}
			return nil, nil
		case args[0] == "api" && args[1] == "graphql":
			return encode(t, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": cleanState()}}}), nil
		case strings.Contains(joined, "/files?"):
			return encode(t, []changedFile{{"CHANGELOG.md", "modified"}}), nil
		case strings.Contains(joined, "check-runs?"):
			return []byte(`{"total_count":0,"check_runs":[]}`), nil
		default:
			return encode(t, validPR()), nil
		}
	}
	sent := map[string]bool{}
	for i := 0; i < 2; i++ {
		ready, err := pollReview(gh, testConfig, "12", sent, true)
		if err != nil || ready {
			t.Fatalf("absent Cubic must wait: %v,%v", ready, err)
		}
	}
	if comments != 1 {
		t.Fatalf("duplicate review requests: %d", comments)
	}
}

func TestMergeBudgetOutlivesRequiredGates(t *testing.T) {
	if executionTimeout([]string{"enable"}) <= 2*35*time.Minute {
		t.Fatal("merge must outlive two queued review jobs")
	}
	if executionTimeout([]string{"review"}) >= 35*time.Minute {
		t.Fatal("review must finish within its CI job budget")
	}
}

func TestReadOnlyReviewNeverRequests(t *testing.T) {
	gh := func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "pr":
			t.Fatalf("read-only CI attempted mutation: %v", args)
		case args[1] == "graphql":
			state := cleanState()
			state.Comments.Nodes = nil
			return encode(t, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": state}}}), nil
		case strings.Contains(joined, "/files?"):
			return encode(t, []changedFile{{"CHANGELOG.md", "modified"}}), nil
		case strings.Contains(joined, "check-runs?"):
			return []byte(`{"total_count":0,"check_runs":[]}`), nil
		default:
			return encode(t, validPR()), nil
		}
		return nil, nil
	}
	ready, err := pollReview(gh, testConfig, "12", map[string]bool{}, false)
	if err != nil || ready {
		t.Fatalf("missing reviews must wait without writes: %v,%v", ready, err)
	}
}

func TestLatestReviewCheckWins(t *testing.T) {
	for _, oldStatus := range []string{"failure", "pending", "findings"} {
		t.Run(oldStatus, func(t *testing.T) {
			old := checkRun{Name: "cubic · AI code reviewer", HeadSHA: testHead, Status: "completed", Conclusion: "failure", StartedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)}
			old.App.Slug = "cubic-dev-ai"
			if oldStatus == "pending" {
				old.Status = "in_progress"
			}
			if oldStatus == "findings" {
				old.Conclusion = "success"
				old.Output.Summary = "1 issue found"
			}
			clean := old
			clean.StartedAt = old.StartedAt.Add(time.Minute)
			clean.Status = "completed"
			clean.Conclusion = "success"
			clean.Output.Summary = "0 issues found"
			for _, checks := range [][]checkRun{{old, clean}, {clean, old}} {
				gh := func(...string) ([]byte, error) {
					return encode(t, map[string]any{"total_count": 2, "check_runs": checks}), nil
				}
				ready, requested, err := inspectCubic(gh, testConfig, cleanState())
				if err != nil || !ready || !requested {
					t.Fatalf("latest clean must win: %v,%v,%v", ready, requested, err)
				}
			}
			// Reversing timestamps makes the pending/failed/finding-bearing run authoritative.
			old.StartedAt = clean.StartedAt.Add(time.Minute)
			gh := func(...string) ([]byte, error) {
				return encode(t, map[string]any{"total_count": 2, "check_runs": []checkRun{clean, old}}), nil
			}
			ready, _, _ := inspectCubic(gh, testConfig, cleanState())
			if ready {
				t.Fatal("old clean check must not hide newer non-clean evidence")
			}
		})
	}
}

func TestAmbiguousRepeatedChecksFailClosed(t *testing.T) {
	check := checkRun{Name: "cubic · AI code reviewer"}
	check.App.Slug = "cubic-dev-ai"
	if _, err := latestChecks([]checkRun{check, check}, "cubic-dev-ai"); err == nil {
		t.Fatal("missing times must block")
	}
	check.StartedAt = time.Now()
	if _, err := latestChecks([]checkRun{check, check}, "cubic-dev-ai"); err == nil {
		t.Fatal("tied times must block")
	}
}

func TestQueuedGateRerunWaitsForTimestamp(t *testing.T) {
	old := checkRun{Name: "Changelog AI Review", HeadSHA: testHead, Status: "completed", Conclusion: "success", StartedAt: time.Now()}
	old.App.Slug = "github-actions"
	queued := old
	queued.StartedAt = time.Time{}
	queued.Status = "queued"
	queued.Conclusion = ""
	ready, err := passingReviewGate([]checkRun{old, queued}, testHead)
	if ready || err != nil {
		t.Fatalf("queued rerun should wait, got %v,%v", ready, err)
	}
	queued.StartedAt = old.StartedAt.Add(time.Minute)
	queued.Status = "completed"
	queued.Conclusion = "success"
	ready, err = passingReviewGate([]checkRun{old, queued}, testHead)
	if !ready || err != nil {
		t.Fatalf("completed rerun should pass, got %v,%v", ready, err)
	}
}

func TestCompletionCannotOrderRerunsWithMissingStart(t *testing.T) {
	gh := func(...string) ([]byte, error) {
		return []byte(`{"total_count":2,"check_runs":[{"name":"cubic · AI code reviewer","head_sha":"` + testHead + `","status":"completed","conclusion":"success","started_at":null,"completed_at":"2026-09-05T12:10:00Z","app":{"slug":"cubic-dev-ai"},"output":{"summary":"0 issues found"}},{"name":"cubic · AI code reviewer","head_sha":"` + testHead + `","status":"completed","conclusion":"failure","started_at":"2026-09-05T12:05:00Z","completed_at":"2026-09-05T12:06:00Z","app":{"slug":"cubic-dev-ai"}}]}`), nil
	}
	ready, requested, err := inspectCubic(gh, testConfig, cleanState())
	if ready || !requested || err != nil {
		t.Fatalf("unknown start order must remain pending: %v,%v,%v", ready, requested, err)
	}
}

func TestCubicCommentsCannotOverrideCheckFindings(t *testing.T) {
	state := cleanState()
	r := review{Author: actor{"cubic-dev-ai"}, State: "COMMENTED", SubmittedAt: time.Now(), Body: "<!-- cubic:review-summary:start -->No issues found<!-- cubic:review-summary:end -->"}
	r.Commit.OID = testHead
	state.Reviews.Nodes = []review{r}
	check := checkRun{Name: "cubic · AI code reviewer", StartedAt: r.SubmittedAt.Add(-time.Minute), HeadSHA: testHead, Status: "completed", Conclusion: "success"}
	check.App.Slug = "cubic-dev-ai"
	check.Output.Summary = "1 issue found"
	gh := func(...string) ([]byte, error) {
		return encode(t, map[string]any{"total_count": 1, "check_runs": []checkRun{check}}), nil
	}
	ready, requested, err := inspectCubic(gh, testConfig, state)
	if ready || !requested || err != nil {
		t.Fatalf("a clean comment must not override check findings: %v,%v,%v", ready, requested, err)
	}
}

func TestCodexCommentFindingsSupersedeEarlierCleanEvidence(t *testing.T) {
	for _, tt := range []struct {
		name    string
		delta   time.Duration
		missing bool
		ready   bool
	}{
		{"later finding", time.Minute, false, false},
		{"tied finding", 0, false, false},
		{"earlier finding", -time.Minute, false, true},
		{"ambiguous finding", 0, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			state := cleanState()
			negative := reviewComment{Author: actor{"chatgpt-codex-connector"}, CreatedAt: state.Comments.Nodes[0].CreatedAt.Add(tt.delta), Body: "Codex Review: Found an issue that should be addressed.\n\n**Reviewed commit:** " + testHead[:10]}
			if tt.missing {
				negative.CreatedAt = time.Time{}
			}
			for _, comments := range [][]reviewComment{{state.Comments.Nodes[0], negative}, {negative, state.Comments.Nodes[0]}} {
				state.Comments.Nodes = comments
				ready, requested, err := reviewReady(state, testHead)
				if ready != tt.ready || !requested || (err != nil) != tt.missing {
					t.Fatalf("got %v,%v,%v", ready, requested, err)
				}
			}
		})
	}
}

func TestCodexCleanReceiptVariants(t *testing.T) {
	state := cleanState()
	state.Comments.Nodes[0].Body = "Codex Review: Did not find any major issues.\n\nReviewed commit: " + testHead
	ready, _, err := reviewReady(state, testHead)
	if !ready || err != nil {
		t.Fatalf("supported alternate receipt: %v,%v", ready, err)
	}
	if cleanCodexComment("Codex Review: Found an issue.\nQuoted: Codex Review: Didn't find any major issues.") {
		t.Fatal("quoted clean text must not clear findings")
	}
}

func TestQueuedAutoMergeIsWithdrawnWhenReviewChanges(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		dirty, expire, disableFails bool
	}{
		{"new finding", true, false, false},
		{"monitor deadline", false, true, false},
		{"withdrawal failure", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.expire {
				cancel()
			}
			disabled := false
			gh := func(args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				switch {
				case args[0] == "pr":
					if !strings.Contains(joined, "--disable-auto") {
						t.Fatalf("unexpected mutation: %v", args)
					}
					disabled = true
					if tc.disableFails {
						return nil, errors.New("API unavailable")
					}
					return nil, nil
				case args[1] == "graphql":
					state := cleanState()
					if tc.dirty {
						state.Comments.Nodes[0].Body = "Codex Review: Found an issue.\n**Reviewed commit:** " + testHead
					}
					return encode(t, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": state}}}), nil
				case strings.Contains(joined, "/files?"):
					return encode(t, []changedFile{{"CHANGELOG.md", "modified"}}), nil
				case strings.Contains(joined, "check-runs?"):
					check := checkRun{HeadSHA: testHead, Status: "completed", Conclusion: "success"}
					check.Name = cubicReviewCheckName
					check.App.Slug = "cubic-dev-ai"
					check.Output.Summary = "0 issues found"
					return encode(t, map[string]any{"check_runs": []checkRun{check}}), nil
				default:
					return encode(t, validPR()), nil
				}
			}
			err := waitForMerge(ctx, gh, testConfig, "12")
			if err == nil || !disabled {
				t.Fatalf("queued merge must be withdrawn: %v,%v", disabled, err)
			}
			if tc.disableFails && !strings.Contains(err.Error(), "could not disable") {
				t.Fatalf("withdrawal failure must be explicit: %v", err)
			}
		})
	}
}

func TestOnlyWithdrawalSurvivesMonitorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if commandContext(ctx, []string{"pr", "merge", "12", "--disable-auto"}).Err() != nil {
		t.Fatal("withdrawal needs its cleanup window")
	}
	if commandContext(ctx, []string{"pr", "merge", "12", "--auto"}).Err() == nil {
		t.Fatal("normal commands must retain the deadline")
	}
}

func TestNewFindingBeforeQueuePreventsAutoMerge(t *testing.T) {
	gatePassed := false
	gh := func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case args[0] == "pr" && args[1] == "list":
			return []byte(`[{"number":12}]`), nil
		case args[0] == "pr":
			t.Fatalf("dirty evidence must prevent mutation: %v", args)
		case args[1] == "graphql":
			state := cleanState()
			if gatePassed {
				state.Comments.Nodes[0].Body = "Codex Review: Found an issue.\n**Reviewed commit:** " + testHead
			}
			return encode(t, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": state}}}), nil
		case strings.Contains(joined, "/files?"):
			return encode(t, []changedFile{{"CHANGELOG.md", "modified"}}), nil
		case strings.Contains(joined, "check-runs?"):
			check := checkRun{HeadSHA: testHead, Status: "completed", Conclusion: "success"}
			check.Name = cubicReviewCheckName
			check.App.Slug = "cubic-dev-ai"
			check.Output.Summary = "0 issues found"
			if strings.Contains(joined, "check_name=Changelog") {
				gatePassed = true
				check.App.Slug = "github-actions"
				check.Name = "Changelog AI Review"
			}
			return encode(t, map[string]any{"check_runs": []checkRun{check}}), nil
		default:
			return encode(t, validPR()), nil
		}
		return nil, nil
	}
	if err := run(context.Background(), testConfig, []string{"enable"}, gh); err == nil {
		t.Fatal("finding during gate wait must block enabling auto-merge")
	}
}

func TestMergeDuringGuardReadStillCompletesSuccessfully(t *testing.T) {
	reads := 0
	gh := func(args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if args[0] == "pr" {
			t.Fatalf("completed merge must not be withdrawn: %v", args)
		}
		if strings.Contains(joined, "/files?") {
			return encode(t, []changedFile{{"CHANGELOG.md", "modified"}}), nil
		}
		reads++
		pr := validPR()
		if reads > 1 {
			pr.Merged = true
			pr.State = "closed"
		}
		return encode(t, pr), nil
	}
	if err := waitForMerge(context.Background(), gh, testConfig, "12"); err != nil {
		t.Fatal(err)
	}
	if reads != 3 {
		t.Fatalf("expected open, merged eligibility, merged reconciliation; got %d reads", reads)
	}
}

func TestUnrelatedCubicChecksCannotSatisfyReview(t *testing.T) {
	for _, conclusion := range []string{"success", "skipped"} {
		t.Run(conclusion, func(t *testing.T) {
			check := checkRun{Name: "cubic unrelated check", HeadSHA: testHead, Status: "completed", Conclusion: conclusion}
			check.App.Slug = "cubic-dev-ai"
			check.Output.Summary = "0 issues found"
			gh := func(...string) ([]byte, error) {
				return encode(t, map[string]any{"total_count": 1, "check_runs": []checkRun{check}}), nil
			}
			ready, requested, err := inspectCubic(gh, testConfig, cleanState())
			if ready || requested || err != nil {
				t.Fatalf("unrelated check must not count as review evidence: %v,%v,%v", ready, requested, err)
			}
		})
	}
}

func TestExpiredReviewDoesNotPollOrRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	gh := func(...string) ([]byte, error) {
		t.Fatal("expired review must not read evidence or request reviewers")
		return nil, nil
	}
	if err := waitForReview(ctx, gh, testConfig, "12", true); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled review, got %v", err)
	}
}

func TestCubicLookupFiltersBeforePagination(t *testing.T) {
	gh := func(args ...string) ([]byte, error) {
		if !strings.Contains(strings.Join(args, " "), "&check_name=cubic%20%C2%B7%20AI%20code%20reviewer") {
			t.Fatal("Cubic lookup must exclude unrelated contexts before pagination")
		}
		return []byte(`{"total_count":0,"check_runs":[]}`), nil
	}
	if _, _, err := inspectCubic(gh, testConfig, cleanState()); err != nil {
		t.Fatal(err)
	}
}
