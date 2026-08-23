package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func monitorTestConfig() *monitorConfig {
	cfg := defaultMonitorConfig("owner/one")
	cfg.Repos = append(cfg.Repos, "owner/two")
	return cfg
}

func newTestMonitorModel() monitorModel {
	return newMonitorModel(monitorTestConfig(), "cfg.yml", "", monitorSessionState{})
}

func pressKey(text string) tea.KeyPressMsg {
	runes := []rune(text)
	return tea.KeyPressMsg{Code: runes[0], Text: text}
}

func TestVisibleRowsFiltersByRepoAndText(t *testing.T) {
	data := &monitorFetchResult{
		PRSections: []monitorSectionData{{
			Kind: monitorKindPR,
			Rows: []monitorRow{
				monitorRowForTest("owner/one", 1, "open"),
				monitorRowForTest("owner/two", 2, "open"),
			},
		}},
	}
	rows := computeVisibleRows(data, monitorTabPRs, 0, 0, []string{"owner/one", "owner/two"}, "")
	if len(rows) != 2 {
		t.Fatalf("all repos should show both rows: %d", len(rows))
	}
	rows = computeVisibleRows(data, monitorTabPRs, 0, 1, []string{"owner/one", "owner/two"}, "")
	if len(rows) != 1 || rows[0].Repo != "owner/one" {
		t.Fatalf("repo filter failed: %+v", rows)
	}
	rows = computeVisibleRows(data, monitorTabPRs, 0, 0, []string{"owner/one", "owner/two"}, "2")
	if len(rows) != 1 || rows[0].Number != 2 {
		t.Fatalf("text filter failed: %+v", rows)
	}
	if got := computeVisibleRows(nil, 0, 0, 0, nil, ""); got != nil {
		t.Fatal("nil data should yield no rows")
	}
}

func TestHandleKeyTabSwitchAndSectionJump(t *testing.T) {
	m := newTestMonitorModel()
	model, _ := m.handleKey(pressKey("l"))
	updated := model.(monitorModel)
	if updated.tab != monitorTabIssues {
		t.Fatalf("'l' should switch to issues tab, got %d", updated.tab)
	}
	model, _ = updated.handleKey(pressKey("2"))
	updated = model.(monitorModel)
	if updated.subTab != 1 {
		t.Fatalf("digit jump failed: %d", updated.subTab)
	}
	model, _ = updated.handleKey(pressKey("9"))
	updated = model.(monitorModel)
	if updated.subTab != 1 {
		t.Fatalf("out-of-range digit should not change sub-tab: %d", updated.subTab)
	}
	model, _ = updated.handleKey(pressKey("h"))
	updated = model.(monitorModel)
	if updated.tab != monitorTabPRs {
		t.Fatal("'h' should switch back to PRs")
	}
}

func TestCursorNavigationClamps(t *testing.T) {
	m := newTestMonitorModel()
	m.focus = monitorFocusList
	m.data = &monitorFetchResult{
		PRSections: []monitorSectionData{{Rows: []monitorRow{
			monitorRowForTest("owner/one", 1, "open"),
			monitorRowForTest("owner/one", 2, "open"),
		}}},
	}
	m.subTab = 0 // rows are seeded on the first PR section
	m.layout = computeMonitorLayout(100, 30)
	m.ready = true

	model, _ := m.handleKey(pressKey("G"))
	updated := model.(monitorModel)
	if updated.cursor != 1 {
		t.Fatalf("'G' should land on last row: %d", updated.cursor)
	}
	model, _ = updated.handleKey(pressKey("j"))
	updated = model.(monitorModel)
	if updated.cursor != 1 {
		t.Fatalf("cursor should clamp at bottom: %d", updated.cursor)
	}
	model, _ = updated.handleKey(pressKey("k"))
	model, _ = model.(monitorModel).handleKey(pressKey("k"))
	updated = model.(monitorModel)
	if updated.cursor != 0 {
		t.Fatalf("cursor should clamp at top: %d", updated.cursor)
	}
}

