package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// monitorSectionData is one section's fetch outcome.
type monitorSectionData struct {
	Kind  monitorRowKind
	Total int
	Rows  []monitorRow
}

// monitorFetchResult is the complete payload for one refresh cycle.
type monitorFetchResult struct {
	FetchedAt     time.Time
	RateRemaining int
	RateResetAt   time.Time
	PRSections    []monitorSectionData
	IssueSections []monitorSectionData
	// Accessible maps nameWithOwner -> whether the active account can see
	// the repo. Missing entries default to true (probe absent or failed).
	Accessible map[string]bool
	Warnings   []string
}

type monitorSearchEntry struct {
	IssueCount int             `json:"issueCount"`
	Nodes      json.RawMessage `json:"nodes"`
}

type monitorLabels struct {
	Nodes []struct {
		Name string `json:"name"`
	} `json:"nodes"`
}

// monitorPRNode extends the ATM PR node with fields the detail pane needs.
type monitorPRNode struct {
	atmPullRequestNode
	Body      string `json:"body"`
	Labels    monitorLabels
	Milestone struct {
		Title string `json:"title"`
	} `json:"milestone"`
}

type monitorIssueNode struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	State      string    `json:"state"`
	Body       string    `json:"body"`
	URL        string    `json:"url"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Author     *author   `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Assignees struct {
		Nodes []*author `json:"nodes"`
	} `json:"assignees"`
	Labels    monitorLabels
	Milestone struct {
		Title string `json:"title"`
	} `json:"milestone"`
}

type monitorRepositoryGroup struct {
	Host         string
	Repositories []monitorRepository
}

type monitorHostQuery struct {
	Host         string
	Repositories []monitorRepository
	Query        string
}

// buildMonitorRepoQualifiers renders "repo:a/b repo:c/d" for server-side scoping.
func buildMonitorRepoQualifiers(repos []monitorRepository) string {
	parts := make([]string, 0, len(repos))
	for _, repo := range repos {
		parts = append(parts, fmt.Sprintf("repo:%s", repo.nameWithOwner()))
	}
	return strings.Join(parts, " ")
}

// buildMonitorSearchQuery composes one search string: kind filter, user
// filters, then configured repos. Section filters must not contain repo:
// qualifiers; the config owns repo scope.
func buildMonitorSearchQuery(kind monitorRowKind, filters, repoQualifiers string) string {
	parts := []string{"is:" + string(kind)}
	if trimmed := strings.TrimSpace(filters); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if repoQualifiers != "" {
		parts = append(parts, repoQualifiers)
	}
	return strings.Join(parts, " ")
}

const monitorIssueFieldsFragment = `
        number
        title
        state
        body
        url
        updatedAt
        author { login ... on User { name } }
        repository { nameWithOwner }
        assignees(first: 10) { nodes { login } }
        labels(first: 20) { nodes { name } }
        milestone { title }`

const monitorDetailFieldsFragment = `
        body
        labels(first: 20) { nodes { name } }
        milestone { title }`

func buildMonitorHostQueries(cfg *monitorConfig) ([]monitorHostQuery, error) {
	repositories, err := parseMonitorRepositories(cfg.Repos)
	if err != nil {
		return nil, err
	}
	if len(repositories) == 0 {
		return nil, fmt.Errorf("no repos configured")
	}

	groups := groupMonitorRepositories(repositories)
	queries := make([]monitorHostQuery, 0, len(groups))
	for _, group := range groups {
		queries = append(queries, monitorHostQuery{
			Host:         group.Host,
			Repositories: group.Repositories,
			Query:        buildMonitorGraphQLQueryForRepos(cfg, group.Repositories),
		})
	}
	return queries, nil
}

func groupMonitorRepositories(repositories []monitorRepository) []monitorRepositoryGroup {
	groups := make([]monitorRepositoryGroup, 0)
	indices := make(map[string]int)
	for _, repository := range repositories {
		index, ok := indices[repository.Host]
		if !ok {
			index = len(groups)
			indices[repository.Host] = index
			groups = append(groups, monitorRepositoryGroup{Host: repository.Host})
		}
		groups[index].Repositories = append(groups[index].Repositories, repository)
	}
	return groups
}

