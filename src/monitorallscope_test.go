package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMonitorAllReposFetchIncludesUnconfiguredOrganizationRepository(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		query := strings.Join(args, " ")
		if strings.Contains(query, "viewer {") {
			return *bytes.NewBufferString(`{"data":{"viewer":{"login":"owner","organizations":{"nodes":[{"login":"team"}],"pageInfo":{"hasNextPage":false}}}}}`), bytes.Buffer{}, nil
		}
		repo := "owner/pinned"
		if strings.Contains(query, "user:owner org:team") {
			repo = "team/unconfigured"
		}
		payload := `{"data":{"pr2":{"issueCount":1,"nodes":[{"number":7,"title":"outside configured repos","state":"OPEN","repository":{"nameWithOwner":"` + repo + `"}}]}}}`
		return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
	}
	cfg := defaultMonitorConfig("owner/pinned")
	model := newMonitorModel(cfg, "", "", monitorSessionState{SubTab: 2})
	defer model.cancelRefresh()
	msg := model.initialMonitorCmd()().(monitorFetchedMsg)
	if msg.err != nil {
		t.Fatalf("refresh: %v", msg.err)
	}
	model.applyFetchResult(msg.result)
	rows := model.visibleRows()
	if len(rows) != 1 || rows[0].Repo != "team/unconfigured" {
		t.Fatalf("All repos excluded the unconfigured organization repository: %+v", rows)
	}
}

func TestDiscoverMonitorOwnerScopePaginationAndFailures(t *testing.T) {
	tests := []struct {
		name      string
		responses []string
		execError bool
		want      string
		wantError string
	}{
		{name: "no organizations", responses: []string{`{"data":{"viewer":{"login":"owner","organizations":{"nodes":[],"pageInfo":{"hasNextPage":false}}}}}`}, want: "user:owner"},
		{name: "all organization pages", responses: []string{
			`{"data":{"viewer":{"login":"owner","organizations":{"nodes":[{"login":"team"}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}`,
			`{"data":{"viewer":{"login":"owner","organizations":{"nodes":[{"login":"other"}],"pageInfo":{"hasNextPage":false}}}}}`}, want: "user:owner org:team org:other"},
		{name: "empty organization login", responses: []string{`{"data":{"viewer":{"login":"owner","organizations":{"nodes":[{"login":""}],"pageInfo":{}}}}}`}, want: "user:owner"},
		{name: "partial scope failure", responses: []string{`{"data":{"viewer":{"login":"owner"}},"errors":[{"message":"organization access denied"}]}`}, wantError: "organization access denied"},
		{name: "missing account", responses: []string{`{"data":{"viewer":null}}`}, wantError: "no active account"},
		{name: "blank login", responses: []string{`{"data":{"viewer":{"login":" "}}}`}, wantError: "no active account"},
		{name: "invalid JSON", responses: []string{`broken`}, wantError: "decode repository scope"},
		{name: "missing cursor", responses: []string{`{"data":{"viewer":{"login":"owner","organizations":{"pageInfo":{"hasNextPage":true}}}}}`}, wantError: "pagination did not advance"},
		{name: "repeated cursor", responses: []string{`{"data":{"viewer":{"login":"owner","organizations":{"pageInfo":{"hasNextPage":true,"endCursor":"same"}}}}}`}, wantError: "pagination did not advance"},
		{name: "account changed", responses: []string{
			`{"data":{"viewer":{"login":"owner","organizations":{"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}`,
			`{"data":{"viewer":{"login":"different","organizations":{"pageInfo":{}}}}}`}, wantError: "active account changed"},
		{name: "CLI failure", execError: true, wantError: "discover repository scope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			saved := monitorGHExecFunc
			t.Cleanup(func() { monitorGHExecFunc = saved })
			calls := 0
			monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
				if tc.execError {
					return bytes.Buffer{}, bytes.Buffer{}, errBoom()
				}
				if len(args) < 3 || args[2] != "ghe.example.com" {
					t.Fatalf("scope discovery routed to wrong host: %v", args)
				}
				index := minInt(calls, len(tc.responses)-1)
				calls++
				if tc.name == "all organization pages" && calls == 2 && !strings.Contains(strings.Join(args, " "), `after: "next"`) {
					t.Fatalf("next page cursor missing: %v", args)
				}
				return *bytes.NewBufferString(tc.responses[index]), bytes.Buffer{}, nil
			}
			got, err := discoverMonitorOwnerScope(context.Background(), "ghe.example.com")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("scope %q, error %v; want %q", got, err, tc.wantError)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("scope %q, error %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestMonitorPinnedResultsRemainAvailableOutsideGlobalRowLimit(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	all := newMonitorFetchResult(cfg, time.Time{})
	all.PRSections[2] = monitorSectionData{Total: 80, Rows: []monitorRow{{Number: 7, Repo: "team/unconfigured", Title: "global", Kind: monitorKindPR}}}
	all.Pinned = map[string]*monitorFetchResult{"owner/pinned": newMonitorFetchResult(cfg, time.Time{})}
	all.Pinned["owner/pinned"].PRSections[2] = monitorSectionData{Total: 1, Rows: []monitorRow{{Number: 2, Repo: "owner/pinned", Title: "pinned", Kind: monitorKindPR}}}
	for _, tc := range []struct {
		name  string
		index int
		want  string
		total int
	}{{"all repositories", 0, "team/unconfigured", 80}, {"pinned repository", 1, "owner/pinned", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			rows := computeVisibleRows(all, monitorTabPRs, 2, tc.index, cfg.Repos, "")
			if len(rows) != 1 || rows[0].Repo != tc.want {
				t.Fatalf("scope rows: %+v", rows)
			}
			model := newMonitorModel(cfg, "", "", monitorSessionState{SubTab: 2, RepoIndex: tc.index})
			defer model.cancelRefresh()
			model.data = all
			if total := model.tabTotal(monitorTabPRs); total != tc.total {
				t.Fatalf("scope total %d, want %d", total, tc.total)
			}
		})
	}
	counts := countMonitorRowsByRepo(all, cfg.Repos)
	if counts["owner/pinned"].PRs != 1 {
		t.Fatalf("pinned sidebar count lost: %+v", counts)
	}
}

func TestMonitorAllScopeDiscoveryFailureKeepsPinsAndReportsGlobalUnavailable(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		if strings.Contains(strings.Join(args, " "), "viewer {") {
			return bytes.Buffer{}, bytes.Buffer{}, errBoom()
		}
		return *bytes.NewBufferString(`{"data":{"pr2":{"issueCount":1,"nodes":[{"number":1,"repository":{"nameWithOwner":"owner/pinned"}}]}}}`), bytes.Buffer{}, nil
	}
	result, err := executeMonitorAllRepoFetch(context.Background(), defaultMonitorConfig("owner/pinned"), time.Time{})
	if err != nil || result == nil || !strings.Contains(result.Error, "discover repository scope") || len(result.PRSections[2].Rows) != 0 || len(result.Pinned["owner/pinned"].PRSections[2].Rows) != 1 {
		t.Fatalf("global discovery failure must preserve pins without pretending they are global: %+v, %v", result, err)
	}
}

