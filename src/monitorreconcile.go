package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type monitorChangeKind int

const (
	monitorChangeAdded monitorChangeKind = iota
	monitorChangeRemoved
	monitorChangeField
)

type monitorChange struct {
	Key     string
	Kind    monitorChangeKind
	Summary string
}

// priority orders changes in the footer: removals first, then additions,
// then field edits (matching watch-mode urgency).
func (c monitorChange) priority() int {
	switch c.Kind {
	case monitorChangeRemoved:
		return 0
	case monitorChangeAdded:
		return 1
	default:
		return 2
	}
}

func (c monitorChange) String() string {
	switch c.Kind {
	case monitorChangeAdded:
		return c.Key + " added"
	case monitorChangeRemoved:
		return c.Key + " removed"
	default:
		return c.Key + " " + c.Summary
	}
}

// reconcileMonitorRows diffs two refreshes. Returned rows keep previous order
// (stable scanning) with new rows appended; removed rows are dropped.
func reconcileMonitorRows(previous, current []monitorRow) ([]monitorRow, []monitorChange) {
	currentByKey := make(map[string]monitorRow, len(current))
	for _, row := range current {
		currentByKey[row.key()] = row
	}

	ordered := make([]monitorRow, 0, len(current))
	seen := make(map[string]bool, len(current))
	changes := make([]monitorChange, 0)

	for _, before := range previous {
		key := before.key()
		after, ok := currentByKey[key]
		if !ok {
			changes = append(changes, monitorChange{Key: key, Kind: monitorChangeRemoved})
			continue
		}
		ordered = append(ordered, after)
		seen[key] = true
		if change := diffMonitorRow(before, after); change != nil {
			changes = append(changes, *change)
		}
	}
	for _, row := range current {
		if !seen[row.key()] {
			ordered = append(ordered, row)
			changes = append(changes, monitorChange{Key: row.key(), Kind: monitorChangeAdded})
		}
	}
	sortMonitorChanges(changes)
	return ordered, changes
}

func sortMonitorChanges(changes []monitorChange) {
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].priority() != changes[j].priority() {
			return changes[i].priority() < changes[j].priority()
		}
		return changes[i].Key < changes[j].Key
	})
}

// diffMonitorRow returns a field-change record or nil when unchanged.
func diffMonitorRow(before, after monitorRow) *monitorChange {
	fields := monitorFieldChanges(before, after)
	if len(fields) == 0 {
		return nil
	}
	return &monitorChange{
		Key:     after.key(),
		Kind:    monitorChangeField,
		Summary: strings.Join(fields, "; "),
	}
}

func monitorFieldChanges(before, after monitorRow) []string {
	var fields []string
	appendIfChanged := func(label, beforeValue, afterValue string) {
		if beforeValue != afterValue {
			fields = append(fields, fmt.Sprintf("%s %s -> %s", label, beforeValue, afterValue))
		}
	}
	appendIfChanged("State", before.State, after.State)
	appendIfChanged("Title", before.Title, after.Title)
	appendIfChanged("Review", before.Review, after.Review)
	appendIfChanged("AI", before.AIReview, after.AIReview)
	appendIfChanged("AI clean", formatMonitorClean(before.AIClean), formatMonitorClean(after.AIClean))
	appendIfChanged("Approvals", strconv.Itoa(before.Approvals), strconv.Itoa(after.Approvals))
	appendIfChanged("Checks", before.Checks, after.Checks)
	appendIfChanged("Comments", before.Comments, after.Comments)
	return fields
}

func formatMonitorClean(clean *bool) string {
	if clean != nil && *clean {
		return "yes"
	}
	return "no"
}

// summarizeMonitorChanges renders the footer line, capping detail items.
func summarizeMonitorChanges(changes []monitorChange, maxItems int) string {
	if len(changes) == 0 {
		return "no changes"
	}
	counts := summarizeMonitorCounts(changes)
	shown := make([]string, 0, maxItems)
	for _, change := range changes {
		if len(shown) == maxItems {
			break
		}
		shown = append(shown, change.String())
	}
	summary := counts
	if len(shown) > 0 {
		summary += ": " + strings.Join(shown, ", ")
	}
	if remaining := len(changes) - len(shown); remaining > 0 {
		summary += fmt.Sprintf(", +%d more", remaining)
	}
	return summary
}

func summarizeMonitorCounts(changes []monitorChange) string {
	added, removed, changed := countMonitorChangeKinds(changes)
	parts := make([]string, 0, 3)
	if added > 0 {
		parts = append(parts, fmt.Sprintf("%d added", added))
	}
	if removed > 0 {
		parts = append(parts, fmt.Sprintf("%d removed", removed))
	}
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", changed))
	}
	return strings.Join(parts, ", ")
}

func countMonitorChangeKinds(changes []monitorChange) (added, removed, changed int) {
	for _, change := range changes {
		switch change.Kind {
		case monitorChangeAdded:
			added++
		case monitorChangeRemoved:
			removed++
		default:
			changed++
		}
	}
	return added, removed, changed
}

// changedKeysSet collects keys that should glow (all change kinds except
// pure removals, which have no row to highlight).
func changedKeysSet(changes []monitorChange) map[string]bool {
	keys := make(map[string]bool, len(changes))
	for _, change := range changes {
		if change.Kind != monitorChangeRemoved {
			keys[change.Key] = true
		}
	}
	return keys
}
