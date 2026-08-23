package main

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const monitorSettingsBoxWidth = 52

// renderSettingsScreen draws the settings overlay full-screen.
func (m monitorModel) renderSettingsScreen() string {
	var sb strings.Builder
	sb.WriteString(monitorStyleActive.Render("Settings"))
	sb.WriteString("\n\n")
	sb.WriteString(monitorStyleDim.Render("Repos (one owner/repo per line)"))
	sb.WriteString("\n")
	sb.WriteString(m.settings.repos.View())
	sb.WriteString("\n\n")
	sb.WriteString(monitorStyleDim.Render("Rows per section (1-100): "))
	sb.WriteString(m.settings.limit.View())
	sb.WriteString("\n")
	sb.WriteString(monitorStyleDim.Render("Refresh interval (e.g. 10m):  "))
	sb.WriteString(m.settings.interval.View())
	sb.WriteString("\n\n")
	if m.settings.errText != "" {
		sb.WriteString(monitorStyleError.Render(m.settings.errText))
		sb.WriteString("\n")
	}
	sb.WriteString(monitorStyleDim.Render("tab: next field · enter: newline in repos / save elsewhere · ctrl+s: save · esc: cancel"))
	body := sb.String()

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(monitorSettingsBoxWidth).
		Render(body)
	return centerMonitorText(blockCentered(box, m.layout.Width), m.layout.Width)
}

func blockCentered(text string, width int) string {
	gap := (width - lipgloss.Width(text)) / 4
	if gap < 0 {
		return text
	}
	prefix := strings.Repeat(" ", gap)
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}
