package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// A status run owns one repository target and one rules lookup per base branch.
// No request results escape this invocation's authentication/fallback context.
type statusRemoteSession struct {
	repo  string
	rules map[string]requiredCheckResult
}

func newStatusRemoteSession(repository string) statusRemoteSession {
	repo := ""
	if repository != "" {
		resolved, err := repositoryCurrentFunc()
		if err == nil && resolved.Host != "" {
			repo = resolved.Host + "/" + repository
		}
	}
	// If local resolution fails, leave gh's contextual repository selection
	// intact rather than forcing a host inferred from a different remote.
	return statusRemoteSession{repo: repo, rules: make(map[string]requiredCheckResult)}
}

func fetchCombinedIssueEnrichment(ctx context.Context, owner, name, host string, numbers []int) issueEnrichmentData {
	parts := make([]string, 0, len(numbers))
	for _, number := range numbers {
		parts = append(parts, fmt.Sprintf(`issue%d: issue(number: %d) {
 number closedByPullRequestsReferences(first: 100) { totalCount nodes { number url } }
 parent { number url repository { nameWithOwner } } subIssuesSummary { completed total }
}`, number, number))
	}
	query := fmt.Sprintf(`query { repository(owner: %q, name: %q) { %s } }`, owner, name, strings.Join(parts, " "))
	data, err := fetchGraphQLContext(ctx, host, query)
	if data == nil && err != nil && unsupportedHierarchyMessage(err.Error()) {
		// Schema validation rejects the entire combined operation. Recover the
		// supported relationship field without hiding unavailable hierarchy data.
		rels, missing, relErr := fetchIssueRelationshipsBatchContext(ctx, owner, name, host, numbers)
		return issueEnrichmentData{
			Repository: owner + "/" + name, Relationships: rels, RelationshipsMissing: missing,
			HierarchyMissing: allHierarchyUnavailable(numbers), RelErr: relErr, HierarchyErr: err,
		}
	}
	// Reuse both existing partial-response parsers, including connection truncation
	// and field-specific GraphQL errors, against the same response.
	relData, relFetchErr := combinedIssuePayload(data, err, false)
	hierarchyData, hierarchyFetchErr := combinedIssuePayload(data, err, true)
	rels, missingRels, relErr := fetchIssueRelationshipsBatchWithGraphQL(owner, name, host, numbers, func(string, string) ([]byte, error) { return relData, relFetchErr })
	hierarchy, missingHierarchy, hierarchyErr := fetchIssueHierarchiesBatchWithGraphQL(owner, name, host, numbers, func(string, string) ([]byte, error) { return hierarchyData, hierarchyFetchErr })
	return issueEnrichmentData{
		Repository: owner + "/" + name, Relationships: rels, RelationshipsMissing: missingRels,
		Hierarchies: hierarchy, HierarchyMissing: missingHierarchy, RelErr: relErr, HierarchyErr: hierarchyErr,
	}
}

// Split errors by field before passing the shared payload to the original
// parsers. Errors above either field remain relevant to both acquisitions.
func combinedIssuePayload(data []byte, fetchErr error, hierarchy bool) ([]byte, error) {
	var response struct {
		Data   json.RawMessage `json:"data"`
		Errors []graphQLError  `json:"errors,omitempty"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Errors) == 0 {
		return data, fetchErr
	}
	filtered := make([]graphQLError, 0, len(response.Errors))
	var relevantErr error
	for _, gqlErr := range response.Errors {
		if combinedIssueErrorRelevant(gqlErr, hierarchy) {
			filtered = append(filtered, gqlErr)
			relevantErr = errors.Join(relevantErr, errors.New(gqlErr.Message))
		}
	}
	response.Errors = filtered
	filteredData, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}
	return filteredData, relevantErr
}

func combinedIssueErrorRelevant(gqlErr graphQLError, hierarchy bool) bool {
	for _, element := range gqlErr.Path {
		var field string
		if json.Unmarshal(element, &field) != nil {
			continue
		}
		switch field {
		case "closedByPullRequestsReferences":
			return !hierarchy
		case "parent", "subIssuesSummary":
			return hierarchy
		}
	}
	return true
}
