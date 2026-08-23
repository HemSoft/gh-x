package main

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// wrapMonitorBody hard-wraps text to width using display cells.
// Blank lines are preserved; long words are split without a suffix.
func wrapMonitorBody(text string, width int) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if width < 10 {
		width = 10
	}
	var wrapped []string
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		wrapped = append(wrapped, wrapMonitorLine(paragraph, width)...)
	}
	return wrapped
}

func wrapMonitorLine(line string, width int) []string {
	if runewidth.StringWidth(line) <= width {
		return []string{line}
	}
	var out []string
	var current string
	flush := func() {
		out = append(out, current)
		current = ""
	}
	for _, word := range strings.Split(line, " ") {
		switch {
		case runewidth.StringWidth(word) > width:
			chunks := splitLongWord(word, width)
			if current != "" {
				flush()
			}
			out = append(out, chunks[:len(chunks)-1]...)
			current = chunks[len(chunks)-1]
		case current == "":
			current = word
		case runewidth.StringWidth(current)+1+runewidth.StringWidth(word) <= width:
			current += " " + word
		default:
			flush()
			current = word
		}
	}
	flush()
	return out
}

// splitLongWord chops an over-long word into width-sized chunks.
func splitLongWord(word string, width int) []string {
	var chunks []string
	for runewidth.StringWidth(word) > width {
		head := runewidth.Truncate(word, width, "")
		chunks = append(chunks, head)
		word = strings.TrimPrefix(word, head)
	}
	if word != "" || len(chunks) == 0 {
		chunks = append(chunks, word)
	}
	return chunks
}
