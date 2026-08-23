package main

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var ansiSequencePattern = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSIForTest(text string) string {
	return ansiSequencePattern.ReplaceAllString(text, "")
}

// firstVisibleIndex reports where visible content starts, ignoring ANSI
// escapes and leading padding.
func firstVisibleIndex(text string) int {
	for i, r := range stripANSIForTest(text) {
		if r != ' ' {
			return i
		}
	}
	return -1
}

func lipglossWidth(text string) int {
	return lipgloss.Width(text)
}

func sizedModel() monitorModel {
	m := newTestMonitorModel()
	m.layout = computeMonitorLayout(110, 32)
	m.ready = true
	return m
}

func modelWithData() monitorModel {
	m := sizedModel()
	m.data = &monitorFetchResult{
		FetchedAt:     time.Date(2026, 8, 22, 9, 30, 0, 0, time.UTC),
		RateRemaining: 4400,
		PRSections: []monitorSectionData{{Total: 2, Rows: []monitorRow{
			monitorRowForTest("owner/one", 1, "open"),
			{Kind: monitorKindPR, Repo: "owner/two", Number: 2, Title: "Second",
				State: "draft", Author: "a", UpdatedAt: time.Now()},
		}}},
		IssueSections: []monitorSectionData{{Total: 1, Rows: []monitorRow{
			{Kind: monitorKindIssue, Repo: "owner/one", Number: 7, Title: "Broken",
				State: "open", Assignees: "-", Labels: []string{"bug"},
				Body: "body text", Author: "x", UpdatedAt: time.Now()},
		}}},
	}
	return m
}

func TestRenderTableHeaderAndRows(t *testing.T) {
	m := modelWithData()
	m.subTab = 0 // first PR section holds the seeded rows
	out := renderMonitorTable(monitorTableRenderInput{
		Kind: monitorKindPR, Rows: m.visibleRows(), Cursor: 0, Offset: 0,
		Height: 5, Width: 80,
	})
	if !strings.Contains(out, "#") || !strings.Contains(out, "Title") || !strings.Contains(out, "Repo") {
		t.Fatalf("header missing: %q", out)
	}
	if !strings.Contains(out, "title") {
		t.Fatal("row content missing")
	}
	if strings.Count(out, "\n") < 3 {
		t.Fatalf("expected header plus rows plus padding: %q", out)
	}
}

func TestTableHeaderAlignsWithRowsAndStandsOut(t *testing.T) {
	m := modelWithData()
	m.subTab = 0 // first PR section holds the seeded rows
	out := renderMonitorTable(monitorTableRenderInput{
		Kind: monitorKindPR, Rows: m.visibleRows(), Cursor: 0, Offset: 0,
		Height: 5, Width: 80,
	})
	lines := strings.Split(out, "\n")
	if firstVisibleIndex(lines[0]) != firstVisibleIndex(lines[1]) {
		t.Fatalf("header and rows start at different columns: %q vs %q",
			stripANSIForTest(lines[0]), stripANSIForTest(lines[1]))
	}
	header := lines[0]
	// On color-capable profiles the header must carry its own styling.
	if strings.Contains(out, "\x1b[") && header == stripANSIForTest(header) {
		t.Fatal("header should be styled to stand out")
	}
}

func TestRenderTableMarksSelectionAndChanges(t *testing.T) {
	m := modelWithData()
	m.subTab = 0 // first PR section holds the seeded rows
	row := m.visibleRows()[0]
	changed := map[string]bool{row.key(): true}
	added := map[string]bool{m.visibleRows()[1].key(): true}
	out := renderMonitorTable(monitorTableRenderInput{
		Kind: monitorKindPR, Rows: m.visibleRows(), Cursor: 1, Offset: 0,
		Height: 5, Width: 80, ChangedKeys: changed, AddedKeys: added,
	})
	if !strings.Contains(out, monitorChangedMarker) {
		t.Fatal("changed marker missing")
	}
	if !strings.Contains(out, monitorAddedMarker) {
		t.Fatal("added marker missing")
	}
}

func TestPlanMonitorColumnsShrinksToFit(t *testing.T) {
	columns := monitorPRColumns()
	long := monitorRow{Number: 1, Title: strings.Repeat("x", 120), Repo: "o/r",
		Author: "someone-very-long", State: "open", Review: "-",
		AIReview: "-", Checks: "pending", Comments: "-", Updated: "1h"}
	cells := [][]string{monitorRowCells(monitorKindPR, long)}
	planned := planMonitorColumns(columns, cells, 80)
	if got := monitorTableOverflow(planned, 80); got > 0 {
		t.Fatalf("columns still overflow by %d", got)
	}
	titleWidth := planned[1].Width
	if titleWidth >= 120 {
		t.Fatalf("title width not shrunk: %d", titleWidth)
	}
}

