package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

type monitorOwnerPage struct {
	Login         string `json:"login"`
	Organizations struct {
		Nodes []struct {
			Login string `json:"login"`
		} `json:"nodes"`
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
	} `json:"organizations"`
}

// discoverMonitorOwnerScope follows every organization page for the active host
// account. It never falls back to the configured repository list on failure.
func discoverMonitorOwnerScope(ctx context.Context, host string) (string, error) {
	cursor := ""
	login := ""
	qualifiers := []string{}
	seen := map[string]bool{}
	for {
		page, err := fetchMonitorOwnerPage(ctx, host, cursor)
		if err != nil {
			return "", err
		}
		if login == "" {
			login = page.Login
			qualifiers = append(qualifiers, "user:"+login)
		} else if login != page.Login {
			return "", fmt.Errorf("active account changed during repository scope discovery")
		}
		qualifiers = appendMonitorOrganizationQualifiers(qualifiers, page)
		if !page.Organizations.PageInfo.HasNextPage {
			return strings.Join(qualifiers, " "), nil
		}
		cursor = page.Organizations.PageInfo.EndCursor
		if cursor == "" || seen[cursor] {
			return "", fmt.Errorf("organization pagination did not advance")
		}
		seen[cursor] = true
	}
}

func appendMonitorOrganizationQualifiers(qualifiers []string, page *monitorOwnerPage) []string {
	for _, org := range page.Organizations.Nodes {
		if org.Login != "" && !slices.Contains(qualifiers, "org:"+org.Login) {
			qualifiers = append(qualifiers, "org:"+org.Login)
		}
	}
	return qualifiers
}