func TestMonitorAllReposWorksWithoutConfiguredShortcuts(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	t.Setenv("GH_HOST", "ghe.example.com")
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		query := strings.Join(args, " ")
		if !strings.Contains(query, "--hostname ghe.example.com") {
			t.Fatalf("empty config host: %v", args)
		}
		if strings.Contains(query, "viewer {") {
			return *bytes.NewBufferString(`{"data":{"viewer":{"login":"owner","organizations":{"nodes":[],"pageInfo":{}}}}}`), bytes.Buffer{}, nil
		}
		if !strings.Contains(query, "user:owner") {
			t.Fatalf("query missing discovered account: %v", args)
		}
		return *bytes.NewBufferString(`{"data":{"pr2":{"issueCount":1,"nodes":[{"number":1,"repository":{"nameWithOwner":"owner/new-repo"}}]}}}`), bytes.Buffer{}, nil
	}
	cfg := defaultMonitorConfig("")
	result, err := executeMonitorAllRepoFetch(context.Background(), cfg, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	rows := computeVisibleRows(result, monitorTabPRs, 2, 0, nil, "")
	if len(rows) != 1 || rows[0].Repo != "ghe.example.com/owner/new-repo" {
		t.Fatalf("global rows without shortcuts: %+v", rows)
	}
}

