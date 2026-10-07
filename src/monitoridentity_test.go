package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

func identitySnapshot(cfg *monitorConfig, viewer string, owners ...string) *monitorFetchResult {
	result := newMonitorFetchResult(cfg, time.Now())
	result.HostIdentities = map[string]monitorHostIdentity{defaultGitHubHost: {Viewer: viewer, Owners: normalizedMonitorOwners(owners)}}
	result.PRSections[2].Rows = []monitorRow{monitorRowForTest("owner/pinned", 1, "old")}
	return result
}

func TestMonitorOwnerScopeChangeResetsOnlyGlobalBaseline(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	old := identitySnapshot(cfg, "owner", "user:owner", "org:first")
	old.Pinned = map[string]*monitorFetchResult{"owner/pinned": identitySnapshot(cfg, "owner")}
	current := identitySnapshot(cfg, "owner", "org:second", "user:owner")
	current.PRSections[2].Rows = nil
	current.Pinned = map[string]*monitorFetchResult{"owner/pinned": identitySnapshot(cfg, "owner")}
	current.Pinned["owner/pinned"].PRSections[2].Rows = nil
	m := newMonitorModel(cfg, "", "", monitorSessionState{})
	m.data = old
	updated, _ := m.handleFetched(monitorFetchedMsg{result: current})
	actual := updated.(monitorModel)
	if len(actual.lastChanges) != 1 || actual.lastChanges[0].Key != "owner/pinned#pr#1" {
		t.Fatalf("organization change should preserve only the legitimate pin removal: %+v", actual.lastChanges)
	}
	if actual.data != current {
		t.Fatal("fresh scope was not installed")
	}
}

func TestMonitorAccountChangeOnTotalFailureClearsOnlyAffectedHost(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	cfg.Repos = append(cfg.Repos, "ghe.example.com/corp/repo")
	old := identitySnapshot(cfg, "alice", "user:alice")
	otherCfg := defaultMonitorConfig("ghe.example.com/corp/repo")
	otherPin := newMonitorTestFetchResult(otherCfg, time.Now())
	old.Pinned = map[string]*monitorFetchResult{"owner/pinned": identitySnapshot(defaultMonitorConfig("owner/pinned"), "alice"), cfg.Repos[1]: otherPin}
	failed := unavailableMonitorScope(cfg, errors.New("search failed"))
	setMonitorDiscoveredIdentity(failed, defaultGitHubHost, []string{"user:bob", "org:team"})
	failed.Pinned = map[string]*monitorFetchResult{"owner/pinned": unavailableMonitorScope(cfg, errors.New("pin failed"))}
	failed.Pinned[cfg.Repos[1]] = unavailableMonitorScope(otherCfg, errors.New("enterprise pin failed"))
	m := newMonitorModel(cfg, "", "", monitorSessionState{})
	m.data = old
	m.lastChanges = []monitorChange{{}}
	updated, _ := m.handleFetched(monitorFetchedMsg{result: failed, err: errors.New("search failed")})
	actual := updated.(monitorModel)
	if len(actual.data.PRSections[2].Rows) != 0 || len(actual.data.Pinned["owner/pinned"].PRSections[2].Rows) != 0 || actual.data.Pinned[cfg.Repos[1]].FetchedAt != otherPin.FetchedAt || len(actual.lastChanges) != 0 {
		t.Fatalf("account change leaked old data or discarded another host: %+v", actual.data)
	}
}

func TestMonitorUnknownScopeFailureRetainsProvenance(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	old := identitySnapshot(cfg, "alice", "user:alice", "org:team")
	failed := unavailableMonitorScope(cfg, errors.New("discovery failed"))
	retainMonitorScopeSnapshots(failed, old)
	if failed.HostIdentities[defaultGitHubHost].Viewer != "alice" || len(failed.PRSections[2].Rows) != 1 {
		t.Fatal("retained rows lost their original actor")
	}
	recovered := identitySnapshot(cfg, "bob", "user:bob")
	if changes := diffMonitorFetchScopes(failed, recovered); len(changes) != 0 {
		t.Fatalf("new actor recovery invented row events: %+v", changes)
	}
	freshUnknown := newMonitorFetchResult(cfg, time.Now())
	if monitorIdentitiesEqual(old, freshUnknown, true) || monitorIdentitiesChanged(old, freshUnknown, true) {
		t.Fatal("unknown scope either qualified a diff or claimed a known change")
	}
}

