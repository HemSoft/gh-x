package main

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// View renders the monitor screen with alt-screen and cell-motion mouse.
func (m monitorModel) View() tea.View {
	view := tea.NewView(m.renderScreen())
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m monitorModel) renderScreen() string {
	if !m.ready {
		return "Loading gh x monitor…"
	}
	if m.layout.Width < monitorMinWidth || m.layout.Height < monitorMinHeight {
		return tooSmallMonitorScreen(m.layout.Width, m.layout.Height)
	}
	switch {
	case m.settings.active:
		return m.renderSettingsScreen()
	case m.helpOpen:
		return m.renderHelpScreen()
	default:
		return m.renderMainScreen()
	}
}

func tooSmallMonitorScreen(width, height int) string {
	need := "60x16"
	if width > 60 || height > 16 {
		need = "bigger window"
	}
	return monitorStyleError.Render("Terminal too small for gh x monitor") +
		"\n\nResize to at least " + need + "."
}

func (m monitorModel) renderMainScreen() string {
	contentHeight := m.layout.Height - monitorFooterHeight
	mainWidth := m.layout.Width - m.layout.SidebarWidth - 1
	sidebarLines := padEachLine(strings.Split(
		renderMonitorSidebar(m.cfg.Repos, m.repoIdx, countMonitorRowsByRepo(m.data, m.cfg.Repos), contentHeight, m.layout.SidebarWidth, m.focus == monitorFocusSidebar), "\n"),
		m.layout.SidebarWidth)
	mainLines := m.buildMainLines()

	var sb strings.Builder
	for y := 0; y < contentHeight; y++ {
		sb.WriteString(padMonitorLine(sidebarAt(sidebarLines, y), m.layout.SidebarWidth))
		sb.WriteString(monitorStyleDim.Render("│"))
		sb.WriteString(padEachLine([]string{mainAt(mainLines, y)}, mainWidth)[0])
		sb.WriteString("\n")
	}
	sb.WriteString(padEachLine([]string{m.footerLine()}, m.layout.Width)[0])
	return sb.String()
}

func sidebarAt(lines []string, y int) string {
	if y < len(lines) {
		return lines[y]
	}
	return ""
}

func mainAt(lines []string, y int) string {
	if y < len(lines) {
		return lines[y]
	}
	return ""
}

func padEachLine(lines []string, width int) []string {
	padded := make([]string, len(lines))
	for i, line := range lines {
		padded[i] = padMonitorLine(line, width)
	}
	return padded
}

func padMonitorLine(line string, width int) string {
	gap := width - lipgloss.Width(line)
	if gap <= 0 {
		return line
	}
	return line + strings.Repeat(" ", gap)
}

// buildMainLines assembles tab row, sub-tabs, list, and detail pane.
func (m monitorModel) buildMainLines() []string {
	lines := []string{
		m.renderTabRow(),
		m.renderSubTabRow(),
	}
	list := m.listLines()
	lines = append(lines, strings.Split(list, "\n")...)
	lines = append(lines, m.detailLines()...)
	return lines
}

const monitorTabSlotWidth = 10

func (m monitorModel) renderTabRow() string {
	prSlot := padMonitorLine(" PRs ", monitorTabSlotWidth-2)
	isSlot := padMonitorLine(" Issues ", monitorTabSlotWidth)
	render := func(slot string, active bool) string {
		if active {
			return monitorStyleActive.Render(slot)
		}
		return monitorStyleInactive.Render(slot)
	}
	return render(prSlot, m.tab == monitorTabPRs) + render(isSlot, m.tab == monitorTabIssues)
}

func (m monitorModel) renderSubTabRow() string {
	sections := m.sectionsForTab()
	parts := make([]string, 0, len(sections)+1)
	for i, section := range sections {
		label := truncateMonitorCell(section.Title, monitorSubTabSlotWidth-3)
		slot := "[" + label + "]"
		if i == m.subTab {
			parts = append(parts, monitorStyleActive.Render(slot))
			continue
		}
		parts = append(parts, monitorStyleInactive.Render(slot))
	}
	row := strings.Join(parts, "")
	if m.filtering {
		row += "  /" + m.filter.View()
	} else if m.filter.Value() != "" {
		row += monitorStyleDim.Render("  filter: " + m.filter.Value() + " (esc clears)")
	}
	return row
}