func TestMonitorAllRepoFetchKeepsHostScopesSeparateAndReportsPartialFailures(t *testing.T) {
	for _, mode := range []string{"both hosts", "enterprise scope unavailable", "pinned queries unavailable"} {
		t.Run(mode, func(t *testing.T) {
			saved := monitorGHExecFunc
			t.Cleanup(func() { monitorGHExecFunc = saved })
			monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
				host := args[2]
				query := strings.Join(args, " ")
				login := "owner"
				if host != "github.com" {
					login = "enterprise"
				}
				if strings.Contains(query, "viewer {") {
					if mode == "enterprise scope unavailable" && host != "github.com" {
						return bytes.Buffer{}, bytes.Buffer{}, errBoom()
					}
					return *bytes.NewBufferString(`{"data":{"viewer":{"login":"` + login + `","organizations":{"nodes":[{"login":"team"}],"pageInfo":{}}}}}`), bytes.Buffer{}, nil
				}
				global := strings.Contains(query, "user:"+login+" org:team")
				if !global && mode == "pinned queries unavailable" {
					return bytes.Buffer{}, bytes.Buffer{}, errBoom()
				}
				repo := login + "/pinned"
				if global {
					repo = "team/unconfigured"
				}
				payload := `{"data":{"rateLimit":{"remaining":42,"resetAt":"2026-10-07T20:00:00Z"},"pr2":{"issueCount":1,"nodes":[{"number":7,"state":"OPEN","repository":{"nameWithOwner":"` + repo + `"}}]}}}`
				return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
			}
			cfg := defaultMonitorConfig("owner/pinned")
			cfg.Repos = append(cfg.Repos, "ghe.example.com/enterprise/pinned")
			result, err := executeMonitorAllRepoFetch(context.Background(), cfg, time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if mode == "enterprise scope unavailable" {
				want = 1
			}
			if len(result.PRSections[2].Rows) != want || result.PRSections[2].Total != want {
				t.Fatalf("global host results: %+v", result.PRSections[2])
			}
			if result.PRSections[2].Rows[0].Repo != "team/unconfigured" {
				t.Fatalf("public scope mixed identities: %+v", result.PRSections[2].Rows)
			}
			if want == 2 && result.PRSections[2].Rows[1].Repo != "ghe.example.com/team/unconfigured" {
				t.Fatalf("enterprise scope lost host: %+v", result.PRSections[2].Rows)
			}
			if mode != "both hosts" && len(result.Warnings) == 0 {
				t.Fatal("partial scope failure is silent")
			}
			if mode == "pinned queries unavailable" && result.Pinned["owner/pinned"].Error == "" {
				t.Fatal("failed pins were presented as successful")
			}
			if mode == "both hosts" && (result.Pinned == nil || len(result.Pinned["owner/pinned"].PRSections[2].Rows) != 1 || len(result.Pinned["ghe.example.com/enterprise/pinned"].PRSections[2].Rows) != 1) {
				t.Fatalf("pinned scopes lost: %+v", result.Pinned)
			}
		})
	}
}

func TestApplyMonitorPinnedResultRetainsMostConservativeRateLimit(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name        string
		global      int
		globalReset time.Time
		pin         int
		pinReset    time.Time
		want        int
	}{
		{"lower pin rate", 90, now, 42, now, 42},
		{"lower global rate", 42, now, 90, now, 42},
		{"missing global rate", 0, time.Time{}, 42, now, 42},
		{"missing pin rate", 42, now, 0, time.Time{}, 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &monitorFetchResult{RateRemaining: tc.global, RateResetAt: tc.globalReset}
			pinned := &monitorFetchResult{RateRemaining: tc.pin, RateResetAt: tc.pinReset}
			result.Pinned = map[string]*monitorFetchResult{"owner/pinned": pinned}
			mergeMonitorScopeMetadata(result, pinned)
			if result.RateRemaining != tc.want || result.Pinned["owner/pinned"] != pinned {
				t.Fatalf("merged rate/pins: %+v", result)
			}
		})
	}
}

func TestMonitorQuietShortcutIsNotCrowdedOutByBusyRepository(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		query := strings.Join(args, " ")
		if strings.Contains(query, "viewer {") {
			return *bytes.NewBufferString(`{"data":{"viewer":{"login":"owner","organizations":{"nodes":[],"pageInfo":{}}}}}`), bytes.Buffer{}, nil
		}
		repo := "team/unconfigured"
		total := 50
		if strings.Contains(query, "repo:owner/busy") {
			repo = "owner/busy"
			total = 10
		}
		if strings.Contains(query, "repo:owner/quiet") {
			repo = "owner/quiet"
			total = 1
		}
		if strings.Contains(query, "repo:owner/busy repo:owner/quiet") {
			return bytes.Buffer{}, bytes.Buffer{}, fmt.Errorf("shortcuts still share one cap")
		}
		payload := fmt.Sprintf(`{"data":{"pr2":{"issueCount":%d,"nodes":[{"number":7,"repository":{"nameWithOwner":%q}}]}}}`, total, repo)
		return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
	}
	cfg := defaultMonitorConfig("owner/busy")
	cfg.Repos = append(cfg.Repos, "owner/quiet")
	cfg.Defaults.Limit = 1
	result, err := executeMonitorAllRepoFetch(context.Background(), cfg, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	rows := computeVisibleRows(result, monitorTabPRs, 2, 2, cfg.Repos, "")
	if len(rows) != 1 || rows[0].Repo != "owner/quiet" {
		t.Fatalf("quiet repository disappeared: %+v", rows)
	}
	if result.Pinned["owner/quiet"].PRSections[2].Total != 1 || result.Pinned["owner/busy"].PRSections[2].Total != 10 {
		t.Fatalf("per-repo totals mixed: %+v", result.Pinned)
	}
}