func TestFilterModeEnterAndEscape(t *testing.T) {
	m := newTestMonitorModel()
	m.layout = computeMonitorLayout(100, 30)
	m.ready = true

	model, cmd := m.handleKey(pressKey("/"))
	updated := model.(monitorModel)
	if !updated.filtering || cmd == nil {
		t.Fatalf("'/'' should open the filter input (filtering=%v cmd=%v)", updated.filtering, cmd)
	}

	typed := []string{"f", "i", "x"}
	for _, char := range typed {
		model, _ = updated.filterInputUpdate(pressKey(char))
		updated = model.(monitorModel)
	}
	if updated.filter.Value() != "fix" {
		t.Fatalf("filter input did not capture text: %q", updated.filter.Value())
	}

	model, _ = updated.filterInputUpdate(tea.KeyPressMsg{Code: tea.KeyEscape})
	updated = model.(monitorModel)
	if updated.filtering || updated.filter.Value() != "" {
		t.Fatal("esc should close and clear the filter")
	}
}

func TestApplyFetchResultTracksChangesAndBackoff(t *testing.T) {
	m := newTestMonitorModel()
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)

	first := &monitorFetchResult{
		FetchedAt: now,
		PRSections: []monitorSectionData{{Total: 1, Rows: []monitorRow{
			monitorRowForTest("owner/one", 1, "open"),
		}}},
		IssueSections: make([]monitorSectionData, len(newTestMonitorModel().cfg.IssueSections)),
	}
	m.applyFetchResult(first)
	if m.refreshErr != "" || m.lastRefresh != now {
		t.Fatalf("first fetch should succeed cleanly: err=%q at=%v", m.refreshErr, m.lastRefresh)
	}
	if len(m.changedKeys) != 0 {
		t.Fatalf("first fetch is silent, got %v", m.changedKeys)
	}

	second := &monitorFetchResult{
		FetchedAt: now.Add(time.Minute),
		PRSections: []monitorSectionData{{Total: 1, Rows: []monitorRow{
			{Kind: monitorKindPR, Repo: "owner/one", Number: 1, Title: "title",
				State: "closed", UpdatedAt: first.PRSections[0].Rows[0].UpdatedAt},
		}}},
		IssueSections: first.IssueSections,
	}
	m.applyFetchResult(second)
	key := "owner/one#pr#1"
	if !m.changedKeys[key] {
		t.Fatalf("state flip should glow: %v", m.changedKeys)
	}
	row := second.PRSections[0].Rows[0]
	m.setCursor(0)
	m.markSeen(row)
	if m.changedKeys[key] {
		t.Fatal("seen row should stop glowing")
	}
}

func TestHandleFetchedErrorBacksOff(t *testing.T) {
	m := newTestMonitorModel()
	before := m.backoff
	model, cmd := m.handleFetched(monitorFetchedMsg{err: errors.New("boom"), at: time.Now()})
	updated := model.(monitorModel)
	if updated.refreshErr == "" {
		t.Fatal("error should surface")
	}
	if updated.backoff <= before {
		t.Fatalf("backoff should grow: %v -> %v", before, updated.backoff)
	}
	if cmd == nil {
		t.Fatal("backoff tick must be scheduled")
	}
}

func TestApplyFetchResultSurfacesPartialHostWarning(t *testing.T) {
	m := newTestMonitorModel()
	m.layout = computeMonitorLayout(100, 24)
	result := newMonitorFetchResult(m.cfg, time.Now())
	result.Warnings = []string{"ghe.example.com: connection refused\nretry later"}

	m.applyFetchResult(result)
	if !strings.Contains(m.refreshWarn, "ghe.example.com: connection refused retry later") {
		t.Fatalf("partial-host warning not retained safely: %q", m.refreshWarn)
	}
	footer := m.footerLine()
	if !strings.Contains(footer, "warning:") || !strings.Contains(footer, "ghe.example.com") {
		t.Fatalf("partial-host warning not visible in footer: %q", footer)
	}
}