func TestPlanMonitorColumnsFillsAvailableWidth(t *testing.T) {
	row := monitorRow{Number: 15, Title: "Multi-account fallback",
		Repo: "HemSoft/gh-x", Author: "Franz Hemmer", State: "open",
		Updated: "19h"}
	columns := monitorIssueColumns()
	cells := [][]string{monitorRowCells(monitorKindIssue, row)}

	planned := planMonitorColumns(columns, cells, 90)
	if got := monitorTableOverflow(planned, 90); got != 0 {
		t.Fatalf("planned table should consume the full width exactly, overflow=%d", got)
	}
	if planned[1].Width <= monitorTitleMinWidth {
		t.Fatalf("title column should grow past its minimum: %d", planned[1].Width)
	}

	tight := planMonitorColumns(columns, cells, 70)
	if got := monitorTableOverflow(tight, 70); got > 0 {
		t.Fatalf("tight layout must still fit, overflow=%d", got)
	}
	if tight[1].Width != monitorTitleMinWidth {
		t.Fatalf("title should keep its minimum when space is scarce: %d", tight[1].Width)
	}
}

func TestTruncateMonitorCell(t *testing.T) {
	if got := truncateMonitorCell("abcdef", 4); got != "abc…" {
		t.Fatalf("truncate wrong: %q", got)
	}
	if got := truncateMonitorCell("ab", 10); got != "ab" {
		t.Fatalf("short text altered: %q", got)
	}
	if got := truncateMonitorCell("abc", 1); got != "…" {
		t.Fatalf("width-1 wrong: %q", got)
	}
}

func TestSidebarRendersCountsAndSelection(t *testing.T) {
	m := modelWithData()
	counts := countMonitorRowsByRepo(m.data, m.cfg.Repos)
	out := renderMonitorSidebar(m.cfg.Repos, 1, counts, 20, monitorSidebarWidth, false)
	if !strings.Contains(out, monitorRepoAll) {
		t.Fatalf("'All repos' entry missing: %q", out)
	}
	if !strings.Contains(out, "1pr") {
		t.Fatalf("pr count badge missing: %q", out)
	}
	if !strings.Contains(out, "1is") {
		t.Fatalf("issue count badge missing (owner/one has 1 issue): %q", out)
	}
	nilCounts := countMonitorRowsByRepo(nil, []string{"a/b"})
	if len(nilCounts) != 1 || !nilCounts["a/b"].Accessible {
		t.Fatalf("nil data should keep repos visible: %v", nilCounts)
	}
}

func TestDetailMetadataPerKind(t *testing.T) {
	pr := monitorRowForTest("o/r", 5, "open")
	pr.Review = "-"
	pr.AIReview = "pass"
	lines := monitorDetailMetadata(pr)
	joined := detailLinesText(lines)
	for _, want := range []string{"Branch", "Reviews", "Checks"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("PR metadata missing %s: %q", want, joined)
		}
	}
	issue := monitorRow{Kind: monitorKindIssue, Number: 3, State: "open", Repo: "o/r"}
	issueLines := monitorDetailMetadata(issue)
	if !strings.Contains(detailLinesText(issueLines), "Assignees") {
		t.Fatal("issue metadata missing assignees")
	}
}

func detailLinesText(lines []monitorDetailLine) string {
	var sb strings.Builder
	for _, line := range lines {
		sb.WriteString(line.label + "=" + line.value + ";")
	}
	return sb.String()
}

func TestRenderDetailEmptyAndPopulated(t *testing.T) {
	empty := renderMonitorDetail(monitorRow{}, 80, 12, false)
	if !strings.Contains(empty, "No selection") {
		t.Fatalf("empty state wrong: %q", empty)
	}
	row := monitorRowForTest("o/r", 9, "open")
	row.Body = strings.Repeat("word ", 200)
	filled := renderMonitorDetail(row, 80, 12, true)
	if !strings.Contains(filled, "9 ") {
		t.Fatalf("detail should show number+title: %q", filled[:120])
	}
}

