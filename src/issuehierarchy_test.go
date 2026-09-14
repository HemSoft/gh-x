package main

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIssueParentDisplay(t *testing.T) {
	sameRepo := testIssueParent(7, "owner/repo")
	otherRepo := testIssueParent(9, "other/tasks")
	tests := []struct {
		name        string
		parent      *issueParentReference
		unavailable bool
		want        string
		wantRefs    int
	}{
		{name: "none", want: "-"},
		{name: "same repository", parent: sameRepo, want: "#7", wantRefs: 1},
		{name: "same repository ignores case", parent: testIssueParent(8, "OWNER/REPO"), want: "#8", wantRefs: 1},
		{name: "cross repository", parent: otherRepo, want: "other/tasks#9", wantRefs: 1},
		{name: "unavailable", parent: sameRepo, unavailable: true, want: "?"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			text, refs := issueParentDisplay(test.parent, "owner/repo", test.unavailable)
			if text != test.want || len(refs) != test.wantRefs {
				t.Fatalf("issueParentDisplay() = %q with %d refs, want %q with %d refs", text, len(refs), test.want, test.wantRefs)
			}
			if len(refs) > 0 && refs[0].Text != test.want {
				t.Fatalf("parent link text = %q, want %q", refs[0].Text, test.want)
			}
		})
	}
}

