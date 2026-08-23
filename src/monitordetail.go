package main

import (
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

// monitorDetailLine is one rendered line of the detail pane.
type monitorDetailLine struct {
	label string
	value string
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
			monitorDetailLine{label: "Checks", value: row.Checks},
			monitorDetailLine{label: "Comments", value: row.Comments},
		)
	} else {
		lines = append(lines,
			monitorDetailLine{label: "State", value: row.State},
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
func renderMonitorDetail(row monitorRow, width, height int, focused bool) string {
	if row.Number == 0 {
		return monitorStyleDim.Render("No selection")
	}
	bodyWidth := maxInt(width-2, 10)
	metadataLines := renderMonitorMetadataLines(monitorDetailMetadata(row), bodyWidth)
	bodyLines := wrapMonitorBody(row.Body, bodyWidth)

	title := truncateMonitorCell(strconv.Itoa(row.Number)+" "+row.Title, bodyWidth-1)
	var sb strings.Builder
	sb.WriteString(renderDetailTitle(title, focused))
	sb.WriteString("\n")
	for _, line := range metadataLines {
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	remaining := height - 1 - len(metadataLines)
	for i := 0; i < remaining; i++ {
		if i < len(bodyLines) {
			sb.WriteString(bodyLines[i])
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderDetailTitle(title string, focused bool) string {
	if focused {
		return monitorStyleActive.Render(title)
	}
	return title
}

// renderMonitorMetadataLines renders each non-empty entry, with labels
// padded to a shared column so values line up.
func renderMonitorMetadataLines(metadata []monitorDetailLine, width int) []string {
	items := make([]monitorDetailLine, 0, len(metadata))
	labelWidth := 0
	for _, item := range metadata {
		if item.value == "" || item.value == "-" {
			continue
		}
		if w := runewidth.StringWidth(item.label); w > labelWidth {
			labelWidth = w
		}
		items = append(items, item)
	}
	lines := make([]string, 0, len(items))
	for _, item := range items {
		lines = append(lines, renderMonitorMetadataItem(item, labelWidth, width))
	}
	return lines
}

func renderMonitorMetadataItem(item monitorDetailLine, labelWidth, width int) string {
	pad := labelWidth - runewidth.StringWidth(item.label)
	label := monitorStyleDim.Render(item.label+":") + strings.Repeat(" ", pad+1)
	valueWidth := maxInt(width-labelWidth-2, 10)
	value := truncateMonitorCell(item.value, valueWidth)
	return label + value
}