func TestMonitorFailedScopesRetainIndependentSnapshotsAndErrors(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	model := newMonitorModel(cfg, "", "", monitorSessionState{SubTab: 2})
	defer model.cancelRefresh()
	before := newMonitorFetchResult(cfg, time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC))
	before.PRSections[2] = monitorSectionData{Total: 1, Rows: []monitorRow{{Repo: "team/outside", Number: 1, Title: "before", Kind: monitorKindPR}}}
	pin := newMonitorFetchResult(cfg, before.FetchedAt)
	pin.PRSections[2] = monitorSectionData{Total: 1, Rows: []monitorRow{{Repo: "owner/pinned", Number: 2, Title: "pin before", Kind: monitorKindPR}}}
	before.Pinned = map[string]*monitorFetchResult{"owner/pinned": pin}
	model.applyFetchResult(before)
	after := unavailableMonitorScope(cfg, errBoom())
	after.Pinned = map[string]*monitorFetchResult{"owner/pinned": unavailableMonitorScope(cfg, errBoom())}
	model.applyFetchResult(after)
	if len(model.visibleRows()) != 1 || model.visibleRows()[0].Repo != "team/outside" || !model.lastRefresh.Equal(before.FetchedAt) {
		t.Fatal("global snapshot was discarded")
	}
	model.repoIdx = 1
	if len(model.visibleRows()) != 1 || model.visibleRows()[0].Repo != "owner/pinned" {
		t.Fatal("pin snapshot was discarded")
	}
	model.layout = computeMonitorLayout(120, 40)
	if !strings.Contains(model.footerLine(), "unavailable:") {
		t.Fatal("failed scope is presented as fresh data")
	}
	first := unavailableMonitorScope(cfg, errBoom())
	first.Pinned = map[string]*monitorFetchResult{"owner/pinned": unavailableMonitorScope(cfg, errBoom())}
	model.data = first
	if len(model.visibleRows()) != 0 || !strings.Contains(model.emptyListMessage(), "unavailable") || model.tabTotal(monitorTabPRs) != -1 {
		t.Fatal("failed scope is presented as an empty successful search")
	}
}

func TestMonitorRetainedEmptySectionSuggestsCachedRowsAndKeepsFailureNotice(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	m := newMonitorModel(cfg, "", "", monitorSessionState{})
	defer m.cancelRefresh()
	previous := newMonitorFetchResult(cfg, time.Now())
	previous.PRSections[2] = monitorSectionData{Total: 1, Rows: []monitorRow{{Repo: "owner/pinned", Number: 1, Kind: monitorKindPR}}}
	m.applyFetchResult(previous)
	m.applyFetchResult(unavailableMonitorScope(cfg, errBoom()))
	if message := m.emptyListMessage(); !strings.Contains(message, "All open has 1") || !strings.Contains(message, "press 3") {
		t.Fatalf("cached rows in other sections are not discoverable: %q", message)
	}
	m.layout = computeMonitorLayout(120, 40)
	if !strings.Contains(m.footerLine(), "unavailable:") {
		t.Fatal("retained-row suggestions hid the active refresh failure")
	}
}

func TestMonitorSnapshotsDoNotCrossChangedSectionLayouts(t *testing.T) {
	changes := map[string]func(*monitorConfig){
		"filter":         func(cfg *monitorConfig) { cfg.PRSections[0].Filters = "is:open label:bug" },
		"order":          func(cfg *monitorConfig) { cfg.PRSections[0], cfg.PRSections[1] = cfg.PRSections[1], cfg.PRSections[0] },
		"title":          func(cfg *monitorConfig) { cfg.PRSections[0].Title = "Different" },
		"section limit":  func(cfg *monitorConfig) { cfg.PRSections[0].Limit = 1 },
		"default limit":  func(cfg *monitorConfig) { cfg.Defaults.Limit = 1 },
		"issue filter":   func(cfg *monitorConfig) { cfg.IssueSections[0].Filters = "is:open label:bug" },
		"remove section": func(cfg *monitorConfig) { cfg.PRSections = cfg.PRSections[:1] },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			cfg := defaultMonitorConfig("owner/pinned")
			previous := newMonitorFetchResult(cfg, time.Now())
			previous.PRSections[0].Rows = []monitorRow{{Repo: "owner/pinned", Number: 1, Kind: monitorKindPR}}
			change(cfg)
			current := unavailableMonitorScope(cfg, errBoom())
			retainMonitorScopeSnapshot(current, previous)
			if !current.FetchedAt.IsZero() || len(current.PRSections[0].Rows) != 0 {
				t.Fatal("incompatible section rows were retained")
			}
			if changes := diffMonitorScope(previous, newMonitorFetchResult(cfg, time.Now())); len(changes) != 0 {
				t.Fatal("configuration change invented a removal event")
			}
			m := newMonitorModel(cfg, "", "", monitorSessionState{})
			defer m.cancelRefresh()
			m.data = previous
			m.invalidateMonitorConfigSnapshot()
			if m.data != nil {
				t.Fatal("old layout remained visible after configuration changed")
			}
		})
	}
}

