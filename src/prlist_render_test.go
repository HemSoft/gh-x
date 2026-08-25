package main

import (
	"bytes"
	"github.com/muesli/termenv"
	"strings"
	"testing"
)

func TestRenderTableNoColor(t *testing.T) {
	var buf bytes.Buffer
	prs := []displayPullRequest{
		{Number: 42, Title: "My PR", Author: "user", State: "open", Review: "approved", AIReview: "fail", Approvals: 0, Checks: "pass", Comments: "3/5", Branch: "feat", Updated: "2h"},
	}
	err := renderTableWithStyle(&buf, listOptions{}, prs, false)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if strings.Contains(output, "\x1b[") {
		t.Fatal("expected no ANSI escape codes when color is disabled")
	}
	if !strings.Contains(output, "#42") {
		t.Fatal("expected PR number in output")
	}
	if !strings.Contains(output, "My PR") {
		t.Fatal("expected title in output")
	}
	if !strings.Contains(output, "✓") {
		t.Fatal("expected compact approval symbols in output")
	}
	if !strings.Contains(output, "Rev") || !strings.Contains(output, "Upd") {
		t.Fatalf("expected compact PR headers, got %q", output)
	}
}

func TestRenderTableWithColor(t *testing.T) {
	var buf bytes.Buffer
	prs := []displayPullRequest{
		{Number: 7, Title: "Add colors", Author: "dev", State: "open", Review: "review", AIReview: "-", Approvals: 0, Checks: "pending", Comments: "-", Branch: "color", Updated: "5m"},
	}
	err := renderTableWithStyle(&buf, listOptions{}, prs, true)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "\x1b[") {
		t.Fatal("expected ANSI escape codes when color is enabled")
	}
	if !strings.Contains(output, "#7") {
		t.Fatal("expected PR number in output")
	}
}

func TestRenderTableAlignment(t *testing.T) {
	var buf bytes.Buffer
	prs := []displayPullRequest{
		{Number: 1, Title: "Short", Author: "a", State: "open", Review: "-", AIReview: "-", Approvals: 0, Checks: "-", Comments: "-", Branch: "x", Updated: "1h"},
		{Number: 999, Title: "Longer title here", Author: "longuser", State: "merged", Review: "approved", AIReview: "pass", Approvals: 3, Checks: "pass", Comments: "5/5", Branch: "feature/long", Updated: "30d"},
	}
	err := renderTableWithStyle(&buf, listOptions{}, prs, false)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines (rules + header + 2 rows), got %d: %v", len(lines), lines)
	}
	if lines[0] != lines[2] || strings.Trim(lines[0], "─") != "" {
		t.Fatalf("expected matching horizontal rules around header, got %q and %q", lines[0], lines[2])
	}

	// Verify header labels are present
	if !strings.Contains(lines[1], "Title") || !strings.Contains(lines[1], "Branch") {
		t.Fatal("expected header labels")
	}

	// Verify columns are aligned: the "Title" column should start at the same
	// position in header and data rows
	headerTitleIdx := strings.Index(lines[1], "Title")
	row1TitleIdx := strings.Index(lines[3], "Short")
	row2TitleIdx := strings.Index(lines[4], "Longer")
	if headerTitleIdx != row1TitleIdx || headerTitleIdx != row2TitleIdx {
		t.Fatalf("Title column misaligned: header=%d row1=%d row2=%d", headerTitleIdx, row1TitleIdx, row2TitleIdx)
	}
}

func TestRenderTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := renderTableWithStyle(&buf, listOptions{}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No pull requests found") {
		t.Fatal("expected empty message")
	}
}

func TestRenderTableLimitNotice(t *testing.T) {
	prs := make([]displayPullRequest, 3)
	for i := range prs {
		prs[i] = displayPullRequest{Number: i + 1, Title: "PR", Author: "u", State: "open", Review: "-", AIReview: "-", Checks: "-", Comments: "-", Branch: "b", Updated: "1h"}
	}

	t.Run("shows notice at limit", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderTableWithStyle(&buf, listOptions{limit: 3}, prs, false)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(buf.String(), "limit reached") {
			t.Fatal("expected limit notice when count == limit")
		}
	})

	t.Run("no notice below limit", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderTableWithStyle(&buf, listOptions{limit: 10}, prs, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "limit reached") {
			t.Fatal("should not show notice when under limit")
		}
	})
}

func TestStateCellAllStates(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, false)
	tests := []struct {
		state, wantText string
	}{
		{"open", "open"},
		{"draft", "draft"},
		{"closed", "closed"},
		{"merged", "merged"},
		{"unknown", "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			cell := styler.stateCell(tc.state)
			if cell.text != tc.wantText {
				t.Fatalf("stateCell(%q).text = %q, want %q", tc.state, cell.text, tc.wantText)
			}
		})
	}
}

