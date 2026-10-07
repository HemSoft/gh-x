package main

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"strconv"
	"strings"
	"unicode"

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
	return lipgloss.NewStyle().Bold(true).Render("Terminal too small for gh x monitor") +
		"\n\nResize to at least " + need + "."
}

func (m monitorModel) renderMainScreen() string {
	mainWidth := m.layout.Width - m.layout.MainLeft
	sidebarLines := strings.Split(renderMonitorSidebar(m.cfg.Repos, m.repoIdx,
		countMonitorRowsByRepo(m.data, m.cfg.Repos), m.layout.FooterTop-1,
		m.layout.SidebarWidth, m.focus == monitorFocusSidebar, m.theme), "\n")
	mainLines := m.buildMainLines()
	lines := []string{m.dashboardHeading()}
	for y := m.layout.TabTop; y < m.layout.FooterTop; y++ {
		line := ""
		if m.layout.SidebarWidth > 0 {
			line = fitMonitorLine(sidebarAt(sidebarLines, y-m.layout.SidebarTop), m.layout.SidebarWidth) + m.theme.Muted.Render("│")
		}
		line += fitMonitorLine(mainAt(mainLines, y-m.layout.TabTop), mainWidth)
		lines = append(lines, m.theme.Text.Render(line))
	}
	lines = append(lines, m.helpFooter(), m.footerLine())
	return strings.Join(lines, "\n")
}

func fitMonitorLine(line string, width int) string {
	width = maxInt(width, 0)
	line = ansi.Truncate(line, width, "")
	return padMonitorLine(line, width)
}

func monitorPlainCell(text string) string {
	return strings.Join(strings.Fields(monitorSafeText(text)), " ")
}

func monitorSafeText(text string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, ansi.Strip(text))
}

func (m monitorModel) dashboardHeading() string {
	scope := monitorRepoAll
	if m.repoIdx > 0 && m.repoIdx <= len(m.cfg.Repos) {
		scope = m.cfg.Repos[m.repoIdx-1]
	}
	label := "  repo: " + monitorPlainCell(scope)
	if m.focus == monitorFocusSidebar {
		label += " [focus]"
	}
	left := m.theme.Accent.Render(" GH X / MONITOR") + m.theme.Muted.Render(label)
	right := fmt.Sprintf(" %d PRs · %d issues ", m.tabTotal(monitorTabPRs), m.tabTotal(monitorTabIssues))
	budget := maxInt(m.layout.Width-lipgloss.Width(right), 0)
	return m.theme.Surface.Render(fitMonitorLine(left, budget) + right)
}

// Tab counts refer to the broad All open section, independent of active section.
func (m monitorModel) tabTotal(tab int) int {
	sections := m.cfg.PRSections
	if tab == monitorTabIssues {
		sections = m.cfg.IssueSections
	}
	return monitorSectionTotal(m.data, tab, maxInt(monitorAllOpenIndex(sections), 0))
}

func (m monitorModel) helpFooter() string {
	focus := []string{"list", "details", "repos"}[m.focus]
	text := " " + focus + " focus · tab panes · j/k move · / filter · r refresh · ? help · q quit"
	if m.filter.Value() != "" {
		text = " /" + monitorPlainCell(m.filter.Value()) + " · esc clear ·" + text
	}
	return m.theme.Heading.Render(fitMonitorLine(text, m.layout.Width))
}

