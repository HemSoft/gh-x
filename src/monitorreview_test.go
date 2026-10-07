package main

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func scrollableReviewModel() monitorModel {
	m := modelWithData()
	m.subTab = 0
	m.repoIdx = 0
	for i := range m.data.PRSections[0].Rows {
		m.data.PRSections[0].Rows[i].Body = strings.Repeat("long detail line\n", 40)
	}
	m.detailScroll = 12
	return m
}

func TestMonitorReviewFilterReplacesSelectedIdentity(t *testing.T) {
	m := scrollableReviewModel()
	m.filtering = true
	m.filter.Focus()
	model, _ := m.filterInputUpdate(pressKey("S"))
	updated := model.(monitorModel)
	if updated.selectedRowKey() == m.selectedRowKey() || updated.detailScroll != 0 {
		t.Fatal("same-index filter selection retained previous detail offset")
	}
	updated.detailScroll = 12
	model, _ = updated.escapeMonitor()
	if model.(monitorModel).detailScroll != 0 {
		t.Fatal("clearing filter retained detail offset")
	}
	updated.filtering = true
	model, _ = updated.filterInputUpdate(tea.KeyPressMsg{Code: tea.KeyEscape})
	if model.(monitorModel).detailScroll != 0 {
		t.Fatal("canceling filter retained detail offset")
	}
}

func TestMonitorReviewRefreshResetsOnlyChangedSelection(t *testing.T) {
	for _, reorder := range []bool{false, true} {
		m := scrollableReviewModel()
		result := *m.data
		rows := append([]monitorRow(nil), m.data.PRSections[0].Rows...)
		if reorder {
			rows[0], rows[1] = rows[1], rows[0]
		}
		result.PRSections = []monitorSectionData{{Total: 2, Rows: rows}}
		m.applyFetchResult(&result)
		expected := 12
		if reorder {
			expected = 0
		}
		if m.detailScroll != expected {
			t.Fatalf("reorder=%v scroll=%d", reorder, m.detailScroll)
		}
	}
	m := scrollableReviewModel()
	result := *m.data
	result.PRSections = []monitorSectionData{{Total: 1, Rows: m.data.PRSections[0].Rows[1:]}}
	m.applyFetchResult(&result)
	if m.detailScroll != 0 {
		t.Fatal("removed selection kept detail offset")
	}
}

func TestMonitorReviewBlankListClickPreservesDetailPosition(t *testing.T) {
	m := scrollableReviewModel()
	m.cursor = 1
	model, _ := m.handleClick(tea.Mouse{X: m.layout.MainLeft + 1, Y: m.layout.ListTop + 7})
	updated := model.(monitorModel)
	if updated.cursor != 1 || updated.detailScroll != 12 {
		t.Fatal("blank list click changed selection/detail position")
	}
}

func TestMonitorReviewCompactScopeRetainsDistinctRepositoryTail(t *testing.T) {
	for _, width := range []int{60, 80} {
		for _, focus := range []int{monitorFocusList, monitorFocusSidebar} {
			m := modelWithData()
			m.layout = computeMonitorLayout(width, 24)
			m.focus = focus
			m.repoIdx = 1
			outputs := []string{}
			for _, repo := range []string{"alpha", "beta"} {
				m.cfg.Repos = []string{strings.Repeat("enterprise", 8) + ".test/long-owner/" + repo}
				heading := m.dashboardHeading()
				if lipgloss.Width(heading) != width || !strings.Contains(stripANSIForTest(heading), repo) {
					t.Fatalf("scope lost at width%d: %q", width, heading)
				}
				if focus == monitorFocusSidebar && !strings.Contains(stripANSIForTest(heading), "[focus]") {
					t.Fatal("scope lost focus cue")
				}
				outputs = append(outputs, stripANSIForTest(heading))
			}
			if outputs[0] == outputs[1] {
				t.Fatal("distinct repositories have identical scope")
			}
		}
	}
}

func TestMonitorReviewOnlyConfiguredAggregateCountsAreShown(t *testing.T) {
	m := modelWithData()
	m.cfg.PRSections = []monitorSection{{Title: "Mine"}}
	m.cfg.IssueSections = []monitorSection{{Title: "All open"}}
	m.data.PRSections[0].Total = 7
	m.data.IssueSections[0].Total = 0
	if got := stripANSIForTest(m.dashboardHeading()); strings.Contains(got, "7 PRs") || !strings.Contains(got, "0 issues") {
		t.Fatalf("misleading aggregates: %q", got)
	}
	if got := stripANSIForTest(m.renderTabRow()); strings.Contains(got, "PRs (") || !strings.Contains(got, "Issues (0)") {
		t.Fatalf("misleading tab aggregate: %q", got)
	}
	m.cfg.IssueSections[0].Title = "Assigned"
	if m.dashboardTotals() != "" {
		t.Fatal("unknown counts rendered as totals")
	}
}