func TestReviewCellAllDecisions(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, false)
	tests := []struct {
		review string
		want   string
	}{
		{review: "approved", want: "✓"},
		{review: "changes", want: "✗"},
		{review: "review", want: "•"},
		{review: "-", want: "-"},
	}
	for _, tc := range tests {
		cell := styler.reviewCell(tc.review)
		if cell.text != tc.want {
			t.Fatalf("reviewCell(%q).text = %q, want %q", tc.review, cell.text, tc.want)
		}
	}
}

func TestChecksCellAllStates(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, false)
	for _, state := range []string{"pass", "review", "fail", "pending", "merge", "-"} {
		cell := styler.checksCell(state)
		if cell.text != state {
			t.Fatalf("checksCell(%q).text = %q", state, cell.text)
		}
	}
}

func TestChecksCellReviewIsGreen(t *testing.T) {
	styler := newTableStyler(bytes.NewBuffer(nil), true)
	got := styler.checksCell("review")
	want := styler.colored("review", termenv.ANSIGreen)
	if got.styled != want.styled {
		t.Fatalf("checksCell(\"review\").styled = %q, want green %q", got.styled, want.styled)
	}
}

func TestRenderListOutputJSON(t *testing.T) {
	var buf bytes.Buffer
	prs := []displayPullRequest{
		{Number: 1, Title: "Test PR"},
	}
	err := renderListOutput(&buf, listOptions{json: true}, prs)
	if err != nil {
		t.Fatalf("renderListOutput(json) error: %v", err)
	}
	if !strings.Contains(buf.String(), `"number": 1`) {
		t.Fatalf("JSON output missing PR number: %s", buf.String())
	}
}

func TestApprovalCell(t *testing.T) {
	s := newTableStyler(bytes.NewBuffer(nil), true)
	dim0 := s.dim("0")
	green1 := s.colored("1", termenv.ANSIGreen)

	got0 := s.approvalCell(0)
	if got0.text != "0" || got0.styled != dim0.styled {
		t.Fatalf("approvalCell(0): got styled=%q, want dim=%q", got0.styled, dim0.styled)
	}

	got1 := s.approvalCell(1)
	if got1.text != "1" || got1.styled != green1.styled {
		t.Fatalf("approvalCell(1): got styled=%q, want green=%q", got1.styled, green1.styled)
	}
}

func TestCommentsCell(t *testing.T) {
	s := newTableStyler(bytes.NewBuffer(nil), true)
	tests := []struct {
		name     string
		input    string
		aiClean  *bool
		wantText string
		wantKind string // "plain", "green", "red", "yellow", "green+bang", "yellow+bang", "red+bang", "plain+bang"
	}{
		{"dash is plain", "-", boolPtr(false), "-", "plain"},
		{"question is plain", "?", boolPtr(false), "?", "plain"},
		{"all resolved is green", "2/2", boolPtr(false), "2/2", "green"},
		{"none resolved is red", "0/2", boolPtr(false), "0/2", "red"},
		{"partial is yellow", "1/2", boolPtr(false), "1/2", "yellow"},
		{"no slash is yellow", "5", boolPtr(false), "5", "yellow"},
		{"ai clean with dash", "-", boolPtr(true), "0/0!", "green+bang"},
		{"ai clean all resolved", "2/2", boolPtr(true), "2/2!", "green+bang"},
		{"ai clean partial", "1/2", boolPtr(true), "1/2!", "yellow+bang"},
		{"ai clean none resolved", "0/2", boolPtr(true), "0/2!", "red+bang"},
		{"nil aiClean treated as false", "2/2", nil, "2/2", "green"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := s.commentsCell(tc.input, tc.aiClean)
			if got.text != tc.wantText {
				t.Fatalf("text = %q, want %q", got.text, tc.wantText)
			}
			var wantStyled string
			base := strings.TrimSuffix(tc.wantText, "!")
			switch tc.wantKind {
			case "plain":
				wantStyled = s.plain(tc.input).styled
			case "green":
				wantStyled = s.colored(tc.input, termenv.ANSIGreen).styled
			case "red":
				wantStyled = s.colored(tc.input, termenv.ANSIRed).styled
			case "yellow":
				wantStyled = s.colored(tc.input, termenv.ANSIYellow).styled
			case "plain+bang":
				wantStyled = s.output.String(base).String() +
					s.output.String("!").Foreground(termenv.ANSIBrightGreen).String()
			case "green+bang":
				wantStyled = s.output.String(base).Foreground(termenv.ANSIGreen).String() +
					s.output.String("!").Foreground(termenv.ANSIBrightGreen).String()
			case "yellow+bang":
				wantStyled = s.output.String(base).Foreground(termenv.ANSIYellow).String() +
					s.output.String("!").Foreground(termenv.ANSIBrightGreen).String()
			case "red+bang":
				wantStyled = s.output.String(base).Foreground(termenv.ANSIRed).String() +
					s.output.String("!").Foreground(termenv.ANSIBrightGreen).String()
			}
			if got.styled != wantStyled {
				t.Fatalf("styled mismatch for %q (aiClean=%v): got %q, want %s %q", tc.input, tc.aiClean, got.styled, tc.wantKind, wantStyled)
			}
		})
	}
}
