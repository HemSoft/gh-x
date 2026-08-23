package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func singleMonitorQuery(t *testing.T, cfg *monitorConfig) string {
	t.Helper()
	queries, err := buildMonitorHostQueries(cfg)
	if err != nil {
		t.Fatalf("buildMonitorHostQueries: %v", err)
	}
	if len(queries) != 1 {
		t.Fatalf("expected one host query, got %d", len(queries))
	}
	return queries[0].Query
}

func parseSingleMonitorResponse(t *testing.T, data []byte, cfg *monitorConfig, now time.Time) (*monitorFetchResult, error) {
	t.Helper()
	repositories, err := parseMonitorRepositories(cfg.Repos)
	if err != nil {
		t.Fatalf("parseMonitorRepositories: %v", err)
	}
	return parseMonitorHostResponse(data, cfg, repositories, now)
}

func TestBuildMonitorRepoQualifiers(t *testing.T) {
	repos := []monitorRepository{
		{Host: "ghe.example.com", Owner: "o", Name: "r1"},
		{Host: "ghe.example.com", Owner: "o", Name: "r2"},
	}
	if got := buildMonitorRepoQualifiers(repos); got != "repo:o/r1 repo:o/r2" {
		t.Fatalf("unexpected qualifiers: %q", got)
	}
	if got := buildMonitorRepoQualifiers(nil); got != "" {
		t.Fatalf("empty repos should produce empty qualifiers: %q", got)
	}
}

func TestExecuteMonitorFetchRoutesAndMergesHostGroups(t *testing.T) {
	saved := monitorGHExecFunc
	defer func() { monitorGHExecFunc = saved }()

	cfg := defaultMonitorConfig("owner/public")
	cfg.Repos = append(cfg.Repos, "ghe.example.com/corp/private")
	cfg.PRSections[0].Limit = 1
	normalizeMonitorConfig(cfg)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	var calls [][]string
	monitorGHExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		calls = append(calls, append([]string(nil), args...))
		host := defaultGitHubHost
		nameWithOwner := "owner/public"
		updatedAt := "2026-08-23T11:00:00Z"
		if len(args) >= 3 && args[1] == "--hostname" && args[2] != defaultGitHubHost {
			host = args[2]
			nameWithOwner = "corp/private"
			updatedAt = "2026-08-23T11:30:00Z"
		}
		query := strings.Join(args, " ")
		if strings.Contains(query, "repo:ghe.example.com/") {
			t.Fatalf("GraphQL qualifier leaked host: %s", query)
		}
		if !strings.Contains(query, "repo:"+nameWithOwner) {
			t.Fatalf("query for %s missing repo qualifier: %s", host, query)
		}
		payload := `{"data":{"rateLimit":{"remaining":99,"resetAt":"2026-08-23T13:00:00Z"},` +
			`"acc0":{"nameWithOwner":"` + nameWithOwner + `"},` +
			`"pr0":{"issueCount":1,"nodes":[{"number":3,"title":"t","state":"OPEN",` +
			`"updatedAt":"` + updatedAt + `","repository":{"nameWithOwner":"` + nameWithOwner + `"}}]},` +
			`"is0":{"issueCount":1,"nodes":[{"number":4,"title":"i","state":"OPEN",` +
			`"updatedAt":"2026-08-23T10:00:00Z","repository":{"nameWithOwner":"` + nameWithOwner + `"}}]}}}`
		return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
	}

	result, err := executeMonitorFetch(cfg, now)
	if err != nil {
		t.Fatalf("executeMonitorFetch: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected one call per host, got %d", len(calls))
	}
	if got := strings.Join(calls[0], " "); !strings.Contains(got, "api --hostname github.com graphql") {
		t.Fatalf("github.com call missing deterministic hostname routing: %v", calls[0])
	}
	if got := strings.Join(calls[1], " "); !strings.Contains(got, "api --hostname ghe.example.com graphql") {
		t.Fatalf("enterprise call missing hostname routing: %v", calls[1])
	}
	if len(result.PRSections[0].Rows) != 1 || result.PRSections[0].Total != 2 {
		t.Fatalf("host results were not merged: %+v", result.PRSections[0])
	}
	if result.PRSections[0].Rows[0].Repo != "ghe.example.com/corp/private" {
		t.Fatalf("configured cap did not retain the newest merged row: %+v", result.PRSections[0].Rows)
	}
	if len(result.IssueSections[0].Rows) != 2 || result.IssueSections[0].Total != 2 {
		t.Fatalf("issue host results were not merged: %+v", result.IssueSections[0])
	}
	if result.IssueSections[0].Rows[0].Repo != "owner/public" ||
		result.IssueSections[0].Rows[1].Repo != "ghe.example.com/corp/private" {
		t.Fatalf("issue row host identities were not preserved: %+v", result.IssueSections[0].Rows)
	}
	if !result.Accessible["owner/public"] || !result.Accessible["ghe.example.com/corp/private"] {
		t.Fatalf("access probes were not merged: %+v", result.Accessible)
	}
}