func TestMonitorReviewSectionsOnlyUseCompleteClickableSlots(t *testing.T) {
	m := sizedModel()
	m.layout = computeMonitorLayout(80, 24)
	m.cfg.PRSections = nil
	for i := range 12 {
		m.cfg.PRSections = append(m.cfg.PRSections, monitorSection{Title: fmt.Sprintf("Section%d", i)})
	}
	for m.subTab < 11 {
		last := minInt(m.firstVisibleSection()+m.visibleSectionSlots(), 12) - 1
		model, _ := m.handleClick(tea.Mouse{X: (last-m.firstVisibleSection())*monitorSubTabSlotWidth + 1, Y: m.layout.SubTabTop})
		next := model.(monitorModel)
		if next.subTab <= m.subTab {
			t.Fatal("mouse cannot reach remaining sections")
		}
		m = next
	}
	row := stripANSIForTest(m.renderSubTabRow())
	if lipgloss.Width(row) > m.visibleSectionSlots()*monitorSubTabSlotWidth {
		t.Fatal("partial section emitted")
	}
	before := m.subTab
	model, _ := m.handleClick(tea.Mouse{X: 79, Y: m.layout.SubTabTop})
	if model.(monitorModel).subTab != before {
		t.Fatal("unused section gutter responds to click")
	}
}

func TestMonitorReviewTabsPreserveIndentationAndDisplayStops(t *testing.T) {
	for _, input := range []struct{ text, want string }{{"a\tb", "a   b"}, {"界\tb", "界  b"}, {"abcd\tb", "abcd    b"}} {
		if got := expandMonitorTabs(input.text); got != input.want {
			t.Fatalf("tab %q became %q", input.text, got)
		}
	}
	lines := wrapMonitorBody("\tindented long line\n\n\tlast", 10)
	if !strings.HasPrefix(lines[0], "    ") || !strings.Contains(strings.Join(lines, "\n"), "\n\n    last") {
		t.Fatalf("indentation/blank lines lost: %q", lines)
	}
	for _, line := range lines {
		if lipgloss.Width(line) > 10 {
			t.Fatal("body overflow")
		}
	}
}

func TestMonitorReviewHiddenRepositoryIsAnErrorAndSettingsShowEnter(t *testing.T) {
	m := modelWithData()
	m.cfg.Repos = []string{"owner/one", "owner/hidden"}
	m.data.Accessible = map[string]bool{"owner/one": true, "owner/hidden": false}
	m.theme = newMonitorTheme(true, false)
	footer := m.footerLine()
	attrs := monitorTestAttributesAt(t, footer, "owner/hidden")
	if attrs.foreground == "" || attrs.foreground == monitorTestAttributesAt(t, footer, "rate").foreground {
		t.Fatal("hidden repository has no error color")
	}
	m.layout = computeMonitorLayout(60, 16)
	m.settings.open(m.cfg)
	screen := stripANSIForTest(m.renderSettingsScreen())
	for _, hint := range []string{"enter newline/save", "ctrl+s save", "esc cancel"} {
		if !strings.Contains(screen, hint) {
			t.Fatalf("missing settings hint %q", hint)
		}
	}
}

func TestMonitorReviewRejectedSettingsPreserveScopeAndSelection(t *testing.T) {
	for _, failure := range []string{"limit", "interval", "save"} {
		t.Run(failure, func(t *testing.T) {
			m := scrollableReviewModel()
			m.cfg.Repos = []string{"owner/one"}
			m.repoIdx = 1
			m.settings.open(m.cfg)
			m.settings.repos.SetValue("owner/two")
			m.settings.limit.SetValue("10")
			m.settings.interval.SetValue("5m")
			m.configPath = t.TempDir() // A directory cannot be replaced by a config file.
			switch failure {
			case "limit":
				m.settings.limit.SetValue("0")
			case "interval":
				m.settings.interval.SetValue("bad")
			}
			beforeKey, beforeLimit, beforeInterval := m.selectedRowKey(), m.cfg.Defaults.Limit, m.cfg.Defaults.Interval
			model, cmd := m.applySettingsForm()
			updated := model.(monitorModel)
			if cmd != nil || updated.selectedRowKey() != beforeKey || updated.detailScroll != 12 || updated.cfg.Defaults.Limit != beforeLimit || updated.cfg.Defaults.Interval != beforeInterval {
				t.Fatal("rejected settings partially changed live config or selection")
			}
		})
	}
}
