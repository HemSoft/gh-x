package main

import (
	"strings"
	"testing"
	"time"
)

func monitorRowForTest(repo string, number int, state string) monitorRow {
	return monitorRow{
		Kind: monitorKindPR, Repo: repo, Number: number,
		Title: "title", State: state, UpdatedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestRowKeyFormat(t *testing.T) {
	row := monitorRowForTest("owner/repo", 12, "open")
	if row.key() != "owner/repo#pr#12" {
		t.Fatalf("unexpected key: %q", row.key())
	}
}

func TestReconcileMonitorRowsDetectsAllChangeKinds(t *testing.T) {
	previous := []monitorRow{
		monitorRowForTest("o/r", 1, "open"),
		{Kind: monitorKindPR, Repo: "o/r", Number: 2, Title: "old title", State: "open"},
		monitorRowForTest("o/r", 3, "open"),
	}
	current := []monitorRow{
		{Kind: monitorKindPR, Repo: "o/r", Number: 2, Title: "new title", State: "open"},
		monitorRowForTest("o/r", 4, "open"),
		monitorRowForTest("o/r", 1, "closed"),
	}

	rows, changes := reconcileMonitorRows(previous, current)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	if rows[0].Number != 1 || rows[1].Number != 2 || rows[2].Number != 4 {
		t.Fatalf("order not preserved with new rows appended: %+v", numbersOf(rows))
	}

	added, removed, changed := countMonitorChangeKinds(changes)
	if added != 1 || removed != 1 || changed != 2 {
		t.Fatalf("change kinds wrong: added=%d removed=%d changed=%d", added, removed, changed)
	}
	if changes[0].Key != "o/r#pr#3" || changes[0].Kind != monitorChangeRemoved {
		t.Fatalf("removal should sort first: %+v", changes[0])
	}
	if !containsStr(changes[2].Summary, "State open -> closed") {
		t.Fatalf("row 1 state change missing: %q", changes[2].Summary)
	}
	if !containsStr(changes[3].Summary, "Title old title -> new title") {
		t.Fatalf("row 2 title change missing: %q", changes[3].Summary)
	}
}

func TestChangedAndAddedKeySetsExcludeRemovals(t *testing.T) {
	changes := []monitorChange{
		{Key: "a#pr#1", Kind: monitorChangeRemoved},
		{Key: "a#pr#2", Kind: monitorChangeAdded},
		{Key: "a#pr#3", Kind: monitorChangeField},
	}
	glow := changedKeysSet(changes)
	if glow["a#pr#1"] || !glow["a#pr#2"] || !glow["a#pr#3"] {
		t.Fatalf("glow set wrong: %v", glow)
	}
	added := addedKeysSet(changes)
	if added["a#pr#3"] || !added["a#pr#2"] {
		t.Fatalf("added set wrong: %v", added)
	}
}

func TestSummarizeMonitorChanges(t *testing.T) {
	if got := summarizeMonitorChanges(nil, 2); got != "no changes" {
		t.Fatalf("empty summary wrong: %q", got)
	}
	changes := []monitorChange{
		{Key: "o/r#pr#1", Kind: monitorChangeRemoved},
		{Key: "o/r#pr#2", Kind: monitorChangeAdded},
		{Key: "o/r#pr#3", Kind: monitorChangeField, Summary: "Checks fail -> pass"},
	}
	got := summarizeMonitorChanges(changes, 2)
	if !containsStr(got, "1 added") || !containsStr(got, "1 removed") || !containsStr(got, "+1 more") {
		t.Fatalf("summary missing parts: %q", got)
	}
}

func TestMonitorFieldChangesIgnoresEqualValues(t *testing.T) {
	before := monitorRowForTest("o/r", 1, "open")
	before.Review = "-"
	before.AIReview = "-"
	before.Comments = "-"
	after := before
	if fields := monitorFieldChanges(before, after); len(fields) != 0 {
		t.Fatalf("identical rows should produce no fields: %v", fields)
	}
	after.Checks = "pass"
	fields := monitorFieldChanges(before, after)
	if len(fields) != 1 || !containsStr(fields[0], "Checks  -> pass") {
		t.Fatalf("checks change not detected: %v", fields)
	}
}

func TestDiffMonitorSectionsPairsByIndex(t *testing.T) {
	previous := []monitorSectionData{
		{Kind: monitorKindPR, Rows: []monitorRow{monitorRowForTest("o/r", 5, "open")}},
		{Kind: monitorKindPR, Rows: nil},
	}
	current := []monitorSectionData{
		{Kind: monitorKindPR, Rows: []monitorRow{monitorRowForTest("o/r", 5, "merged")}},
	}
	changes := diffMonitorSections(previous, current)
	if len(changes) != 1 || !containsStr(changes[0].Summary, "State open -> merged") {
		t.Fatalf("section diff wrong: %+v", changes)
	}
}

func numbersOf(rows []monitorRow) []int {
	numbers := make([]int, 0, len(rows))
	for _, row := range rows {
		numbers = append(numbers, row.Number)
	}
	return numbers
}

func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
