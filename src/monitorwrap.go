package main

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// wrapMonitorBody hard-wraps text by display cells, preserving whitespace.
func wrapMonitorBody(text string, width int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	width = maxInt(width, 10)
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	var wrapped []string
	for _, paragraph := range strings.Split(text, "\n") {
		wrapped = append(wrapped, strings.Split(ansi.Hardwrap(expandMonitorTabs(paragraph), width, true), "\n")...)
	}
	return wrapped
}

func expandMonitorTabs(line string) string {
	var out strings.Builder
	column := 0
	for i, part := range strings.Split(line, "\t") {
		if i > 0 {
			spaces := 4 - column%4
			out.WriteString(strings.Repeat(" ", spaces))
			column += spaces
		}
		out.WriteString(part)
		column += ansi.StringWidth(part)
	}
	return out.String()
}