func fetchMonitorOwnerPage(ctx context.Context, host, cursor string) (*monitorOwnerPage, error) {
	after := ""
	if cursor != "" {
		after = fmt.Sprintf(", after: %q", cursor)
	}
	query := fmt.Sprintf(`{ viewer { login organizations(first: 100%s) { nodes { login } pageInfo { hasNextPage endCursor } } } }`, after)
	stdout, stderr, err := executeMonitorHostQuery(ctx, host, query)
	if err != nil {
		return nil, wrapExecError(fmt.Errorf("discover repository scope: %w", err), stderr.String())
	}
	var response struct {
		Data struct {
			Viewer *monitorOwnerPage `json:"viewer"`
		} `json:"data"`
		Errors []monitorGraphQLError `json:"errors"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("decode repository scope: %w", err)
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("discover repository scope: %s", response.Errors[0].Message)
	}
	if response.Data.Viewer == nil || strings.TrimSpace(response.Data.Viewer.Login) == "" {
		return nil, fmt.Errorf("repository scope response has no active account")
	}
	return response.Data.Viewer, nil
}

const monitorOwnerBatchSize = 16
const monitorQueryConcurrency = 4

type monitorQuerySlotsKey struct{}

// One semaphore covers actual API calls across nested owner batches and pins.
// Acquire only at the API boundary so parent orchestration cannot deadlock.
func monitorQueryContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Value(monitorQuerySlotsKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, monitorQuerySlotsKey{}, make(chan struct{}, monitorQueryConcurrency))
}

func resolveMonitorAllHostQueries(ctx context.Context, request monitorHostQuery, cfg *monitorConfig) ([]monitorHostQuery, error) {
	scope, err := discoverMonitorOwnerScope(ctx, request.Host)
	if err != nil {
		return nil, err
	}
	owners := strings.Fields(scope)
	requests := make([]monitorHostQuery, 0)
	for first := 0; first < len(owners); first += monitorOwnerBatchSize {
		batch := request
		batch.Repositories = nil
		batch.OwnerScope = owners
		qualifiers := strings.Join(owners[first:minInt(first+monitorOwnerBatchSize, len(owners))], " ")
		batch.Query = buildMonitorGraphQLQueryWithScope(cfg, nil, true, qualifiers)
		batch.FallbackQuery = buildMonitorGraphQLQueryWithScope(cfg, nil, false, qualifiers)
		requests = append(requests, batch)
	}
	return requests, nil
}

func fetchMonitorAllHost(ctx context.Context, request monitorHostQuery, cfg *monitorConfig, now time.Time) (*monitorFetchResult, error) {
	requests, err := resolveMonitorAllHostQueries(ctx, request, cfg)
	if err != nil {
		return nil, err
	}
	result, err := executeMonitorQueries(ctx, cfg, requests, now, fetchMonitorHost)
	if result != nil && err != nil {
		setMonitorDiscoveredIdentity(result, request.Host, requests[0].OwnerScope)
	}
	if err == nil {
		qualifyMonitorResultRows(result, request.Host)
	}
	return result, err
}

// All repos and each configured shortcut have separate results and row limits.
// Their queries share cancellation, a total deadline and bounded concurrency.
func executeMonitorAllRepoFetch(ctx context.Context, cfg *monitorConfig, now time.Time) (*monitorFetchResult, error) {
	queries, err := buildMonitorHostQueries(cfg)
	if err != nil {
		return nil, err
	}
	ctx = monitorQueryContext(ctx)
	outcomes := make(chan monitorHostFetchOutcome, len(cfg.Repos)+1)
	go func() {
		result, err := executeMonitorQueries(ctx, cfg, queries, now, fetchMonitorAllHost)
		outcomes <- monitorHostFetchOutcome{Index: -1, Result: result, Err: err}
	}()
	launchMonitorPinnedFetches(ctx, cfg, now, outcomes)
	var global monitorHostFetchOutcome
	pins := make([]monitorHostFetchOutcome, len(cfg.Repos))
	for range len(cfg.Repos) + 1 {
		outcome := <-outcomes
		if outcome.Index < 0 {
			global = outcome
		} else {
			pins[outcome.Index] = outcome
		}
	}
	return combineMonitorScopes(cfg, global, pins)
}

func launchMonitorPinnedFetches(ctx context.Context, cfg *monitorConfig, now time.Time, outcomes chan<- monitorHostFetchOutcome) {
	slots := make(chan struct{}, monitorQueryConcurrency)
	for index, repo := range cfg.Repos {
		go func() {
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				outcomes <- monitorHostFetchOutcome{Index: index, Err: githubContextError(ctx.Err())}
				return
			}
			pinCfg := *cfg
			pinCfg.Repos = []string{repo}
			result, err := executeMonitorFetch(ctx, &pinCfg, now)
			outcomes <- monitorHostFetchOutcome{Index: index, Result: result, Err: err}
		}()
	}
}

func combineMonitorScopes(cfg *monitorConfig, global monitorHostFetchOutcome, pins []monitorHostFetchOutcome) (*monitorFetchResult, error) {
	result := global.Result
	if global.Err != nil {
		result = unavailableMonitorScope(cfg, global.Err)
		mergeMonitorIdentities(result, global.Result)
		result.Warnings = append(result.Warnings, "All repos: "+global.Err.Error())
	}
	result.Pinned = make(map[string]*monitorFetchResult, len(cfg.Repos))
	for index, pin := range pins {
		repo := cfg.Repos[index]
		if pin.Err != nil {
			pinCfg := *cfg
			pinCfg.Repos = []string{repo}
			result.Pinned[repo] = unavailableMonitorScope(&pinCfg, pin.Err)
			mergeMonitorIdentities(result.Pinned[repo], pin.Result)
			result.Warnings = append(result.Warnings, repo+": "+pin.Err.Error())
			continue
		}
		result.Pinned[repo] = pin.Result
		mergeMonitorScopeMetadata(result, pin.Result)
	}
	result.Warnings = uniqueMonitorWarnings(result.Warnings)
	if global.Err != nil && !monitorAnyScopeSucceeded(pins) {
		return result, global.Err
	}
	return result, nil
}

func monitorAnyScopeSucceeded(outcomes []monitorHostFetchOutcome) bool {
	for _, outcome := range outcomes {
		if outcome.Err == nil {
			return true
		}
	}
	return false
}

func unavailableMonitorScope(cfg *monitorConfig, err error) *monitorFetchResult {
	result := newMonitorFetchResult(cfg, time.Time{})
	result.Error = err.Error()
	return result
}

func mergeMonitorScopeMetadata(result, scope *monitorFetchResult) {
	result.Warnings = append(result.Warnings, scope.Warnings...)
	for repo, accessible := range scope.Accessible {
		result.Accessible[repo] = accessible
	}
	if !scope.RateResetAt.IsZero() && (result.RateResetAt.IsZero() || scope.RateRemaining < result.RateRemaining) {
		result.RateRemaining = scope.RateRemaining
		result.RateResetAt = scope.RateResetAt
	}
}

type monitorSectionLayout struct {
	PRSections    []monitorSection
	IssueSections []monitorSection
	DefaultLimit  int
}

func newMonitorSectionLayout(cfg *monitorConfig) monitorSectionLayout {
	return monitorSectionLayout{
		PRSections: slices.Clone(cfg.PRSections), IssueSections: slices.Clone(cfg.IssueSections), DefaultLimit: cfg.Defaults.Limit,
	}
}

func monitorSectionLayoutsEqual(a, b monitorSectionLayout) bool {
	return a.DefaultLimit == b.DefaultLimit && slices.Equal(a.PRSections, b.PRSections) && slices.Equal(a.IssueSections, b.IssueSections)
}

func (m *monitorModel) invalidateMonitorConfigSnapshot() {
	if m.data == nil {
		return
	}
	if !monitorSectionLayoutsEqual(m.data.SectionLayout, newMonitorSectionLayout(m.cfg)) {
		m.data = nil
		m.clearMonitorSnapshotChanges()
		return
	}
	pins := compatibleMonitorPins(m.data.Pinned, m.cfg.Repos)
	if !slices.Equal(m.data.HostScope, monitorConfigHosts(m.cfg)) {
		m.data = unavailableMonitorScope(m.cfg, errors.New("repository host scope changed; refresh pending"))
		m.clearMonitorSnapshotChanges()
	}
	m.data.Pinned = pins
}

func (m *monitorModel) clearMonitorSnapshotChanges() {
	m.lastRefresh = time.Time{}
	m.lastChanges = nil
	m.changedKeys = nil
	m.addedKeys = nil
}

type monitorQueryConfig struct {
	SectionLayout    monitorSectionLayout
	RepositoryConfig []string
	HostScope        []string
}

func newMonitorQueryConfig(cfg *monitorConfig) monitorQueryConfig {
	return monitorQueryConfig{SectionLayout: newMonitorSectionLayout(cfg), RepositoryConfig: slices.Clone(cfg.Repos), HostScope: monitorConfigHosts(cfg)}
}

func monitorQueryConfigMatches(snapshot monitorQueryConfig, cfg *monitorConfig) bool {
	return monitorSectionLayoutsEqual(snapshot.SectionLayout, newMonitorSectionLayout(cfg)) && slices.Equal(snapshot.RepositoryConfig, cfg.Repos) && slices.Equal(snapshot.HostScope, monitorConfigHosts(cfg))
}

func monitorResultMatchesConfig(result *monitorFetchResult, cfg *monitorConfig) bool {
	return monitorQueryConfigMatches(result.monitorQueryConfig, cfg)
}

func monitorFetchedConfigStale(msg monitorFetchedMsg, cfg *monitorConfig) bool {
	if msg.queryConfig != nil {
		return !monitorQueryConfigMatches(*msg.queryConfig, cfg)
	}
	return msg.result != nil && !monitorResultMatchesConfig(msg.result, cfg)
}

func monitorDefaultHost(cfg *monitorConfig) string {
	if cfg.defaultHost != "" {
		return cfg.defaultHost
	}
	return legacyMonitorHost()
}

func monitorConfigHosts(cfg *monitorConfig) []string {
	if len(cfg.Repos) == 0 {
		return []string{monitorDefaultHost(cfg)}
	}
	return monitorRepositoryHosts(cfg.Repos)
}

func monitorRepositoryHosts(repos []string) []string {
	hosts := []string{}
	for _, name := range repos {
		repo, err := parseMonitorRepository(name)
		if err == nil && !slices.Contains(hosts, repo.Host) {
			hosts = append(hosts, repo.Host)
		}
	}
	sort.Strings(hosts)
	return hosts
}

func monitorPinnedScope(pins map[string]*monitorFetchResult, repo string) *monitorFetchResult {
	if pin, ok := pins[repo]; ok {
		return pin
	}
	for name, pin := range pins {
		if strings.EqualFold(name, repo) {
			return pin
		}
	}
	return nil
}

func compatibleMonitorPins(pins map[string]*monitorFetchResult, repos []string) map[string]*monitorFetchResult {
	if pins == nil {
		return nil
	}
	compatible := make(map[string]*monitorFetchResult)
	for _, repo := range repos {
		if pin := monitorPinnedScope(pins, repo); pin != nil {
			compatible[repo] = pin
		}
	}
	return compatible
}

func retainMonitorScopeSnapshot(current, previous *monitorFetchResult) {
	if current == nil || previous == nil || current.Error == "" {
		return
	}
	if !monitorSectionLayoutsEqual(current.SectionLayout, previous.SectionLayout) || monitorIdentitiesChanged(previous, current, false) {
		return
	}
	current.Incomplete = current.Incomplete || previous.Incomplete
	current.PRSections = previous.PRSections
	current.IssueSections = previous.IssueSections
	current.FetchedAt = previous.FetchedAt
	current.HostIdentities = previous.HostIdentities
}

func retainMonitorScopeSnapshots(current, previous *monitorFetchResult) {
	if previous == nil {
		return
	}
	if slices.Equal(current.HostScope, previous.HostScope) && !monitorIdentitiesChanged(previous, current, true) {
		retainMonitorScopeSnapshot(current, previous)
	}
	for repo, pin := range current.Pinned {
		retainMonitorScopeSnapshot(pin, monitorPinnedScope(previous.Pinned, repo))
	}
}

func diffMonitorFetchScopes(previous, current *monitorFetchResult) []monitorChange {
	var changes []monitorChange
	if slices.Equal(previous.HostScope, current.HostScope) && monitorIdentitiesEqual(previous, current, true) {
		changes = diffMonitorScope(previous, current)
	}
	for repo, pin := range current.Pinned {
		changes = append(changes, diffMonitorScope(monitorPinnedScope(previous.Pinned, repo), pin)...)
	}
	return uniqueMonitorScopeChanges(changes)
}

func diffMonitorScope(previous, current *monitorFetchResult) []monitorChange {
	if previous == nil || current == nil || previous.Incomplete || current.Incomplete || !monitorIdentitiesEqual(previous, current, false) {
		return nil
	}
	if previous.Error != "" && previous.FetchedAt.IsZero() {
		return nil
	}
	if !monitorSectionLayoutsEqual(previous.SectionLayout, current.SectionLayout) {
		return nil
	}
	changes := diffMonitorSections(previous.PRSections, current.PRSections)
	return append(changes, diffMonitorSections(previous.IssueSections, current.IssueSections)...)
}

func uniqueMonitorWarnings(warnings []string) []string {
	unique := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		if !slices.Contains(unique, warning) {
			unique = append(unique, warning)
		}
	}
	return unique
}

func monitorConfiguredSearchIncomplete(data map[string]json.RawMessage, cfg *monitorConfig) bool {
	for index := range cfg.PRSections {
		if monitorSearchConnectionUnavailable(data[fmt.Sprintf("pr%d", index)]) {
			return true
		}
	}
	for index := range cfg.IssueSections {
		if monitorSearchConnectionUnavailable(data[fmt.Sprintf("is%d", index)]) {
			return true
		}
	}
	return false
}

func monitorSearchAliasesIncomplete(data map[string]json.RawMessage, errors []monitorGraphQLError) bool {
	for _, entry := range errors {
		if len(entry.Path) == 0 {
			return true
		}
		alias, ok := entry.Path[0].(string)
		if !ok {
			return true
		}
		if !strings.HasPrefix(alias, "pr") && !strings.HasPrefix(alias, "is") {
			continue
		}
		if monitorSearchConnectionUnavailable(data[alias]) {
			return true
		}
		_, _, field, hierarchy := parseMonitorHierarchyErrorPath(entry.Path)
		if !hierarchy || (field != "parent" && field != "subIssuesSummary") {
			return true
		}
	}
	return false
}

func monitorSearchConnectionUnavailable(raw json.RawMessage) bool {
	var connection struct {
		Nodes []json.RawMessage `json:"nodes"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &connection) != nil || connection.Nodes == nil {
		return true
	}
	for _, node := range connection.Nodes {
		if strings.TrimSpace(string(node)) == "null" {
			return true
		}
	}
	return false
}

func uniqueMonitorScopeChanges(changes []monitorChange) []monitorChange {
	seen := make(map[monitorChange]bool)
	unique := make([]monitorChange, 0, len(changes))
	for _, change := range changes {
		if !seen[change] {
			seen[change] = true
			unique = append(unique, change)
		}
	}
	sortMonitorChanges(unique)
	return unique
}
