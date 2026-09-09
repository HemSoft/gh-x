package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRelationshipDisplay(t *testing.T) {
	tests := []struct {
		name        string
		refs        []linkedReference
		unavailable bool
		wantText    string
		wantNumbers []int
	}{
		{name: "unavailable", unavailable: true, wantText: "?"},
		{name: "none", wantText: "-"},
		{
			name: "sorted unique references preserve cross-repository numbers",
			refs: []linkedReference{
				{Number: 21, URL: "https://example.test/repo-a/21"},
				{Number: 3, URL: "https://example.test/3"},
				{Number: 21, URL: "https://example.test/repo-a/21"},
				{Number: 21, URL: "https://example.test/repo-b/21"},
				{Number: 0, URL: "https://example.test/invalid"},
			},
			wantText:    "#3, #21, #21",
			wantNumbers: []int{3, 21, 21},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, refs := relationshipDisplay(test.refs, test.unavailable)
			if text != test.wantText {
				t.Fatalf("relationshipDisplay text = %q, want %q", text, test.wantText)
			}
			var numbers []int
			for _, ref := range refs {
				numbers = append(numbers, ref.Number)
			}
			if !reflect.DeepEqual(numbers, test.wantNumbers) {
				t.Fatalf("relationshipDisplay numbers = %v, want %v", numbers, test.wantNumbers)
			}
		})
	}
}

func TestDisplayPullRequestOmitsUnfetchedIssuesFromJSON(t *testing.T) {
	unfetched, err := json.Marshal(displayPullRequest{Number: 1})
	if err != nil {
		t.Fatalf("marshal unfetched PR: %v", err)
	}
	if strings.Contains(string(unfetched), `"issues"`) {
		t.Fatalf("unfetched PR JSON includes issues: %s", unfetched)
	}

	enriched, err := json.Marshal(displayPullRequest{Number: 1, Issues: "-"})
	if err != nil {
		t.Fatalf("marshal enriched PR: %v", err)
	}
	if !strings.Contains(string(enriched), `"issues":"-"`) {
		t.Fatalf("enriched PR JSON omits issues: %s", enriched)
	}
}

func TestRelationshipCellLinksSurviveTruncation(t *testing.T) {
	refs := []linkedReference{
		{Number: 3, URL: "https://github.com/owner/repo/issues/3"},
		{Number: 21, URL: "https://github.com/owner/repo/issues/21"},
	}
	text, refs := relationshipDisplay(refs, false)
	styler := newTableStyler(&bytes.Buffer{}, true)
	cell := styler.relationshipCell(text, refs)

	for _, url := range []string{refs[0].URL, refs[1].URL} {
		if !strings.Contains(cell.styled, "\x1b]8;;"+url) {
			t.Fatalf("relationship cell missing link %q: %q", url, cell.styled)
		}
	}

	truncated := cell.withText("#3, #...")
	if !strings.Contains(truncated.styled, "\x1b]8;;"+refs[0].URL) {
		t.Fatalf("truncated relationship cell lost complete link: %q", truncated.styled)
	}
	if strings.Contains(truncated.styled, refs[1].URL) {
		t.Fatalf("truncated relationship cell linked an incomplete reference: %q", truncated.styled)
	}
}

func TestRepositoryTargetHostUsesExplicitRepoHost(t *testing.T) {
	if got := repositoryTargetHost("ghe.example.com/owner/repo"); got != "ghe.example.com" {
		t.Fatalf("repositoryTargetHost() = %q, want ghe.example.com", got)
	}
}

func TestFetchGraphQLUsesRequestedHost(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	var captured []string
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		captured = append([]string(nil), args...)
		return *bytes.NewBufferString(`{"data":{}}`), bytes.Buffer{}, nil
	}

	if _, err := fetchGraphQL("ghe.example.com", "query { viewer { login } }"); err != nil {
		t.Fatalf("fetchGraphQL returned error: %v", err)
	}
	want := []string{"api", "--hostname", "ghe.example.com", "graphql", "-f", "query=query { viewer { login } }"}
	if !reflect.DeepEqual(captured, want) {
		t.Fatalf("fetchGraphQL args = %v, want %v", captured, want)
	}
}

