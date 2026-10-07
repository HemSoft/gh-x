package main

const (
	monitorSidebarWidth    = 28
	monitorFooterHeight    = 2
	monitorTabRowHeight    = 1
	monitorSubTabHeight    = 1
	monitorMinWidth        = 60
	monitorMinHeight       = 16
	monitorDetailMinHeight = 5
	monitorDetailMaxHeight = 16
	monitorTabSlotWidth    = 16
	monitorSubTabSlotWidth = 18
)

type monitorLayout struct {
	Width, Height, SidebarWidth, MainLeft                   int
	TabTop, SubTabTop, SidebarTop                           int
	ListTop, ListHeight, DetailTop, DetailHeight, FooterTop int
}

func computeMonitorLayout(width, height int) monitorLayout {
	sidebar := 0
	if width >= 100 {
		sidebar = clampInt(width/5, 22, monitorSidebarWidth)
	}
	detail := clampInt(height/3, monitorDetailMinHeight, monitorDetailMaxHeight)
	list := maxInt(height-monitorFooterHeight-3-detail, 1)
	mainLeft := 0
	if sidebar > 0 {
		mainLeft = sidebar + 1
	}
	return monitorLayout{
		Width: width, Height: height, SidebarWidth: sidebar, MainLeft: mainLeft,
		TabTop: 1, SubTabTop: 2, SidebarTop: 1, ListTop: 3, ListHeight: list,
		DetailTop: 3 + list, DetailHeight: detail, FooterTop: height - monitorFooterHeight,
	}
}

type monitorHit struct {
	area  string
	index int
}

// All hit coordinates use the same origins and slots as rendering.
func hitMonitorLocation(layout monitorLayout, x, y int) monitorHit {
	if x < 0 || y < 0 || x >= layout.Width || y >= layout.FooterTop {
		return monitorHit{area: "none"}
	}
	if y == 0 {
		return monitorHit{area: "scope"}
	}
	if layout.SidebarWidth > 0 && x < layout.SidebarWidth {
		if y == layout.SidebarTop {
			return monitorHit{area: "none"}
		}
		return monitorHit{area: "sidebar", index: y - layout.SidebarTop - 1}
	}
	return hitMonitorMain(layout, x-layout.MainLeft, y)
}

func hitMonitorMain(layout monitorLayout, x, y int) monitorHit {
	if x < 0 {
		return monitorHit{area: "none"}
	}
	switch {
	case y == layout.TabTop:
		return monitorHit{area: "tab", index: x / monitorTabSlotWidth}
	case y == layout.SubTabTop:
		return monitorHit{area: "subtab", index: subTabIndexAt(x)}
	case y > layout.ListTop && y < layout.DetailTop:
		return monitorHit{area: "list", index: y - layout.ListTop - 1}
	case y > layout.DetailTop:
		return monitorHit{area: "detail"}
	default:
		return monitorHit{area: "none"}
	}
}

func subTabIndexAt(x int) int { return x / monitorSubTabSlotWidth }

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
		return low
	}
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