// buildMonitorGraphQLQueryForRepos builds one aliased GraphQL document for a
// set of repositories that all belong to the same GitHub host.
func buildMonitorGraphQLQueryForRepos(cfg *monitorConfig, repositories []monitorRepository) string {
	repoQualifiers := buildMonitorRepoQualifiers(repositories)

	var sb strings.Builder
	sb.WriteString("{\n")
	sb.WriteString("  rateLimit { remaining resetAt }\n")
	writeMonitorAccessProbes(&sb, repositories)
	for i, section := range cfg.PRSections {
		writeMonitorSearchAlias(&sb, fmt.Sprintf("pr%d", i), monitorKindPR,
			buildMonitorSearchQuery(monitorKindPR, section.Filters, repoQualifiers),
			monitorSectionLimit(section, cfg.Defaults.Limit))
	}
	for i, section := range cfg.IssueSections {
		writeMonitorSearchAlias(&sb, fmt.Sprintf("is%d", i), monitorKindIssue,
			buildMonitorSearchQuery(monitorKindIssue, section.Filters, repoQualifiers),
			monitorSectionLimit(section, cfg.Defaults.Limit))
	}
	sb.WriteString("}")
	return sb.String()
}

// writeMonitorAccessProbes emits one aliased repository lookup per configured
// repo. Inaccessible repos come back null with a GraphQL error entry, which
// the parser turns into an "invisible to this account" badge.
func writeMonitorAccessProbes(sb *strings.Builder, repos []monitorRepository) {
	for i, repo := range repos {
		fmt.Fprintf(sb, "  acc%d: repository(owner: %q, name: %q) { nameWithOwner }\n", i, repo.Owner, repo.Name)
	}
}

func writeMonitorSearchAlias(sb *strings.Builder, alias string, kind monitorRowKind, query string, limit int) {
	fmt.Fprintf(sb, "  %s: search(query: %q, type: ISSUE, first: %d) {\n", alias, query, limit)
	sb.WriteString("    issueCount\n    nodes {\n")
	if kind == monitorKindPR {
		fmt.Fprintf(sb, "      ... on PullRequest {%s\n", atmPRFieldsFragment+monitorDetailFieldsFragment)
	} else {
		fmt.Fprintf(sb, "      ... on Issue {%s\n", monitorIssueFieldsFragment)
	}
	sb.WriteString("      }\n")
	sb.WriteString("    }\n")
	sb.WriteString("  }\n")
}

// monitorGHExecFunc is the gh choke point for monitor; swapped in tests.
var monitorGHExecFunc = execGHActive

// executeMonitorFetch runs one batched query per GitHub host. A host failure
// becomes a warning when another host succeeds, so a transient Enterprise
// outage does not hide otherwise usable monitor data.
func executeMonitorFetch(cfg *monitorConfig, now time.Time) (*monitorFetchResult, error) {
	queries, err := buildMonitorHostQueries(cfg)
	if err != nil {
		return nil, err
	}

	combined := newMonitorFetchResult(cfg, now)
	failedHosts := make([]string, 0)
	successfulHosts := 0
	for _, request := range queries {
		partial, fetchErr := fetchMonitorHost(request, cfg, now)
		if fetchErr != nil {
			failedHosts = append(failedHosts, fmt.Sprintf("%s: %v", request.Host, fetchErr))
			continue
		}
		mergeMonitorFetchResult(combined, partial, len(queries) > 1, request.Host)
		successfulHosts++
	}
	if successfulHosts == 0 {
		return nil, fmt.Errorf("monitor refresh failed for all configured hosts: %s", strings.Join(failedHosts, "; "))
	}
	combined.Warnings = append(combined.Warnings, failedHosts...)
	sortAllMonitorSections(combined)
	limitMonitorSectionRows(combined, cfg)
	return combined, nil
}

