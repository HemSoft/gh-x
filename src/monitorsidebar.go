package main

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
)

// monitorRepoCounts summarizes loaded rows per repo for sidebar badges.
type monitorRepoCounts struct {
	PRs        int
	Issues     int
	Accessible bool
}

func countMonitorRowsByRepo(result *monitorFetchResult, repos []string) map[string]monitorRepoCounts {
	counts := make(map[string]monitorRepoCounts)
	for _, repo := range repos {
		counts[repo] = monitorRepoCounts{Accessible: true}
	}
	if result == nil {
		return counts
	}
	for repo, accessible := range result.Accessible {
		entry := counts[repo]
		entry.Accessible = accessible
		counts[repo] = entry
	}
	for i := range result.PRSections {
		for _, row := range result.PRSections[i].Rows {
			entry := counts[row.Repo]
			if _, known := counts[row.Repo]; !known {
				entry.Accessible = true
			}
			entry.PRs++
			counts[row.Repo] = entry
		}
	}
	for i := range result.IssueSections {
		for _, row := range result.IssueSections[i].Rows {
			entry := counts[row.Repo]
			if _, known := counts[row.Repo]; !known {
				entry.Accessible = true
			}
			entry.Issues++
			counts[row.Repo] = entry
		}
	}
	return counts
}

func renderMonitorSidebar(repos []string, selected int, counts map[string]monitorRepoCounts, height int, width int, focused bool) string {
	header := monitorStyleDim.Render("REPOS")
	if focused {
		header = monitorStyleActive.Render("REPOS")
	}
	lines := []string{
		header,
		renderMonitorSidebarEntry(monitorRepoAll, selected == 0, monitorRepoCounts{Accessible: true}, true, true, width),
	}
	for i, repo := range repos {
		accessible := accessibleForDisplay(counts, repo)
		lines = append(lines, renderMonitorSidebarEntry(repo, selected == i+1, counts[repo], false, accessible, width))
	}
	if len(lines) > maxInt(height, 1) {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

// accessibleForDisplay reports visibility for badge rendering; unknown
// entries (probe absent) count as visible.
func accessibleForDisplay(counts map[string]monitorRepoCounts, repo string) bool {
	return counts[repo].Accessible
}

func renderMonitorSidebarEntry(label string, isSelected bool, counts monitorRepoCounts, isAll bool, accessible bool, width int) string {
	badge := sidebarBadge(counts, isAll, accessible)
	badgeWidth := lipgloss.Width(badge)
	nameWidth := width
	if badgeWidth > 0 {
		nameWidth = width - badgeWidth - 1
	}
	name := truncateMonitorRepoName(label, nameWidth)
	gap := width - runewidth.StringWidth(name) - badgeWidth
	if gap < 0 {
		gap = 0
	}
	line := name + strings.Repeat(" ", gap) + badge
	if isSelected {
		return monitorStyleSelected.Render(line)
	}
	return line
}

// truncateMonitorRepoName spends the width budget on the repo half of an
// "org/name" label: the tail after the last slash always stays readable and
// the org head absorbs the cut.
func truncateMonitorRepoName(label string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(label) <= width {
		return label
	}
	slash := strings.LastIndex(label, "/")
	if slash >= 0 {
		tail := label[slash:]
		tailWidth := runewidth.StringWidth(tail)
		if tailWidth <= width {
			org := truncateMonitorCell(label[:slash], maxInt(width-tailWidth, 0))
			return org + tail
		}
		return ellipsizeMonitorLeft(tail, width)
	}
	return truncateMonitorCell(label, width)
}

// ellipsizeMonitorLeft trims from the head, keeping the tail end.
func ellipsizeMonitorLeft(text string, width int) string {
	if width <= 0 || runewidth.StringWidth(text) <= width {
		return truncateMonitorCell(text, width)
	}
	runes := []rune(text)
	budget := width - len([]rune(monitorTruncateSuffix))
	kept := make([]rune, 0, len(runes))
	used := 0
	for i := len(runes) - 1; i >= 0; i-- {
		w := runewidth.RuneWidth(runes[i])
		if used+w > budget {
			break
		}
		used += w
		kept = append(kept, runes[i])
	}
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return monitorTruncateSuffix + string(kept)
}

// sidebarBadge renders the right-aligned hint: counts, "all", or an access
// marker for repos this account cannot see (the footer names them).
func sidebarBadge(counts monitorRepoCounts, isAll bool, accessible bool) string {
	switch {
	case isAll:
		return monitorStyleDim.Render("all")
	case !accessible:
		return monitorStyleError.Render("×")
	case counts.PRs > 0 || counts.Issues > 0:
		parts := make([]string, 0, 2)
		if counts.PRs > 0 {
			parts = append(parts, strconv.Itoa(counts.PRs)+"pr")
		}
		if counts.Issues > 0 {
			parts = append(parts, strconv.Itoa(counts.Issues)+"is")
		}
		return monitorStyleDim.Render(strings.Join(parts, " "))
	default:
		return ""
	}
}
