package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"os"
)

// Focus targets for pane navigation.
const (
	monitorFocusList = iota
	monitorFocusDetail
	monitorFocusSidebar
	monitorFocusCount
)

// monitorModel is the root Bubble Tea model for gh x monitor.
type monitorModel struct {
	cfg        *monitorConfig
	configPath string
	statePath  string

	theme  monitorTheme
	layout monitorLayout
	ready  bool

	tab     int // PRs | Issues
	subTab  int
	repoIdx int // 0 = All repos
	cursor  int
	offset  int

	focus        int // list | detail | sidebar
	detailScroll int

	filtering bool
	filter    textinput.Model

	data           *monitorFetchResult
	changedKeys    map[string]bool
	addedKeys      map[string]bool
	seenKeys       map[string]bool
	lastChanges    []monitorChange
	lastRefresh    time.Time
	refreshErr     string
	refreshWarn    string
	refreshing     bool
	refreshContext context.Context
	cancelRefresh  context.CancelFunc
	refreshState   *monitorRefreshState
	interval       time.Duration
	backoff        time.Duration

	helpOpen bool
	settings monitorSettingsModel
	quitting bool
}

type monitorTickMsg time.Time

type monitorFetchedMsg struct {
	result *monitorFetchResult
	err    error
	at     time.Time
}

func newMonitorModel(cfg *monitorConfig, configPath, statePath string, state monitorSessionState) monitorModel {
	clamped := clampMonitorState(state, monitorTabCount, len(cfg.PRSections), len(cfg.IssueSections), len(cfg.Repos)+1)
	filter := textinput.New()
	filter.Placeholder = "filter…"
	refreshContext, cancelRefresh := context.WithCancel(context.Background())
	model := monitorModel{
		cfg:            cfg,
		theme:          newMonitorTheme(true, os.Getenv("NO_COLOR") != ""),
		refreshContext: refreshContext,
		cancelRefresh:  cancelRefresh,
		refreshState:   newMonitorRefreshState(),
		refreshing:     true,
		configPath:     configPath,
		statePath:      statePath,
		tab:            clamped.Tab,
		subTab:         clamped.SubTab,
		repoIdx:        clamped.RepoIndex,
		focus:          monitorFocusSidebar,
		interval:       parseMonitorIntervalOrDefault(cfg.Defaults.Interval, defaultMonitorInterval),
		backoff:        minimumMonitorInterval,
		changedKeys:    map[string]bool{},
		addedKeys:      map[string]bool{},
		seenKeys:       map[string]bool{},
		filter:         filter,
		settings:       newMonitorSettingsModel(),
	}
	model.applyMonitorTheme()
	return model
}

func (m monitorModel) Init() tea.Cmd {
	return tea.Batch(m.initialMonitorCmd(), tea.RequestBackgroundColor)
}

// sectionsForTab returns the active tab's configured sections.
func (m monitorModel) sectionsForTab() []monitorSection {
	if m.tab == monitorTabIssues {
		return m.cfg.IssueSections
	}
	return m.cfg.PRSections
}

// applyDefaultSubTab lands on the "All open" section of the active tab, so
// each view opens on everything. Configs without such a section keep the
// current selection.
func (m *monitorModel) applyDefaultSubTab() {
	m.subTab = clampInt(m.subTab, 0, maxInt(len(m.sectionsForTab())-1, 0))
	if idx := monitorAllOpenIndex(m.sectionsForTab()); idx >= 0 {
		m.subTab = idx
	}
}

func monitorAllOpenIndex(sections []monitorSection) int {
	for i, section := range sections {
		if strings.EqualFold(strings.TrimSpace(section.Title), "All open") {
			return i
		}
	}
	return -1
}

// currentSection resolves the active section, falling back safely.
func (m monitorModel) currentSection() monitorSection {
	sections := m.sectionsForTab()
	if len(sections) == 0 {
		return monitorSection{Title: "All"}
	}
	return sections[clampInt(m.subTab, 0, len(sections)-1)]
}

// visibleRows computes the rows matching repo selection and filter text.
func (m monitorModel) visibleRows() []monitorRow {
	return computeVisibleRows(m.data, m.tab, m.subTab, m.repoIdx, m.cfg.Repos, m.filter.Value())
}

// computeVisibleRows is the pure core of visibleRows for testability.
func computeVisibleRows(data *monitorFetchResult, tab, subTab, repoIdx int, repos []string, filterText string) []monitorRow {
	if data == nil {
		return nil
	}
	sections := data.PRSections
	if tab == monitorTabIssues {
		sections = data.IssueSections
	}
	if subTab < 0 || subTab >= len(sections) {
		return nil
	}
	rows := sections[subTab].Rows
	if repoIdx > 0 && repoIdx <= len(repos) {
		rows = filterMonitorRowsByRepo(rows, repos[repoIdx-1])
	}
	return filterMonitorRowsByText(rows, filterText)
}

func filterMonitorRowsByRepo(rows []monitorRow, nameWithOwner string) []monitorRow {
	filtered := make([]monitorRow, 0, len(rows))
	for _, row := range rows {
		if row.Repo == nameWithOwner {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func filterMonitorRowsByText(rows []monitorRow, text string) []monitorRow {
	needle := strings.ToLower(strings.TrimSpace(text))
	if needle == "" {
		return rows
	}
	filtered := make([]monitorRow, 0, len(rows))
	for _, row := range rows {
		if monitorRowMatchesText(row, needle) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

func monitorRowMatchesText(row monitorRow, needle string) bool {
	haystacks := []string{
		row.Title, row.Author, row.Repo, row.State,
		strconv.Itoa(row.Number),
		strings.Join(row.Labels, " "), row.Assignees, row.Branch,
	}
	for _, hay := range haystacks {
		if strings.Contains(strings.ToLower(hay), needle) {
			return true
		}
	}
	return false
}

func (m monitorModel) selectedRow() (monitorRow, bool) {
	rows := m.visibleRows()
	if len(rows) == 0 || m.cursor < 0 || m.cursor >= len(rows) {
		return monitorRow{}, false
	}
	return rows[m.cursor], true
}

func formatMonitorClock(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Local().Format("15:04:05")
}

func hiddenReposSummary(repos []string, data *monitorFetchResult) string {
	if data == nil || len(data.Accessible) == 0 {
		return ""
	}
	var hidden []string
	for _, repo := range repos {
		if accessible, known := data.Accessible[repo]; known && !accessible {
			hidden = append(hidden, repo)
		}
	}
	switch len(hidden) {
	case 0:
		return ""
	case 1:
		return truncateMonitorCell(hidden[0]+" hidden", 40)
	default:
		return fmt.Sprintf("%d repos hidden", len(hidden))
	}
}

func monitorTabLabel(tab int) string {
	if tab == monitorTabIssues {
		return "Issues"
	}
	return "PRs"
}

func monitorSectionTotal(data *monitorFetchResult, tab, subTab int) int {
	if data == nil {
		return 0
	}
	sections := data.PRSections
	if tab == monitorTabIssues {
		sections = data.IssueSections
	}
	if subTab < 0 || subTab >= len(sections) {
		return 0
	}
	return sections[subTab].Total
}

// RateRemainingSafe avoids panicking on a nil data payload.
func (r *monitorFetchResult) RateRemainingSafe() int {
	if r == nil {
		return 0
	}
	return r.RateRemaining
}