func TestQuitSavesState(t *testing.T) {
	m := newTestMonitorModel()
	m.statePath = "state.json"
	m.repoIdx = 1
	savedPath := ""
	var savedState monitorSessionState
	original := saveMonitorState
	saveMonitorState = func(path string, state monitorSessionState) error {
		savedPath = path
		savedState = state
		return nil
	}
	defer func() { saveMonitorState = original }()
	model, cmd := m.quitMonitor()
	updated := model.(monitorModel)
	if !updated.quitting || cmd == nil {
		t.Fatal("quit should flag and request program exit")
	}
	if savedPath == "" || savedState.RepoIndex != 1 {
		t.Fatalf("state not saved: path=%q state=%+v", savedPath, savedState)
	}
}

func TestSettingsApplyHappyAndSadPaths(t *testing.T) {
	m := newTestMonitorModel()
	m.settings.open(m.cfg)
	m.settings.repos.SetValue("owner/new\n\nowner/other")
	m.settings.limit.SetValue("45")
	m.settings.interval.SetValue("15m")

	model, cmd := m.applySettingsForm()
	updated := model.(monitorModel)
	if updated.settings.active {
		t.Fatal("settings should close after apply")
	}
	if cmd == nil {
		t.Fatal("apply should trigger a refresh")
	}
	if len(updated.cfg.Repos) != 2 || updated.cfg.Repos[0] != "owner/new" {
		t.Fatalf("repos not applied: %v", updated.cfg.Repos)
	}
	if updated.cfg.Defaults.Limit != 45 || updated.interval != 15*time.Minute {
		t.Fatalf("defaults not applied: %+v", updated.cfg.Defaults)
	}

	bad := newTestMonitorModel()
	bad.settings.open(bad.cfg)
	bad.settings.repos.SetValue("not-a-repo")
	model, _ = bad.applySettingsForm()
	if !model.(monitorModel).settings.active || model.(monitorModel).settings.errText == "" {
		t.Fatal("invalid repos should keep settings open with an error")
	}
}

func TestHelpToggleAndAnyKeyCloses(t *testing.T) {
	m := newTestMonitorModel()
	model, _ := m.handleKey(pressKey("?"))
	updated := model.(monitorModel)
	if !updated.helpOpen {
		t.Fatal("'?' should open help")
	}
	if !strings.Contains(updated.renderScreen(), "gh x monitor") {
		t.Fatal("help screen should render")
	}
	model, _ = updated.handleKey(pressKey("x"))
	updated = model.(monitorModel)
	if updated.helpOpen {
		t.Fatal("any key should close help")
	}
}

func TestTabsDefaultToAllOpen(t *testing.T) {
	m := newMonitorModel(monitorTestConfig(), "cfg.yml", "", monitorSessionState{SubTab: 1})
	if got := m.currentSection().Title; got != "All open" {
		t.Fatalf("stored sub-tab should be overridden, PRs should open on All open, got %q", got)
	}

	model, _ := m.switchTab(1)
	updated := model.(monitorModel)
	if updated.tab != monitorTabIssues || updated.currentSection().Title != "All open" {
		t.Fatalf("issues tab should default to All open: tab=%d section=%q",
			updated.tab, updated.currentSection().Title)
	}

	model, _ = updated.jumpToSection(0)
	updated = model.(monitorModel)
	if got := updated.currentSection().Title; got != "Assigned to me" {
		t.Fatalf("manual section choice lost: %q", got)
	}

	model, _ = updated.switchTab(-1)
	updated = model.(monitorModel)
	if updated.tab != monitorTabPRs {
		t.Fatal("should be back on PRs")
	}
	if got := updated.currentSection().Title; got != "All open" {
		t.Fatalf("returning to a tab should reset to All open, got %q", got)
	}
}

func TestTabsWithoutAllOpenKeepSelection(t *testing.T) {
	cfg := monitorTestConfig()
	cfg.PRSections = []monitorSection{
		{Title: "Mine", Filters: "is:open"},
		{Title: "Review requests", Filters: "is:open"},
	}
	cfg.IssueSections = []monitorSection{{Title: "Assigned to me", Filters: "is:open"}}
	m := newMonitorModel(cfg, "cfg.yml", "", monitorSessionState{SubTab: 1})
	if m.subTab != 1 {
		t.Fatalf("configs without an All open section should keep the stored selection: %d", m.subTab)
	}
}
