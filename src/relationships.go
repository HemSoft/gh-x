package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const relationshipBatchSize = 30

type linkedReference struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
}

func relationshipDisplay(refs []linkedReference, unavailable bool) (string, []linkedReference) {
	if unavailable {
		return "?", nil
	}

	normalized := normalizeLinkedReferences(refs)
	if len(normalized) == 0 {
		return "-", nil
	}

	parts := make([]string, len(normalized))
	for i, ref := range normalized {
		parts[i] = fmt.Sprintf("#%d", ref.Number)
	}
	return strings.Join(parts, ", "), normalized
}

func normalizeLinkedReferences(refs []linkedReference) []linkedReference {
	normalized := make([]linkedReference, 0, len(refs))
	seen := make(map[int]bool, len(refs))
	for _, ref := range refs {
		if ref.Number <= 0 || seen[ref.Number] {
			continue
		}
		seen[ref.Number] = true
		normalized = append(normalized, ref)
	}
	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].Number < normalized[j].Number
	})
	return normalized
}

func (s tableStyler) relationshipCell(text string, refs []linkedReference) tableCell {
	if text == "" {
		text = "-"
	}
	style := func(value string) string {
		return s.styleRelationshipText(value, refs)
	}
	return tableCell{text: text, styled: style(text), styleFn: style}
}

func (s tableStyler) styleRelationshipText(text string, refs []linkedReference) string {
	if len(refs) == 0 {
		return s.dim(text).styled
	}

	var styled strings.Builder
	remaining := text
	for _, ref := range refs {
		token := fmt.Sprintf("#%d", ref.Number)
		index := strings.Index(remaining, token)
		if index < 0 {
			break
		}
		styled.WriteString(s.dim(remaining[:index]).styled)
		styled.WriteString(s.dimLinkCell(token, ref.URL).styled)
		remaining = remaining[index+len(token):]
	}
	styled.WriteString(s.dim(remaining).styled)
	return styled.String()
}

func repositoryTargetHost(repo string) string {
	args := []string{"api", "graphql"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	return targetHost(args)
}

func fetchGraphQL(host, query string) ([]byte, error) {
	args := []string{"api"}
	if host != "" {
		args = append(args, "--hostname", host)
	}
	args = append(args, "graphql", "-f", fmt.Sprintf("query=%s", query))
	stdout, _, err := ghExecFunc(args...)
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

var fetchIssueRelationshipsBatchFunc = fetchIssueRelationshipsBatch
var fetchIssueRelationshipsFunc = fetchIssueRelationships

func fetchIssueRelationships(owner, name, host string, issueNumbers []int) (map[int][]linkedReference, error) {
	if len(issueNumbers) == 0 {
		return nil, nil
	}

	result := make(map[int][]linkedReference)
	for start := 0; start < len(issueNumbers); start += relationshipBatchSize {
		end := min(start+relationshipBatchSize, len(issueNumbers))
		batch, err := fetchIssueRelationshipsBatchFunc(owner, name, host, issueNumbers[start:end])
		if err != nil {
			return nil, err
		}
		for number, refs := range batch {
			result[number] = refs
		}
	}
	return result, nil
}

func fetchIssueRelationshipsBatch(owner, name, host string, issueNumbers []int) (map[int][]linkedReference, error) {
	queryParts := make([]string, 0, len(issueNumbers))
	for _, number := range issueNumbers {
		queryParts = append(queryParts, fmt.Sprintf(
			`issue%d: issue(number: %d) { number closedByPullRequestsReferences(first: 100) { nodes { number url } } }`,
			number, number,
		))
	}
	query := fmt.Sprintf(
		`query { repository(owner: %q, name: %q) { %s } }`,
		owner, name, strings.Join(queryParts, " "),
	)
	data, err := fetchGraphQL(host, query)
	if err != nil {
		return nil, err
	}
	return parseIssueRelationships(data)
}

func parseIssueRelationships(data []byte) (map[int][]linkedReference, error) {
	var response struct {
		Data struct {
			Repository map[string]json.RawMessage `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, err
	}

	result := make(map[int][]linkedReference)
	for _, raw := range response.Data.Repository {
		var issue struct {
			Number                         int `json:"number"`
			ClosedByPullRequestsReferences struct {
				Nodes []linkedReference `json:"nodes"`
			} `json:"closedByPullRequestsReferences"`
		}
		if err := json.Unmarshal(raw, &issue); err != nil || issue.Number <= 0 {
			continue
		}
		result[issue.Number] = issue.ClosedByPullRequestsReferences.Nodes
	}
	return result, nil
}
