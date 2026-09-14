package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type issueParentReference struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
}

type issueSubIssuesSummary struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
}

type issueHierarchy struct {
	Parent    *issueParentReference
	SubIssues issueSubIssuesSummary
}

type issueHierarchyUnavailable struct {
	Parent    bool
	SubIssues bool
}

var fetchIssueHierarchiesBatchFunc = fetchIssueHierarchiesBatchContext
var fetchIssueHierarchiesFunc = fetchIssueHierarchiesContext

func fetchIssueHierarchiesContext(ctx context.Context, owner, name, host string, issueNumbers []int) (map[int]issueHierarchy, map[int]issueHierarchyUnavailable, error) {
	if len(issueNumbers) == 0 {
		return nil, nil, nil
	}

	result := make(map[int]issueHierarchy)
	unavailable := make(map[int]issueHierarchyUnavailable)
	var firstErr error
	for start := 0; start < len(issueNumbers); start += relationshipBatchSize {
		end := min(start+relationshipBatchSize, len(issueNumbers))
		batch, batchUnavailable, err := fetchIssueHierarchiesBatchFunc(ctx, owner, name, host, issueNumbers[start:end])
		for number, hierarchy := range batch {
			result[number] = hierarchy
		}
		for number, fields := range batchUnavailable {
			unavailable[number] = fields
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return result, unavailable, firstPartialFetchError(firstErr, hierarchyUnavailableCount(unavailable), len(issueNumbers), "issues")
}

func hierarchyUnavailableCount(unavailable map[int]issueHierarchyUnavailable) map[int]bool {
	result := make(map[int]bool, len(unavailable))
	for number, fields := range unavailable {
		if fields.Parent || fields.SubIssues {
			result[number] = true
		}
	}
	return result
}

func fetchIssueHierarchiesBatchContext(ctx context.Context, owner, name, host string, issueNumbers []int) (map[int]issueHierarchy, map[int]issueHierarchyUnavailable, error) {
	return fetchIssueHierarchiesBatchWithGraphQL(owner, name, host, issueNumbers, func(host, query string) ([]byte, error) {
		return fetchGraphQLContext(ctx, host, query)
	})
}

func fetchIssueHierarchiesBatchWithGraphQL(owner, name, host string, issueNumbers []int, fetch func(string, string) ([]byte, error)) (map[int]issueHierarchy, map[int]issueHierarchyUnavailable, error) {
	queryParts := make([]string, 0, len(issueNumbers))
	for _, number := range issueNumbers {
		queryParts = append(queryParts, fmt.Sprintf(
			`issue%d: issue(number: %d) { number parent { number url repository { nameWithOwner } } subIssuesSummary { completed total } }`,
			number, number,
		))
	}
	query := fmt.Sprintf(
		`query { repository(owner: %q, name: %q) { %s } }`,
		owner, name, strings.Join(queryParts, " "),
	)
	data, err := fetch(host, query)
	if data == nil {
		return nil, allHierarchyUnavailable(issueNumbers), err
	}
	hierarchies, unavailable, parseErr := parseIssueHierarchies(data)
	if parseErr != nil {
		return nil, allHierarchyUnavailable(issueNumbers), parseErr
	}
	for _, number := range issueNumbers {
		if _, ok := hierarchies[number]; !ok {
			unavailable[number] = issueHierarchyUnavailable{Parent: true, SubIssues: true}
		}
	}
	return hierarchies, unavailable, err
}

func allHierarchyUnavailable(issueNumbers []int) map[int]issueHierarchyUnavailable {
	unavailable := make(map[int]issueHierarchyUnavailable, len(issueNumbers))
	for _, number := range issueNumbers {
		unavailable[number] = issueHierarchyUnavailable{Parent: true, SubIssues: true}
	}
	return unavailable
}

func parseIssueHierarchies(data []byte) (map[int]issueHierarchy, map[int]issueHierarchyUnavailable, error) {
	var response struct {
		Data struct {
			Repository map[string]json.RawMessage `json:"repository"`
		} `json:"data"`
		Errors []graphQLError `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, nil, err
	}

	result := make(map[int]issueHierarchy)
	unavailable := make(map[int]issueHierarchyUnavailable)
	for alias, raw := range response.Data.Repository {
		number, hierarchy, missing, ok := parseIssueHierarchyAlias(alias, raw)
		if !ok {
			continue
		}
		result[number] = hierarchy
		if missing.Parent || missing.SubIssues {
			unavailable[number] = missing
		}
	}
	mergeHierarchyErrors(unavailable, response.Errors)
	return result, unavailable, nil
}

func parseIssueHierarchyAlias(alias string, raw json.RawMessage) (int, issueHierarchy, issueHierarchyUnavailable, bool) {
	aliasIssueNumber, ok := aliasNumber(alias, "issue")
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return 0, issueHierarchy{}, issueHierarchyUnavailable{}, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return 0, issueHierarchy{}, issueHierarchyUnavailable{}, false
	}
	var issueNumber int
	if json.Unmarshal(fields["number"], &issueNumber) != nil || issueNumber <= 0 {
		return 0, issueHierarchy{}, issueHierarchyUnavailable{}, false
	}
	hierarchy, missing := parseIssueHierarchyFields(fields)
	if aliasIssueNumber != issueNumber {
		missing = issueHierarchyUnavailable{Parent: true, SubIssues: true}
	}
	return issueNumber, hierarchy, missing, true
}

func parseIssueHierarchyFields(fields map[string]json.RawMessage) (issueHierarchy, issueHierarchyUnavailable) {
	parent, parentUnavailable := parseIssueParent(fields)
	summary, summaryUnavailable := parseSubIssuesSummary(fields)
	return issueHierarchy{Parent: parent, SubIssues: summary}, issueHierarchyUnavailable{
		Parent:    parentUnavailable,
		SubIssues: summaryUnavailable,
	}
}

func parseIssueParent(fields map[string]json.RawMessage) (*issueParentReference, bool) {
	parentRaw, present := fields["parent"]
	if !present {
		return nil, true
	}
	if string(parentRaw) == "null" {
		return nil, false
	}
	var parent issueParentReference
	if json.Unmarshal(parentRaw, &parent) != nil || !validIssueParent(parent) {
		return nil, true
	}
	return &parent, false
}

func validIssueParent(parent issueParentReference) bool {
	return parent.Number > 0 && parent.URL != "" && parent.Repository.NameWithOwner != ""
}

func parseSubIssuesSummary(fields map[string]json.RawMessage) (issueSubIssuesSummary, bool) {
	summaryRaw, present := fields["subIssuesSummary"]
	if !present || string(summaryRaw) == "null" {
		return issueSubIssuesSummary{}, true
	}
	var summaryFields map[string]json.RawMessage
	if json.Unmarshal(summaryRaw, &summaryFields) != nil || !hasJSONValue(summaryFields, "completed") || !hasJSONValue(summaryFields, "total") {
		return issueSubIssuesSummary{}, true
	}
	var summary issueSubIssuesSummary
	if json.Unmarshal(summaryRaw, &summary) != nil || !validSubIssuesSummary(summary) {
		return issueSubIssuesSummary{}, true
	}
	return summary, false
}

func hasJSONValue(fields map[string]json.RawMessage, name string) bool {
	value, ok := fields[name]
	return ok && len(value) > 0 && string(value) != "null"
}

func validSubIssuesSummary(summary issueSubIssuesSummary) bool {
	return summary.Total >= 0 && summary.Completed >= 0 && summary.Completed <= summary.Total
}

func mergeHierarchyErrors(unavailable map[int]issueHierarchyUnavailable, errors []graphQLError) {
	for _, gqlErr := range errors {
		number, field, ok := hierarchyErrorPath(gqlErr.Path)
		if !ok {
			continue
		}
		missing := unavailable[number]
		switch field {
		case "parent":
			missing.Parent = true
		case "subIssuesSummary":
			missing.SubIssues = true
		default:
			missing.Parent = true
			missing.SubIssues = true
		}
		unavailable[number] = missing
	}
}

func hierarchyErrorPath(path []json.RawMessage) (int, string, bool) {
	for index, element := range path {
		var name string
		if json.Unmarshal(element, &name) != nil {
			continue
		}
		number, ok := aliasNumber(name, "issue")
		if !ok {
			continue
		}
		field := ""
		if index+1 < len(path) {
			_ = json.Unmarshal(path[index+1], &field)
		}
		return number, field, true
	}
	return 0, "", false
}

func issueParentDisplay(parent *issueParentReference, currentRepo string, unavailable bool) (string, []linkedReference) {
	if unavailable {
		return "?", nil
	}
	if parent == nil {
		return "-", nil
	}
	if !validIssueParent(*parent) {
		return "?", nil
	}
	text := fmt.Sprintf("#%d", parent.Number)
	if !strings.EqualFold(parent.Repository.NameWithOwner, currentRepo) {
		text = parent.Repository.NameWithOwner + text
	}
	ref := linkedReference{Number: parent.Number, URL: parent.URL, Text: text}
	return text, []linkedReference{ref}
}

func subIssueProgressDisplay(summary issueSubIssuesSummary, unavailable bool) string {
	if unavailable {
		return "?"
	}
	if summary.Total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d/%d", summary.Completed, summary.Total)
}