func TestFetchIssueRelationshipsBatchesAndPreservesHost(t *testing.T) {
	saved := fetchIssueRelationshipsBatchFunc
	defer func() { fetchIssueRelationshipsBatchFunc = saved }()

	calls := 0
	fetchIssueRelationshipsBatchFunc = func(owner, name, host string, numbers []int) (map[int][]linkedReference, map[int]bool, error) {
		calls++
		if owner != "owner" || name != "repo" || host != "ghe.example.com" {
			t.Fatalf("unexpected target: %s/%s on %s", owner, name, host)
		}
		result := make(map[int][]linkedReference, len(numbers))
		for _, number := range numbers {
			result[number] = []linkedReference{{Number: number + 100}}
		}
		return result, nil, nil
	}

	numbers := make([]int, 35)
	for i := range numbers {
		numbers[i] = i + 1
	}
	result, unavailable, err := fetchIssueRelationships("owner", "repo", "ghe.example.com", numbers)
	if err != nil {
		t.Fatalf("fetchIssueRelationships returned error: %v", err)
	}
	if calls != 2 || len(result) != len(numbers) || len(unavailable) != 0 {
		t.Fatalf("batch result calls=%d entries=%d unavailable=%d, want calls=2 entries=%d unavailable=0", calls, len(result), len(unavailable), len(numbers))
	}
}

func TestFetchIssueRelationshipsReturnsBatchError(t *testing.T) {
	saved := fetchIssueRelationshipsBatchFunc
	defer func() { fetchIssueRelationshipsBatchFunc = saved }()

	fetchIssueRelationshipsBatchFunc = func(_, _, _ string, _ []int) (map[int][]linkedReference, map[int]bool, error) {
		return nil, map[int]bool{1: true}, fmt.Errorf("graphql unavailable")
	}
	result, unavailable, err := fetchIssueRelationships("owner", "repo", "github.com", []int{1})
	if err == nil || err.Error() != "graphql unavailable" {
		t.Fatalf("fetchIssueRelationships error = %v, want graphql unavailable", err)
	}
	if !unavailable[1] || len(result) != 0 {
		t.Fatalf("failed batch must keep issue 1 unavailable, got unavailable=%v result=%v", unavailable, result)
	}
}

func TestFetchIssueRelationshipsEmpty(t *testing.T) {
	result, unavailable, err := fetchIssueRelationships("owner", "repo", "github.com", nil)
	if err != nil || result != nil || unavailable != nil {
		t.Fatalf("empty relationship fetch = (%v, %v, %v), want (nil, nil, nil)", result, unavailable, err)
	}
}

func TestFetchIssueRelationshipsBatchUsesOneGraphQLRequest(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	calls := 0
	var captured string
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		captured = strings.Join(args, " ")
		response := `{"data":{"repository":{"issue7":{"number":7,"closedByPullRequestsReferences":{"totalCount":1,"nodes":[{"number":25,"url":"https://github.com/owner/repo/pull/25"}]}},"issue9":{"number":9,"closedByPullRequestsReferences":{"totalCount":0,"nodes":[]}}}}}`
		return *bytes.NewBufferString(response), bytes.Buffer{}, nil
	}

	result, unavailable, err := fetchIssueRelationshipsBatch("owner", "repo", "ghe.example.com", []int{7, 9})
	if err != nil {
		t.Fatalf("fetchIssueRelationshipsBatch returned error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("GraphQL calls = %d, want 1", calls)
	}
	for _, want := range []string{"--hostname ghe.example.com", "issue7: issue(number: 7)", "issue9: issue(number: 9)", "totalCount nodes"} {
		if !strings.Contains(captured, want) {
			t.Fatalf("GraphQL request missing %q: %s", want, captured)
		}
	}
	if len(result[7]) != 1 || result[7][0].Number != 25 {
		t.Fatalf("issue 7 relationships = %#v, want PR #25", result[7])
	}
	if refs, ok := result[9]; !ok || len(refs) != 0 {
		t.Fatalf("issue 9 relationships = %#v, want known empty", refs)
	}
	if len(unavailable) != 0 {
		t.Fatalf("successful batch must leave no issue unavailable, got %v", unavailable)
	}
}