func (m monitorModel) footerLine() string {
	width := maxInt(m.layout.Width, 1)
	left := fmt.Sprintf(" %s · %d/%d rows", monitorTabLabel(m.tab), len(m.visibleRows()), monitorSectionTotal(m.data, m.tab, m.subTab))
	right := fmt.Sprintf("rate %d · last %s ", m.data.RateRemainingSafe(), formatMonitorClock(m.lastRefresh))
	notice := ""
	if m.refreshing {
		right = "refreshing… "
		notice = "refreshing… · "
	}
	if m.refreshErr != "" {
		return m.theme.Error.Render(fitMonitorLine(" "+notice+"error: r retry · data retained · "+monitorPlainCell(m.refreshErr), width))
	}
	if m.refreshWarn != "" {
		return m.theme.Warning.Render(fitMonitorLine(" "+notice+"warning: r retry · partial data · "+monitorPlainCell(m.refreshWarn), width))
	}
	if hidden := hiddenReposSummary(m.cfg.Repos, m.data); hidden != "" {
		left += " · " + hidden
	}
	if len(m.lastChanges) > 0 {
		left += " · " + summarizeMonitorChanges(m.lastChanges, 1)
	}
	budget := maxInt(width-lipgloss.Width(right), 0)
	return m.theme.Muted.Render(fitMonitorLine(left, budget) + fitMonitorLine(right, minInt(width, lipgloss.Width(right))))
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
		padded[i] = fitMonitorLine(line, width)
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

func (m monitorModel) renderTabRow() string {
	var out strings.Builder
	for i, label := range []string{"PRs", "Issues"} {
		slot := fitMonitorLine(fmt.Sprintf(" %s (%d)", label, m.tabTotal(i)), monitorTabSlotWidth)
		style := m.theme.Muted
		if i == m.tab {
			style = m.theme.Selected
		}
		out.WriteString(style.Render(slot))
	}
	return out.String()
}

func (m monitorModel) firstVisibleSection() int {
	slots := maxInt(m.listWidth()/monitorSubTabSlotWidth, 1)
	return maxInt(m.subTab-slots+1, 0)
}

func (m monitorModel) renderSubTabRow() string {
	if m.filtering {
		return m.theme.Accent.Render(" / ") + m.filter.View()
	}
	sections := m.sectionsForTab()
	var out strings.Builder
	for i := m.firstVisibleSection(); i < len(sections); i++ {
		label := truncateMonitorCell(monitorPlainCell(sections[i].Title), monitorSubTabSlotWidth-6)
		slot := fitMonitorLine(fmt.Sprintf("%d [%s]", i+1, label), monitorSubTabSlotWidth)
		style := m.theme.Muted
		if i == m.subTab {
			style = m.theme.Accent
		}
		out.WriteString(style.Render(slot))
	}
	if m.filter.Value() != "" {
		out.WriteString(m.theme.Warning.Render(" /" + monitorPlainCell(m.filter.Value())))
	}
	return out.String()
}

func (m monitorModel) listLines() string {
	rows := m.visibleRows()
	if m.data == nil {
		return m.theme.Muted.Render(centeredDim("Loading GitHub data…", m.listWidth(), maxInt(m.layout.ListHeight, 1)))
	}
	if len(rows) == 0 {
		return m.theme.Muted.Render(centeredDim(m.emptyListMessage(), m.listWidth(), maxInt(m.layout.ListHeight, 1)))
	}
	table := renderMonitorTable(monitorTableRenderInput{
		Kind:        m.currentKind(),
		Rows:        rows,
		Cursor:      m.cursor,
		Offset:      m.offset,
		Height:      m.layout.ListHeight,
		Width:       m.listWidth(),
		Theme:       m.theme,
		Focused:     m.focus == monitorFocusList,
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
	width := m.layout.Width - m.layout.MainLeft
	style := m.theme.Muted
	if m.focus == monitorFocusDetail {
		style = m.theme.Accent
	}
	label := " DETAILS "
	if m.focus == monitorFocusDetail {
		label += "[focus] "
	}
	header := style.Render(fitMonitorLine(label+strings.Repeat("─", maxInt(width-lipgloss.Width(label), 0)), width))
	bodyHeight := maxInt(m.layout.DetailHeight-1, 1)
	body := m.theme.Muted.Render("Select a row to see details")
	if row, ok := m.selectedRow(); ok {
		body = renderMonitorDetail(row, width, bodyHeight, m.focus == monitorFocusDetail, m.detailScroll, m.theme)
	}
	lines := append([]string{header}, strings.Split(body, "\n")...)
	for i := range lines {
		lines[i] = fitMonitorLine(lines[i], width)
	}
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
	return maxInt(m.layout.Width-m.layout.MainLeft-1, 20)
}

func centeredDim(text string, width, height int) string {
	lines := make([]string, height)
	middle := height / 2
	for i := range lines {
		if i == middle {
			lines[i] = centerMonitorText(lipgloss.NewStyle().Faint(true).Render(text), width)
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
	"  tab / shift+tab   focus list, details, or repositories",
	"  j/k up/down       move pane; g/G home/end jump edges",
	"  left/right        PR/issue tabs; 1–9 select section",
	"  / enter esc       type/apply/clear filter",
	"  pgup/pgdown       scroll details",
	"  mouse / wheel     select; scroll focused pane",
	"  r                 refresh / retry",
	"  o y Y             open / copy URL / copy checkout",
	"  s e               settings / edit YAML",
	"  q ctrl+c          quit; esc clears filter or quits",
	"  ?                 open help",
	"",
	"  press any key to close",
}

func (m monitorModel) renderHelpScreen() string {
	lines := []string{m.theme.Accent.Render(" GH X / MONITOR · keys"), ""}
	for _, line := range monitorHelpLines[2:] {
		lines = append(lines, m.theme.Text.Render(line))
	}
	for i := range lines {
		lines[i] = fitMonitorLine(lines[i], m.layout.Width)
	}
	return strings.Join(padToMonitorLines(lines, m.layout.Height), "\n")
}
