package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/repository"
)

func TestStatusRemoteSessionPreservesSelectedHost(t *testing.T) {
	saved := repositoryCurrentFunc
	t.Cleanup(func() { repositoryCurrentFunc = saved })
	for _, test := range []struct {
		name, host, label, target string
		failed                    bool
	}{
		{name: "public remote", host: "github.com", label: "owner/repo", target: "github.com/owner/repo"},
		{name: "Enterprise selected over public origin", host: "ghe.example.com", label: "org/project", target: "ghe.example.com/org/project"},
		{name: "unresolved SSH alias preserves gh selection", label: "owner/repo", failed: true},
		{name: "unknown host preserves gh selection", label: "owner/repo"},
		{name: "unknown repository"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repositoryCurrentFunc = func() (repository.Repository, error) {
				if test.failed {
					return repository.Repository{}, errors.New("unknown remote")
				}
				return repository.Repository{Host: test.host}, nil
			}
			session := newStatusRemoteSession(test.label)
			if session.repo != test.target || session.rules == nil {
				t.Fatalf("target=%q, want %q", session.repo, test.target)
			}
		})
	}
}

func TestCombinedIssueEnrichmentLegacySchemaFallback(t *testing.T) {
	fixture, err := os.ReadFile("../tests/behavior/testdata/issue-relationships.json")
	if err != nil {
		t.Fatal(err)
	}
	saved := ghExecContextFunc
	t.Cleanup(func() { ghExecContextFunc = saved })
	for _, test := range []struct {
		name, message            string
		fallback, fallbackFailed bool
	}{
		{name: "parent unsupported", message: `Cannot query field "parent" on type "Issue".`, fallback: true},
		{name: "summary unsupported", message: `Field 'subIssuesSummary' doesn't exist on type 'Issue'.`, fallback: true},
		{name: "unknown hierarchy field", message: `Unknown field parent on Issue.`, fallback: true},
		{name: "failed relationship retry", message: `Unknown field parent on Issue.`, fallback: true, fallbackFailed: true},
		{name: "ordinary permission failure", message: `Parent issue is inaccessible.`},
		{name: "unrelated schema failure", message: `Unknown field project on Issue.`},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			ghExecContextFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
				calls++
				query := strings.Join(args, " ")
				if !strings.Contains(query, "--hostname ghe.example.com") {
					t.Fatalf("host changed: %s", query)
				}
				if calls == 1 {
					return *bytes.NewBufferString(`{"data":null}`), *bytes.NewBufferString(test.message), errors.New("exit 1")
				}
				if strings.Contains(query, "subIssuesSummary") || strings.Contains(query, "parent {") {
					t.Fatalf("retry retains unsupported fields: %s", query)
				}
				if test.fallbackFailed {
					return bytes.Buffer{}, bytes.Buffer{}, errors.New("offline")
				}
				return *bytes.NewBuffer(fixture), bytes.Buffer{}, nil
			}
			result := fetchCombinedIssueEnrichment(context.Background(), "HemSoft", "gh-x", "ghe.example.com", []int{50})
			wantedCalls := 1
			if test.fallback {
				wantedCalls = 2
			}
			if calls != wantedCalls {
				t.Fatalf("calls=%d, want %d", calls, wantedCalls)
			}
			if result.HierarchyErr == nil || !result.HierarchyMissing[50].Parent || !result.HierarchyMissing[50].SubIssues {
				t.Fatal("unsupported hierarchy looks healthy")
			}
			failed := !test.fallback || test.fallbackFailed
			if result.RelationshipsMissing[50] != failed || (result.RelErr != nil) != failed {
				t.Fatalf("relationship retry outcome wrong: %+v", result)
			}
			if !failed && len(result.Relationships[50]) != 1 {
				t.Fatal("legacy relationship missing")
			}
		})
	}
}