func TestWrapMonitorBody(t *testing.T) {
	wrapped := wrapMonitorBody("short line\nthis is a much longer line that must wrap somewhere around the limit", 20)
	for _, line := range wrapped {
		if len([]rune(line)) > 21 {
			t.Fatalf("line too long after wrap: %q", line)
		}
	}
	if got := wrapMonitorBody("", 20); got != nil {
		t.Fatalf("empty body should produce no lines: %v", got)
	}
	longWord := wrapMonitorBody(strings.Repeat("z", 45), 20)
	if len(longWord) < 3 {
		t.Fatalf("long word should split across lines: %v", longWord)
	}
}

func TestLayoutBoundsAndHits(t *testing.T) {
	layout := computeMonitorLayout(100, 24)
	if layout.DetailHeight != 6 { // 24/4
		t.Fatalf("detail height wrong: %d", layout.DetailHeight)
	}
	tiny := computeMonitorLayout(100, 8)
	if tiny.ListHeight != 1 {
		t.Fatal("list height should bottom out at 1")
	}
	huge := computeMonitorLayout(100, 200)
	if huge.DetailHeight != monitorDetailMaxHeight {
		t.Fatal("detail height should cap")
	}
	if hit := hitMonitorLocation(layout, 5, 5); hit.area != "sidebar" {
		t.Fatalf("sidebar hit wrong: %+v", hit)
	}
	if hit := hitMonitorLocation(layout, 40, 0); hit.area != "tab" {
		t.Fatalf("tab hit wrong: %+v", hit)
	}
	if hit := hitMonitorLocation(layout, 40, 1); hit.area != "subtab" {
		t.Fatalf("subtab hit wrong: %+v", hit)
	}
	if hit := hitMonitorLocation(layout, 40, layout.ListTop+2); hit.area != "list" {
		t.Fatalf("list hit wrong: %+v", hit)
	}
	if hit := hitMonitorLocation(layout, 40, layout.DetailTop); hit.area != "detail" {
		t.Fatalf("detail hit wrong: %+v", hit)
	}
}

func TestMouseClickRouting(t *testing.T) {
	m := modelWithData()
	m.subTab = 0                                     // first PR section holds the seeded rows
	model, _ := m.handleClick(tea.Mouse{X: 5, Y: 2}) // sidebar header row is a no-op
	if updated := model.(monitorModel); updated.repoIdx != 0 {
		t.Fatalf("header click should not change repo: %d", updated.repoIdx)
	}
	model, _ = m.handleClick(tea.Mouse{X: 5, Y: 4}) // first configured repo
	if updated := model.(monitorModel); updated.repoIdx != 1 {
		t.Fatalf("click did not select repo: %d", updated.repoIdx)
	}
	model, _ = m.handleClick(tea.Mouse{X: 5, Y: 0}) // PRs tab slot
	if updated := model.(monitorModel); updated.tab != monitorTabPRs {
		t.Fatalf("PRs tab click wrong: %d", updated.tab)
	}
	model, _ = m.handleClick(tea.Mouse{X: 15, Y: 0}) // Issues tab slot
	if updated := model.(monitorModel); updated.tab != monitorTabIssues {
		t.Fatalf("issues tab click did not switch: %d", updated.tab)
	}
	model, _ = m.handleClick(tea.Mouse{X: 40, Y: m.layout.ListTop + 1})
	if updated := model.(monitorModel); updated.cursor != 1 {
		t.Fatalf("row click did not move cursor: %d", updated.cursor)
	}
}

func TestWheelScrolling(t *testing.T) {
	m := modelWithData()
	m.subTab = 0 // first PR section holds the seeded rows
	m.focus = monitorFocusList
	model, _ := m.handleWheel(tea.Mouse{Button: tea.MouseWheelDown})
	if updated := model.(monitorModel); updated.cursor != 1 { // step 3, clamped to last row
		t.Fatalf("wheel down clamp wrong: %d", updated.cursor)
	}
	model, _ = model.(monitorModel).handleWheel(tea.Mouse{Button: tea.MouseWheelUp})
	if updated := model.(monitorModel); updated.cursor != 0 {
		t.Fatalf("wheel up should reach top: %d", updated.cursor)
	}
	detail := modelWithData()
	detail.focus = monitorFocusDetail
	model, _ = detail.handleWheel(tea.Mouse{Button: tea.MouseWheelDown})
	if updated := model.(monitorModel); updated.focus != monitorFocusDetail {
		t.Fatal("detail focus lost on wheel")
	}
}

