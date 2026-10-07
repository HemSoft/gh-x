package main

import (
	"fmt"
	"image/color"
	"math"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func TestMonitorWholeScreenBounds(t *testing.T) {
	states := []struct {
		name  string
		apply func(*monitorModel)
	}{
		{"populated PRs", func(m *monitorModel) {}},
		{"issues", func(m *monitorModel) { m.tab = monitorTabIssues }},
		{"loading", func(m *monitorModel) { m.data = nil }},
		{"empty", func(m *monitorModel) { m.filter.SetValue("no-match") }},
		{"settings", func(m *monitorModel) { m.settings.open(m.cfg); m.settings.errText = strings.Repeat("invalid ", 20) }},
		{"help", func(m *monitorModel) { m.helpOpen = true }},
		{"error", func(m *monitorModel) { m.refreshErr = strings.Repeat("connection failed ", 20) }},
		{"warning", func(m *monitorModel) { m.refreshWarn = "host unavailable; last successful data retained" }},
	}
	for _, size := range [][2]int{{60, 16}, {80, 24}, {120, 40}, {160, 50}} {
		for _, state := range states {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], state.name), func(t *testing.T) {
				m := modelWithData()
				m.subTab = 0
				m.layout = computeMonitorLayout(size[0], size[1])
				m.theme = newMonitorTheme(true, false)
				m.applyMonitorTheme()
				m.data.PRSections[0].Rows[0].Title = "Review 界面 👩‍💻 e\u0301 " + strings.Repeat("long ", 50)
				state.apply(&m)
				lines := strings.Split(m.renderScreen(), "\n")
				if len(lines) != size[1] {
					t.Fatalf("screen height = %d, want %d", len(lines), size[1])
				}
				for i, line := range lines {
					if got := lipgloss.Width(line); got > size[0] {
						t.Fatalf("line %d width = %d > %d: %q", i, got, size[0], line)
					}
				}
			})
		}
	}
}

func TestMonitorSemanticColorsAndMonochrome(t *testing.T) {
	colorSGR := regexp.MustCompile("\x1b\\[[0-9;]*(?:3[0-9]|4[0-9]|9[0-9]|10[0-7])(?:;[0-9]+)*m")
	for _, dark := range []bool{false, true} {
		for _, noColor := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/noColor=%v", dark, noColor), func(t *testing.T) {
				theme := newMonitorTheme(dark, noColor)
				output := renderMonitorTable(monitorTableRenderInput{
					Kind: monitorKindPR, Width: 120, Height: 4, Cursor: -1, Theme: theme,
					Rows: []monitorRow{{Number: 1, Title: "Passing", State: "open", Checks: "pass", Review: "approved"},
						{Number: 2, Title: "Failing", State: "draft", Checks: "fail", Review: "changes"},
						{Number: 3, Title: "Waiting", State: "open", Checks: "pending"}},
				})
				for _, status := range []string{"pass", "fail", "pending", "draft", "approved", "changes"} {
					if !strings.Contains(stripANSIForTest(output), status) {
						t.Fatalf("textual status %q lost: %q", status, output)
					}
				}
				if colorSGR.MatchString(output) == noColor {
					t.Fatalf("color sequences do not match mode: noColor=%v output=%q", noColor, output)
				}
				if !noColor && theme.Success.GetForeground() == theme.Error.GetForeground() {
					t.Fatal("success and failure share a color")
				}
			})
		}
	}
}

func TestMonitorDetailsScrollToFinalLineAndResetOnSelection(t *testing.T) {
	m := modelWithData()
	m.subTab = 0
	m.focus = monitorFocusDetail
	var body []string
	for i := range 35 {
		body = append(body, fmt.Sprintf("body-line-%02d", i))
	}
	m.data.PRSections[0].Rows[0].Body = strings.Join(body, "\n")
	before := strings.Join(m.detailLines(), "\n")
	m.jumpToPaneEdge(1)
	after := strings.Join(m.detailLines(), "\n")
	if before == after || !strings.Contains(after, "body-line-34") || strings.Contains(before, "body-line-34") {
		t.Fatalf("final body line is not reachable: before=%q after=%q", before, after)
	}
	m.setCursor(1)
	if m.detailScroll != 0 {
		t.Fatalf("new selection kept old scroll offset %d", m.detailScroll)
	}
}