func TestCombinedIssueEnrichmentFieldIsolation(t *testing.T) {
	fixture, err := os.ReadFile("../tests/behavior/testdata/issue-status-enrichment.json")
	if err != nil {
		t.Fatal(err)
	}
	saved := ghExecContextFunc
	t.Cleanup(func() { ghExecContextFunc = saved })
	for _, test := range []struct {
		name, field                string
		relFailed, hierarchyFailed bool
	}{
		{name: "healthy"},
		{name: "parent", field: "parent", hierarchyFailed: true},
		{name: "subissues", field: "subIssuesSummary", hierarchyFailed: true},
		{name: "relationships", field: "closedByPullRequestsReferences", relFailed: true},
		{name: "whole issue", field: "", relFailed: true, hierarchyFailed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := fixture
			var commandErr error
			if test.name != "healthy" {
				var envelope map[string]any
				if err := json.Unmarshal(fixture, &envelope); err != nil {
					t.Fatal(err)
				}
				path := []string{"repository", "issue50"}
				if test.field != "" {
					path = append(path, test.field)
				}
				envelope["errors"] = []any{map[string]any{"message": "field unavailable", "path": path}}
				data, err = json.Marshal(envelope)
				if err != nil {
					t.Fatal(err)
				}
				commandErr = errors.New("exit 1")
			}
			calls := 0
			ghExecContextFunc = func(context.Context, ...string) (bytes.Buffer, bytes.Buffer, error) {
				calls++
				return *bytes.NewBuffer(data), *bytes.NewBufferString("field unavailable"), commandErr
			}
			result := fetchCombinedIssueEnrichment(context.Background(), "HemSoft", "gh-x", "github.com", []int{50})
			if calls != 1 {
				t.Fatalf("calls=%d", calls)
			}
			if result.RelationshipsMissing[50] != test.relFailed || (result.RelErr != nil) != test.relFailed {
				t.Fatalf("relationships lost isolation: %+v", result)
			}
			missing := result.HierarchyMissing[50]
			if (missing.Parent || missing.SubIssues) != test.hierarchyFailed || (result.HierarchyErr != nil) != test.hierarchyFailed {
				t.Fatalf("hierarchy lost isolation: %+v", result)
			}
			if !test.relFailed && len(result.Relationships[50]) != 1 {
				t.Fatal("relationship missing")
			}
		})
	}
}

func TestCombinedIssueEnrichmentInvalidResponses(t *testing.T) {
	saved := ghExecContextFunc
	t.Cleanup(func() { ghExecContextFunc = saved })
	for _, response := range []string{"", "{", `{"data":{"repository":{}}}`} {
		ghExecContextFunc = func(context.Context, ...string) (bytes.Buffer, bytes.Buffer, error) {
			return *bytes.NewBufferString(response), bytes.Buffer{}, errors.New("offline")
		}
		result := fetchCombinedIssueEnrichment(context.Background(), "HemSoft", "gh-x", "github.com", []int{50})
		if !result.RelationshipsMissing[50] || !result.HierarchyMissing[50].Parent || !result.HierarchyMissing[50].SubIssues {
			t.Fatalf("response %q did not fail closed: %+v", response, result)
		}
	}
}

func TestStatusRequiredCheckCacheSharesSuccessAndFailure(t *testing.T) {
	saved := ghExecFunc
	t.Cleanup(func() { ghExecFunc = saved })
	for _, broken := range []bool{false, true} {
		calls := 0
		ghExecFunc = func(...string) (bytes.Buffer, bytes.Buffer, error) {
			calls++
			if broken {
				return bytes.Buffer{}, bytes.Buffer{}, errors.New("rules offline")
			}
			return *bytes.NewBufferString(`[{"type":"required_status_checks","parameters":{"required_status_checks":[{"context":"Quality Gate"}]}}]`), bytes.Buffer{}, nil
		}
		cache := make(map[string]requiredCheckResult)
		for range 2 {
			rules, failures := fetchRequiredChecksCached("owner", "repo", []pullRequest{{BaseRefName: "main"}}, cache)
			if (failures["main"] != nil) != broken || (!broken && !rules["main"]["Quality Gate"]) {
				t.Fatal("cached rules changed outcome")
			}
		}
		if calls != 1 {
			t.Fatalf("shared base fetched %d times", calls)
		}
		fetchRequiredChecksCached("owner", "other", []pullRequest{{BaseRefName: "main"}}, cache)
		fetchRequiredChecksCached("owner", "repo", []pullRequest{{BaseRefName: "develop"}}, cache)
		if calls != 3 {
			t.Fatalf("different repository/base reused rules: %d", calls)
		}
	}
}