func TestMonitorInFlightOldLayoutTriggersFreshFetch(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	m := newMonitorModel(cfg, "", "", monitorSessionState{})
	defer m.cancelRefresh()
	old := newMonitorFetchResult(cfg, time.Now())
	cfg.PRSections[0].Filters = "is:open label:bug"
	model, cmd := m.handleFetched(monitorFetchedMsg{result: old})
	updated := model.(monitorModel)
	if cmd == nil || !updated.refreshing || updated.data != nil {
		t.Fatal("in-flight result for an old layout was accepted instead of scheduling the current layout")
	}
}

func TestMonitorInFlightRepositoryEditsTriggerFreshFetch(t *testing.T) {
	changes := map[string]func(*monitorConfig){
		"add":    func(cfg *monitorConfig) { cfg.Repos = append(cfg.Repos, "team/new") },
		"remove": func(cfg *monitorConfig) { cfg.Repos = nil },
		"host":   func(cfg *monitorConfig) { cfg.Repos[0] = "ghe.example.com/owner/pinned" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			cfg := defaultMonitorConfig("owner/pinned")
			m := newMonitorModel(cfg, "", "", monitorSessionState{})
			defer m.cancelRefresh()
			old := newMonitorFetchResult(cfg, time.Now())
			m.data = old
			m.refreshing = true
			change(cfg)
			model, cmd := m.handleFetched(monitorFetchedMsg{result: old})
			updated := model.(monitorModel)
			if cmd == nil || !updated.refreshing {
				t.Fatal("repository/host edit accepted an old result instead of immediately fetching the new configuration")
			}
			if name == "host" {
				if updated.data == old || !updated.data.FetchedAt.IsZero() || updated.data.Error == "" {
					t.Fatal("changed host retained an incompatible global snapshot")
				}
			} else if updated.data != old {
				t.Fatal("same-host shortcut edit discarded the valid global snapshot")
			}
			if old.RepositoryConfig[0] != "owner/pinned" {
				t.Fatal("query generation retained a mutable configuration slice")
			}
		})
	}
}

func TestMonitorAllOwnerSearchBatchesLargeOrganizationMembership(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		nodes := make([]map[string]string, 0)
		for i := 0; i < 32; i++ {
			nodes = append(nodes, map[string]string{"login": fmt.Sprintf("team%d", i)})
		}
		nodes = append(nodes, map[string]string{"login": "team0"})
		payload, _ := json.Marshal(map[string]any{"data": map[string]any{"viewer": map[string]any{"login": "owner", "organizations": map[string]any{"nodes": nodes, "pageInfo": map[string]bool{"hasNextPage": false}}}}})
		return *bytes.NewBuffer(payload), bytes.Buffer{}, nil
	}
	cfg := defaultMonitorConfig("owner/pinned")
	requests, err := resolveMonitorAllHostQueries(context.Background(), monitorHostQuery{Host: "github.com"}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 {
		t.Fatalf("expected three bounded owner batches, got %d", len(requests))
	}
	scopes := map[string]bool{}
	for _, request := range requests {
		for _, line := range strings.Split(request.Query, "\n") {
			if !strings.Contains(line, "pr2: search") {
				continue
			}
			owners := regexp.MustCompile(`(?:user|org):[A-Za-z0-9-]+`).FindAllString(line, -1)
			if len(owners) > 16 {
				t.Fatalf("owner batch exceeds GitHub limit: %s", line)
			}
			for _, owner := range owners {
				if scopes[owner] {
					t.Fatalf("duplicate owner can inflate totals: %s", owner)
				}
				scopes[owner] = true
			}
		}
	}
	if len(scopes) != 33 {
		t.Fatalf("lost account/organization scopes: %v", scopes)
	}
}

func TestMonitorNewPinIsLoadingUntilItsFirstFetch(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	model := newMonitorModel(cfg, "", "", monitorSessionState{SubTab: 2, RepoIndex: 1})
	defer model.cancelRefresh()
	model.layout = computeMonitorLayout(120, 40)
	model.data = newMonitorFetchResult(cfg, time.Now())
	model.data.Pinned = map[string]*monitorFetchResult{}
	if !strings.Contains(model.listLines(), "Loading GitHub data") || model.tabTotal(monitorTabPRs) != -1 {
		t.Fatal("new shortcut looked successfully empty before its fetch")
	}
}

func TestMonitorShortcutNamesAreCaseInsensitive(t *testing.T) {
	cfg := defaultMonitorConfig("hemsoft/gh-x")
	model := newMonitorModel(cfg, "", "", monitorSessionState{SubTab: 2, RepoIndex: 1})
	defer model.cancelRefresh()
	result := newMonitorFetchResult(cfg, time.Now())
	pin := newMonitorFetchResult(cfg, time.Now())
	pin.PRSections[2] = monitorSectionData{Total: 1, Rows: []monitorRow{{Repo: "HemSoft/gh-x", Kind: monitorKindPR, Number: 1}}}
	result.Pinned = map[string]*monitorFetchResult{"hemsoft/gh-x": pin}
	model.data = result
	if len(model.visibleRows()) != 1 || !model.keyInScope("HemSoft/gh-x#pr#1") || countMonitorRowsByRepo(result, cfg.Repos)["hemsoft/gh-x"].PRs != 1 {
		t.Fatal("canonical API case hid a configured shortcut row")
	}
}