func TestMonitorMouseTracksRenderedSlotsAndHeaders(t *testing.T) {
	for _, width := range []int{80, 120, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := modelWithData()
			m.subTab = 0
			m.layout = computeMonitorLayout(width, 40)
			model, _ := m.handleClick(tea.Mouse{X: m.layout.MainLeft + monitorTabSlotWidth + 1, Y: m.layout.TabTop})
			m = model.(monitorModel)
			if m.tab != monitorTabIssues {
				t.Fatal("rendered Issues tab did not select issues")
			}
			m.tab = monitorTabPRs
			m.subTab = 0
			model, _ = m.handleClick(tea.Mouse{X: m.layout.MainLeft + 2, Y: m.layout.ListTop})
			if model.(monitorModel).cursor != m.cursor {
				t.Fatal("table header selected a row")
			}
			model, _ = m.handleClick(tea.Mouse{X: m.layout.MainLeft + 2, Y: m.layout.ListTop + 2})
			if model.(monitorModel).cursor != 1 {
				t.Fatal("second rendered row did not select second item")
			}
			model, _ = m.handleClick(tea.Mouse{X: m.layout.Width - 1, Y: m.layout.FooterTop})
			if model.(monitorModel).focus != m.focus {
				t.Fatal("footer click changed pane focus")
			}
		})
	}
}

func TestMonitorBackgroundReplyAndNoColorEnvironment(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	m := newTestMonitorModel()
	model, _ := m.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 255, G: 255, B: 255, A: 255}})
	updated := model.(monitorModel)
	if updated.theme.Dark || !updated.theme.NoColor {
		t.Fatal("background reply lost monochrome preference")
	}
}

func TestMonitorManySectionsRemainVisibleAndClickable(t *testing.T) {
	m := sizedModel()
	m.layout = computeMonitorLayout(80, 24)
	m.cfg.PRSections = nil
	for i := range 12 {
		m.cfg.PRSections = append(m.cfg.PRSections, monitorSection{Title: fmt.Sprintf("Section%d", i)})
	}
	m.subTab = 10
	first := m.firstVisibleSection()
	if !strings.Contains(stripANSIForTest(m.renderSubTabRow()), "Section10") {
		t.Fatal("active section disappeared")
	}
	model, _ := m.handleClick(tea.Mouse{X: 1, Y: m.layout.SubTabTop})
	if model.(monitorModel).subTab != first {
		t.Fatal("section click ignored scrolled origin")
	}
}

func TestMonitorBodyCannotInjectTerminalCommands(t *testing.T) {
	row := monitorRow{Number: 1, Title: "Safe", Body: "before\x1b[2Jafter"}
	text := renderMonitorDetail(row, 60, 10, false, 0, newMonitorTheme(true, true))
	if strings.Contains(text, "\x1b[2J") || !strings.Contains(text, "beforeafter") {
		t.Fatalf("unsafe body render: %q", text)
	}
}

func TestMonitorPaletteReadability(t *testing.T) {
	for _, test := range []struct {
		name       string
		dark       bool
		background color.Color
	}{
		{"dark", true, lipgloss.Color("#171a21")},
		{"light", false, lipgloss.Color("#f8fafc")},
	} {
		t.Run(test.name, func(t *testing.T) {
			theme := newMonitorTheme(test.dark, false)
			for _, style := range []lipgloss.Style{theme.Text, theme.Muted, theme.Accent, theme.Success, theme.Warning, theme.Error} {
				foreground := monitorTestLuminance(style.GetForeground())
				background := monitorTestLuminance(test.background)
				contrast := (max(foreground, background) + 0.05) / (min(foreground, background) + 0.05)
				if contrast < 4.5 {
					t.Fatalf("palette text contrast %.2f < 4.5", contrast)
				}
			}
		})
	}
}

func monitorTestLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	channel := func(value uint32) float64 {
		v := float64(value) / 65535
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

func TestMonitorTableFitsItsPaneAndPreservesNextRow(t *testing.T) {
	rows := []monitorRow{{Number: 1, Title: "first"}, {Number: 2, Title: "second"}, {Number: 3, Title: "third"}}
	text := renderMonitorTable(monitorTableRenderInput{Kind: monitorKindPR, Rows: rows, Width: 80, Height: 3, Cursor: -1})
	if got := len(strings.Split(text, "\n")); got != 3 {
		t.Fatalf("pane height=%d, want 3", got)
	}
	if !strings.Contains(text, "second") || strings.Contains(text, "third") {
		t.Fatalf("unexpected visible slice: %q", text)
	}
	text = renderMonitorTable(monitorTableRenderInput{Kind: monitorKindPR, Rows: rows, Width: 80, Height: 3, Offset: 1, Cursor: -1})
	if !strings.Contains(text, "third") || strings.Contains(text, "first") {
		t.Fatalf("unexpected scrolled slice: %q", text)
	}
}

func TestMonitorMonochromeScreensKeepTextAndFocus(t *testing.T) {
	m := modelWithData()
	m.subTab = 0
	m.theme = newMonitorTheme(true, true)
	m.applyMonitorTheme()
	for _, test := range []struct {
		name  string
		apply func(*monitorModel)
	}{
		{"main", func(m *monitorModel) {}},
		{"filter", func(m *monitorModel) { m.filtering = true; m.filter.Focus() }},
		{"settings", func(m *monitorModel) { m.settings.open(m.cfg) }},
		{"help", func(m *monitorModel) { m.helpOpen = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := m
			test.apply(&copy)
			output := copy.renderScreen()
			if regexp.MustCompile("\x1b\\[[0-9;]*(?:3[0-9]|4[0-9]|9[0-9]|10[0-7])(?:;[0-9]+)*m").MatchString(output) {
				t.Fatalf("monochrome screen emitted color: %q", output)
			}
		})
	}
	m.settings.open(m.cfg)
	if !strings.Contains(stripANSIForTest(m.renderSettingsScreen()), "> Repositories") {
		t.Fatal("focused settings field has no text cue")
	}
	m.settings.cycleFocus(1)
	if !strings.Contains(stripANSIForTest(m.renderSettingsScreen()), "> Rows per section") {
		t.Fatal("settings focus cue did not move")
	}
}

func TestMonitorRetryShowsRefreshAlongsideRetainedFailure(t *testing.T) {
	for _, test := range []struct {
		name  string
		apply func(*monitorModel)
		cue   string
	}{
		{"error", func(m *monitorModel) { m.refreshErr = "offline" }, "offline"},
		{"partial", func(m *monitorModel) { m.refreshWarn = "host unavailable" }, "host unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := modelWithData()
			m.refreshing = false
			test.apply(&m)
			model, _ := m.startRefresh()
			updated := model.(monitorModel)
			text := stripANSIForTest(updated.footerLine())
			if !strings.Contains(text, "refreshing") || !strings.Contains(text, test.cue) {
				t.Fatalf("retry hides progress or retained failure: %q", text)
			}
		})
	}
}

func TestMonitorResizeKeepsDetailWheelResponsive(t *testing.T) {
	m := modelWithData()
	m.subTab = 0
	m.focus = monitorFocusDetail
	m.layout = computeMonitorLayout(80, 24)
	m.data.PRSections[0].Rows[0].Body = strings.Repeat(strings.Repeat("x", 100)+"\n", 30)
	m.jumpToPaneEdge(1)
	model, _ := m.handleResize(tea.WindowSizeMsg{Width: 160, Height: 50})
	updated := model.(monitorModel)
	before := strings.Join(updated.detailLines(), "\n")
	model, _ = updated.handleWheel(tea.Mouse{Button: tea.MouseWheelUp})
	updated = model.(monitorModel)
	if before == strings.Join(updated.detailLines(), "\n") {
		t.Fatal("detail wheel did not move after resize")
	}
}

func TestMonitorSelectionAttributesCoverTitleStatusAndGaps(t *testing.T) {
	for _, noColor := range []bool{false, true} {
		for _, focused := range []bool{false, true} {
			t.Run(fmt.Sprintf("mono=%v/focused=%v", noColor, focused), func(t *testing.T) {
				text := renderMonitorTable(monitorTableRenderInput{Kind: monitorKindPR, Width: 100, Height: 3, Cursor: 0, Focused: focused, Theme: newMonitorTheme(true, noColor), Rows: []monitorRow{{Number: 101, Title: "SelectedTitle", State: "open", Checks: "pass"}, {Number: 102, Title: "OtherTitle", Checks: "fail"}}})
				number := monitorTestAttributesAt(t, text, "101")
				for _, target := range []string{"SelectedTitle", "open", "pass"} {
					attrs := monitorTestAttributesAt(t, text, target)
					if attrs.background != number.background || attrs.bold != number.bold || attrs.reverse != number.reverse {
						t.Fatalf("selection lost at %q: %+v vs %+v", target, attrs, number)
					}
				}
				other := monitorTestAttributesAt(t, text, "OtherTitle")
				if noColor {
					if number.reverse != focused || !number.bold || number.background != "" {
						t.Fatalf("monochrome selection=%+v", number)
					}
				} else if number.background == "" || number.background == other.background {
					t.Fatalf("selection has no distinct background: selected=%+v other=%+v", number, other)
				}
			})
		}
	}
}

type monitorTestAttributes struct {
	foreground    string
	background    string
	bold, reverse bool
}

