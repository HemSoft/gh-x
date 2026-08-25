package main

import (
	"encoding/json"
	"fmt"
	"github.com/cli/go-gh/v2/pkg/repository"
	"net/url"
	"os"
	"strings"
)

// ghExecFunc is the single choke point for gh subprocess execution; it adds
// multi-account fallback and can be swapped in tests.
var ghExecFunc = execGH

// fetchPRSupplementalBatchFunc is swapped in tests to avoid real API calls.
var fetchPRSupplementalBatchFunc = fetchPRSupplementalBatch

// fetchSupplementalData retrieves supplemental PR data via GraphQL (best-effort).
func fetchSupplementalData(repo string, prs []pullRequest) (map[int]prSupplementalInfo, bool, string, string) {
	owner, name, err := resolveRepo(repo)
	if err != nil {
		return nil, true, "", ""
	}
	numbers := make([]int, len(prs))
	for i, pr := range prs {
		numbers[i] = pr.Number
	}
	fetched, err := fetchPRSupplemental(owner, name, repositoryTargetHost(repo), numbers)
	if err != nil {
		return nil, true, owner, name
	}
	return fetched, false, owner, name
}

// fetchRequiredChecks retrieves required check contexts per base branch (best-effort).
func fetchRequiredChecks(owner, name string, prs []pullRequest) (map[string]map[string]bool, map[string]bool) {
	result := make(map[string]map[string]bool)
	failed := make(map[string]bool)
	if owner == "" {
		for _, base := range uniqueBaseBranches(prs) {
			failed[base] = true
		}
		return result, failed
	}
	for _, base := range uniqueBaseBranches(prs) {
		if ctx, ok := fetchRequiredCheckContexts(owner, name, base); ok && len(ctx) > 0 {
			result[base] = ctx
		} else if !ok {
			failed[base] = true
		}
	}
	return result, failed
}

func resolveRepo(repoOverride string) (string, string, error) {
	if repoOverride == "" {
		repoOverride = strings.TrimSpace(os.Getenv("GH_REPO"))
	}
	if repoOverride != "" {
		parts := strings.Split(repoOverride, "/")
		if len(parts) < 2 {
			return "", "", fmt.Errorf("invalid repo format: %s", repoOverride)
		}
		return parts[len(parts)-2], parts[len(parts)-1], nil
	}

	repo, err := repository.Current()
	if err == nil {
		return repo.Owner, repo.Name, nil
	}

	// Fall back to gh repo view for SSH aliases and non-standard remotes
	stdout, _, execErr := ghExecFunc("repo", "view", "--json", "owner,name")
	if execErr != nil {
		return "", "", fmt.Errorf("repo resolution failed: %w; fallback: %v", err, execErr)
	}
	return parseRepoViewResponse(stdout.Bytes())
}

func parseRepoViewResponse(data []byte) (string, string, error) {
	var info struct {
		Name  string `json:"name"`
		Owner struct {
			Login string `json:"login"`
		} `json:"owner"`
	}
	if err := json.Unmarshal(data, &info); err != nil {
		return "", "", err
	}
	if info.Owner.Login == "" || info.Name == "" {
		return "", "", fmt.Errorf("could not resolve repo from gh repo view")
	}
	return info.Owner.Login, info.Name, nil
}

// resolveAuthorLoginFunc is swapped in tests to avoid real API calls.
var resolveAuthorLoginFunc = resolveAuthorLogin

// resolveAuthorLogin resolves an author value to a GitHub login.
// If the value contains no spaces, it's assumed to be a login already (with
// optional "@" prefix stripped). If it contains a space, it's treated as a
// display name and resolved via org member search (GraphQL), falling back to
// global GitHub user search.
func resolveAuthorLogin(author, org string) (string, error) {
	author = strings.TrimPrefix(author, "@")
	if !strings.Contains(author, " ") {
		return author, nil
	}

	// Try org-scoped member lookup first (most reliable for org repos).
	if org != "" {
		if login := resolveAuthorFromOrg(author, org); login != "" {
			return login, nil
		}
	}

	// Fall back to global GitHub user search.
	query := url.QueryEscape(author + " in:name")
	endpoint := fmt.Sprintf("search/users?q=%s&per_page=1", query)
	stdout, _, err := ghExecFunc("api", endpoint, "--jq", ".items[0].login")
	if err != nil {
		return "", fmt.Errorf("resolving author %q: %w", author, err)
	}
	login := strings.TrimSpace(stdout.String())
	if login == "" || login == "null" {
		return "", fmt.Errorf("no GitHub user found matching name %q", author)
	}
	return login, nil
}

// resolveAuthorFromOrg resolves a display name to a login within an org.
// Searches globally for matching users, then verifies org membership.
// Returns the matching login or empty string if not found.
func resolveAuthorFromOrg(name, org string) string {
	query := url.QueryEscape(name + " in:name")
	endpoint := fmt.Sprintf("search/users?q=%s&per_page=5", query)
	stdout, _, err := ghExecFunc("api", endpoint, "--jq", ".items[].login")
	if err != nil {
		return ""
	}
	logins := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	for _, login := range logins {
		login = strings.TrimSpace(login)
		if login == "" || login == "null" {
			continue
		}
		memberEndpoint := fmt.Sprintf("orgs/%s/members/%s", org, login)
		_, _, memberErr := ghExecFunc("api", memberEndpoint)
		if memberErr == nil {
			return login
		}
	}
	return ""
}

func fetchPRSupplemental(owner, name, host string, prNumbers []int) (map[int]prSupplementalInfo, error) {
	if len(prNumbers) == 0 {
		return nil, nil
	}

	// Batch PRs to avoid exceeding Windows command-line length limits (~32K chars).
	// Each PR's query fragment is ~350 chars; batches of 30 stay well under the limit.
	result := make(map[int]prSupplementalInfo)
	for i := 0; i < len(prNumbers); i += relationshipBatchSize {
		end := i + relationshipBatchSize
		if end > len(prNumbers) {
			end = len(prNumbers)
		}
		batch, err := fetchPRSupplementalBatchFunc(owner, name, host, prNumbers[i:end])
		if err != nil {
			return nil, err
		}
		for k, v := range batch {
			result[k] = v
		}
	}
	return result, nil
}

func fetchPRSupplementalBatch(owner, name, host string, prNumbers []int) (map[int]prSupplementalInfo, error) {
	var queryParts []string
	for _, num := range prNumbers {
		queryParts = append(queryParts, fmt.Sprintf(
			`pr%d: pullRequest(number: %d) { number headRefOid closingIssuesReferences(first: 100) { totalCount nodes { number url } } comments(last: 100) { totalCount nodes { body createdAt author { login __typename } } } reviewThreads(last: 100) { totalCount nodes { isResolved comments(first: 1) { nodes { author { login __typename } } } } } reviews(last: 100) { totalCount nodes { state submittedAt commit { oid } author { login __typename } comments { totalCount } } } approvedReviews: reviews(states: [APPROVED], last: 50) { nodes { author { login __typename } } } }`,
			num, num,
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
	return parseSupplementalResponse(data)
}
