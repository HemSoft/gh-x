package main

import (
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
)

type monitorColumn struct {
	Title   string
	Width   int
	Flex    bool // flexible columns absorb width changes
	Primary bool // the main reading column, protected while shrinking
	Min     int  // floor for flex columns during fair shrinking
}

const (
	monitorTitleMinWidth  = 18
	monitorCellMinWidth   = 4
	monitorCellGap        = 1
	monitorChangedMarker  = "●"
	monitorAddedMarker    = "＋"
	monitorTruncateSuffix = "…"
)

// monitorPRColumns defines the enriched PR table shape.
func monitorPRColumns() []monitorColumn {
	return []monitorColumn{
		{Title: "#", Width: 6},
		{Title: "Title", Width: monitorTitleMinWidth, Flex: true, Primary: true, Min: monitorTitleMinWidth},
		{Title: "Repo", Width: 12, Flex: true, Min: 8},
		{Title: "Author", Width: 12, Flex: true, Min: 8},
		{Title: "State", Width: 7},
		{Title: "Rev", Width: 4},
		{Title: "AI", Width: 5},
		{Title: "Appv", Width: 4},
		{Title: "Checks", Width: 8},
		{Title: "Cmts", Width: 6},
		{Title: "Upd", Width: 5},
	}
}

// monitorIssueColumns defines the issue table shape.
func monitorIssueColumns() []monitorColumn {
	return []monitorColumn{
		{Title: "#", Width: 6},
		{Title: "Title", Width: monitorTitleMinWidth, Flex: true, Primary: true, Min: monitorTitleMinWidth},
		{Title: "Repo", Width: 12, Flex: true, Min: 8},
		{Title: "Author", Width: 12, Flex: true, Min: 8},
		{Title: "State", Width: 7},
		{Title: "Labels", Width: 16, Flex: true, Min: 6},
		{Title: "Assignees", Width: 14, Flex: true, Min: 6},
		{Title: "Upd", Width: 5},
	}
}

func monitorColumnsForKind(kind monitorRowKind) []monitorColumn {
	if kind == monitorKindIssue {
		return monitorIssueColumns()
	}
	return monitorPRColumns()
}

func monitorRowCells(kind monitorRowKind, row monitorRow) []string {
	if kind == monitorKindIssue {
		return []string{
			strconv.Itoa(row.Number), row.Title, row.Repo, row.Author,
			row.State, strings.Join(row.Labels, ","), row.Assignees, row.Updated,
		}
	}
	return []string{
		strconv.Itoa(row.Number), row.Title, row.Repo, row.Author,
		row.State, row.Review, row.AIReview, strconv.Itoa(row.Approvals),
		row.Checks, row.Comments, row.Updated,
	}
}

// planMonitorColumns caps natural widths and shrinks flex columns to fit.
func planMonitorColumns(columns []monitorColumn, rows [][]string, availWidth int) []monitorColumn {
	planned := make([]monitorColumn, len(columns))
	copy(planned, columns)
	for i := range planned {
		natural := len(planned[i].Title)
		for _, cells := range rows {
			if i < len(cells) {
				natural = maxInt(natural, runewidth.StringWidth(cells[i]))
			}
		}
		if !planned[i].Flex && natural < planned[i].Width {
			continue
		}
		planned[i].Width = minInt(natural, planned[i].Width+2)
	}
	shrinkMonitorColumns(planned, availWidth)
	growMonitorColumns(planned, availWidth)
	return planned
}

// growMonitorColumns spends leftover width on flex columns, widest first,
// so the table spans the full list width instead of stopping early.
func growMonitorColumns(planned []monitorColumn, availWidth int) {
	for {
		leftover := -monitorTableOverflow(planned, availWidth)
		if leftover <= 0 {
			return
		}
		index := widestFlexibleColumn(planned)
		if index < 0 {
			return
		}
		planned[index].Width++
	}
}

// shrinkMonitorColumns first shrinks flex columns widest-first while
// respecting their minimums; if the table still does not fit, it squeezes
// past those minimums toward a hard floor, taking from the title last.
func shrinkMonitorColumns(planned []monitorColumn, availWidth int) {
	floors := make([]int, len(planned))
	hard := make([]int, len(planned))
	primary := -1
	for i, col := range planned {
		floors[i] = col.Min
		switch {
		case !col.Flex:
			hard[i] = col.Width
		case col.Primary:
			hard[i] = monitorCellMinWidth
			primary = i
		default:
			hard[i] = monitorCellMinWidth
		}
	}
	shrinkPass(planned, floors, availWidth, -1)
	shrinkPass(planned, hard, availWidth, primary)
}