func fetchMonitorHost(request monitorHostQuery, cfg *monitorConfig, now time.Time) (*monitorFetchResult, error) {
	args := []string{"api", "--hostname", request.Host, "graphql", "-f", fmt.Sprintf("query=%s", request.Query)}
	stdoutBuf, stderrBuf, execErr := monitorGHExecFunc(args...)
	if execErr != nil && !hasUsableGraphQLData(stdoutBuf.Bytes()) {
		return nil, wrapExecError(fmt.Errorf("GraphQL search failed: %w", execErr), stderrBuf.String())
	}
	result, err := parseMonitorHostResponse(stdoutBuf.Bytes(), cfg, request.Repositories, now)
	if err != nil {
		return nil, fmt.Errorf("parse GraphQL response: %w", err)
	}
	return result, nil
}

func newMonitorFetchResult(cfg *monitorConfig, now time.Time) *monitorFetchResult {
	result := &monitorFetchResult{
		FetchedAt:     now,
		Accessible:    map[string]bool{},
		PRSections:    make([]monitorSectionData, len(cfg.PRSections)),
		IssueSections: make([]monitorSectionData, len(cfg.IssueSections)),
	}
	for i := range result.PRSections {
		result.PRSections[i].Kind = monitorKindPR
	}
	for i := range result.IssueSections {
		result.IssueSections[i].Kind = monitorKindIssue
	}
	return result
}

func mergeMonitorFetchResult(dst, src *monitorFetchResult, qualifyWarnings bool, host string) {
	if !src.RateResetAt.IsZero() && (dst.RateResetAt.IsZero() || src.RateRemaining < dst.RateRemaining) {
		dst.RateRemaining = src.RateRemaining
		dst.RateResetAt = src.RateResetAt
	}
	for repo, accessible := range src.Accessible {
		dst.Accessible[repo] = accessible
	}
	for i := range dst.PRSections {
		dst.PRSections[i].Total += src.PRSections[i].Total
		dst.PRSections[i].Rows = append(dst.PRSections[i].Rows, src.PRSections[i].Rows...)
	}
	for i := range dst.IssueSections {
		dst.IssueSections[i].Total += src.IssueSections[i].Total
		dst.IssueSections[i].Rows = append(dst.IssueSections[i].Rows, src.IssueSections[i].Rows...)
	}
	for _, warning := range src.Warnings {
		if qualifyWarnings {
			warning = host + ": " + warning
		}
		dst.Warnings = append(dst.Warnings, warning)
	}
}

// hasUsableGraphQLData reports whether a response body carries a non-null
// data object despite an overall failure.
func hasUsableGraphQLData(data []byte) bool {
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return false
	}
	return len(envelope.Data) > 0
}

