package main

import (
	"image/color"
	"os"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// monitorTheme belongs to a model, so terminal replies never mutate shared styles.
type monitorTheme struct {
	Dark, NoColor, Initialized bool
	Text, Muted, Accent        lipgloss.Style
	Success, Warning, Error    lipgloss.Style
	Selected, Selection        lipgloss.Style
	Heading, Surface           lipgloss.Style
}

func newMonitorTheme(dark, noColor bool) monitorTheme {
	t := monitorTheme{Dark: dark, NoColor: noColor, Initialized: true}
	t.Text = lipgloss.NewStyle()
	t.Muted = lipgloss.NewStyle().Faint(true)
	t.Accent = lipgloss.NewStyle().Bold(true)
	t.Selected = lipgloss.NewStyle().Bold(true).Reverse(true)
	t.Selection = lipgloss.NewStyle().Bold(true)
	t.Heading = lipgloss.NewStyle().Bold(true)
	if noColor {
		return t
	}
	choose := func(darkColor, lightColor color.Color) color.Color {
		return lipgloss.LightDark(dark)(lightColor, darkColor)
	}
	fg := choose(lipgloss.Color("#dce6ef"), lipgloss.Color("#233343"))
	muted := choose(lipgloss.Color("#a3b2c2"), lipgloss.Color("#526172"))
	accent := choose(lipgloss.Color("#78d3ee"), lipgloss.Color("#006783"))
	surface := choose(lipgloss.Color("#172532"), lipgloss.Color("#edf3f7"))
	t.Text = t.Text.Foreground(fg)
	t.Muted = lipgloss.NewStyle().Foreground(muted)
	t.Accent = t.Accent.Foreground(accent)
	t.Success = t.Text.Foreground(choose(lipgloss.Color("#9bdfad"), lipgloss.Color("#176632")))
	t.Warning = t.Text.Foreground(choose(lipgloss.Color("#f2cf83"), lipgloss.Color("#805600")))
	t.Error = t.Text.Foreground(choose(lipgloss.Color("#ff9d9d"), lipgloss.Color("#ad2433")))
	t.Selected = lipgloss.NewStyle().Bold(true).Foreground(fg).Background(
		choose(lipgloss.Color("#24465b"), lipgloss.Color("#d0e8f2")))
	t.Selection = t.Text.Background(surface)
	t.Heading = t.Heading.Foreground(accent).Background(surface)
	t.Surface = t.Text.Background(surface)
	return t
}

func (t monitorTheme) resolved() monitorTheme {
	if !t.Initialized {
		return newMonitorTheme(true, os.Getenv("NO_COLOR") != "")
	}
	return t
}

func (t monitorTheme) semantic(value string) lipgloss.Style {
	t = t.resolved()
	switch strings.ToLower(value) {
	case "pass", "approved", "approv", "open", "merged", "done":
		return t.Success
	case "fail", "error", "changes", "chang", "changes_requested", "closed":
		return t.Error
	case "pending", "wait", "review", "review_required", "draft", "unknown", "?":
		return t.Warning
	default:
		return t.Muted
	}
}

func (m *monitorModel) applyMonitorTheme() {
	t := m.theme
	state := textinput.StyleState{Text: t.Text, Prompt: t.Accent, Placeholder: t.Muted, Suggestion: t.Muted}
	inputStyles := textinput.Styles{Focused: state, Blurred: state, Cursor: textinput.CursorStyle{Blink: true}}
	m.filter.SetStyles(inputStyles)
	m.settings.limit.SetStyles(inputStyles)
	m.settings.interval.SetStyles(inputStyles)
	areaState := textarea.StyleState{
		Base: t.Text, Text: t.Text, Prompt: t.Accent, Placeholder: t.Muted,
		LineNumber: t.Muted, CursorLineNumber: t.Accent, EndOfBuffer: t.Muted,
	}
	m.settings.repos.SetStyles(textarea.Styles{Focused: areaState, Blurred: areaState})
	m.filter.SetWidth(maxInt(m.listWidth()-12, 10))
	m.settings.repos.SetWidth(minInt(58, maxInt(m.layout.Width-12, 20)))
	m.settings.repos.SetHeight(clampInt(m.layout.Height-13, 3, 8))
	m.settings.limit.SetWidth(8)
	m.settings.interval.SetWidth(12)
}

func (m monitorModel) handleMonitorBackground(msg tea.BackgroundColorMsg) (tea.Model, tea.Cmd) {
	m.theme = newMonitorTheme(msg.IsDark(), m.theme.NoColor)
	m.applyMonitorTheme()
	return m, nil
}