func TestFetchPRSupplementalBatchIncludesClosingIssuesAndHost(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	var captured string
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		captured = strings.Join(args, " ")
		response := `{"data":{"repository":{"pr25":{"number":25,"headRefOid":"abc","closingIssuesReferences":{"totalCount":1,"nodes":[{"number":18,"url":"https://github.com/owner/repo/issues/18"}]},"comments":{"totalCount":0,"nodes":[]},"reviewThreads":{"totalCount":0,"nodes":[]},"reviews":{"totalCount":0,"nodes":[]},"approvedReviews":{"nodes":[]}}}}}`
		return *bytes.NewBufferString(response), bytes.Buffer{}, nil
	}

	result, unavailable, err := fetchPRSupplementalBatch("owner", "repo", "ghe.example.com", []int{25})
	if err != nil {
		t.Fatalf("fetchPRSupplementalBatch returned error: %v", err)
	}
	for _, want := range []string{"--hostname ghe.example.com", "closingIssuesReferences(first: 100) { totalCount"} {
		if !strings.Contains(captured, want) {
			t.Fatalf("PR GraphQL request missing %q: %s", want, captured)
		}
	}
	refs := result[25].ClosingIssues
	if len(refs) != 1 || refs[0].Number != 18 {
		t.Fatalf("PR #25 closing issues = %#v, want issue #18", refs)
	}
	if !result[25].ClosingIssuesAvailable {
		t.Fatal("PR #25 closing issue relationship should be available")
	}
	if len(unavailable) != 0 {
		t.Fatalf("successful batch must leave no PR unavailable, got %v", unavailable)
	}
}

func TestParseIssueRelationships(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantLen int
		wantErr bool
	}{
		{
			name:    "valid and null nodes",
			input:   `{"data":{"repository":{"issue1":{"number":1,"closedByPullRequestsReferences":{"totalCount":0,"nodes":[]}},"issue2":null,"issue3":{"number":3,"closedByPullRequestsReferences":null},"issue4":{"number":4,"closedByPullRequestsReferences":{"totalCount":2,"nodes":[{"number":8,"url":"https://example.test/8"}]}},"issue5":{"number":5,"closedByPullRequestsReferences":{"nodes":[]}},"issue6":{"number":6,"closedByPullRequestsReferences":{"totalCount":null,"nodes":[]}}}}}`,
			wantLen: 1,
		},
		{name: "empty repository", input: `{"data":{"repository":{}}}`, wantLen: 0},
		{name: "invalid JSON", input: `not json`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := parseIssueRelationships([]byte(test.input))
			if test.wantErr {
				if err == nil {
					t.Fatalf("parseIssueRelationships returned %v, want error", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIssueRelationships returned error: %v", err)
			}
			if len(result) != test.wantLen {
				t.Fatalf("parseIssueRelationships entries = %d, want %d", len(result), test.wantLen)
			}
		})
	}
}

func TestEnrichPullRequestsAddsIssueRelationships(t *testing.T) {
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	prs := []pullRequest{{Number: 1}, {Number: 2}, {Number: 3}}
	supplemental := map[int]prSupplementalInfo{
		1: {ClosingIssues: []linkedReference{{Number: 21}, {Number: 3}}, ClosingIssuesAvailable: true},
		2: {ClosingIssuesAvailable: true},
	}

	rendered := enrichPullRequests(prs, prSupplementalData{Info: supplemental}, nil, now)
	want := []string{"#3, #21", "-", "?"}
	for i := range rendered {
		if rendered[i].Issues != want[i] {
			t.Fatalf("PR %d Issues = %q, want %q", rendered[i].Number, rendered[i].Issues, want[i])
		}
	}

	failed := enrichPullRequests(prs[:1], prSupplementalData{Unavailable: map[int]bool{1: true}}, nil, now)
	if failed[0].Issues != "?" {
		t.Fatalf("failed enrichment Issues = %q, want ?", failed[0].Issues)
	}
}

func TestEnrichPullRequestsTreatsNullRelationshipConnectionAsUnavailable(t *testing.T) {
	prs := []pullRequest{{Number: 1}}
	supplemental := map[int]prSupplementalInfo{1: {ClosingIssuesAvailable: false}}

	rendered := enrichPullRequests(prs, prSupplementalData{Info: supplemental}, nil, time.Time{})
	if rendered[0].Issues != "?" {
		t.Fatalf("null relationship connection Issues = %q, want ?", rendered[0].Issues)
	}
}

