package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestMonitorUpdateTransitions(t *testing.T) {
	now := time.Date(2026, 8, 24, 9, 30, 0, 0, time.UTC)
	success := &monitorFetchResult{
		FetchedAt:     now,
		PRSections:    make([]monitorSectionData, len(monitorTestConfig().PRSections)),
		IssueSections: make([]monitorSectionData, len(monitorTestConfig().IssueSections)),
	}

	tests := []struct {
		name  string
		model func() monitorModel
		msg   tea.Msg
		check func(*testing.T, monitorModel, tea.Cmd)
	}{
		{
			name: "successful refresh result",
			model: func() monitorModel {
				m := newTestMonitorModel()
				m.refreshing = true
				m.refreshErr = "old error"
				return m
			},
			msg: monitorFetchedMsg{result: success, at: now},
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if m.refreshing || m.refreshErr != "" || m.lastRefresh != now || cmd == nil {
					t.Fatalf("successful refresh transition = refreshing %v, error %q, time %v, cmd %v",
						m.refreshing, m.refreshErr, m.lastRefresh, cmd)
				}
			},
		},
		{
			name: "failed refresh result",
			model: func() monitorModel {
				m := newTestMonitorModel()
				m.refreshing = true
				m.refreshWarn = "old warning"
				return m
			},
			msg: monitorFetchedMsg{err: errors.New("api\ndown"), at: now},
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if m.refreshing || m.refreshErr != "api down" || m.refreshWarn != "" || cmd == nil {
					t.Fatalf("failed refresh transition = refreshing %v, error %q, warning %q, cmd %v",
						m.refreshing, m.refreshErr, m.refreshWarn, cmd)
				}
			},
		},
		{
			name:  "tick starts refresh",
			model: newTestMonitorModel,
			msg:   monitorTickMsg(now),
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if !m.refreshing || cmd == nil {
					t.Fatalf("tick transition = refreshing %v, cmd %v", m.refreshing, cmd)
				}
			},
		},
		{
			name: "tick ignores active refresh",
			model: func() monitorModel {
				m := newTestMonitorModel()
				m.refreshing = true
				return m
			},
			msg: monitorTickMsg(now),
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if !m.refreshing || cmd != nil {
					t.Fatalf("active refresh transition = refreshing %v, cmd %v", m.refreshing, cmd)
				}
			},
		},
		{
			name: "settings key dispatch",
			model: func() monitorModel {
				m := newTestMonitorModel()
				m.settings.open(m.cfg)
				return m
			},
			msg: tea.KeyPressMsg{Code: tea.KeyEscape},
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if m.settings.active || cmd != nil {
					t.Fatalf("settings escape = active %v, cmd %v", m.settings.active, cmd)
				}
			},
		},
		{
			name:  "mouse click dispatch",
			model: modelWithData,
			msg:   tea.MouseClickMsg(tea.Mouse{X: 5, Y: 4}),
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if m.repoIdx != 1 || cmd != nil {
					t.Fatalf("mouse click = repo %d, cmd %v", m.repoIdx, cmd)
				}
			},
		},
		{
			name: "mouse wheel dispatch",
			model: func() monitorModel {
				m := modelWithData()
				m.subTab = 0
				m.focus = monitorFocusList
				return m
			},
			msg: tea.MouseWheelMsg(tea.Mouse{Button: tea.MouseWheelDown}),
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if m.cursor != 1 || cmd != nil {
					t.Fatalf("mouse wheel = cursor %d, cmd %v", m.cursor, cmd)
				}
			},
		},
		{
			name:  "editor error dispatch",
			model: newTestMonitorModel,
			msg:   monitorEditorDoneMsg{err: errors.New("exit status 1")},
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if !strings.Contains(m.refreshErr, "editor") || cmd != nil {
					t.Fatalf("editor transition = error %q, cmd %v", m.refreshErr, cmd)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, cmd := tt.model().Update(tt.msg)
			updated, ok := model.(monitorModel)
			if !ok {
				t.Fatalf("Update returned %T, want monitorModel", model)
			}
			tt.check(t, updated, cmd)
		})
	}
}

