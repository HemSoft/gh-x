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

// buildMonitorRepoQualifiers renders "repo:a/b repo:c/d" for server-side scoping.
func buildMonitorRepoQualifiers(repos []string) string {
	parts := make([]string, 0, len(repos))
	for _, repo := range repos {
		parts = append(parts, fmt.Sprintf("repo:%s", repo))
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

// buildMonitorGraphQLQuery builds one aliased GraphQL document fetching every
// PR and issue section plus rate limit info in a single round trip.
func buildMonitorGraphQLQuery(cfg *monitorConfig) (string, error) {
	if len(cfg.Repos) == 0 {
		return "", fmt.Errorf("no repos configured")
	}
	repoQualifiers := buildMonitorRepoQualifiers(cfg.Repos)

	var sb strings.Builder
	sb.WriteString("{\n")
	sb.WriteString("  rateLimit { remaining resetAt }\n")
	writeMonitorAccessProbes(&sb, cfg.Repos)
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
	return sb.String(), nil
}

// writeMonitorAccessProbes emits one aliased repository lookup per configured
// repo. Inaccessible repos come back null with a GraphQL error entry, which
// the parser turns into an "invisible to this account" badge.
func writeMonitorAccessProbes(sb *strings.Builder, repos []string) {
	for i, repo := range repos {
		owner, name := splitMonitorRepo(repo)
		if owner == "" || name == "" {
			continue
		}
		fmt.Fprintf(sb, "  acc%d: repository(owner: %q, name: %q) { nameWithOwner }\n", i, owner, name)
	}
}

// splitMonitorRepo splits OWNER/REPO or HOST/OWNER/REPO into its last two parts.
func splitMonitorRepo(repo string) (owner, name string) {
	parts := strings.Split(strings.Trim(repo, "/"), "/")
	if len(parts) < 2 {
		return "", ""
	}
	return parts[len(parts)-2], parts[len(parts)-1]
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

// executeMonitorFetch runs the batched query via gh as the active account.
// Partial failures (an inaccessible repository probe) still yield usable
// data; only responses without any data are treated as errors.
func executeMonitorFetch(cfg *monitorConfig, now time.Time) (*monitorFetchResult, error) {
	query, err := buildMonitorGraphQLQuery(cfg)
	if err != nil {
		return nil, err
	}
	stdoutBuf, stderrBuf, err := monitorGHExecFunc("api", "graphql", "-f", fmt.Sprintf("query=%s", query))
	if err != nil && !hasUsableGraphQLData(stdoutBuf.Bytes()) {
		return nil, wrapExecError(fmt.Errorf("GraphQL search failed: %w", err), stderrBuf.String())
	}
	return parseMonitorResponse(stdoutBuf.Bytes(), cfg, now)
}

// hasUsableGraphQLData reports whether a response body carries a non-null
// data object despite an overall failure.
func hasUsableGraphQLData(data []byte) bool {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return false
	}
	return len(envelope.Data) > 0 && string(envelope.Data) != "null"
}

// parseMonitorResponse decodes aliases pr0..prN, is0..isN, and acc0..accN.
// GraphQL partial errors (e.g. an inaccessible repository probe) become
// warnings instead of failing the whole refresh; a response with no data at
// all is still fatal.
func parseMonitorResponse(data []byte, cfg *monitorConfig, now time.Time) (*monitorFetchResult, error) {
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

	result := &monitorFetchResult{
		FetchedAt:     now,
		Accessible:    map[string]bool{},
		Warnings:      monitorWarnings(envelope.Errors),
		PRSections:    make([]monitorSectionData, len(cfg.PRSections)),
		IssueSections: make([]monitorSectionData, len(cfg.IssueSections)),
	}
	result.Accessible = decodeAccessProbes(dataMap, cfg.Repos)
	if payload := decodeRateLimit(dataMap["rateLimit"]); payload != nil {
		result.RateRemaining = payload.Remaining
		result.RateResetAt = payload.ResetAt
	}
	decodePRSections(dataMap, result, now)
	decodeIssueSections(dataMap, result, now)
	sortAllMonitorSections(result)
	return result, nil
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
func decodeAccessProbes(dataMap map[string]json.RawMessage, repos []string) map[string]bool {
	accessible := make(map[string]bool, len(repos))
	for i, repo := range repos {
		raw, ok := dataMap[fmt.Sprintf("acc%d", i)]
		if !ok {
			continue // probe absent or errored; leave default-visible
		}
		var probe struct {
			NameWithOwner string `json:"nameWithOwner"`
		}
		accessible[repo] = len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &probe) == nil && probe.NameWithOwner != ""
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