func TestExecuteMonitorFetchKeepsSuccessfulHostsWhenAnotherFails(t *testing.T) {
	saved := monitorGHExecFunc
	defer func() { monitorGHExecFunc = saved }()

	cfg := defaultMonitorConfig("owner/public")
	cfg.Repos = append(cfg.Repos, "ghe.example.com/corp/private")
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	monitorGHExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		if len(args) >= 3 && args[1] == "--hostname" && args[2] != defaultGitHubHost {
			return bytes.Buffer{}, *bytes.NewBufferString("connection refused"), errBoom()
		}
		payload := `{"data":{"pr0":{"issueCount":1,"nodes":[{"number":3,"title":"t","state":"OPEN",` +
			`"updatedAt":"2026-08-23T11:00:00Z","repository":{"nameWithOwner":"owner/public"}}]}}}`
		return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
	}

	result, err := executeMonitorFetch(cfg, now)
	if err != nil {
		t.Fatalf("successful host should keep refresh usable: %v", err)
	}
	if len(result.PRSections[0].Rows) != 1 || result.PRSections[0].Rows[0].Repo != "owner/public" {
		t.Fatalf("successful host data was lost: %+v", result.PRSections[0])
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "ghe.example.com") ||
		!strings.Contains(result.Warnings[0], "connection refused") {
		t.Fatalf("failed host warning missing context: %v", result.Warnings)
	}
}

func TestExecuteMonitorFetchUsesRateLimitFromHostThatReturnsIt(t *testing.T) {
	saved := monitorGHExecFunc
	defer func() { monitorGHExecFunc = saved }()

	cfg := defaultMonitorConfig("owner/public")
	cfg.Repos = append(cfg.Repos, "ghe.example.com/corp/private")
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	wantReset := "2026-08-23T13:00:00Z"
	monitorGHExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		if len(args) >= 3 && args[1] == "--hostname" && args[2] != defaultGitHubHost {
			payload := `{"data":{"rateLimit":{"remaining":42,"resetAt":"` + wantReset + `"}}}`
			return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
		}
		return *bytes.NewBufferString(`{"data":{}}`), bytes.Buffer{}, nil
	}

	result, err := executeMonitorFetch(cfg, now)
	if err != nil {
		t.Fatalf("executeMonitorFetch: %v", err)
	}
	if result.RateRemaining != 42 || result.RateResetAt.Format(time.RFC3339) != wantReset {
		t.Fatalf("valid later rate limit was ignored: remaining=%d reset=%v", result.RateRemaining, result.RateResetAt)
	}
}

func TestHasUsableGraphQLDataRequiresPayloadField(t *testing.T) {
	for _, data := range []string{"not json", `{"data":null}`, `{"data":{}}`} {
		if hasUsableGraphQLData([]byte(data)) {
			t.Fatalf("response without a payload field treated as usable: %s", data)
		}
	}
	if !hasUsableGraphQLData([]byte(`{"data":{"rateLimit":null}}`)) {
		t.Fatal("requested payload field should make partial GraphQL data usable")
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
	query := singleMonitorQuery(t, cfg)
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
	if _, err := buildMonitorHostQueries(cfg); err == nil {
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

	result, err := parseSingleMonitorResponse(t, raw, cfg, now)
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
	if _, err := parseSingleMonitorResponse(t, raw, cfg, time.Now()); err == nil {
		t.Fatal("expected GraphQL error to surface")
	}
	raw = []byte(`not json`)
	if _, err := parseSingleMonitorResponse(t, raw, cfg, time.Now()); err == nil {
		t.Fatal("expected decode error for invalid JSON")
	}
}

func TestParseMonitorResponseToleratesMissingAliases(t *testing.T) {
	cfg := defaultMonitorConfig("owner/repo")
	raw := []byte(`{"data":{"rateLimit":{"remaining":10,"resetAt":"2026-01-01T00:00:00Z"}}}`)
	result, err := parseSingleMonitorResponse(t, raw, cfg, time.Now())
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