func TestMonitorIdentityNormalizationAndKnownHostMismatch(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	old := identitySnapshot(cfg, "owner", "org:TEAM", "user:OWNER", "org:team")
	current := identitySnapshot(cfg, "owner", "user:owner", "org:team")
	if !monitorIdentitiesEqual(old, current, true) || monitorIdentitiesChanged(old, current, true) {
		t.Fatal("case/order/duplicate owner changes reset the baseline")
	}
	old.HostScope = append(old.HostScope, "ghe.example.com")
	old.HostIdentities["ghe.example.com"] = monitorHostIdentity{Viewer: "enterprise", Owners: []string{"user:enterprise"}}
	current.HostIdentities["ghe.example.com"] = monitorHostIdentity{}
	current.HostIdentities[defaultGitHubHost] = monitorHostIdentity{Viewer: "bob", Owners: []string{"user:bob"}}
	if !monitorIdentitiesChanged(old, current, true) {
		t.Fatal("an unknown host hid another host's known account change")
	}
}

func TestMonitorPinResponseKeepsItsOwnActor(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		if !strings.Contains(strings.Join(args, " "), "viewer { login }") {
			t.Fatal("search did not bind rows and viewer in one request")
		}
		return *bytes.NewBufferString(`{"data":{"viewer":{"login":"Alice"},"pr0":{"nodes":[]},"pr1":{"nodes":[]},"pr2":{"nodes":[]},"is0":{"nodes":[]},"is1":{"nodes":[]}}}`), bytes.Buffer{}, nil
	}
	requests, err := buildMonitorHostQueries(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := fetchMonitorHost(context.Background(), requests[0], cfg, time.Now())
	if err != nil || pin.HostIdentities[defaultGitHubHost].Viewer != "alice" {
		t.Fatalf("pin actor: %+v, %v", pin, err)
	}
	global := identitySnapshot(cfg, "bob", "user:bob")
	combined, err := combineMonitorScopes(cfg, monitorHostFetchOutcome{Result: global}, []monitorHostFetchOutcome{{Result: pin}})
	if err != nil || combined.Pinned["owner/pinned"].HostIdentities[defaultGitHubHost].Viewer != "alice" {
		t.Fatal("global discovery relabeled pin rows")
	}
	requests[0].OwnerScope = []string{"user:bob", "org:team"}
	changed, err := fetchMonitorHost(context.Background(), requests[0], cfg, time.Now())
	if err == nil || changed.HostIdentities[defaultGitHubHost].Viewer != "alice" || len(changed.HostIdentities[defaultGitHubHost].Owners) != 0 {
		t.Fatal("discovery/search actor mismatch was accepted")
	}
}

func TestMonitorAllFailedSearchesPreserveDiscoveredScope(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	saved := monitorGHExecFunc
	t.Cleanup(func() { monitorGHExecFunc = saved })
	monitorGHExecFunc = func(_ context.Context, args ...string) (bytes.Buffer, bytes.Buffer, error) {
		if strings.Contains(strings.Join(args, " "), "organizations(first:") {
			return *bytes.NewBufferString(`{"data":{"viewer":{"login":"Bob","organizations":{"nodes":[{"login":"Team"}],"pageInfo":{}}}}}`), bytes.Buffer{}, nil
		}
		return bytes.Buffer{}, bytes.Buffer{}, errors.New("search unavailable")
	}
	result, err := executeMonitorAllRepoFetch(context.Background(), cfg, time.Now())
	if err == nil || result == nil || result.HostIdentities[defaultGitHubHost].Viewer != "bob" || !slices.Equal(result.HostIdentities[defaultGitHubHost].Owners, []string{"org:team", "user:bob"}) {
		t.Fatalf("failed fetch dropped scope: %+v, %v", result, err)
	}
	if !result.FetchedAt.IsZero() {
		t.Fatal("failed scope acquired a fresh row timestamp")
	}
}