func shrinkPass(planned []monitorColumn, floors []int, availWidth int, protect int) {
	for monitorTableOverflow(planned, availWidth) > 0 {
		index := widestFlexAbove(planned, floors, protect)
		if index < 0 {
			return
		}
		planned[index].Width--
	}
}

// widestFlexAbove picks the widest flexible column still above its floor.
// The protected column is only chosen when nothing else can give up width,
// so the title keeps its reading width as long as possible.
func widestFlexAbove(planned []monitorColumn, floors []int, protect int) int {
	best, bestWidth := -1, 0
	for i, col := range planned {
		if i == protect || !col.Flex || col.Width <= floors[i] || col.Width <= bestWidth {
			continue
		}
		best, bestWidth = i, col.Width
	}
	if best >= 0 {
		return best
	}
	if protect >= 0 && planned[protect].Flex && planned[protect].Width > floors[protect] {
		return protect
	}
	return -1
}

func monitorTableOverflow(planned []monitorColumn, availWidth int) int {
	total := 0
	for i, col := range planned {
		total += col.Width
		if i < len(planned)-1 {
			total += monitorCellGap
		}
	}
	return total - availWidth
}

func widestFlexibleColumn(planned []monitorColumn) int {
	index := -1
	width := 0
	for i, col := range planned {
		if col.Flex && col.Width > width {
			index = i
			width = col.Width
		}
	}
	return index
}

func truncateMonitorCell(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(text) <= width {
		return text
	}
	if width == 1 {
		return monitorTruncateSuffix
	}
	return runewidth.Truncate(text, width-1, "") + monitorTruncateSuffix
}

type monitorTableRenderInput struct {
	Kind        monitorRowKind
	Rows        []monitorRow
	Cursor      int
	Offset      int
	Height      int
	Width       int
	ChangedKeys map[string]bool
	AddedKeys   map[string]bool
}

// renderMonitorTable renders the visible slice of rows with header.
func renderMonitorTable(input monitorTableRenderInput) string {
	columns := monitorColumnsForKind(input.Kind)
	cells := make([][]string, len(input.Rows))
	for i, row := range input.Rows {
		cells[i] = monitorRowCells(input.Kind, row)
	}
	planned := planMonitorColumns(columns, cells, input.Width)

	var sb strings.Builder
	sb.WriteString(renderMonitorHeaderLine(planned))
	sb.WriteString("\n")

	last := clampInt(input.Offset+input.Height-1, 0, len(input.Rows)-1)
	for i := input.Offset; i <= last && len(input.Rows) > 0; i++ {
		line := renderMonitorRowLine(planned, cells[i])
		sb.WriteString(decorateMonitorRowLine(line, input.Rows[i], input, i))
		sb.WriteString("\n")
	}
	for i := last - input.Offset + 1; i < input.Height; i++ {
		sb.WriteString("\n")
	}
	return sb.String()
}

func renderMonitorHeaderLine(planned []monitorColumn) string {
	parts := make([]string, len(planned))
	for i, col := range planned {
		text := truncateMonitorCell(col.Title, col.Width)
		parts[i] = monitorStyleHeader.Render(text + padTo(col.Width, text))
	}
	// Data rows carry a one-cell change marker before column 0; the header
	// reserves the same gutter so titles sit above their data.
	return " " + strings.Join(parts, strings.Repeat(" ", monitorCellGap))
}

// padTo returns the padding needed for text to reach width.
func padTo(width int, text string) string {
	padding := width - runewidth.StringWidth(text)
	if padding < 0 {
		return ""
	}
	return strings.Repeat(" ", padding)
}

func renderMonitorRowLine(planned []monitorColumn, cells []string) string {
	parts := make([]string, len(planned))
	for i, col := range planned {
		text := ""
		if i < len(cells) {
			text = truncateMonitorCell(cells[i], col.Width)
		}
		parts[i] = text + padTo(col.Width, text)
	}
	return strings.Join(parts, strings.Repeat(" ", monitorCellGap))
}

// decorateMonitorRowLine applies selection, glow, and marker styling.
func decorateMonitorRowLine(line string, row monitorRow, input monitorTableRenderInput, index int) string {
	marker := " "
	switch {
	case input.AddedKeys[row.key()]:
		marker = monitorStyleAdded.Render(monitorAddedMarker)
	case input.ChangedKeys[row.key()]:
		marker = monitorStyleChanged.Render(monitorChangedMarker)
	}
	if index == input.Cursor {
		return monitorStyleSelected.Render(marker + line)
	}
	return marker + line
}