// Interpret the emitted SGR stream at a visible word, including inner resets.
func monitorTestAttributesAt(t *testing.T, text, target string) monitorTestAttributes {
	t.Helper()
	position := strings.Index(text, target)
	if position < 0 {
		t.Fatalf("missing %q in %q", target, text)
	}
	var style monitorTestAttributes
	for _, sequence := range regexp.MustCompile("\x1b\\[[0-9;]*m").FindAllString(text[:position], -1) {
		params := strings.Split(strings.TrimSuffix(strings.TrimPrefix(sequence, "\x1b["), "m"), ";")
		for i := 0; i < len(params); i++ {
			switch params[i] {
			case "", "0":
				style = monitorTestAttributes{}
			case "1":
				style.bold = true
			case "22":
				style.bold = false
			case "7":
				style.reverse = true
			case "27":
				style.reverse = false
			case "49":
				style.background = ""
			case "39":
				style.foreground = ""
			case "38", "48":
				count := 2
				if i+1 < len(params) && params[i+1] == "2" {
					count = 4
				}
				if i+count < len(params) {
					if params[i] == "48" {
						style.background = strings.Join(params[i+1:i+count+1], ";")
					} else {
						style.foreground = strings.Join(params[i+1:i+count+1], ";")
					}
					i += count
				}
			}
		}
	}
	return style
}

func TestMonitorResizeReclaimsListSpace(t *testing.T) {
	m := modelWithData()
	m.subTab = 0
	m.focus = monitorFocusList
	m.layout = computeMonitorLayout(80, 24)
	m.data.PRSections[0].Rows = nil
	for i := range 30 {
		m.data.PRSections[0].Rows = append(m.data.PRSections[0].Rows, monitorRow{Number: i + 1, Title: fmt.Sprintf("row%02d", i+1)})
	}
	m.jumpToPaneEdge(1)
	model, _ := m.handleResize(tea.WindowSizeMsg{Width: 160, Height: 50})
	m = model.(monitorModel)
	want := maxInt(30-(m.layout.ListHeight-1), 0)
	if m.offset != want {
		t.Fatalf("expanded pane kept offset %d, want %d", m.offset, want)
	}
	m.data.PRSections[0].Rows = m.data.PRSections[0].Rows[:3]
	model, _ = m.handleResize(tea.WindowSizeMsg{Width: 160, Height: 50})
	m = model.(monitorModel)
	if m.offset != 0 || m.cursor != 2 {
		t.Fatalf("shortened list kept invalid scroll: cursor%d offset%d", m.cursor, m.offset)
	}
}

func TestMonitorDetailIndicatorReachesEnd(t *testing.T) {
	row := monitorRow{Number: 1, Title: "ScrollTitle", Body: strings.Repeat("body\n", 20)}
	text := renderMonitorDetail(row, 80, 6, true, 999, newMonitorTheme(true, false))
	total := len(monitorDetailContent(row, 80, newMonitorTheme(true, false)))
	if !strings.Contains(stripANSIForTest(strings.Split(text, "\n")[0]), fmt.Sprintf("%d/%d", total, total)) {
		t.Fatalf("detail range does not show end: %q", text)
	}
	if monitorTestAttributesAt(t, text, "ScrollTitle").background == "" {
		t.Fatal("detail title surface missing")
	}
}

func TestMonitorHelpAndLongErrorKeepEssentialActions(t *testing.T) {
	m := modelWithData()
	m.layout = computeMonitorLayout(60, 16)
	m.helpOpen = true
	for _, target := range []string{"1–9", "/ enter esc", "left/right", "pgup/pgdown", "mouse / wheel", "esc clears filter"} {
		if !strings.Contains(stripANSIForTest(m.renderScreen()), target) {
			t.Fatalf("help missing %q", target)
		}
	}
	m.helpOpen = false
	m.refreshing = false
	m.refreshErr = strings.Repeat("long failure ", 30)
	if !strings.Contains(stripANSIForTest(m.footerLine()), "r retry") {
		t.Fatal("long failure hides retry action")
	}
}

func TestMonitorFetchedTextCannotMoveCursorOrRingBell(t *testing.T) {
	row := monitorRow{Number: 1, Title: "Safe\aTitle", Body: "first\r\nsecond\rthird\tcolumn\a\v\f\u0085\x1b[2J"}
	text := renderMonitorDetail(row, 80, 12, false, 0, newMonitorTheme(true, true))
	if strings.ContainsAny(text, "\r\t\a\v\f\u0085") {
		t.Fatalf("terminal control survived: %q", text)
	}
	for _, word := range []string{"SafeTitle", "first", "second", "third   column"} {
		if !strings.Contains(stripANSIForTest(text), word) {
			t.Fatalf("normalized text lost %q", word)
		}
	}
	m := modelWithData()
	m.refreshErr = "host\a\x1b[2J\r\nfailed"
	if strings.ContainsAny(m.footerLine(), "\a\r\n") || strings.Contains(m.footerLine(), "\x1b[2J") {
		t.Fatal("error footer permits terminal controls")
	}
}