func TestParsePRSupplementalNodeTreatsIncompleteRelationshipsAsUnavailable(t *testing.T) {
	tests := []struct {
		name       string
		connection string
	}{
		{name: "truncated", connection: `{"totalCount":2,"nodes":[{"number":18,"url":"https://example.test/18"}]}`},
		{name: "missing count", connection: `{"nodes":[]}`},
		{name: "null count", connection: `{"totalCount":null,"nodes":[]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := json.RawMessage(`{"number":25,"closingIssuesReferences":` + test.connection + `}`)
			_, info, ok := parsePRSupplementalNode(raw)
			if !ok {
				t.Fatal("incomplete relationship supplemental node should still parse")
			}
			if info.ClosingIssuesAvailable || len(info.ClosingIssues) != 0 {
				t.Fatalf("incomplete relationships = available %t, refs %#v; want unavailable", info.ClosingIssuesAvailable, info.ClosingIssues)
			}
		})
	}
}

func TestRelationshipColumnsRenderInStatus(t *testing.T) {
	issueRefs := []linkedReference{{Number: 25, URL: "https://github.com/owner/repo/pull/25"}}
	prRefs := []linkedReference{{Number: 18, URL: "https://github.com/owner/repo/issues/18"}}
	dashboard := statusDashboard{
		Repository:        "owner/repo",
		DefaultBranch:     "main",
		DefaultStatus:     statusSummary{Branch: "main", Upstream: "origin/main"},
		DefaultCheckedOut: true,
		CurrentStatus:     statusSummary{Branch: "main", Upstream: "origin/main"},
		Issues: []displayIssue{{
			Number: 7, PullRequests: "#25", pullRequestRefs: issueRefs, Title: "Issue", State: "open",
		}},
		PullRequests: []displayPullRequest{{
			Number: 25, Issues: "#18", issueRefs: prRefs, Title: "PR", State: "open",
		}},
	}

	var output bytes.Buffer
	if err := renderStatus(&output, dashboard, false); err != nil {
		t.Fatalf("renderStatus returned error: %v", err)
	}
	text := output.String()
	for _, want := range []string{"PRs", "Issues", "#25", "#18"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status output missing %q: %s", want, text)
		}
	}
}

func TestFetchIssueRelationshipsBatchRecoversHealthyAliasesFromPartialError(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		body := `{"data":{"repository":{"issue332":{"number":332,"closedByPullRequestsReferences":{"totalCount":1,"nodes":[{"number":333,"url":"https://github.com/HemSoft/codexbar-ios/pull/333"}]}},"issue998":null}},"errors":[{"type":"NOT_FOUND","path":["repository","issue998"],"message":"Could not resolve to a Issue with the number of 998."}]}`
		return *bytes.NewBufferString(body), *bytes.NewBufferString("gh: Could not resolve to a Issue with the number of 998.\n"), errors.New("exit status 1")
	}

	refs, unavailable, err := fetchIssueRelationshipsBatch("HemSoft", "codexbar-ios", "github.com", []int{332, 998})
	if err == nil {
		t.Fatal("partial GraphQL error must be carried for display")
	}
	if len(refs[332]) != 1 || refs[332][0].Number != 333 {
		t.Fatalf("healthy issue 332 data must survive a partial batch error, got %#v", refs[332])
	}
	if !unavailable[998] {
		t.Fatalf("unparsed issue 998 must be unavailable, got %v", unavailable)
	}
	if unavailable[332] {
		t.Fatal("healthy issue 332 must not be marked unavailable")
	}
}

func TestFetchDisplayIssuesMarksOnlyUnavailableIssueUnknown(t *testing.T) {
	savedIssues := fetchIssuesFunc
	savedRelationships := fetchIssueRelationshipsFunc
	defer func() {
		fetchIssuesFunc = savedIssues
		fetchIssueRelationshipsFunc = savedRelationships
	}()

	fetchIssuesFunc = func(_ issueListOptions) ([]issueEntry, error) {
		return []issueEntry{{Number: 332}, {Number: 998}}, nil
	}
	fetchIssueRelationshipsFunc = func(_, _, _ string, _ []int) (map[int][]linkedReference, map[int]bool, error) {
		return map[int][]linkedReference{
			332: {{Number: 333, URL: "https://github.com/HemSoft/codexbar-ios/pull/333"}},
		}, map[int]bool{998: true}, fmt.Errorf("gh api graphql: Could not resolve to a Issue with the number of 998.")
	}

	result, err := fetchDisplayIssues(issueListOptions{repo: "HemSoft/codexbar-ios"}, time.Time{})
	if err != nil {
		t.Fatalf("fetchDisplayIssues returned error: %v", err)
	}
	if result.Display[0].PullRequests != "#333" {
		t.Fatalf("healthy issue display = %q, want #333", result.Display[0].PullRequests)
	}
	if result.Display[1].PullRequests != "?" {
		t.Fatalf("unavailable issue display = %q, want ? (never a false empty relationship)", result.Display[1].PullRequests)
	}
	if result.RelErr == nil {
		t.Fatal("expected the relationship error to be carried for display")
	}
}