func TestMonitorActionKeys(t *testing.T) {
	t.Setenv("VISUAL", "editor-for-test")

	tests := []struct {
		name  string
		key   tea.KeyPressMsg
		model func() monitorModel
		check func(*testing.T, monitorModel, tea.Cmd)
	}{
		{
			name:  "lowercase refresh",
			key:   pressKey("r"),
			model: newTestMonitorModel,
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if !m.refreshing || cmd == nil {
					t.Fatalf("refresh action = refreshing %v, cmd %v", m.refreshing, cmd)
				}
			},
		},
		{
			name:  "uppercase refresh",
			key:   pressKey("R"),
			model: newTestMonitorModel,
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if !m.refreshing || cmd == nil {
					t.Fatalf("refresh action = refreshing %v, cmd %v", m.refreshing, cmd)
				}
			},
		},
		{
			name:  "open settings",
			key:   pressKey("s"),
			model: newTestMonitorModel,
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if !m.settings.active || cmd != nil {
					t.Fatalf("settings action = active %v, cmd %v", m.settings.active, cmd)
				}
			},
		},
		{
			name:  "edit config",
			key:   pressKey("e"),
			model: newTestMonitorModel,
			check: func(t *testing.T, _ monitorModel, cmd tea.Cmd) {
				if cmd == nil {
					t.Fatal("edit action returned no command")
				}
			},
		},
		{
			name:  "open help",
			key:   pressKey("?"),
			model: newTestMonitorModel,
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if !m.helpOpen || cmd != nil {
					t.Fatalf("help action = open %v, cmd %v", m.helpOpen, cmd)
				}
			},
		},
		{
			name:  "open without selection",
			key:   pressKey("o"),
			model: newTestMonitorModel,
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if m.refreshErr != "" || cmd != nil {
					t.Fatalf("safe open action = error %q, cmd %v", m.refreshErr, cmd)
				}
			},
		},
		{
			name: "copy URL",
			key:  pressKey("y"),
			model: func() monitorModel {
				m := modelWithData()
				m.subTab = 0
				return m
			},
			check: func(t *testing.T, _ monitorModel, cmd tea.Cmd) {
				if cmd == nil {
					t.Fatal("copy URL action returned no command")
				}
			},
		},
		{
			name: "copy checkout command",
			key:  pressKey("Y"),
			model: func() monitorModel {
				m := modelWithData()
				m.subTab = 0
				return m
			},
			check: func(t *testing.T, _ monitorModel, cmd tea.Cmd) {
				if cmd == nil {
					t.Fatal("copy checkout action returned no command")
				}
			},
		},
		{
			name: "digit section jump",
			key:  pressKey("1"),
			model: func() monitorModel {
				m := newTestMonitorModel()
				m.subTab = 1
				return m
			},
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if m.subTab != 0 || cmd != nil {
					t.Fatalf("section action = section %d, cmd %v", m.subTab, cmd)
				}
			},
		},
		{
			name: "detail page down",
			key:  tea.KeyPressMsg{Code: tea.KeyPgDown},
			model: func() monitorModel {
				m := modelWithData()
				m.subTab = 0
				m.focus = monitorFocusDetail
				m.data.PRSections[0].Rows[0].Body = strings.Repeat("line\n", 60)
				return m
			},
			check: func(t *testing.T, m monitorModel, cmd tea.Cmd) {
				if m.detailScroll == 0 || cmd != nil {
					t.Fatalf("detail action = scroll %d, cmd %v", m.detailScroll, cmd)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, cmd := tt.model().handleActionKey(tt.key)
			updated, ok := model.(monitorModel)
			if !ok {
				t.Fatalf("handleActionKey returned %T, want monitorModel", model)
			}
			tt.check(t, updated, cmd)
		})
	}
}