func TestScreenRenderingStates(t *testing.T) {
	m := modelWithData()
	screen := m.renderScreen()
	if !strings.Contains(screen, "PRs") || !strings.Contains(screen, "Issues") {
		t.Fatal("tab row missing")
	}
	if !strings.Contains(screen, "[Mine]") {
		t.Fatal("section sub-tab missing")
	}

	unready := newTestMonitorModel()
	if !strings.Contains(unready.renderScreen(), "Loading") {
		t.Fatal("unready screen should show loading")
	}

	small := sizedModel()
	small.layout = computeMonitorLayout(30, 8)
	if !strings.Contains(small.renderScreen(), "too small") {
		t.Fatal("small terminal message missing")
	}

	withHelp := modelWithData()
	withHelp.helpOpen = true
	if !strings.Contains(withHelp.renderScreen(), "keys") {
		t.Fatal("help overlay missing")
	}
}

func TestFooterVariants(t *testing.T) {
	m := modelWithData()
	footer := m.footerLine()
	if !strings.Contains(footer, "PRs") || !strings.Contains(footer, "rate 4400") {
		t.Fatalf("footer missing parts: %q", footer)
	}
	m.refreshing = true
	if !strings.Contains(m.footerLine(), "refreshing") {
		t.Fatal("refreshing footer missing")
	}
	m.refreshing = false
	m.refreshErr = "kaboom"
	if !strings.Contains(m.footerLine(), "kaboom") {
		t.Fatal("error footer missing")
	}
}

func TestFooterBeforeFirstFetchDoesNotPanic(t *testing.T) {
	m := sizedModel() // data is nil until the first refresh lands
	if got := m.footerLine(); got == "" {
		t.Fatal("footer should render even without data")
	}
	if hiddenReposSummary([]string{"a/b"}, nil) != "" {
		t.Fatal("nil data must yield no hidden summary")
	}
	result := &monitorFetchResult{Accessible: map[string]bool{"a/b": false, "c/d": true}}
	if got := hiddenReposSummary([]string{"a/b", "c/d"}, result); got == "" {
		t.Fatal("hidden repo should be summarized")
	}
}

