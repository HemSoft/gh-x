package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// handleKey routes key presses by UI mode.
func (m monitorModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.settings.active:
		return m.handleSettingsKey(msg)
	case m.filtering:
		return m.filterInputUpdate(msg)
	case m.helpOpen:
		return m.closeHelp()
	default:
		return m.handleMainKey(msg)
	}
}

func (m monitorModel) closeHelp() (tea.Model, tea.Cmd) {
	m.helpOpen = false
	return m, nil
}

func (m monitorModel) handleSettingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if updateSettingsKey(&m.settings, msg) {
		return m.applySettingsForm()
	}
	return m, nil
}

// applySettingsForm commits the settings overlay onto the config.
func (m monitorModel) applySettingsForm() (tea.Model, tea.Cmd) {
	if err := applySettings(&m.settings, m.cfg); err != nil {
		return m, nil // error text already set on the form
	}
	if err := saveMonitorConfig(m.configPath, m.cfg); err != nil {
		m.refreshErr = "save config: " + err.Error()
		m.settings.close()
		return m, nil
	}
	m.interval = parseMonitorIntervalOrDefault(m.cfg.Defaults.Interval, defaultMonitorInterval)
	m.clampSelections()
	m.settings.close()
	return m.startRefresh()
}

func (m monitorModel) handleMainKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m.quitMonitor()
	case "esc":
		return m.escapeMonitor()
	case "/":
		m.filtering = true
		return m, m.filter.Focus()
	case "tab":
		m.cycleFocus(1)
		return m, nil
	case "shift+tab":
		m.cycleFocus(-1)
		return m, nil
	}
	if model, cmd, handled := m.handleNavigationKey(msg); handled {
		return model, cmd
	}
	return m.handleActionKey(msg)
}

// handleNavigationKey covers cursor and pane movement keys.
func (m monitorModel) handleNavigationKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "j", "down":
		return m.moveCursor(1), nil, true
	case "k", "up":
		return m.moveCursor(-1), nil, true
	case "g", "home":
		m.jumpToPaneEdge(-1)
		return m, nil, true
	case "G", "end":
		m.jumpToPaneEdge(1)
		return m, nil, true
	case "h", "left":
		model, cmd := m.switchTab(-1)
		return model, cmd, true
	case "l", "right":
		model, cmd := m.switchTab(1)
		return model, cmd, true
	default:
		return m, nil, false
	}
}

// cycleFocus moves focus list → detail → sidebar → list.
func (m *monitorModel) cycleFocus(delta int) {
	m.focus = (m.focus + delta + monitorFocusCount) % monitorFocusCount
	if m.focus != monitorFocusDetail {
		m.detailScroll = 0
	}
}

// handleActionKey covers commands that act on data or the session.
func (m monitorModel) handleActionKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "r", "R":
		return m.startRefresh()
	case "s":
		m.settings.open(m.cfg)
		return m, nil
	case "e":
		return m.editConfigCmd()
	case "?":
		m.helpOpen = true
		return m, nil
	case "o":
		return m.openSelectedInBrowser(), nil
	case "y":
		return m.copySelectedURL(false)
	case "Y":
		return m.copySelectedURL(true)
	default:
		if index, ok := digitSectionIndex(msg.String()); ok {
			return m.jumpToSection(index)
		}
		return m.scrollDetail(msg)
	}
}

// jumpToPaneEdge moves to the top/bottom of the focused pane.
func (m *monitorModel) jumpToPaneEdge(direction int) {
	if direction < 0 {
		switch m.focus {
		case monitorFocusSidebar:
			m.moveRepoSelection(-len(m.cfg.Repos))
		case monitorFocusDetail:
			m.scrollDetailBy(-1 << 20)
		default:
			m.setCursor(0)
		}
		return
	}
	switch m.focus {
	case monitorFocusSidebar:
		m.moveRepoSelection(len(m.cfg.Repos))
	case monitorFocusDetail:
		m.scrollDetailBy(1 << 20)
	default:
		m.setCursor(maxInt(len(m.visibleRows())-1, 0))
	}
}

func digitSectionIndex(text string) (int, bool) {
	if len(text) != 1 || text[0] < '1' || text[0] > '9' {
		return 0, false
	}
	return int(text[0] - '1'), true
}

func (m monitorModel) jumpToSection(index int) (tea.Model, tea.Cmd) {
	if index < len(m.sectionsForTab()) {
		m.subTab = index
		m.resetListScroll()
	}
	return m, nil
}

func (m monitorModel) switchTab(delta int) (tea.Model, tea.Cmd) {
	m.tab = (m.tab + delta + monitorTabCount) % monitorTabCount
	m.applyDefaultSubTab()
	m.resetListScroll()
	return m, nil
}

