package main

import (
	"bytes"
	"github.com/muesli/termenv"
	"reflect"
	"strings"
	"testing"
)

func TestWriteRow(t *testing.T) {
	cells := []tableCell{
		{text: "hello", styled: "hello"},
		{text: "world", styled: "world"},
	}
	widths := []int{8, 5}
	var buf bytes.Buffer
	writeRow(&buf, cells, widths)
	got := buf.String()

	// Last cell should NOT have trailing spaces before newline
	if !strings.HasSuffix(got, "world\n") {
		t.Fatalf("writeRow should end with last cell + newline, got %q", got)
	}

	// First cell should have padding (8 - 5 + 1 = 4 spaces).
	if !strings.Contains(got, "hello    world") {
		t.Fatalf("writeRow padding incorrect, got %q", got)
	}
}

func TestWriteTableHeader(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, false)
	headers := []tableCell{styler.header("First"), styler.header("Second")}
	widths := []int{5, 8}

	writeTableHeader(&buf, styler, headers, widths)

	rule := strings.Repeat("─", tableWidth(widths))
	want := rule + "\nFirst Second\n" + rule + "\n"
	if got := buf.String(); got != want {
		t.Fatalf("writeTableHeader() = %q, want %q", got, want)
	}
}

func TestTableWidth(t *testing.T) {
	tests := []struct {
		name   string
		widths []int
		want   int
	}{
		{name: "empty", widths: nil, want: 0},
		{name: "single column", widths: []int{7}, want: 7},
		{name: "multiple columns include gaps", widths: []int{5, 8, 3}, want: 18},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := tableWidth(test.widths); got != test.want {
				t.Fatalf("tableWidth(%v) = %d, want %d", test.widths, got, test.want)
			}
		})
	}
}

func TestHeaderStyle(t *testing.T) {
	styler := newTableStyler(&bytes.Buffer{}, true)
	got := styler.header("Title")
	want := styler.output.String("Title").Foreground(termenv.ANSICyan).Bold().String()

	if got.text != "Title" || got.styled != want {
		t.Fatalf("header() = %#v, want bold cyan %q", got, want)
	}
}

func TestFitColumnsToTerminal(t *testing.T) {
	tests := []struct {
		name         string
		colWidths    []int
		flexibleCols []int
		termWidth    int
		wantTotal    int
	}{
		{
			name:         "no shrink needed",
			colWidths:    []int{5, 20, 15, 10},
			flexibleCols: []int{1, 2},
			termWidth:    100,
			wantTotal:    -1, // unchanged
		},
		{
			name:         "shrinks to fit",
			colWidths:    []int{5, 40, 30, 10},
			flexibleCols: []int{1, 2},
			termWidth:    80,
			wantTotal:    80,
		},
		{
			name:         "zero term width returns unchanged",
			colWidths:    []int{5, 40, 30, 10},
			flexibleCols: []int{1, 2},
			termWidth:    0,
			wantTotal:    -1,
		},
		{
			name:         "respects minimum width",
			colWidths:    []int{5, 40, 30, 10},
			flexibleCols: []int{1, 2},
			termWidth:    20,
			wantTotal:    -1, // can't fit, but flex cols at minimum 10
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := make([]int, len(tc.colWidths))
			copy(original, tc.colWidths)
			result := fitColumnsToTerminal(tc.colWidths, tc.flexibleCols, tc.termWidth)

			totalWidth := 0
			for i, w := range result {
				totalWidth += w
				if i < len(result)-1 {
					totalWidth += tableColumnGap
				}
			}

			if tc.wantTotal == -1 {
				// Should be unchanged or at minimum
				if tc.termWidth <= 0 {
					if !reflect.DeepEqual(result, original) {
						t.Fatalf("expected unchanged widths for termWidth=0, got %v", result)
					}
				}
			} else {
				if totalWidth > tc.wantTotal {
					t.Fatalf("expected total width ≤%d, got %d (widths: %v)", tc.wantTotal, totalWidth, result)
				}
			}

			// Flexible cols should never go below 10
			for _, idx := range tc.flexibleCols {
				if result[idx] < 10 {
					t.Fatalf("flexible col %d below minimum: %d", idx, result[idx])
				}
			}
		})
	}
}

func TestTruncateCells(t *testing.T) {
	rows := [][]tableCell{
		{
			{text: "#1", styled: "#1"},
			{text: "A very long title that exceeds limit", styled: "A very long title that exceeds limit"},
			{text: "short", styled: "short"},
		},
	}
	colWidths := []int{5, 15, 10}
	flexibleCols := []int{1}

	result := truncateCells(rows, colWidths, flexibleCols)
	if len(result[0][1].text) > 15 {
		t.Fatalf("expected truncated title ≤15 chars, got %q (%d)", result[0][1].text, len(result[0][1].text))
	}
	if !strings.HasSuffix(result[0][1].text, "...") {
		t.Fatalf("expected ... suffix, got %q", result[0][1].text)
	}
	// Non-flexible col unchanged
	if result[0][2].text != "short" {
		t.Fatalf("non-flexible col should be unchanged, got %q", result[0][2].text)
	}
}

func TestTruncateCellsPreservesStyleFn(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, true)

	longBranch := "feature/very-long-branch-name-that-exceeds-column-width"
	rows := [][]tableCell{
		{
			styler.plain("#1"),
			styler.colored(longBranch, termenv.ANSICyan),
		},
	}
	colWidths := []int{5, 15}
	flexibleCols := []int{1}

	result := truncateCells(rows, colWidths, flexibleCols)
	cell := result[0][1]

	// text should be truncated
	if len(cell.text) > 15 {
		t.Fatalf("expected truncated text ≤15 chars, got %q (%d)", cell.text, len(cell.text))
	}
	if !strings.HasSuffix(cell.text, "...") {
		t.Fatalf("expected ... suffix, got %q", cell.text)
	}

	// styled should differ from text (ANSI codes preserved)
	if cell.styled == cell.text {
		t.Fatalf("expected styled to contain ANSI codes, but styled == text: %q", cell.styled)
	}

	// styled should contain the truncated text, not the original long text
	if strings.Contains(cell.styled, longBranch) {
		t.Fatal("styled should contain truncated text, not original")
	}

	// styleFn should still be set for further re-styling
	if cell.styleFn == nil {
		t.Fatal("expected styleFn to be preserved after truncation")
	}
}