func TestMonitorScopeRecoveryDoesNotInventChanges(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	complete := newMonitorFetchResult(cfg, time.Now())
	complete.PRSections[2] = monitorSectionData{Total: 1, Rows: []monitorRow{{Repo: "owner/pinned", Kind: monitorKindPR, Number: 1}}}
	unavailable := unavailableMonitorScope(cfg, errBoom())
	if changes := diffMonitorScope(unavailable, complete); len(changes) != 0 {
		t.Fatalf("initial recovery invented additions: %+v", changes)
	}
	complete.Incomplete = true
	if changes := diffMonitorScope(complete, newMonitorFetchResult(cfg, time.Now())); len(changes) != 0 {
		t.Fatalf("partial snapshot recovery invented removals: %+v", changes)
	}
	complete.Incomplete = false
	if changes := diffMonitorScope(complete, newMonitorFetchResult(cfg, time.Now())); len(changes) != 1 {
		t.Fatalf("complete successful snapshots lost real removals: %+v", changes)
	}
	previous := newMonitorFetchResult(cfg, time.Now())
	previous.Pinned = map[string]*monitorFetchResult{"owner/pinned": unavailable}
	current := newMonitorFetchResult(cfg, time.Now())
	current.Pinned = map[string]*monitorFetchResult{"owner/pinned": complete}
	if changes := diffMonitorFetchScopes(previous, current); len(changes) != 0 {
		t.Fatalf("initial pin recovery invented additions: %+v", changes)
	}
}