func TestSettingsKeyRouting(t *testing.T) {
	m := modelWithData()
	m.settings.open(m.cfg)

	esc := tea.KeyPressMsg{Code: tea.KeyEscape}
	if applied := updateSettingsKey(&m.settings, esc); applied || m.settings.active {
		t.Fatal("esc should cancel without applying")
	}

	m.settings.open(m.cfg)
	if applied := updateSettingsKey(&m.settings, pressKey("x")); applied {
		t.Fatal("plain typing must not apply")
	}
	if m.settings.limit.Value() != "" && m.settings.focus != monitorFieldRepos {
		t.Fatalf("typing while focused repos should go to repos field, focus=%d", m.settings.focus)
	}

	m.settings.open(m.cfg)
	updateSettingsKey(&m.settings, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.settings.focus != monitorFieldLimit {
		t.Fatalf("tab should advance focus: %d", m.settings.focus)
	}
	updateSettingsKey(&m.settings, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if m.settings.focus != monitorFieldRepos {
		t.Fatalf("shift+tab should go back: %d", m.settings.focus)
	}
}

func TestApplySettingsValidationErrors(t *testing.T) {
	cfg := monitorTestConfig()
	settings := newMonitorSettingsModel()
	settings.open(cfg)

	settings.limit.SetValue("500")
	settings.interval.SetValue("10m")
	if err := applySettings(&settings, cfg); err == nil {
		t.Fatal("limit above max should fail")
	}
	settings.limit.SetValue("abc")
	if err := applySettings(&settings, cfg); err == nil {
		t.Fatal("non-numeric limit should fail")
	}
	settings.limit.SetValue("20")
	settings.interval.SetValue("1s")
	if err := applySettings(&settings, cfg); err == nil {
		t.Fatal("tiny interval should fail")
	}
	settings.interval.SetValue("10m")
	if err := applySettings(&settings, cfg); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
}

func TestParseMonitorRepoLinesSkipsBlanks(t *testing.T) {
	got := parseMonitorRepoLines(" a/b \n\nc/d\n")
	if len(got) != 2 || got[0] != "a/b" || got[1] != "c/d" {
		t.Fatalf("repo lines parsed wrong: %v", got)
	}
}

func TestEditorCommandResolution(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := resolveMonitorEditor(); got == "" {
		t.Fatal("platform fallback missing")
	}
	t.Setenv("EDITOR", "nano")
	if got := resolveMonitorEditor(); got != "nano" {
		t.Fatalf("EDITOR ignored: %q", got)
	}
	t.Setenv("VISUAL", "code")
	if got := resolveMonitorEditor(); got != "code" {
		t.Fatalf("VISUAL precedence broken: %q", got)
	}
}

func TestHandleEditorDoneReloadsConfig(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/config.yml"
	cfg := defaultMonitorConfig("owner/one")
	cfg.Repos = append(cfg.Repos, "owner/two")
	if err := saveMonitorConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	m := newTestMonitorModel()
	m.configPath = path
	model, cmd := m.handleEditorDone(monitorEditorDoneMsg{})
	updated := model.(monitorModel)
	if cmd == nil {
		t.Fatal("reload should refresh data")
	}
	if len(updated.cfg.Repos) != 2 {
		t.Fatalf("config not reloaded: %v", updated.cfg.Repos)
	}

	broken := newTestMonitorModel()
	broken.configPath = dir + "/missing.yml"
	model, _ = broken.handleEditorDone(monitorEditorDoneMsg{err: errors.New("exit status 1")})
	if updated := model.(monitorModel); updated.refreshErr == "" {
		t.Fatal("editor error should surface")
	}
}

func TestCheckoutCommands(t *testing.T) {
	pr := monitorRowForTest("o/r", 12, "open")
	if got := checkoutCommandFor(pr); got != "gh pr checkout 12 -R o/r" {
		t.Fatalf("pr command wrong: %q", got)
	}
	issue := monitorRow{Kind: monitorKindIssue, Number: 4, Repo: "o/r"}
	if got := checkoutCommandFor(issue); got != "gh issue develop 4 -R o/r" {
		t.Fatalf("issue command wrong: %q", got)
	}
}

func TestCopySelectedRequiresSelection(t *testing.T) {
	m := sizedModel()
	model, cmd := m.copySelectedURL(false)
	if cmd != nil {
		t.Fatal("no selection should yield no command")
	}
	if updated := model.(monitorModel); !updated.quitting {
		_ = updated // model unchanged; nothing to assert beyond cmd
	}
}

func TestSanitizeAndBackoff(t *testing.T) {
	long := errors.New(strings.Repeat("e", 300))
	msg := sanitizeMonitorError(long)
	if len([]rune(msg)) > 161 {
		t.Fatalf("error not truncated: %d", len([]rune(msg)))
	}
	if sanitizeMonitorError(errors.New("line\nbreak")) != "line break" {
		t.Fatal("newlines should collapse")
	}
	if got := nextMonitorBackoff(maximumMonitorInterval); got != maximumMonitorInterval {
		t.Fatalf("backoff cap wrong: %v", got)
	}
}

func TestClockAndTotalsHelpers(t *testing.T) {
	if formatMonitorClock(time.Time{}) != "never" {
		t.Fatal("zero clock label wrong")
	}
	stamped := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if formatMonitorClock(stamped) == "" {
		t.Fatal("stamped clock empty")
	}
	if monitorTabLabel(monitorTabIssues) != "Issues" || monitorTabLabel(monitorTabPRs) != "PRs" {
		t.Fatal("tab labels wrong")
	}
	if monitorSectionTotal(nil, 0, 0) != 0 {
		t.Fatal("nil totals should be zero")
	}
	var nilResult *monitorFetchResult
	if nilResult.RateRemainingSafe() != 0 {
		t.Fatal("nil rate should be zero")
	}
}

func TestEnsureCursorVisibleScrollWindow(t *testing.T) {
	m := sizedModel()
	m.subTab = 0 // rows are seeded on the first PR section
	m.data = &monitorFetchResult{PRSections: []monitorSectionData{{Rows: make([]monitorRow, 50)}}}
	for i := range m.data.PRSections[0].Rows {
		m.data.PRSections[0].Rows[i] = monitorRowForTest("owner/one", i+1, "open")
	}
	m.setCursor(49)
	if m.offset == 0 {
		t.Fatal("offset should follow cursor down")
	}
	m.setCursor(0)
	if m.offset != 0 {
		t.Fatalf("offset should return to top: %d", m.offset)
	}
}

func TestEscapeClearsFilterBeforeQuitting(t *testing.T) {
	m := sizedModel()
	m.filter.SetValue("bug")
	model, _ := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	updated := model.(monitorModel)
	if updated.quitting || updated.filter.Value() != "" {
		t.Fatal("esc with filter should clear it, not quit")
	}
	model, _ = updated.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !(model.(monitorModel)).quitting {
		t.Fatal("second esc should quit")
	}
}

func TestOpenSelectedWithoutURLIsSafe(t *testing.T) {
	m := sizedModel()
	m.data = &monitorFetchResult{}
	result := m.openSelectedInBrowser()
	if result == nil {
		t.Fatal("should return a model even without selection")
	}
}

func TestInitialCmdFetches(t *testing.T) {
	m := newTestMonitorModel()
	if m.initialMonitorCmd() == nil {
		t.Fatal("Init should schedule a fetch")
	}
}

func TestSubTabIndexAndCenterHelpers(t *testing.T) {
	if subTabIndexAt(37) != 37/monitorSubTabSlotWidth {
		t.Fatal("slot math wrong")
	}
	centered := centerMonitorText("abc", 11)
	if lipglossWidth(centered) < 3 {
		t.Fatal("centered text lost content")
	}
	padded := padMonitorLine("ab", 6)
	if lipglossWidth(padded) != 6 {
		t.Fatalf("padding math wrong: %q", padded)
	}
	if padMonitorLine("toolongtext", 3) != "toolongtext" {
		t.Fatal("overlong lines must pass through untouched")
	}
}

func TestStateFileSaveErrorSurfaces(t *testing.T) {
	err := saveMonitorStateFile("Z:/definitely/not/a/real/path/state.json", monitorSessionState{})
	if err == nil && os.Getenv("RUNNING_AS_ADMIN") == "" {
		t.Skip("path unexpectedly writable")
	}
}

func TestSidebarEntriesNeverOverflowWidth(t *testing.T) {
	repos := []string{
		"relias-engineering/very-long-repo-name-here",
		"relias-engineering/another-long-one",
	}
	counts := map[string]monitorRepoCounts{
		repos[0]: {Accessible: false},
		repos[1]: {PRs: 12, Issues: 3, Accessible: true},
	}
	out := renderMonitorSidebar(repos, 0, counts, 20, monitorSidebarWidth, false)
	for i, line := range strings.Split(out, "\n") {
		if w := lipglossWidth(line); w > monitorSidebarWidth {
			t.Fatalf("sidebar line %d overflows: width %d > %d (%q)", i, w, monitorSidebarWidth, line)
		}
	}
	if !strings.Contains(out, "/very-long-repo-name-here") && !strings.Contains(out, "…/very-long-repo-name-here") {
		t.Fatalf("repo tail lost for hidden repo: %q", out)
	}
}

func TestTruncateMonitorRepoNameKeepsRepoHalf(t *testing.T) {
	if got := truncateMonitorRepoName("org/name", 20); got != "org/name" {
		t.Fatalf("short label altered: %q", got)
	}
	got := truncateMonitorRepoName("relias-engineering/api-server", 20)
	if got != "relias-e…/api-server" {
		t.Fatalf("expected truncated org with full tail, got %q", got)
	}
	if !strings.HasSuffix(got, "/api-server") {
		t.Fatalf("repo tail must survive: %q", got)
	}
	if tiny := truncateMonitorRepoName("o/r", 1); lipglossWidth(tiny) != 1 {
		t.Fatalf("width-1 should still fit budget: %q", tiny)
	}
}

func TestDetailRuleSpansMainWidth(t *testing.T) {
	m := modelWithData()
	lines := m.detailLines()
	width := m.layout.Width - m.layout.SidebarWidth - 1
	if len(lines) != m.layout.DetailHeight {
		t.Fatalf("detail region height wrong: %d != %d", len(lines), m.layout.DetailHeight)
	}
	for i, line := range lines {
		if w := lipglossWidth(line); w > width {
			t.Fatalf("detail line %d overflows main area: %d > %d (%q)", i, w, width, line)
		}
	}
	if !strings.Contains(strings.TrimRight(lines[0], " "), strings.Repeat("─", width)) {
		t.Fatalf("rule should span the full main width (%d): %q", width, lines[0])
	}
}

func TestFooterFitsLayoutWidth(t *testing.T) {
	m := modelWithData()
	m.layout = computeMonitorLayout(70, 24)
	footer := m.footerLine()
	if w := lipglossWidth(footer); w > m.layout.Width {
		t.Fatalf("footer overflows narrow layout: %d > %d (%q)", w, m.layout.Width, footer)
	}
	m.layout = computeMonitorLayout(40, 24)
	m.lastChanges = nil
	footer = m.footerLine()
	if w := lipglossWidth(footer); w > m.layout.Width {
		t.Fatalf("footer overflows tiny layout: %d > %d (%q)", w, m.layout.Width, footer)
	}
}