func (m monitorModel) listLines() string {
	rows := m.visibleRows()
	if m.data == nil {
		return centeredDim("Loading GitHub data…", m.listWidth(), maxInt(m.layout.ListHeight-1, 1))
	}
	if len(rows) == 0 {
		return centeredDim(m.emptyListMessage(), m.listWidth(), maxInt(m.layout.ListHeight-1, 1))
	}
	table := renderMonitorTable(monitorTableRenderInput{
		Kind:        m.currentKind(),
		Rows:        rows,
		Cursor:      m.cursor,
		Offset:      m.offset,
		Height:      m.layout.ListHeight,
		Width:       m.listWidth(),
		ChangedKeys: m.visibleChangedKeys(),
		AddedKeys:   m.visibleAddedKeys(),
	})
	return table
}

// emptyListMessage explains an empty view and points at sections with data.
func (m monitorModel) emptyListMessage() string {
	sections := m.sectionsForTab()
	message := "No items match " + strconv.Quote(m.currentSection().Title)
	suggestions := make([]string, 0, len(sections))
	for i, section := range sections {
		if i == m.subTab {
			continue
		}
		if total := monitorSectionTotal(m.data, m.tab, i); total > 0 {
			suggestions = append(suggestions,
				fmt.Sprintf("%s has %d — press %d", section.Title, total, i+1))
		}
	}
	if len(suggestions) == 0 {
		return message
	}
	return message + " · " + strings.Join(suggestions, ", ")
}

func (m monitorModel) currentKind() monitorRowKind {
	if m.tab == monitorTabIssues {
		return monitorKindIssue
	}
	return monitorKindPR
}

// visibleChangedKeys scopes glow to the current repo selection.
func (m monitorModel) visibleChangedKeys() map[string]bool {
	return m.keysVisibleInScope(m.changedKeys)
}

func (m monitorModel) visibleAddedKeys() map[string]bool {
	return m.keysVisibleInScope(m.addedKeys)
}

func (m monitorModel) keysVisibleInScope(keys map[string]bool) map[string]bool {
	scoped := make(map[string]bool, len(keys))
	for key := range keys {
		if m.keyInScope(key) {
			scoped[key] = true
		}
	}
	return scoped
}

func (m monitorModel) keyInScope(key string) bool {
	if m.repoIdx == 0 {
		return true
	}
	repos := m.cfg.Repos
	if m.repoIdx-1 >= len(repos) {
		return false
	}
	return strings.HasPrefix(key, repos[m.repoIdx-1]+"#")
}

// detailLines renders the detail region: a separator rule spanning the full
// main-area width, then the detail body padded to the same width.
func (m monitorModel) detailLines() []string {
	width := maxInt(m.layout.Width-m.layout.SidebarWidth-1, 10)
	rule := monitorStyleDim.Render(strings.Repeat("─", width))
	bodyHeight := maxInt(m.layout.DetailHeight-1, 1)
	var body string
	if row, ok := m.selectedRow(); ok {
		body = renderMonitorDetail(row, width-2, bodyHeight, m.focus == monitorFocusDetail)
	} else {
		lines := make([]string, bodyHeight)
		lines[0] = monitorStyleDim.Render("Select a row to see details")
		body = strings.Join(lines, "\n")
	}
	lines := append([]string{rule}, padEachLine(strings.Split(body, "\n"), width)...)
	return padToMonitorLines(lines, m.layout.DetailHeight)
}

func padToMonitorLines(lines []string, height int) []string {
	if len(lines) >= height {
		return lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func (m monitorModel) listWidth() int {
	return maxInt(m.layout.Width-m.layout.SidebarWidth-3, 20)
}

func centeredDim(text string, width, height int) string {
	lines := make([]string, height)
	middle := height / 2
	for i := range lines {
		if i == middle {
			lines[i] = centerMonitorText(monitorStyleDim.Render(text), width)
			continue
		}
		lines[i] = ""
	}
	return strings.Join(lines, "\n")
}

func centerMonitorText(text string, width int) string {
	gap := (width - lipgloss.Width(text)) / 2
	if gap < 0 {
		return text
	}
	return strings.Repeat(" ", gap) + text
}

var monitorHelpLines = []string{
	"gh x monitor — keys",
	"",
	"  tab              cycle focus: list → detail → sidebar",
	"  j/k or arrows    move in the focused pane (rows / scroll / repos)",
	"  g/G home/end     jump to top/bottom of focused pane",
	"  r                refresh now",
	"  o                open selected in browser",
	"  y                copy URL",
	"  Y                copy checkout command",
	"  s                settings (repos, limit, interval)",
	"  e                edit config file in $EDITOR",
	"  ?                toggle this help",
	"  q or esc         quit",
	"",
	"  press any key to close",
}

func (m monitorModel) renderHelpScreen() string {
	return strings.Join(monitorHelpLines, "\n")
}
