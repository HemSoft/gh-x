package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

type tableCell struct {
	text    string              // plain text for width calculation
	styled  string              // styled text for display (may contain ANSI codes)
	styleFn func(string) string // re-applies styling to new text (e.g. after truncation)
}

// withText returns a copy of the cell with new text, re-applying styleFn if set.
func (c tableCell) withText(text string) tableCell {
	styled := text
	if c.styleFn != nil {
		styled = c.styleFn(text)
	}
	return tableCell{text: text, styled: styled, styleFn: c.styleFn}
}

type tableStyler struct {
	output       *termenv.Output
	colorEnabled bool
}

func newTableStyler(w io.Writer, colorEnabled bool) tableStyler {
	profile := termenv.Ascii
	if colorEnabled {
		profile = termenv.ANSI
	}
	output := termenv.NewOutput(w, termenv.WithProfile(profile))
	return tableStyler{output: output, colorEnabled: colorEnabled}
}

func (s tableStyler) colored(text string, color termenv.ANSIColor) tableCell {
	fn := func(t string) string {
		return s.output.String(t).Foreground(color).String()
	}
	return tableCell{text: text, styled: fn(text), styleFn: fn}
}

func (s tableStyler) dim(text string) tableCell {
	fn := func(t string) string {
		return s.output.String(t).Faint().String()
	}
	return tableCell{text: text, styled: fn(text), styleFn: fn}
}

func (s tableStyler) plain(text string) tableCell {
	return tableCell{text: text, styled: text}
}

func (s tableStyler) linkCell(text, url string, color termenv.ANSIColor) tableCell {
	fn := func(t string) string {
		styled := s.output.String(t).Foreground(color).String()
		if url != "" && s.colorEnabled {
			styled = fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", url, styled)
		}
		return styled
	}
	return tableCell{text: text, styled: fn(text), styleFn: fn}
}

func (s tableStyler) dimLinkCell(text, url string) tableCell {
	fn := func(t string) string {
		styled := s.output.String(t).Faint().String()
		if url != "" && s.colorEnabled {
			styled = fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", url, styled)
		}
		return styled
	}
	return tableCell{text: text, styled: fn(text), styleFn: fn}
}

func writeRow(w io.Writer, cells []tableCell, widths []int) {
	for i, cell := range cells {
		fmt.Fprint(w, cell.styled)
		if i < len(cells)-1 {
			padding := widths[i] - runewidth.StringWidth(cell.text) + 2
			fmt.Fprint(w, strings.Repeat(" ", padding))
		}
	}
	fmt.Fprintln(w)
}

func computeColumnWidths(headers []tableCell, rows [][]tableCell) []int {
	colWidths := make([]int, len(headers))
	for i, h := range headers {
		if w := runewidth.StringWidth(h.text); w > colWidths[i] {
			colWidths[i] = w
		}
	}
	for _, row := range rows {
		for i, cell := range row {
			if w := runewidth.StringWidth(cell.text); w > colWidths[i] {
				colWidths[i] = w
			}
		}
	}
	return colWidths
}

// writeOSC52 copies text to the system clipboard via the OSC 52 escape sequence.
// Silently ignored by terminals that don't support it.
func writeOSC52(w io.Writer, text string) {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	fmt.Fprintf(w, "\x1b]52;c;%s\x07", encoded)
}

// getTerminalWidth returns the terminal width, or 0 if unavailable.
func getTerminalWidth() int {
	w, _, err := term.FromEnv().Size()
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// fitColumnsToTerminal shrinks flexible columns so total row width fits
// within the terminal. flexibleCols are indices of columns that can be
// truncated (e.g., Title, Repo, Author, Branch). Each flexible column
// has a minimum width of 10.
func fitColumnsToTerminal(colWidths []int, flexibleCols []int, termWidth int) []int {
	if termWidth <= 0 {
		return colWidths
	}

	const colGap = 2
	const minFlexWidth = 10

	totalWidth := 0
	for i, w := range colWidths {
		totalWidth += w
		if i < len(colWidths)-1 {
			totalWidth += colGap
		}
	}

	overflow := totalWidth - termWidth
	if overflow <= 0 {
		return colWidths
	}

	// Shrink flexible columns proportionally
	result := make([]int, len(colWidths))
	copy(result, colWidths)

	for overflow > 0 {
		// Find widest flexible column
		widestIdx := -1
		widestWidth := 0
		for _, idx := range flexibleCols {
			if result[idx] > widestWidth && result[idx] > minFlexWidth {
				widestWidth = result[idx]
				widestIdx = idx
			}
		}
		if widestIdx == -1 {
			break // all at minimum, can't shrink further
		}

		// Shrink by 1 character at a time
		result[widestIdx]--
		overflow--
	}

	return result
}

// truncateCells trims cell text/styled content to fit within colWidths.
// Only applies to cells in flexibleCols. Cells that exceed their allotted
// width get truncated with "..." suffix.
func truncateCells(rows [][]tableCell, colWidths []int, flexibleCols []int) [][]tableCell {
	flexSet := make(map[int]bool, len(flexibleCols))
	for _, idx := range flexibleCols {
		flexSet[idx] = true
	}

	for i, row := range rows {
		for j, cell := range row {
			if !flexSet[j] {
				continue
			}
			if runewidth.StringWidth(cell.text) > colWidths[j] {
				trimmed := trimTitle(cell.text, colWidths[j])
				rows[i][j] = cell.withText(trimmed)
			}
		}
	}
	return rows
}