func TestMonitorIdentityConflictAndUnknownSearch(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	first := identitySnapshot(cfg, "alice", "user:alice")
	mergeMonitorIdentities(first, identitySnapshot(cfg, "bob", "user:bob"))
	if !first.Incomplete || !first.HostIdentities[defaultGitHubHost].Conflicted || monitorIdentitiesEqual(first, first, true) {
		t.Fatal("mixed actors qualified a baseline")
	}
	unknown, err := bindMonitorHostIdentity(newMonitorFetchResult(cfg, time.Now()), monitorHostQuery{Host: defaultGitHubHost})
	if err != nil || !unknown.Incomplete {
		t.Fatal("unknown response actor qualified fresh data")
	}
	setMonitorDiscoveredIdentity(first, defaultGitHubHost, []string{"user:alice"})
	if first.HostIdentities[defaultGitHubHost].Viewer != "bob" {
		t.Fatal("discovery overwrote a response actor")
	}
	if monitorOwnerViewer([]string{"org:team"}) != "" {
		t.Fatal("organization was treated as the viewer")
	}
	mergeMonitorIdentities(first, nil)
}

func TestMonitorUnknownBatchDoesNotHideKnownAccountChange(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	old := identitySnapshot(cfg, "alice", "user:alice")
	current := identitySnapshot(cfg, "bob", "user:bob")
	unknown := newMonitorFetchResult(cfg, time.Now())
	unknown.HostIdentities = map[string]monitorHostIdentity{defaultGitHubHost: {}}
	mergeMonitorIdentities(current, unknown)
	if current.HostIdentities[defaultGitHubHost].Viewer != "bob" || !monitorIdentitiesChanged(old, current, true) {
		t.Fatal("unknown batch hid a known account change")
	}
	mixed := identitySnapshot(cfg, "alice", "user:alice")
	mergeMonitorIdentities(mixed, unknown)
	mergeMonitorIdentities(mixed, current)
	if !mixed.HostIdentities[defaultGitHubHost].Conflicted || !mixed.Incomplete {
		t.Fatal("unknown batch erased a mixed-account conflict")
	}
}

func TestMonitorTotalFailurePreservesScopeErrorsAndBackoff(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	old := identitySnapshot(cfg, "owner", "user:owner")
	old.Pinned = map[string]*monitorFetchResult{"owner/pinned": identitySnapshot(cfg, "owner")}
	failed := unavailableMonitorScope(cfg, errors.New("global search unavailable"))
	failed.Pinned = map[string]*monitorFetchResult{"owner/pinned": unavailableMonitorScope(cfg, errors.New("shortcut search unavailable"))}
	m := newMonitorModel(cfg, "", "", monitorSessionState{})
	m.data = old
	m.lastRefresh = old.FetchedAt
	m.backoff = minimumMonitorInterval * 4
	m.repoIdx = 1
	updated, _ := m.handleFetched(monitorFetchedMsg{result: failed, err: errors.New("global search unavailable")})
	actual := updated.(monitorModel)
	pin := actual.data.Pinned["owner/pinned"]
	if actual.data.Error != "global search unavailable" || pin.Error != "shortcut search unavailable" || len(pin.PRSections[2].Rows) != 1 || actual.refreshErr != "global search unavailable" || actual.backoff != minimumMonitorInterval*8 || actual.lastRefresh != old.FetchedAt {
		t.Fatalf("scope failures lost state/backoff: %+v", actual)
	}
	if message := monitorScopeRefreshError(pin, actual.refreshErr, actual.refreshErrIsFetch); message != "shortcut search unavailable" {
		t.Fatalf("selected failure hidden: %s", message)
	}
	if message := monitorScopeRefreshError(nil, actual.refreshErr, actual.refreshErrIsFetch); message != actual.refreshErr {
		t.Fatal("global error lost without a scope")
	}
	fresh := newMonitorModel(cfg, "", "", monitorSessionState{})
	// Reuse a separately constructed failure to avoid cached-row retention.
	empty := unavailableMonitorScope(cfg, errors.New("global search unavailable"))
	empty.Pinned = map[string]*monitorFetchResult{"owner/pinned": unavailableMonitorScope(cfg, errors.New("shortcut search unavailable"))}
	updated, _ = fresh.handleFetched(monitorFetchedMsg{result: empty, err: errors.New("global search unavailable")})
	actual = updated.(monitorModel)
	if actual.data == nil || actual.data.Pinned["owner/pinned"].Error == "" || len(actual.lastChanges) != 0 {
		t.Fatal("initial failures did not reach the UI")
	}
}