func TestJumpToPaneEdges(t *testing.T) {
	tests := []struct {
		name      string
		focus     int
		direction int
		prepare   func(*monitorModel)
		check     func(*testing.T, monitorModel)
	}{
		{
			name: "list top", focus: monitorFocusList, direction: -1,
			prepare: func(m *monitorModel) { m.cursor = 1 },
			check:   func(t *testing.T, m monitorModel) { assertMonitorIndex(t, "cursor", m.cursor, 0) },
		},
		{
			name: "list bottom", focus: monitorFocusList, direction: 1,
			check: func(t *testing.T, m monitorModel) { assertMonitorIndex(t, "cursor", m.cursor, 1) },
		},
		{
			name: "sidebar top", focus: monitorFocusSidebar, direction: -1,
			prepare: func(m *monitorModel) { m.repoIdx = len(m.cfg.Repos) },
			check:   func(t *testing.T, m monitorModel) { assertMonitorIndex(t, "repo", m.repoIdx, 0) },
		},
		{
			name: "sidebar bottom", focus: monitorFocusSidebar, direction: 1,
			check: func(t *testing.T, m monitorModel) {
				assertMonitorIndex(t, "repo", m.repoIdx, len(m.cfg.Repos))
			},
		},
		{
			name: "detail top", focus: monitorFocusDetail, direction: -1,
			prepare: func(m *monitorModel) { m.detailScroll = 10 },
			check:   func(t *testing.T, m monitorModel) { assertMonitorIndex(t, "detail scroll", m.detailScroll, 0) },
		},
		{
			name: "detail bottom", focus: monitorFocusDetail, direction: 1,
			check: func(t *testing.T, m monitorModel) {
				if m.detailScroll == 0 {
					t.Fatal("detail scroll did not move to the bottom")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := modelWithData()
			m.subTab = 0
			m.focus = tt.focus
			m.data.PRSections[0].Rows[0].Body = strings.Repeat("line\n", 60)
			if tt.prepare != nil {
				tt.prepare(&m)
			}
			m.jumpToPaneEdge(tt.direction)
			tt.check(t, m)
		})
	}
}

func TestMonitorFocusCycle(t *testing.T) {
	tests := []struct {
		name       string
		focus      int
		detail     int
		key        tea.KeyPressMsg
		wantFocus  int
		wantDetail int
	}{
		{
			name: "tab enters detail", focus: monitorFocusList, detail: 4,
			key: tea.KeyPressMsg{Code: tea.KeyTab}, wantFocus: monitorFocusDetail, wantDetail: 4,
		},
		{
			name: "tab leaves detail", focus: monitorFocusDetail, detail: 4,
			key: tea.KeyPressMsg{Code: tea.KeyTab}, wantFocus: monitorFocusSidebar, wantDetail: 0,
		},
		{
			name: "shift tab wraps backward", focus: monitorFocusList, detail: 4,
			key: tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, wantFocus: monitorFocusSidebar, wantDetail: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMonitorModel()
			m.focus = tt.focus
			m.detailScroll = tt.detail
			model, cmd := m.handleMainKey(tt.key)
			updated, ok := model.(monitorModel)
			if !ok {
				t.Fatalf("handleMainKey returned %T, want monitorModel", model)
			}
			if updated.focus != tt.wantFocus || updated.detailScroll != tt.wantDetail || cmd != nil {
				t.Fatalf("focus cycle = focus %d, detail %d, cmd %v; want focus %d, detail %d",
					updated.focus, updated.detailScroll, cmd, tt.wantFocus, tt.wantDetail)
			}
		})
	}
}

func assertMonitorIndex(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %d, want %d", name, got, want)
	}
}

func TestSettingsCommitKeys(t *testing.T) {
	tests := []struct {
		name    string
		focus   int
		key     tea.KeyPressMsg
		applied bool
	}{
		{name: "control save", focus: monitorFieldRepos, key: tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}, applied: true},
		{name: "enter in repositories", focus: monitorFieldRepos, key: tea.KeyPressMsg{Code: tea.KeyEnter}, applied: false},
		{name: "enter in limit", focus: monitorFieldLimit, key: tea.KeyPressMsg{Code: tea.KeyEnter}, applied: true},
		{name: "enter in interval", focus: monitorFieldInterval, key: tea.KeyPressMsg{Code: tea.KeyEnter}, applied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := newMonitorSettingsModel()
			settings.open(monitorTestConfig())
			settings.focus = tt.focus
			settings.refocus()
			if got := updateSettingsKey(&settings, tt.key); got != tt.applied {
				t.Fatalf("updateSettingsKey applied = %v, want %v", got, tt.applied)
			}
		})
	}
}

func TestMonitorVisibleStateHelpers(t *testing.T) {
	m := modelWithData()
	m.subTab = 0
	m.repoIdx = 1
	m.changedKeys = map[string]bool{
		"owner/one#pr#1": true,
		"owner/two#pr#2": true,
	}
	m.addedKeys = map[string]bool{
		"owner/one#pr#3": true,
		"owner/two#pr#4": true,
	}

	if m.currentKind() != monitorKindPR {
		t.Fatal("PR tab reported the wrong row kind")
	}
	changed := m.visibleChangedKeys()
	if len(changed) != 1 || !changed["owner/one#pr#1"] {
		t.Fatalf("visible changed keys = %v", changed)
	}
	added := m.visibleAddedKeys()
	if len(added) != 1 || !added["owner/one#pr#3"] {
		t.Fatalf("visible added keys = %v", added)
	}

	m.tab = monitorTabIssues
	if m.currentKind() != monitorKindIssue {
		t.Fatal("issue tab reported the wrong row kind")
	}
}
