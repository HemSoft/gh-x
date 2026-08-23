package main

import "charm.land/lipgloss/v2"

// Monitor layout geometry. The screen is: full-height sidebar on the left,
// main area right of it with a tab row, section sub-tab row, list pane,
// detail pane, and a one-line footer spanning the full width.
const (
	monitorSidebarWidth = 30
	monitorFooterHeight = 1
	monitorTabRowHeight = 1
	monitorSubTabHeight = 1
	monitorMinWidth     = 60
	monitorMinHeight    = 16

	monitorDetailMinHeight = 5
	monitorDetailMaxHeight = 16
)

type monitorLayout struct {
	Width        int
	Height       int
	SidebarWidth int
	ListTop      int
	ListHeight   int
	DetailTop    int
	DetailHeight int
	FooterTop    int
}

func computeMonitorLayout(width, height int) monitorLayout {
	detailHeight := height / 4
	if detailHeight < monitorDetailMinHeight {
		detailHeight = monitorDetailMinHeight
	}
	if detailHeight > monitorDetailMaxHeight {
		detailHeight = monitorDetailMaxHeight
	}
	contentHeight := height - monitorFooterHeight - monitorTabRowHeight - monitorSubTabHeight
	listHeight := contentHeight - detailHeight
	if listHeight < 1 {
		listHeight = 1
	}
	return monitorLayout{
		Width:        width,
		Height:       height,
		SidebarWidth: minInt(monitorSidebarWidth, width),
		ListTop:      monitorTabRowHeight + monitorSubTabHeight,
		ListHeight:   listHeight,
		DetailTop:    monitorTabRowHeight + monitorSubTabHeight + listHeight,
		DetailHeight: detailHeight,
		FooterTop:    height - monitorFooterHeight,
	}
}

// monitorHit identifies what sits at terminal coordinates (x, y).
type monitorHit struct {
	area  string // "sidebar" | "tab" | "subtab" | "list" | "detail"
	index int    // sidebar repo index (-1=all), tab index, subtab index, or list cursor
}

func hitMonitorLocation(layout monitorLayout, x, y int) monitorHit {
	switch {
	case x < layout.SidebarWidth && y >= monitorTabRowHeight+monitorSubTabHeight+1:
		// Sidebar body starts below its REPOS header; index 0 is "All repos"
		// rendered on the first body line.
		return monitorHit{area: "sidebar", index: y - monitorTabRowHeight - monitorSubTabHeight - 1}
	case y == 0:
		return monitorHit{area: "tab", index: x / monitorTabSlotWidth}
	case y == 1:
		return monitorHit{area: "subtab", index: subTabIndexAt(x)}
	case y >= layout.ListTop && y < layout.DetailTop:
		return monitorHit{area: "list", index: y - layout.ListTop}
	default:
		return monitorHit{area: "detail"}
	}
}

// subTabIndexAt maps an x coordinate to a sub-tab slot; slots are fixed-width.
func subTabIndexAt(x int) int {
	return x / monitorSubTabSlotWidth
}

const monitorSubTabSlotWidth = 18

var (
	monitorStyleActive   = lipgloss.NewStyle().Bold(true)
	monitorStyleInactive = lipgloss.NewStyle().Faint(true)
	monitorStyleSelected = lipgloss.NewStyle().Reverse(true)
	monitorStyleChanged  = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // bright yellow
	monitorStyleAdded    = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	monitorStyleError    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))  // red
	monitorStyleDim      = lipgloss.NewStyle().Faint(true)
	monitorStyleHeader   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // bright green
)

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampInt(value, low, high int) int {
	if high < low {
		high = low
	}
	switch {
	case value < low:
		return low
	case value > high:
		return high
	default:
		return value
	}
}
