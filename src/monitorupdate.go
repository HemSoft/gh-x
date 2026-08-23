package main

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// monitorNowFunc is swappable in tests.
var monitorNowFunc = time.Now

// Update dispatches messages; each branch delegates to a small handler.
func (m monitorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)
	case monitorFetchedMsg:
		return m.handleFetched(msg)
	case monitorTickMsg:
		return m.startRefresh()
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.MouseClickMsg:
		return m.handleClick(msg.Mouse())
	case tea.MouseWheelMsg:
		return m.handleWheel(msg.Mouse())
	case monitorEditorDoneMsg:
		return m.handleEditorDone(msg)
	default:
		return m, nil
	}
}

func (m monitorModel) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.layout = computeMonitorLayout(msg.Width, msg.Height)
	m.ready = true
	m.cursor = m.clampedCursor()
	return m, nil
}

func (m monitorModel) clampedCursor() int {
	return clampInt(m.cursor, 0, maxInt(len(m.visibleRows())-1, 0))
}

func (m monitorModel) startRefresh() (tea.Model, tea.Cmd) {
	if m.refreshing {
		return m, nil
	}
	m.refreshing = true
	return m, newMonitorFetchCmd(*m.cfg)
}

// newMonitorFetchCmd snapshots the config for one refresh cycle.
func newMonitorFetchCmd(cfg monitorConfig) tea.Cmd {
	return func() tea.Msg {
		result, err := executeMonitorFetch(&cfg, monitorNowFunc())
		return monitorFetchedMsg{result: result, err: err, at: monitorNowFunc()}
	}
}

// initialMonitorCmd performs the first fetch.
// The refreshing flag is set by the caller before Init runs.
func (m monitorModel) initialMonitorCmd() tea.Cmd {
	return newMonitorFetchCmd(*m.cfg)
}

func (m monitorModel) handleFetched(msg monitorFetchedMsg) (tea.Model, tea.Cmd) {
	m.refreshing = false
	if msg.err != nil {
		m.refreshErr = sanitizeMonitorError(msg.err)
		m.refreshWarn = ""
		m.backoff = nextMonitorBackoff(m.backoff)
		return m, scheduleMonitorTick(m.backoff)
	}
	m.applyFetchResult(msg.result)
	return m, scheduleMonitorTick(m.interval)
}

func sanitizeMonitorError(err error) string {
	return sanitizeMonitorMessage(err.Error())
}

func sanitizeMonitorMessage(text string) string {
	const maxLen = 160
	if len([]rune(text)) > maxLen {
		text = string([]rune(text)[:maxLen]) + "…"
	}
	return strings.ReplaceAll(text, "\n", " ")
}

func nextMonitorBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > maximumMonitorInterval {
		return maximumMonitorInterval
	}
	return next
}

func scheduleMonitorTick(after time.Duration) tea.Cmd {
	return tea.Tick(after, func(at time.Time) tea.Msg {
		return monitorTickMsg(at)
	})
}

// applyFetchResult merges a successful refresh into the model, diffing
// against previous rows per section for change tracking.
func (m *monitorModel) applyFetchResult(result *monitorFetchResult) {
	var changes []monitorChange
	if m.data != nil {
		changes = append(changes, diffMonitorSections(m.data.PRSections, result.PRSections)...)
		changes = append(changes, diffMonitorSections(m.data.IssueSections, result.IssueSections)...)
	}
	m.data = result
	m.lastChanges = changes
	m.changedKeys = changedKeysSet(changes)
	m.addedKeys = addedKeysSet(changes)
	m.lastRefresh = result.FetchedAt
	m.refreshErr = ""
	m.refreshWarn = sanitizeMonitorMessage(strings.Join(result.Warnings, "; "))
	m.backoff = minimumMonitorInterval
	m.clampSelections()
}

// diffMonitorSections pairs up same-index sections and reconciles rows.
func diffMonitorSections(previous, current []monitorSectionData) []monitorChange {
	count := minInt(len(previous), len(current))
	changes := make([]monitorChange, 0)
	for i := range count {
		_, sectionChanges := reconcileMonitorRows(previous[i].Rows, current[i].Rows)
		changes = append(changes, sectionChanges...)
	}
	return changes
}

// addedKeysSet collects keys of newly added rows for the green glow.
func addedKeysSet(changes []monitorChange) map[string]bool {
	keys := make(map[string]bool)
	for _, change := range changes {
		if change.Kind == monitorChangeAdded {
			keys[change.Key] = true
		}
	}
	return keys
}

func (m *monitorModel) clampSelections() {
	m.subTab = clampInt(m.subTab, 0, maxInt(len(m.sectionsForTab())-1, 0))
	m.repoIdx = clampInt(m.repoIdx, 0, len(m.cfg.Repos))
	m.cursor = m.clampedCursor()
	m.offset = clampInt(m.offset, 0, maxInt(len(m.visibleRows())-1, 0))
}

// markSeen records that the user looked at the cursor row, clearing glow.
func (m *monitorModel) markSeen(row monitorRow) {
	m.seenKeys[row.key()] = true
	delete(m.changedKeys, row.key())
	delete(m.addedKeys, row.key())
}

// filterInputUpdate forwards keys to the filter input while filtering.
func (m monitorModel) filterInputUpdate(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.filtering = false
		m.filter.SetValue("")
		m.filter.Blur()
		return m, nil
	}
	if msg.String() == "enter" {
		m.filtering = false
		m.filter.Blur()
		return m, nil
	}
	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.cursor = 0
	m.offset = 0
	return m, cmd
}