func TestMonitorSearchFailuresAreIncompleteWithoutSuppressingHierarchyChanges(t *testing.T) {
	tests := []struct {
		name string
		path []any
		data map[string]json.RawMessage
		want bool
	}{
		{"search alias missing", []any{"pr0"}, nil, true},
		{"search alias null", []any{"is0"}, map[string]json.RawMessage{"is0": json.RawMessage("null")}, true},
		{"search nodes null", []any{"pr0", "nodes"}, map[string]json.RawMessage{"pr0": json.RawMessage(`{"issueCount":3,"nodes":null}`)}, true},
		{"search nodes missing", []any{"is0", "nodes"}, map[string]json.RawMessage{"is0": json.RawMessage(`{"issueCount":3}`)}, true},
		{"search connection malformed", []any{"pr0", "nodes"}, map[string]json.RawMessage{"pr0": json.RawMessage(`{`)}, true},
		{"hierarchy cell unavailable", []any{"is0", "nodes", float64(0), "parent"}, map[string]json.RawMessage{"is0": json.RawMessage(`{"nodes":[]}`)}, false},
		{"access probe", []any{"acc0"}, nil, false},
		{"no path", nil, nil, false},
		{"nonstring path", []any{1}, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := monitorSearchAliasesIncomplete(tc.data, []monitorGraphQLError{{Path: tc.path}}); got != tc.want {
				t.Fatalf("incomplete=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestMonitorAllScopesShareFourAPICalls(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	var mu sync.Mutex
	active, maximum := 0, 0
	started := make(chan struct{}, 100)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	monitorGHExecFunc = func(ctx context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		mu.Lock()
		active++
		maximum = maxInt(maximum, active)
		mu.Unlock()
		defer func() { mu.Lock(); active--; mu.Unlock() }()
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return bytes.Buffer{}, bytes.Buffer{}, ctx.Err()
		}
		if strings.Contains(strings.Join(args, " "), "viewer {") {
			orgs := []string{}
			for i := range 33 {
				orgs = append(orgs, fmt.Sprintf(`{"login":"team%d"}`, i))
			}
			payload := `{"data":{"viewer":{"login":"owner","organizations":{"nodes":[` + strings.Join(orgs, ",") + `],"pageInfo":{}}}}}`
			return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
		}
		return *bytes.NewBufferString(`{"data":{"pr0":{"issueCount":0,"nodes":[]}}}`), bytes.Buffer{}, nil
	}
	cfg := defaultMonitorConfig("")
	for i := range 8 {
		cfg.Repos = append(cfg.Repos, fmt.Sprintf("owner/pin%d", i))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := executeMonitorAllRepoFetch(ctx, cfg, time.Now()); done <- err }()
	for range monitorQueryConcurrency {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("refresh did not fill four available API slots")
		}
	}
	select {
	case <-started:
		t.Error("fifth API call started while all four refresh slots were occupied")
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if maximum > monitorQueryConcurrency {
		t.Fatalf("nested discovery, owner batches and pins reached %d simultaneous calls", maximum)
	}
}

func TestMonitorQueuedAPICallHonorsCancellation(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	invoked := false
	monitorGHExecFunc = func(context.Context, ...string) (bytes.Buffer, bytes.Buffer, error) {
		invoked = true
		return bytes.Buffer{}, bytes.Buffer{}, errors.New("queued API call was dispatched")
	}
	ctx, cancel := context.WithCancel(monitorQueryContext(context.Background()))
	slots := ctx.Value(monitorQuerySlotsKey{}).(chan struct{})
	for range monitorQueryConcurrency {
		slots <- struct{}{}
	}
	cancel()
	_, _, err := executeMonitorHostQuery(ctx, defaultGitHubHost, "must not run")
	if invoked || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled queued call must preserve cancellation without dispatch: invoked=%v, err=%v", invoked, err)
	}
}

func TestMonitorFailureWithoutSnapshotReportsUnavailableInsteadOfLoading(t *testing.T) {
	for _, edited := range []bool{false, true} {
		t.Run(fmt.Sprint(edited), func(t *testing.T) {
			cfg := defaultMonitorConfig("owner/pinned")
			m := newMonitorModel(cfg, "", "", monitorSessionState{})
			defer m.cancelRefresh()
			if edited {
				m.applyFetchResult(newMonitorFetchResult(cfg, time.Now()))
				cfg.Defaults.Limit = 1
			}
			model, _ := m.handleFetched(monitorFetchedMsg{err: errBoom()})
			updated := model.(monitorModel)
			updated.layout = computeMonitorLayout(120, 40)
			if updated.data != nil || !strings.Contains(updated.listLines(), "data unavailable") || strings.Contains(updated.listLines(), "Loading") {
				t.Fatal("failed fetch without a compatible snapshot still looks like loading")
			}
			if !strings.Contains(updated.footerLine(), "data unavailable") || strings.Contains(updated.footerLine(), "data retained") || updated.tabTotal(monitorTabPRs) != -1 {
				t.Fatal("unavailable data was presented as retained or successfully empty")
			}
			updated.refreshing = true
			if !strings.Contains(updated.listLines(), "Loading") {
				t.Fatal("active retry did not show loading progress")
			}
		})
	}
}

func TestMonitorScopeWarningsAndChangesAreDeduplicated(t *testing.T) {
	warning := "ghe.example.com: scope unavailable"
	if got := uniqueMonitorWarnings([]string{warning, warning}); len(got) != 1 {
		t.Fatalf("duplicate warnings: %v", got)
	}
	change := monitorChange{Key: "owner/repo#pr#1", Kind: monitorChangeAdded}
	if got := uniqueMonitorScopeChanges([]monitorChange{change, change}); len(got) != 1 {
		t.Fatalf("duplicate scope changes: %v", got)
	}
	dst := &monitorFetchResult{Accessible: map[string]bool{}}
	src := &monitorFetchResult{Warnings: []string{warning}, Incomplete: true}
	mergeMonitorFetchResult(dst, src, true, "ghe.example.com")
	if len(dst.Warnings) != 1 || dst.Warnings[0] != warning || !dst.Incomplete {
		t.Fatalf("nested scope warning/partial marker: %+v", dst)
	}
}

func TestMonitorStaleFailureImmediatelyFetchesCurrentSettings(t *testing.T) {
	changes := map[string]func(*monitorConfig){
		"section": func(cfg *monitorConfig) { cfg.PRSections[0].Filters = "is:open label:bug" },
		"pin":     func(cfg *monitorConfig) { cfg.Repos = append(cfg.Repos, "team/new") },
		"host":    func(cfg *monitorConfig) { cfg.Repos[0] = "ghe.example.com/owner/pinned" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			cfg := defaultMonitorConfig("owner/pinned")
			m := newMonitorModel(cfg, "", "", monitorSessionState{})
			defer m.cancelRefresh()
			captured := newMonitorQueryConfig(cfg)
			m.refreshing = true
			m.backoff = 20 * time.Second
			change(cfg)
			model, cmd := m.handleFetched(monitorFetchedMsg{err: errBoom(), queryConfig: &captured})
			updated := model.(monitorModel)
			if cmd == nil || !updated.refreshing || updated.refreshErr != "" || updated.backoff != 20*time.Second {
				t.Fatal("stale failure entered backoff instead of fetching the current settings")
			}
		})
	}
}

func TestMonitorFetchCommandCapturesConfigBeforeExecution(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	cfg := defaultMonitorConfig("owner/pinned")
	cmd := newMonitorFetchCmd(context.Background(), *cfg, newMonitorRefreshState())
	cfg.Repos[0] = "other/changed"
	cfg.PRSections[0].Filters = "label:changed"
	cfg.IssueSections[0].Filters = "label:changed"
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		query := strings.Join(args, " ")
		if strings.Contains(query, "other/changed") || strings.Contains(query, "label:changed") {
			t.Error("command used configuration mutated after its creation")
		}
		return bytes.Buffer{}, bytes.Buffer{}, errBoom()
	}
	msg := cmd().(monitorFetchedMsg)
	if msg.err == nil || msg.queryConfig == nil || msg.queryConfig.RepositoryConfig[0] != "owner/pinned" || !monitorFetchedConfigStale(msg, cfg) {
		t.Fatal("failed command lost the original request configuration")
	}
}

