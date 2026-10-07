package main

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// monitorDetailLine is one rendered line of the detail pane.
type monitorDetailLine struct {
	label     string
	value     string
	showEmpty bool
}

// monitorDetailMetadata builds the label/value block for the selected row.
func monitorDetailMetadata(row monitorRow) []monitorDetailLine {
	lines := []monitorDetailLine{
		{label: "Repo", value: row.Repo},
		{label: "Author", value: row.Author},
	}
	if row.Kind == monitorKindPR {
		lines = append(lines,
			monitorDetailLine{label: "Branch", value: row.Branch},
			monitorDetailLine{label: "State", value: detailMonitorState(row)},
			monitorDetailLine{label: "Reviews", value: detailMonitorReview(row)},
			monitorDetailLine{label: "Review", value: row.Review},
			monitorDetailLine{label: "AI", value: row.AIReview},
			monitorDetailLine{label: "Checks", value: row.Checks},
			monitorDetailLine{label: "Comments", value: row.Comments},
		)
	} else {
		lines = append(lines,
			monitorDetailLine{label: "State", value: row.State},
			monitorDetailLine{label: "Parent", value: row.Parent, showEmpty: true},
			monitorDetailLine{label: "Sub-issues", value: row.SubIssues, showEmpty: true},
			monitorDetailLine{label: "Assignees", value: row.Assignees},
		)
	}
	if len(row.Labels) > 0 {
		lines = append(lines, monitorDetailLine{label: "Labels", value: strings.Join(row.Labels, ", ")})
	}
	if row.Milestone != "" {
		lines = append(lines, monitorDetailLine{label: "Milestone", value: row.Milestone})
	}
	lines = append(lines, monitorDetailLine{label: "Updated", value: row.Updated})
	return lines
}

func detailMonitorState(row monitorRow) string {
	if row.State == "open" || row.State == "draft" {
		return row.State
	}
	return row.State + " · " + row.Review
}

func detailMonitorReview(row monitorRow) string {
	parts := make([]string, 0, 3)
	if row.AIReview != "-" && row.AIReview != "" {
		parts = append(parts, "AI "+row.AIReview)
	}
	parts = append(parts, "approvals "+strconv.Itoa(row.Approvals))
	return strings.Join(parts, ", ")
}

// renderMonitorDetail renders the bottom pane: title, metadata grid, body.
func renderMonitorDetail(row monitorRow, width, height int, focused bool, offset int, theme monitorTheme) string {
	if row.Number == 0 {
		return theme.Muted.Render("No selection")
	}
	lines := monitorDetailContent(row, width, theme)
	bodyHeight := maxInt(height-1, 1)
	offset = clampInt(offset, 0, maxInt(len(lines)-bodyHeight, 0))
	title := strconv.Itoa(row.Number) + " " + monitorPlainCell(row.Title)
	hint := " " + strconv.Itoa(offset+1) + "/" + strconv.Itoa(maxInt(len(lines), 1))
	title = fitMonitorLine(theme.Heading.Render(truncateMonitorCell(title, maxInt(width-len(hint), 1))), maxInt(width-len(hint), 0)) + theme.Muted.Render(hint)
	if focused {
		title = theme.Surface.Render(title)
	}
	visible := append([]string{title}, lines[offset:minInt(offset+bodyHeight, len(lines))]...)
	return strings.Join(padToMonitorLines(visible, height), "\n")
}

func monitorDetailContent(row monitorRow, width int, theme monitorTheme) []string {
	metadata := renderMonitorMetadataLines(monitorDetailMetadata(row), width, theme)
	var lines []string
	line := ""
	for _, item := range metadata {
		item = strings.TrimSpace(item)
		if line != "" && lipgloss.Width(line+" · "+item) > width {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " · "
		}
		line += item
	}
	if line != "" {
		lines = append(lines, line)
	}
	lines = append(lines, "")
	body := wrapMonitorBody(ansi.Strip(row.Body), maxInt(width, 10))
	if len(body) == 0 {
		body = []string{"No description provided."}
	}
	for _, text := range body {
		lines = append(lines, theme.Text.Render(text))
	}
	return lines
}

// renderMonitorMetadataLines renders each non-empty entry, with labels
// padded to a shared column so values line up.
func renderMonitorMetadataLines(metadata []monitorDetailLine, width int, theme monitorTheme) []string {
	items := make([]monitorDetailLine, 0, len(metadata))
	labelWidth := 0
	for _, item := range metadata {
		if !item.showEmpty && (item.value == "" || item.value == "-") {
			continue
		}
		if w := runewidth.StringWidth(item.label); w > labelWidth {
			labelWidth = w
		}
		items = append(items, item)
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, renderMonitorMetadataItem(item, labelWidth, width, theme))
	}
	return lines
}

func renderMonitorMetadataItem(item monitorDetailLine, labelWidth, width int, theme monitorTheme) string {
	pad := labelWidth - runewidth.StringWidth(item.label)
	label := theme.Muted.Render(item.label+":") + strings.Repeat(" ", pad+1)
	valueWidth := maxInt(width-labelWidth-2, 10)
	value := theme.semantic(item.value).Render(truncateMonitorCell(monitorPlainCell(item.value), valueWidth))
	return label + value
}
