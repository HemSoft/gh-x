package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildMonitorRepoQualifiers(t *testing.T) {
	if got := buildMonitorRepoQualifiers([]string{"o/r1", "o/r2"}); got != "repo:o/r1 repo:o/r2" {
		t.Fatalf("unexpected qualifiers: %q", got)
	}
	if got := buildMonitorRepoQualifiers(nil); got != "" {
		t.Fatalf("empty repos should produce empty qualifiers: %q", got)
	}
}

func TestBuildMonitorSearchQueryInjectsKindAndRepos(t *testing.T) {
	got := buildMonitorSearchQuery(monitorKindPR, "is:open author:@me", "repo:o/r")
	want := "is:pr is:open author:@me repo:o/r"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := buildMonitorSearchQuery(monitorKindIssue, "  ", ""); got != "is:issue" {
		t.Fatalf("blank filters mishandled: %q", got)
	}
}

func TestBuildMonitorGraphQLQueryShape(t *testing.T) {
	cfg := defaultMonitorConfig("o/r1")
	cfg.Repos = append(cfg.Repos, "o/r2")
	query, err := buildMonitorGraphQLQuery(cfg)
	if err != nil {
		t.Fatalf("buildMonitorGraphQLQuery: %v", err)
	}
	for _, want := range []string{
		"rateLimit { remaining resetAt }",
		`pr0: search(query: "is:pr is:open author:@me repo:o/r1 repo:o/r2"`,
		`is0: search(query: "is:issue is:open assignee:@me repo:o/r1 repo:o/r2"`,
		"... on PullRequest",
		"... on Issue",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q\n%s", want, query)
		}
	}
}

func TestBuildMonitorGraphQLQueryRequiresRepos(t *testing.T) {
	cfg := defaultMonitorConfig("")
	cfg.Repos = nil
	if _, err := buildMonitorGraphQLQuery(cfg); err == nil {
		t.Fatal("expected error without repos")
	}
}

func TestParseMonitorResponseMapsPRAndIssueSections(t *testing.T) {
	cfg := defaultMonitorConfig("owner/repo")
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	response := map[string]any{
		"data": map[string]any{
			"rateLimit": map[string]any{"remaining": 4987, "resetAt": now.Add(time.Hour).Format(time.RFC3339)},
			"pr0": map[string]any{
				"issueCount": 1,
				"nodes": []map[string]any{{
					"number": 42, "title": "Fix the thing", "state": "OPEN", "isDraft": false,
					"reviewDecision": "APPROVED", "updatedAt": now.Add(-time.Hour).Format(time.RFC3339),
					"headRefName": "feature/x", "url": "https://github.com/owner/repo/pull/42",
					"author":     map[string]any{"login": "jdoe"},
					"repository": map[string]any{"nameWithOwner": "owner/repo"},
					"body":       "The body text",
					"labels":     map[string]any{"nodes": []map[string]any{{"name": "bug"}}},
					"commits":    map[string]any{"nodes": []any{}},
				}},
			},
			"is0": map[string]any{
				"issueCount": 1,
				"nodes": []map[string]any{{
					"number": 7, "title": "It is broken", "state": "OPEN",
					"updatedAt":  now.Add(-2 * time.Hour).Format(time.RFC3339),
					"url":        "https://github.com/owner/repo/issues/7",
					"author":     map[string]any{"login": "asmith"},
					"repository": map[string]any{"nameWithOwner": "owner/repo"},
					"assignees":  map[string]any{"nodes": []map[string]any{{"login": "bclark"}}},
					"labels":     map[string]any{"nodes": []map[string]any{{"name": "urgent"}}},
					"body":       "Steps to reproduce…",
				}},
			},
		},
	}
	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}

	result, err := parseMonitorResponse(raw, cfg, now)
	if err != nil {
		t.Fatalf("parseMonitorResponse: %v", err)
	}
	if result.RateRemaining != 4987 {
		t.Fatalf("rate remaining not decoded: %d", result.RateRemaining)
	}
	if len(result.PRSections[0].Rows) != 1 || result.PRSections[0].Total != 1 {
		t.Fatalf("PR section not decoded: %+v", result.PRSections[0])
	}
	pr := result.PRSections[0].Rows[0]
	if pr.Kind != monitorKindPR || pr.Number != 42 || pr.State != "open" || pr.Title != "Fix the thing" {
		t.Fatalf("PR row mismatched: %+v", pr)
	}
	if pr.Author != "jdoe" || pr.Repo != "owner/repo" || len(pr.Labels) != 1 || pr.Labels[0] != "bug" {
		t.Fatalf("PR enrichment mismatched: %+v", pr)
	}
	if pr.Body != "The body text" || pr.Branch != "feature/x" {
		t.Fatalf("PR detail fields missing: %+v", pr)
	}

	if len(result.IssueSections[0].Rows) != 1 || result.IssueSections[0].Total != 1 {
		t.Fatalf("issue section not decoded: %+v", result.IssueSections[0])
	}
	issue := result.IssueSections[0].Rows[0]
	if issue.Kind != monitorKindIssue || issue.Number != 7 || issue.Assignees != "bclark" {
		t.Fatalf("issue row mismatched: %+v", issue)
	}
	if issue.Updated == "" {
		t.Fatal("relative updated time not rendered")
	}
}

func TestParseMonitorResponseReportsGraphQLErrors(t *testing.T) {
	cfg := defaultMonitorConfig("owner/repo")
	raw := []byte(`{"errors":[{"message":"bad query"}],"data":null}`)
	if _, err := parseMonitorResponse(raw, cfg, time.Now()); err == nil {
		t.Fatal("expected GraphQL error to surface")
	}
	raw = []byte(`not json`)
	if _, err := parseMonitorResponse(raw, cfg, time.Now()); err == nil {
		t.Fatal("expected decode error for invalid JSON")
	}
}

func TestParseMonitorResponseToleratesMissingAliases(t *testing.T) {
	cfg := defaultMonitorConfig("owner/repo")
	raw := []byte(`{"data":{"rateLimit":{"remaining":10,"resetAt":"2026-01-01T00:00:00Z"}}}`)
	result, err := parseMonitorResponse(raw, cfg, time.Now())
	if err != nil {
		t.Fatalf("missing aliases should not fail: %v", err)
	}
	if len(result.PRSections) != len(cfg.PRSections) {
		t.Fatal("section count must match config")
	}
	if result.PRSections[0].Total != 0 || len(result.PRSections[0].Rows) != 0 {
		t.Fatal("missing alias should decode as empty section")
	}
}

func TestSortMonitorRowsByUpdated(t *testing.T) {
	old := monitorRow{UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	newer := monitorRow{UpdatedAt: old.UpdatedAt.Add(time.Hour)}
	rows := []monitorRow{old, newer}
	sortMonitorRowsByUpdated(rows)
	if rows[0].UpdatedAt != newer.UpdatedAt {
		t.Fatal("rows not sorted newest first")
	}
}