func TestMonitorFetchCommandCapturesDefaultHost(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	t.Setenv("GH_HOST", "github.com")
	cfg := defaultMonitorConfig("")
	cmd := newMonitorFetchCmd(context.Background(), *cfg, newMonitorRefreshState())
	t.Setenv("GH_HOST", "ghe.example.com")
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		if args[2] != "github.com" {
			t.Errorf("command dispatched to a host changed after its creation: %v", args)
		}
		return bytes.Buffer{}, bytes.Buffer{}, errBoom()
	}
	msg := cmd().(monitorFetchedMsg)
	if msg.err == nil || msg.queryConfig == nil || msg.queryConfig.HostScope[0] != "github.com" || !monitorFetchedConfigStale(msg, cfg) {
		t.Fatal("failed command lost its resolved default host")
	}
}

func TestMonitorShortcutEditsPreserveCompatibleSnapshots(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	cfg.Repos = append(cfg.Repos, "owner/removed")
	m := newMonitorModel(cfg, "", "", monitorSessionState{})
	defer m.cancelRefresh()
	old := newMonitorFetchResult(cfg, time.Now())
	pin := newMonitorFetchResult(cfg, time.Now())
	old.Pinned = map[string]*monitorFetchResult{"owner/pinned": pin, "owner/removed": pin}
	m.data = old
	cfg.Repos = []string{"OWNER/PINNED", "owner/new"}
	cfg.Defaults.Interval = "30s"
	m.invalidateMonitorConfigSnapshot()
	if m.data != old || len(m.data.Pinned) != 1 || m.data.Pinned["OWNER/PINNED"] != pin {
		t.Fatal("shortcut edit discarded compatible global or pin snapshots")
	}
	failed := unavailableMonitorScope(cfg, errBoom())
	failed.Pinned = map[string]*monitorFetchResult{"OWNER/PINNED": unavailableMonitorScope(cfg, errBoom())}
	retainMonitorScopeSnapshots(failed, old)
	if failed.FetchedAt.IsZero() || failed.Pinned["OWNER/PINNED"].FetchedAt.IsZero() {
		t.Fatal("same-host failure discarded retained global or case-insensitive pin snapshots")
	}
	cfg.Repos = append(cfg.Repos, "ghe.example.com/team/added")
	changedHost := unavailableMonitorScope(cfg, errBoom())
	changedHost.Pinned = map[string]*monitorFetchResult{"OWNER/PINNED": unavailableMonitorScope(cfg, errBoom())}
	retainMonitorScopeSnapshots(changedHost, old)
	if !changedHost.FetchedAt.IsZero() || changedHost.Pinned["OWNER/PINNED"].FetchedAt.IsZero() {
		t.Fatal("host change must discard global data and retain unchanged pins independently")
	}
	m.invalidateMonitorConfigSnapshot()
	if !m.data.FetchedAt.IsZero() || m.data.Pinned["OWNER/PINNED"] != pin {
		t.Fatal("host change invalidated an independent compatible pin")
	}
}

func TestMonitorAlreadyCanceledAPICallDoesNotDispatch(t *testing.T) {
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	monitorGHExecFunc = func(context.Context, ...string) (bytes.Buffer, bytes.Buffer, error) {
		t.Error("already-canceled API call dispatched despite a free slot")
		return bytes.Buffer{}, bytes.Buffer{}, errBoom()
	}
	ctx, cancel := context.WithCancel(monitorQueryContext(context.Background()))
	cancel()
	for range 20 {
		_, _, err := executeMonitorHostQuery(ctx, defaultGitHubHost, "must not run")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lost cancellation: %v", err)
		}
	}
}

func TestMonitorDuplicateCaseVariantPinsSelectTheirExactSnapshot(t *testing.T) {
	lower := &monitorFetchResult{Error: "lowercase pin failed"}
	upper := &monitorFetchResult{Error: "uppercase pin failed"}
	pins := map[string]*monitorFetchResult{"owner/repo": lower, "OWNER/REPO": upper}
	for range 20 {
		if monitorPinnedScope(pins, "owner/repo") != lower || monitorPinnedScope(pins, "OWNER/REPO") != upper {
			t.Fatal("case-variant duplicate selected another shortcut's independent result")
		}
	}
	if monitorPinnedScope(map[string]*monitorFetchResult{"OWNER/REPO": upper}, "owner/repo") != upper {
		t.Fatal("case-insensitive fallback lost a compatible snapshot")
	}
}