func TestSubIssueProgressDisplay(t *testing.T) {
	tests := []struct {
		name        string
		summary     issueSubIssuesSummary
		unavailable bool
		want        string
	}{
		{name: "none", want: "-"},
		{name: "in progress", summary: issueSubIssuesSummary{Completed: 2, Total: 5}, want: "2/5"},
		{name: "complete", summary: issueSubIssuesSummary{Completed: 3, Total: 3}, want: "3/3"},
		{name: "unavailable", unavailable: true, want: "?"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := subIssueProgressDisplay(test.summary, test.unavailable); got != test.want {
				t.Fatalf("subIssueProgressDisplay() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestParseIssueHierarchies(t *testing.T) {
	input := `{
		"data":{"repository":{
			"issue7":{"number":7,"parent":{"number":3,"url":"https://github.com/owner/repo/issues/3","repository":{"nameWithOwner":"owner/repo"}},"subIssuesSummary":{"completed":2,"total":4}},
			"issue8":{"number":8,"parent":null,"subIssuesSummary":{"completed":0,"total":0}},
			"issue9":{"number":9,"parent":null,"subIssuesSummary":null},
			"issue10":null
		}},
		"errors":[
			{"path":["repository","issue8","parent"],"message":"parent hidden"},
			{"path":["repository","issue10"],"message":"issue hidden"}
		]
	}`

	hierarchies, unavailable, err := parseIssueHierarchies([]byte(input))
	if err != nil {
		t.Fatalf("parseIssueHierarchies returned error: %v", err)
	}
	if len(hierarchies) != 3 || hierarchies[7].Parent == nil || hierarchies[7].Parent.Number != 3 {
		t.Fatalf("parsed hierarchies = %#v", hierarchies)
	}
	if hierarchies[7].SubIssues != (issueSubIssuesSummary{Completed: 2, Total: 4}) {
		t.Fatalf("issue 7 summary = %#v", hierarchies[7].SubIssues)
	}
	if !unavailable[8].Parent || unavailable[8].SubIssues {
		t.Fatalf("issue 8 availability = %#v, want parent only unavailable", unavailable[8])
	}
	if !unavailable[9].SubIssues || unavailable[9].Parent {
		t.Fatalf("issue 9 availability = %#v, want sub-issues only unavailable", unavailable[9])
	}
	if !unavailable[10].Parent || !unavailable[10].SubIssues {
		t.Fatalf("issue 10 availability = %#v, want both unavailable", unavailable[10])
	}
}

func TestParseIssueHierarchiesRejectsMalformedData(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "invalid json", input: `not json`},
		{name: "negative progress", input: `{"data":{"repository":{"issue1":{"number":1,"parent":null,"subIssuesSummary":{"completed":-1,"total":2}}}}}`},
		{name: "completed exceeds total", input: `{"data":{"repository":{"issue1":{"number":1,"parent":null,"subIssuesSummary":{"completed":3,"total":2}}}}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, unavailable, err := parseIssueHierarchies([]byte(test.input))
			if test.name == "invalid json" {
				if err == nil {
					t.Fatal("invalid JSON must fail")
				}
				return
			}
			if err != nil || !unavailable[1].SubIssues {
				t.Fatalf("malformed summary availability = %#v, error = %v", unavailable[1], err)
			}
		})
	}
}

func TestFetchIssueHierarchiesBatchesAndPreservesHost(t *testing.T) {
	saved := fetchIssueHierarchiesBatchFunc
	t.Cleanup(func() { fetchIssueHierarchiesBatchFunc = saved })

	calls := 0
	fetchIssueHierarchiesBatchFunc = func(owner, name, host string, numbers []int) (map[int]issueHierarchy, map[int]issueHierarchyUnavailable, error) {
		calls++
		if owner != "owner" || name != "repo" || host != "ghe.example.com" {
			t.Fatalf("unexpected target: %s/%s on %s", owner, name, host)
		}
		result := make(map[int]issueHierarchy, len(numbers))
		for _, number := range numbers {
			result[number] = issueHierarchy{SubIssues: issueSubIssuesSummary{Completed: 1, Total: 2}}
		}
		return result, nil, nil
	}

	numbers := make([]int, 35)
	for index := range numbers {
		numbers[index] = index + 1
	}
	result, unavailable, err := fetchIssueHierarchies("owner", "repo", "ghe.example.com", numbers)
	if err != nil || calls != 2 || len(result) != 35 || len(unavailable) != 0 {
		t.Fatalf("batch result calls=%d entries=%d unavailable=%d error=%v", calls, len(result), len(unavailable), err)
	}
}

func TestFetchIssueHierarchiesBatchUsesOneGraphQLRequest(t *testing.T) {
	saved := ghExecFunc
	t.Cleanup(func() { ghExecFunc = saved })

	calls := 0
	var captured string
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		captured = strings.Join(args, " ")
		response := `{"data":{"repository":{"issue7":{"number":7,"parent":null,"subIssuesSummary":{"completed":1,"total":2}},"issue9":{"number":9,"parent":null,"subIssuesSummary":{"completed":0,"total":0}}}}}`
		return *bytes.NewBufferString(response), bytes.Buffer{}, nil
	}

	result, unavailable, err := fetchIssueHierarchiesBatch("owner", "repo", "ghe.example.com", []int{7, 9})
	if err != nil || calls != 1 || len(result) != 2 || len(unavailable) != 0 {
		t.Fatalf("fetch result calls=%d result=%#v unavailable=%#v error=%v", calls, result, unavailable, err)
	}
	for _, want := range []string{"--hostname ghe.example.com", "issue7: issue(number: 7)", "issue9: issue(number: 9)", "subIssuesSummary"} {
		if !strings.Contains(captured, want) {
			t.Fatalf("GraphQL request missing %q: %s", want, captured)
		}
	}
}

func TestFetchIssueHierarchiesRecoversHealthyAliasesFromPartialError(t *testing.T) {
	saved := ghExecFunc
	t.Cleanup(func() { ghExecFunc = saved })

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		body := `{"data":{"repository":{"issue7":{"number":7,"parent":null,"subIssuesSummary":{"completed":1,"total":2}},"issue9":null}},"errors":[{"path":["repository","issue9"],"message":"hidden"}]}`
		return *bytes.NewBufferString(body), *bytes.NewBufferString("gh: hidden"), errors.New("exit status 1")
	}

	result, unavailable, err := fetchIssueHierarchiesBatch("owner", "repo", "github.com", []int{7, 9})
	if err == nil || result[7].SubIssues.Total != 2 || unavailable[7].Parent || unavailable[7].SubIssues {
		t.Fatalf("healthy alias lost: result=%#v unavailable=%#v error=%v", result, unavailable, err)
	}
	if !unavailable[9].Parent || !unavailable[9].SubIssues {
		t.Fatalf("failed alias availability = %#v", unavailable[9])
	}
}

func TestFetchDisplayIssuesAddsHierarchy(t *testing.T) {
	savedIssues := fetchIssuesFunc
	savedRelationships := fetchIssueRelationshipsFunc
	savedHierarchy := fetchIssueHierarchiesFunc
	t.Cleanup(func() {
		fetchIssuesFunc = savedIssues
		fetchIssueRelationshipsFunc = savedRelationships
		fetchIssueHierarchiesFunc = savedHierarchy
	})

	fetchIssuesFunc = func(issueListOptions) ([]issueEntry, error) {
		return []issueEntry{{Number: 7}, {Number: 9}, {Number: 11}, {Number: 13}}, nil
	}
	fetchIssueRelationshipsFunc = func(_, _, _ string, numbers []int) (map[int][]linkedReference, map[int]bool, error) {
		result := make(map[int][]linkedReference, len(numbers))
		for _, number := range numbers {
			result[number] = nil
		}
		return result, nil, nil
	}
	fetchIssueHierarchiesFunc = func(owner, name, host string, numbers []int) (map[int]issueHierarchy, map[int]issueHierarchyUnavailable, error) {
		if owner != "owner" || name != "repo" || host != "ghe.example.com" || !reflect.DeepEqual(numbers, []int{7, 9, 11, 13}) {
			t.Fatalf("unexpected hierarchy request: %s/%s on %s for %v", owner, name, host, numbers)
		}
		return map[int]issueHierarchy{
			7:  {Parent: testIssueParent(3, "owner/repo"), SubIssues: issueSubIssuesSummary{Completed: 2, Total: 4}},
			9:  {Parent: testIssueParent(5, "other/tasks")},
			11: {},
		}, map[int]issueHierarchyUnavailable{13: {Parent: true, SubIssues: true}}, fmt.Errorf("partial hierarchy failure")
	}

	result, err := fetchDisplayIssues(issueListOptions{repo: "ghe.example.com/owner/repo"}, time.Time{})
	if err != nil {
		t.Fatalf("fetchDisplayIssues returned error: %v", err)
	}
	wantParents := []string{"#3", "other/tasks#5", "-", "?"}
	wantProgress := []string{"2/4", "-", "-", "?"}
	for index := range result.Display {
		if result.Display[index].Parent != wantParents[index] || result.Display[index].SubIssues != wantProgress[index] {
			t.Fatalf("issue %d hierarchy = %q %q, want %q %q", result.Display[index].Number, result.Display[index].Parent, result.Display[index].SubIssues, wantParents[index], wantProgress[index])
		}
	}
	if result.HierarchyErr == nil {
		t.Fatal("partial hierarchy error must be carried for display")
	}
}

func TestExecuteIssueListReportsHierarchyFailureWithoutDroppingRows(t *testing.T) {
	savedIssues := fetchIssuesFunc
	savedRelationships := fetchIssueRelationshipsFunc
	savedHierarchy := fetchIssueHierarchiesFunc
	t.Cleanup(func() {
		fetchIssuesFunc = savedIssues
		fetchIssueRelationshipsFunc = savedRelationships
		fetchIssueHierarchiesFunc = savedHierarchy
	})

	fetchIssuesFunc = func(issueListOptions) ([]issueEntry, error) {
		return []issueEntry{{Number: 7, Title: "Visible issue", State: "OPEN"}}, nil
	}
	fetchIssueRelationshipsFunc = func(_, _, _ string, _ []int) (map[int][]linkedReference, map[int]bool, error) {
		return map[int][]linkedReference{7: nil}, nil, nil
	}
	fetchIssueHierarchiesFunc = func(_, _, _ string, _ []int) (map[int]issueHierarchy, map[int]issueHierarchyUnavailable, error) {
		return nil, allHierarchyUnavailable([]int{7}), fmt.Errorf("fields unsupported")
	}

	var output bytes.Buffer
	if err := executeIssueList(issueListOptions{repo: "owner/repo", limit: 30, state: "open"}, &output, time.Time{}); err != nil {
		t.Fatalf("executeIssueList returned error: %v", err)
	}
	for _, want := range []string{"#7", "Visible issue", "Issue hierarchy unavailable: fields unsupported", "?"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q: %s", want, output.String())
		}
	}
}

func TestParentRelationshipCellLinksQualifiedReference(t *testing.T) {
	parent := testIssueParent(9, "other/tasks")
	text, refs := issueParentDisplay(parent, "owner/repo", false)
	cell := newTableStyler(&bytes.Buffer{}, true).relationshipCell(text, refs)
	if !strings.Contains(cell.styled, parent.URL) || !strings.Contains(cell.styled, "other/tasks#9") {
		t.Fatalf("qualified parent is not fully linked: %q", cell.styled)
	}
	truncated := cell.withText("other/task...")
	if strings.Contains(truncated.styled, parent.URL) {
		t.Fatalf("truncated parent must not retain an invalid link: %q", truncated.styled)
	}
}

func testIssueParent(number int, repository string) *issueParentReference {
	parent := &issueParentReference{
		Number: number,
		URL:    fmt.Sprintf("https://github.com/%s/issues/%d", repository, number),
	}
	parent.Repository.NameWithOwner = repository
	return parent
}