// parseMonitorResponse decodes aliases pr0..prN, is0..isN, and acc0..accN.
// GraphQL partial errors (e.g. an inaccessible repository probe) become
// warnings instead of failing the whole refresh; a response with no data at
// all is still fatal.
func parseMonitorHostResponse(data []byte, cfg *monitorConfig, repositories []monitorRepository, now time.Time) (*monitorFetchResult, error) {
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Path    []any  `json:"path"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode GraphQL response: %w", err)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		if len(envelope.Errors) > 0 {
			return nil, fmt.Errorf("GraphQL error: %s", envelope.Errors[0].Message)
		}
		return nil, fmt.Errorf("GraphQL response contained no data")
	}

	dataMap := map[string]json.RawMessage{}
	if err := json.Unmarshal(envelope.Data, &dataMap); err != nil {
		return nil, fmt.Errorf("decode GraphQL data: %w", err)
	}

	result := newMonitorFetchResult(cfg, now)
	result.Warnings = monitorWarnings(envelope.Errors)
	result.Accessible = decodeAccessProbes(dataMap, repositories)
	if payload := decodeRateLimit(dataMap["rateLimit"]); payload != nil {
		result.RateRemaining = payload.Remaining
		result.RateResetAt = payload.ResetAt
	}
	decodePRSections(dataMap, result, now)
	decodeIssueSections(dataMap, result, now)
	if len(repositories) > 0 {
		qualifyMonitorResultRows(result, repositories[0].Host)
	}
	sortAllMonitorSections(result)
	return result, nil
}

func qualifyMonitorResultRows(result *monitorFetchResult, host string) {
	if host == defaultGitHubHost {
		return
	}
	for i := range result.PRSections {
		for j := range result.PRSections[i].Rows {
			result.PRSections[i].Rows[j].Repo = host + "/" + result.PRSections[i].Rows[j].Repo
		}
	}
	for i := range result.IssueSections {
		for j := range result.IssueSections[i].Rows {
			result.IssueSections[i].Rows[j].Repo = host + "/" + result.IssueSections[i].Rows[j].Repo
		}
	}
}

// monitorWarnings condenses partial-error entries into short footer lines.
func monitorWarnings(errors []struct {
	Message string `json:"message"`
	Path    []any  `json:"path"`
}) []string {
	warnings := make([]string, 0, len(errors))
	for _, entry := range errors {
		text := strings.TrimSpace(entry.Message)
		if text == "" {
			continue
		}
		warnings = append(warnings, truncateMonitorCell(text, 80))
		if len(warnings) >= 3 {
			break
		}
	}
	return warnings
}

// decodeAccessProbes resolves which configured repos the active account sees.
func decodeAccessProbes(dataMap map[string]json.RawMessage, repos []monitorRepository) map[string]bool {
	accessible := make(map[string]bool, len(repos))
	for i, repo := range repos {
		raw, ok := dataMap[fmt.Sprintf("acc%d", i)]
		if !ok {
			continue // probe absent or errored; leave default-visible
		}
		var probe struct {
			NameWithOwner string `json:"nameWithOwner"`
		}
		accessible[repo.configValue()] = len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &probe) == nil && probe.NameWithOwner != ""
	}
	return accessible
}

type monitorRateLimitPayload struct {
	Remaining int       `json:"remaining"`
	ResetAt   time.Time `json:"resetAt"`
}

func decodeRateLimit(raw json.RawMessage) *monitorRateLimitPayload {
	var payload monitorRateLimitPayload
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	return &payload
}

func decodePRSections(dataMap map[string]json.RawMessage, result *monitorFetchResult, now time.Time) {
	for i := range result.PRSections {
		result.PRSections[i] = decodeMonitorPRSection(dataMap[fmt.Sprintf("pr%d", i)], now)
	}
}

func decodeIssueSections(dataMap map[string]json.RawMessage, result *monitorFetchResult, now time.Time) {
	for i := range result.IssueSections {
		result.IssueSections[i] = decodeMonitorIssueSection(dataMap[fmt.Sprintf("is%d", i)], now)
	}
}

func decodeMonitorPRSection(raw json.RawMessage, now time.Time) monitorSectionData {
	entry, nodes, ok := decodeMonitorSearchEntry(raw)
	if !ok {
		return monitorSectionData{Kind: monitorKindPR}
	}
	rows := make([]monitorRow, 0, len(nodes))
	for _, node := range nodes {
		var prNode monitorPRNode
		if json.Unmarshal(node, &prNode) != nil {
			continue
		}
		rows = append(rows, mapMonitorPRNode(prNode, now))
	}
	return monitorSectionData{Kind: monitorKindPR, Total: entry.IssueCount, Rows: rows}
}

func decodeMonitorIssueSection(raw json.RawMessage, now time.Time) monitorSectionData {
	entry, nodes, ok := decodeMonitorSearchEntry(raw)
	if !ok {
		return monitorSectionData{Kind: monitorKindIssue}
	}
	rows := make([]monitorRow, 0, len(nodes))
	for _, node := range nodes {
		var issueNode monitorIssueNode
		if json.Unmarshal(node, &issueNode) != nil {
			continue
		}
		rows = append(rows, mapMonitorIssueNode(issueNode, now))
	}
	return monitorSectionData{Kind: monitorKindIssue, Total: entry.IssueCount, Rows: rows}
}

func decodeMonitorSearchEntry(raw json.RawMessage) (monitorSearchEntry, []json.RawMessage, bool) {
	var entry monitorSearchEntry
	if len(raw) == 0 || json.Unmarshal(raw, &entry) != nil {
		return monitorSearchEntry{}, nil, false
	}
	var nodes []json.RawMessage
	if json.Unmarshal(entry.Nodes, &nodes) != nil {
		return entry, nil, false
	}
	return entry, nodes, true
}

func sortAllMonitorSections(result *monitorFetchResult) {
	for i := range result.PRSections {
		sortMonitorRowsByUpdated(result.PRSections[i].Rows)
	}
	for i := range result.IssueSections {
		sortMonitorRowsByUpdated(result.IssueSections[i].Rows)
	}
}

func limitMonitorSectionRows(result *monitorFetchResult, cfg *monitorConfig) {
	for i := range result.PRSections {
		limit := monitorSectionLimit(cfg.PRSections[i], cfg.Defaults.Limit)
		if len(result.PRSections[i].Rows) > limit {
			result.PRSections[i].Rows = result.PRSections[i].Rows[:limit]
		}
	}
	for i := range result.IssueSections {
		limit := monitorSectionLimit(cfg.IssueSections[i], cfg.Defaults.Limit)
		if len(result.IssueSections[i].Rows) > limit {
			result.IssueSections[i].Rows = result.IssueSections[i].Rows[:limit]
		}
	}
}

// sortMonitorRowsByUpdated orders newest-first; zero timestamps sink.
func sortMonitorRowsByUpdated(rows []monitorRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].UpdatedAt.After(rows[j].UpdatedAt)
	})
}

func labelNames(labels monitorLabels) []string {
	names := make([]string, 0, len(labels.Nodes))
	for _, label := range labels.Nodes {
		names = append(names, label.Name)
	}
	return names
}

func mapMonitorPRNode(node monitorPRNode, now time.Time) monitorRow {
	base := atmPullRequestNode(node.atmPullRequestNode)
	enriched := mapAtmNode(base, now)
	return monitorRow{
		Kind:      monitorKindPR,
		Number:    enriched.Number,
		Title:     node.Title,
		Author:    enriched.Author,
		State:     normalizeState(node.State, node.IsDraft),
		Review:    enriched.Review,
		Approvals: enriched.Approvals,
		Checks:    enriched.Checks,
		Comments:  enriched.Comments,
		AIReview:  enriched.AIReview,
		AIClean:   enriched.AIClean,
		Branch:    node.HeadRefName,
		Labels:    labelNames(node.Labels),
		Milestone: node.Milestone.Title,
		Body:      node.Body,
		Updated:   formatRelativeTime(node.UpdatedAt, now),
		UpdatedAt: node.UpdatedAt,
		URL:       node.URL,
		Repo:      node.Repository.NameWithOwner,
	}
}

func mapMonitorIssueNode(node monitorIssueNode, now time.Time) monitorRow {
	return monitorRow{
		Kind:      monitorKindIssue,
		Number:    node.Number,
		Title:     node.Title,
		Author:    formatMonitorLogin(node.Author),
		State:     normalizeState(node.State, false),
		Assignees: joinMonitorLogins(node.Assignees.Nodes),
		Labels:    labelNames(node.Labels),
		Milestone: node.Milestone.Title,
		Body:      node.Body,
		Updated:   formatRelativeTime(node.UpdatedAt, now),
		UpdatedAt: node.UpdatedAt,
		URL:       node.URL,
		Repo:      node.Repository.NameWithOwner,
	}
}

func formatMonitorLogin(user *author) string {
	if user == nil || user.Login == "" {
		return "-"
	}
	return formatAuthor(user.Login, user.Name)
}

func joinMonitorLogins(users []*author) string {
	logins := make([]string, 0, len(users))
	for _, user := range users {
		if user != nil && user.Login != "" {
			logins = append(logins, user.Login)
		}
	}
	if len(logins) == 0 {
		return "-"
	}
	return strings.Join(logins, ", ")
}