func TestMonitorActionErrorOverridesRetainedRefreshError(t *testing.T) {
	cfg := defaultMonitorConfig("owner/pinned")
	m := newMonitorModel(cfg, "", "", monitorSessionState{})
	m.layout = computeMonitorLayout(120, 40)
	m.data = unavailableMonitorScope(cfg, errors.New("old repository failure"))
	m.refreshErr = "old global failure"
	m.refreshErrIsFetch = true
	model, _ := m.handleEditorDone(monitorEditorDoneMsg{err: errors.New("new editor failure")})
	actual := model.(monitorModel)
	if actual.refreshErrIsFetch || !strings.Contains(actual.footerLine(), "new editor failure") || strings.Contains(actual.footerLine(), "old repository failure") {
		t.Fatalf("action failure masked: %s", actual.footerLine())
	}
	if got := monitorScopeRefreshError(m.data, "new browser failure", false); got != "new browser failure" {
		t.Fatal("new action error lost")
	}
	actual.applyFetchResult(newMonitorTestFetchResult(cfg, time.Now()))
	if actual.refreshErr != "" || actual.refreshErrIsFetch {
		t.Fatal("successful refresh did not clear error provenance")
	}
}

func TestMonitorPinAccountObservationInvalidatesFailedSiblingAndGlobalCaches(t *testing.T) {
	for _, viewer := range []string{"bob", "alice", ""} {
		t.Run("pin-viewer-"+viewer, func(t *testing.T) {
			cfg := defaultMonitorConfig("owner/pinned")
			cfg.Repos = append(cfg.Repos, "owner/sibling", "ghe.example.com/corp/repo")
			old := identitySnapshot(cfg, "alice", "user:alice")
			old.Pinned = make(map[string]*monitorFetchResult)
			for _, repo := range cfg.Repos {
				pinCfg := defaultMonitorConfig(repo)
				pin := newMonitorTestFetchResult(pinCfg, time.Now())
				for host := range pin.HostIdentities {
					pin.HostIdentities[host] = monitorHostIdentity{Viewer: "alice"}
				}
				pin.PRSections[2].Rows = []monitorRow{monitorRowForTest(repo, 1, "cached")}
				old.Pinned[repo] = pin
			}
			current := unavailableMonitorScope(cfg, errors.New("discovery unavailable"))
			current.Pinned = make(map[string]*monitorFetchResult)
			for _, repo := range cfg.Repos {
				current.Pinned[repo] = unavailableMonitorScope(defaultMonitorConfig(repo), errors.New("pin unavailable"))
			}
			if viewer != "" {
				current.Pinned[cfg.Repos[0]] = identitySnapshot(defaultMonitorConfig(cfg.Repos[0]), viewer)
				current.Pinned[cfg.Repos[0]].PRSections = old.Pinned[cfg.Repos[0]].PRSections
			}
			m := newMonitorModel(cfg, "", "", monitorSessionState{})
			m.data = old
			updated, _ := m.handleFetched(monitorFetchedMsg{result: current})
			actual := updated.(monitorModel)
			rootRows := len(actual.data.PRSections[2].Rows)
			siblingRows := len(actual.data.Pinned[cfg.Repos[1]].PRSections[2].Rows)
			wantCached := 1
			if viewer == "bob" {
				wantCached = 0
			}
			if rootRows != wantCached || siblingRows != wantCached || len(actual.data.Pinned[cfg.Repos[2]].PRSections[2].Rows) != 1 {
				t.Fatalf("wrong cache invalidation: global=%d sibling=%d other=%d", rootRows, siblingRows, len(actual.data.Pinned[cfg.Repos[2]].PRSections[2].Rows))
			}
			if len(actual.lastChanges) != 0 {
				t.Fatalf("account observation invented events: %+v", actual.lastChanges)
			}
			if viewer == "bob" && actual.data.HostIdentities[defaultGitHubHost].Viewer == "bob" {
				t.Fatal("pin observation relabeled global rows")
			}
			if viewer != "" && actual.data.Pinned[cfg.Repos[0]].HostIdentities[defaultGitHubHost].Viewer != viewer {
				t.Fatal("pin lost its own row actor")
			}
		})
	}
}
