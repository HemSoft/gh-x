package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRecentlyMergedSurvivesEmptySearchIndex(t *testing.T) {
	saved := ghExecFunc
	t.Cleanup(func() { ghExecFunc = saved })
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		command := strings.Join(args, " ")
		// Reproduce Mini: the search index is empty but the repository has merges.
		if strings.Contains(command, "pr list") {
			return *bytes.NewBufferString("[]"), bytes.Buffer{}, nil
		}
		return *bytes.NewBufferString(`{"data":{"repository":{"pullRequests":{"nodes":[{"number":208,"title":"record release","state":"MERGED","mergedAt":"2026-10-07T00:40:20Z","updatedAt":"2026-10-07T00:40:23Z"}],"pageInfo":{"hasNextPage":false}}}}}`), bytes.Buffer{}, nil
	}
	got, err := fetchPullRequests(listOptions{repo: "HemSoft/gh-x", limit: 5, state: "merged", search: "sort:updated-desc", recentlyMerged: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Number != 208 {
		t.Fatalf("recent merges = %#v, want existing repository PR #208 despite empty search index", got)
	}
}

func TestMergedRepositoryPaginationAndFields(t *testing.T) {
	saved := ghExecFunc
	t.Cleanup(func() { ghExecFunc = saved })
	calls := 0
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		command := strings.Join(args, " ")
		for _, want := range []string{"--hostname ghe.example.com", `owner: "owner"`, `name: "repo"`, "states: MERGED", "UPDATED_AT, direction: DESC"} {
			if !strings.Contains(command, want) {
				t.Fatalf("query missing %q: %s", want, command)
			}
		}
		if calls == 1 {
			if !strings.Contains(command, "first: 100, after: null") {
				t.Fatalf("initial pagination = %s", command)
			}
			nodes := make([]map[string]int, 100)
			for i := range nodes {
				nodes[i] = map[string]int{"number": i + 1}
			}
			data, err := json.Marshal(nodes)
			if err != nil {
				t.Fatal(err)
			}
			return *bytes.NewBufferString(fmt.Sprintf(`{"data":{"repository":{"pullRequests":{"nodes":%s,"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}`, data)), bytes.Buffer{}, nil
		}
		if calls != 2 || !strings.Contains(command, `first: 1, after: "next"`) {
			t.Fatalf("next pagination = %s", command)
		}
		return *bytes.NewBufferString(`{"data":{"repository":{"pullRequests":{"nodes":[{
		  "number":101,"title":"old PR merged today","state":"MERGED","author":{"login":"user","name":"User"},
		  "mergedAt":"2026-10-07T01:00:00Z","updatedAt":"2026-10-07T02:00:00Z","headRefName":"fix","baseRefName":"main","mergeable":"MERGEABLE","reviewDecision":"APPROVED",
		  "latestReviews":{"nodes":[{"state":"APPROVED","author":{"login":"reviewer"}}]},
		  "commits":{"nodes":[{"commit":{"statusCheckRollup":{"contexts":{"nodes":[{"__typename":"CheckRun","name":"CI","status":"COMPLETED","conclusion":"SUCCESS","checkSuite":{"workflowRun":{"workflow":{"name":"Tests"}}}},{"__typename":"StatusContext","context":"legacy","state":"SUCCESS"}]}}}}]}
		}],"pageInfo":{"hasNextPage":false}}}}}`), bytes.Buffer{}, nil
	}
	got, err := fetchMergedRepositoryPullRequests(listOptions{repo: "ghe.example.com/owner/repo", limit: 101})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 101 || calls != 2 {
		t.Fatalf("rows = %d, calls = %d, want 101 and 2", len(got), calls)
	}
	pr := got[100]
	if pr.Number != 101 || pr.Title != "old PR merged today" || pr.Author.Login != "user" || pr.HeadRefName != "fix" || pr.BaseRefName != "main" || pr.ReviewDecision != "APPROVED" || pr.Mergeable != "MERGEABLE" || pr.State != "MERGED" || pr.MergedAt != time.Date(2026, 10, 7, 1, 0, 0, 0, time.UTC) {
		t.Fatalf("lost PR fields: %#v", pr)
	}
	if len(pr.LatestReviews) != 1 || pr.LatestReviews[0].Author.Login != "reviewer" || len(pr.StatusCheckRollup) != 2 || pr.StatusCheckRollup[0].Conclusion != "SUCCESS" || pr.StatusCheckRollup[0].WorkflowName != "Tests" || pr.StatusCheckRollup[1].Context != "legacy" {
		t.Fatalf("lost reviews/checks: %#v", pr)
	}
}

func TestMergedRepositoryFailures(t *testing.T) {
	for _, tc := range []struct {
		name, output, want string
		execErr            error
	}{
		{"command error", "", "offline", errors.New("offline")},
		{"invalid JSON", "not json", "decode merged", nil},
		{"missing repository", `{"data":{"repository":null}}`, "unavailable", nil},
		{"missing connection", `{"data":{"repository":{}}}`, "unavailable", nil},
		{"partial error", `{"data":{"repository":{"pullRequests":{"nodes":[]}}},"errors":[{"message":"denied"}]}`, "denied", nil},
		{"empty cursor", `{"data":{"repository":{"pullRequests":{"nodes":[{"number":1}],"pageInfo":{"hasNextPage":true}}}}}`, "did not advance", nil},
		{"empty page", `{"data":{"repository":{"pullRequests":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}`, "did not advance", nil},
		{"repeated cursor", `{"data":{"repository":{"pullRequests":{"nodes":[{"number":1}],"pageInfo":{"hasNextPage":true,"endCursor":"same"}}}}}`, "did not advance", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := ghExecFunc
			t.Cleanup(func() { ghExecFunc = saved })
			ghExecFunc = func(...string) (bytes.Buffer, bytes.Buffer, error) {
				return *bytes.NewBufferString(tc.output), bytes.Buffer{}, tc.execErr
			}
			got, err := fetchMergedRepositoryPullRequests(listOptions{repo: "owner/repo", limit: 3})
			if err == nil || !strings.Contains(err.Error(), tc.want) || len(got) != 0 {
				t.Fatalf("rows = %#v, error = %v, want failure containing %q", got, err, tc.want)
			}
		})
	}
}

func TestRecentlyMergedEmptyAndLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int
		wantErr bool
	}{
		{"truly empty repository", 5, false},
		{"limit exceeds candidate cap", recentMergedCandidateLimit + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			saved := ghExecFunc
			t.Cleanup(func() { ghExecFunc = saved })
			ghExecFunc = func(...string) (bytes.Buffer, bytes.Buffer, error) {
				return *bytes.NewBufferString(`{"data":{"repository":{"pullRequests":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}`), bytes.Buffer{}, nil
			}
			got, err := fetchRecentlyMergedPullRequests(listOptions{repo: "owner/repo", limit: tc.limit})
			if (err != nil) != tc.wantErr || len(got) != 0 {
				t.Fatalf("rows = %#v, error = %v, want empty and error=%v", got, err, tc.wantErr)
			}
		})
	}
}
