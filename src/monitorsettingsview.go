package main

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// Settings use the same theme and terminal bounds as the dashboard.
func (m monitorModel) renderSettingsScreen() string {
	width := minInt(64, m.layout.Width-4)
	innerWidth := width - 6
	lines := []string{
		m.theme.Accent.Render("Settings"),
		m.settingsLabel(monitorFieldRepos, "Repositories · one [host/]owner/repo per line"),
		m.settings.repos.View(),
		"",
		m.settingsLabel(monitorFieldLimit, "Rows per section (1-100): ") + m.settings.limit.View(),
		m.settingsLabel(monitorFieldInterval, "Refresh interval:        ") + m.settings.interval.View(),
	}
	if m.settings.errText != "" {
		lines = append(lines, m.theme.Error.Render(monitorPlainCell(m.settings.errText)))
	}
	lines = append(lines, "", m.theme.Muted.Render("tab next · enter newline/save"),
		m.theme.Muted.Render("ctrl+s save · esc cancel"))
	body := strings.Split(strings.Join(lines, "\n"), "\n")
	body = padEachLine(body, innerWidth)
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		Padding(0, 2).Width(width - 2)
	if !m.theme.NoColor {
		box = box.BorderForeground(m.theme.Accent.GetForeground())
	}
	rendered := box.Render(strings.Join(body, "\n"))
	return centerMonitorBlock(rendered, m.layout.Width, m.layout.Height)
}

func centerMonitorBlock(text string, width, height int) string {
	rows := strings.Split(text, "\n")
	top := maxInt((height-len(rows))/2, 0)
	left := maxInt((width-lipgloss.Width(text))/2, 0)
	lines := make([]string, top)
	for _, row := range rows {
		lines = append(lines, strings.Repeat(" ", left)+row)
	}
	return strings.Join(padEachLine(padToMonitorLines(lines, height), width), "\n")
}

func (m monitorModel) settingsLabel(field int, text string) string {
	if m.settings.focus == field {
		return m.theme.Accent.Render("> " + text)
	}
	return m.theme.Muted.Render("  " + text)
}
