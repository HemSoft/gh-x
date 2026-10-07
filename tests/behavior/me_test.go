package behavior_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestCLIBehaviorMeDependabot(t *testing.T) {
	repository := newFixtureRepository(t)
	for _, test := range []struct {
		name, scenario string
		args           []string
		want           []string
	}{
		{"inferred organization", "me-org", nil, []string{"app#30", "app#40", "other#30", "app#20", "app#10"}},
		{"explicit organization", "me-org", []string{"--org", "HemSoft"}, []string{"app#30", "app#40", "other#30", "app#20", "app#10"}},
		{"personal account", "me-user", []string{"--org", "HemSoft"}, []string{"app#30", "app#40", "other#30", "app#20", "app#10"}},
		{"combined limit", "me-org", []string{"--org", "HemSoft", "--limit", "1"}, []string{"app#30"}},
		{"empty", "me-empty", []string{"--org", "HemSoft"}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"pr", "me"}, test.args...)
			result := runCLI(t, repository, test.scenario, append(args, "--json")...)
			if result.exitCode != 0 {
				t.Fatalf("me JSON failed: %s", result.stderr)
			}
			var rows []struct {
				Number                    int
				Repo, Author, Checks, URL string
			}
			if err := json.Unmarshal([]byte(result.stdout), &rows); err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(rows))
			for _, row := range rows {
				got = append(got, fmt.Sprintf("%s#%d", row.Repo, row.Number))
				if row.Checks != "pass" || row.URL == "" || row.Author == "" {
					t.Fatalf("missing enriched fields: %+v", row)
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("PR selection/order = %v, want %v", got, test.want)
			}
			qualifier := "org:HemSoft"
			if test.scenario == "me-user" {
				qualifier = "user:HemSoft"
			}
			if !strings.Contains(result.calls, "author:app/dependabot "+qualifier+" sort:updated-desc") {
				t.Fatalf("missing scoped Dependabot search: %s", result.calls)
			}
			if strings.Contains(result.calls, "auth ") {
				t.Fatalf("identity-scoped command attempted account fallback: %s", result.calls)
			}
			table := runCLI(t, repository, test.scenario, args...)
			if table.exitCode != 0 {
				t.Fatalf("me table failed: %s", table.stderr)
			}
			if len(test.want) == 0 {
				if !strings.Contains(table.stdout, "No open PRs authored by or assigned to octocat, or opened by Dependabot, in HemSoft.") {
					t.Fatalf("unexpected empty result: %s", table.stdout)
				}
				return
			}
			wantNumbers := make([]string, 0, len(test.want))
			for _, key := range test.want {
				_, number, _ := strings.Cut(key, "#")
				wantNumbers = append(wantNumbers, "#"+number)
			}
			tableNumbers := regexp.MustCompile(`(?m)^#\d+`).FindAllString(table.stdout, -1)
			if !strings.Contains(table.stdout, "dependabot") || !reflect.DeepEqual(tableNumbers, wantNumbers) {
				t.Fatalf("unexpected table selection: %s", table.stdout)
			}
		})
	}
}

func TestCLIBehaviorMeActiveAccountFailure(t *testing.T) {
	result := runCLI(t, newFixtureRepository(t), "me-denied", "pr", "me", "--org", "HemSoft")
	if result.exitCode == 0 || !strings.Contains(result.stderr, "fixture active account denied") {
		t.Fatalf("expected account failure, got exit=%d stderr=%s", result.exitCode, result.stderr)
	}
	if strings.Contains(result.calls, "auth ") {
		t.Fatalf("identity-scoped query retried another account: %s", result.calls)
	}
}

type meCandidate struct {
	IsPR      bool           `json:"isPR"`
	Assignees []string       `json:"assignees"`
	Node      map[string]any `json:"node"`
}

func runFakeMeGH(args []string) int {
	text := strings.Join(args, " ")
	scenario := os.Getenv(fakeGHScenarioEnv)
	switch {
	case hasCommandPrefix(args, "api", "user"):
		fmt.Fprintln(os.Stdout, "octocat")
	case hasCommandPrefix(args, "api", "users/HemSoft"):
		if scenario == "me-user" {
			fmt.Fprintln(os.Stdout, "User")
		} else {
			fmt.Fprintln(os.Stdout, "Organization")
		}
	case strings.Contains(text, "graphql"):
		if scenario == "me-denied" {
			fmt.Fprintln(os.Stderr, "HTTP 403: fixture active account denied")
			return 1
		}
		return writeFakeMeSearch(text, scenario == "me-empty")
	default:
		fmt.Fprintln(os.Stderr, "unsupported me fixture call: "+text)
		return 2
	}
	return 0
}

func writeFakeMeSearch(text string, empty bool) int {
	data, err := os.ReadFile(filepath.Join(os.Getenv(fakeGHFixtureEnv), "me-candidates.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var candidates []meCandidate
	if err := json.Unmarshal(data, &candidates); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	searches := regexp.MustCompile(`(q\d+): search\(query: ("[^"\n]+"), type: ISSUE, first: (\d+)\)`).FindAllStringSubmatch(text, -1)
	response := map[string]any{}
	for _, search := range searches {
		query, _ := strconv.Unquote(search[2])
		limit, _ := strconv.Atoi(search[3])
		nodes := []map[string]any{}
		for _, candidate := range candidates {
			if !empty && matchesMeSearch(candidate, query) {
				nodes = append(nodes, candidate.Node)
			}
		}
		if strings.Contains(query, "sort:updated-desc") {
			sort.Slice(nodes, func(i, j int) bool {
				return nodes[i]["updatedAt"].(string) > nodes[j]["updatedAt"].(string)
			})
		}
		if len(nodes) > limit {
			nodes = nodes[:limit]
		}
		response[search[1]] = map[string]any{"nodes": nodes}
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"data": response}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	return 0
}

// Model GitHub's search filters against candidates that include excluded states,
// lookalike authors, another owner, an issue, and overlap between searches.
func matchesMeSearch(candidate meCandidate, query string) bool {
	author := candidate.Node["author"].(map[string]any)["login"].(string)
	owner := strings.Split(candidate.Node["repository"].(map[string]any)["nameWithOwner"].(string), "/")[0]
	for _, token := range strings.Fields(query) {
		negative := strings.HasPrefix(token, "-")
		key, value, _ := strings.Cut(strings.TrimPrefix(token, "-"), ":")
		match := true
		switch key {
		case "is":
			match = value == "pr" && candidate.IsPR || value == "open" && candidate.Node["state"] == "OPEN"
		case "author":
			if value == "app/dependabot" {
				value = "dependabot[bot]"
			}
			match = author == value
		case "assignee":
			match = false
			for _, assignee := range candidate.Assignees {
				match = match || assignee == value
			}
		case "org", "user":
			match = owner == value
		}
		if match == negative {
			return false
		}
	}
	return true
}
