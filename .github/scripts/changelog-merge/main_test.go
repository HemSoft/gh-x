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
		{"newer finding", func(s *reviewState) {
			r := review{Author: actor{"chatgpt-codex-connector"}, State: "COMMENTED", SubmittedAt: s.Comments.Nodes[0].CreatedAt.Add(time.Minute)}
			r.Commit.OID = testHead
			s.Reviews.Nodes = append(s.Reviews.Nodes, r)
		}, false, true, false},
		{"existing request", func(s *reviewState) {
			s.Comments.Nodes = []reviewComment{{Body: "@codex review\n<!-- changelog-review-head:" + testHead + " -->"}}
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
			ready, err := cubicReady(check, cleanState(), testHead)
			if ready != tt.ready || (err != nil) != tt.wantError {
				t.Fatalf("got %v,%v", ready, err)
			}
		})
	}
	s := cleanState()
	r := review{Author: actor{"cubic-dev-ai"}, Body: "All reported issues were addressed"}
	r.Commit.OID = testHead
	s.Reviews.Nodes = []review{r}
	ready, err := cubicReady(checkRun{HeadSHA: testHead, Status: "completed", Conclusion: "success"}, s, testHead)
	if err != nil || !ready {
		t.Fatalf("resolved findings: %v,%v", ready, err)
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
	if !reflect.DeepEqual(mergeArgs, want) || reads != 3 {
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
	if job.Permissions["pull-requests"] != "write" || job.Permissions["contents"] != "read" {
		t.Fatal("review permissions changed")
	}
	if !strings.Contains(strings.Join(workflow.Jobs["gate"].Needs, ","), "changelog-review") {
		t.Fatal("Quality Gate must depend on changelog review")
	}
	if !strings.Contains(workflow.Jobs["gate"].Steps[0].Run, "needs.changelog-review.result") {
		t.Fatal("Quality Gate must enforce changelog review result")
	}
}