func (m *monitorModel) resetListScroll() {
	m.cursor = 0
	m.offset = 0
	m.detailScroll = 0
}

func (m monitorModel) moveCursor(delta int) tea.Model {
	switch m.focus {
	case monitorFocusDetail:
		m.scrollDetailBy(delta)
	case monitorFocusSidebar:
		m.moveRepoSelection(delta)
	default:
		rows := len(m.visibleRows())
		if rows == 0 {
			return m
		}
		m.setCursor(clampInt(m.cursor+delta, 0, rows-1))
	}
	return m
}

// moveRepoSelection changes the sidebar selection and refreshes the list.
func (m *monitorModel) moveRepoSelection(delta int) {
	m.repoIdx = clampInt(m.repoIdx+delta, 0, len(m.cfg.Repos))
	m.resetListScroll()
}

func (m *monitorModel) setCursor(index int) {
	m.cursor = clampInt(index, 0, maxInt(len(m.visibleRows())-1, 0))
	m.ensureCursorVisible()
	if row, ok := m.selectedRow(); ok {
		m.markSeen(row)
	}
}

// ensureCursorVisible keeps the cursor row inside the scroll window.
func (m *monitorModel) ensureCursorVisible() {
	height := maxInt(m.layout.ListHeight-1, 1)
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+height {
		m.offset = m.cursor - height + 1
	}
}

func (m monitorModel) scrollDetail(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "pgup":
		m.scrollDetailBy(-maxInt(m.layout.DetailHeight-2, 1))
	case "pgdown":
		m.scrollDetailBy(maxInt(m.layout.DetailHeight-2, 1))
	}
	return m, nil
}

func (m *monitorModel) scrollDetailBy(delta int) {
	m.detailScroll = clampInt(m.detailScroll+delta, 0, detailBodyLineCount(*m)-1)
}

func detailBodyLineCount(m monitorModel) int {
	row, ok := m.selectedRow()
	if !ok {
		return 0
	}
	width := maxInt(m.layout.Width-m.layout.SidebarWidth-6, 10)
	return len(wrapMonitorBody(row.Body, width))
}

func (m monitorModel) escapeMonitor() (tea.Model, tea.Cmd) {
	if m.filter.Value() != "" {
		m.filter.SetValue("")
		m.cursor = 0
		m.offset = 0
		return m, nil
	}
	return m.quitMonitor()
}

func (m monitorModel) quitMonitor() (tea.Model, tea.Cmd) {
	m.quitting = true
	if err := saveMonitorState(m.statePath, monitorSessionState{Tab: m.tab, SubTab: m.subTab, RepoIndex: m.repoIdx}); err != nil {
		fmt.Fprintf(accountWarningWriter, "[gh-x] note: could not save session state: %v\n", err)
	}
	return m, tea.Quit
}

// handleClick resolves mouse clicks against the layout.
func (m monitorModel) handleClick(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	hit := hitMonitorLocation(m.layout, mouse.X, mouse.Y)
	switch hit.area {
	case "sidebar":
		return m.selectSidebarIndex(hit.index), nil
	case "tab":
		return m.selectTabByClick(hit.index), nil
	case "subtab":
		return m.selectSubTabByClick(hit.index)
	case "list":
		m.focus = monitorFocusList
		m.setCursor(hit.index + m.offset)
		return m, nil
	default:
		m.focus = monitorFocusDetail
		return m, nil
	}
}

func (m monitorModel) selectSidebarIndex(index int) tea.Model {
	if index < 0 || index > len(m.cfg.Repos) {
		return m
	}
	m.focus = monitorFocusSidebar
	m.repoIdx = index
	m.resetListScroll()
	return m
}

func (m monitorModel) selectTabByClick(slot int) tea.Model {
	tab := clampInt(slot, 0, monitorTabCount-1)
	if tab == m.tab {
		return m
	}
	m.tab = tab
	m.applyDefaultSubTab()
	m.resetListScroll()
	return m
}

func (m monitorModel) selectSubTabByClick(slot int) (tea.Model, tea.Cmd) {
	if slot < len(m.sectionsForTab()) {
		m.subTab = slot
		m.resetListScroll()
	}
	return m, nil
}

// handleWheel scrolls the focused pane.
func (m monitorModel) handleWheel(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	const wheelStep = 3
	delta := wheelStep
	if isWheelUp(mouse.Button) {
		delta = -wheelStep
	}
	switch m.focus {
	case monitorFocusDetail:
		m.scrollDetailBy(delta)
	case monitorFocusSidebar:
		m.moveRepoSelection(delta)
	default:
		rows := len(m.visibleRows())
		m.setCursor(clampInt(m.cursor+delta, 0, maxInt(rows-1, 0)))
	}
	return m, nil
}

func isWheelUp(button tea.MouseButton) bool {
	return button == tea.MouseWheelUp
}
