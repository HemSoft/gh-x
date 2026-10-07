# Monitor dashboard design

## Stack decision

Retain the pinned Go stack: Bubble Tea v2.0.10 for events and terminal lifecycle,
Bubbles v2.2.1 for input controls, and Lip Gloss v2.0.6 for styling and cell
layout. Rewrite the monitor's presentation and interaction geometry.

[Bubble Tea](https://github.com/charmbracelet/bubbletea) provides an event-driven
model/update/view architecture. [Bubbles](https://github.com/charmbracelet/bubbles)
provides inputs and [Lip Gloss](https://github.com/charmbracelet/lipgloss)
provides terminal styling. These capabilities already fit the monitor's Go data
model, asynchronous refreshes, and native release targets.

[tview/tcell](https://github.com/rivo/tview) offers ready-made Go tables and
forms, but a migration would replace the event and input wiring while leaving
the visual design work. [Ratatui](https://ratatui.rs/) offers Rust widgets;
adopting it here would also introduce a language and build boundary. Neither
tradeoff is justified by this presentation rewrite. The two independent Claude
Opus 5.5 consultations recorded in issue #198 reached the same recommendation.
No replacement framework or additional dependency version is introduced.

## Theme and components

`monitorTheme` owns styles per model. Background-color replies select separate
dark and light palettes; a missing reply retains the dark default. `NO_COLOR`
uses uncolored styles while preserving bold, faint, and reverse cues. Status
words and change glyphs carry meaning independently of color. No process-global
style mutation is needed when multiple models or tests use different themes.

The dashboard, repository navigation, tables, details, settings, and help use
this shared theme. Cyan marks focus and headings, green marks success/open,
amber marks waits/drafts/changes, and red marks failures/errors. A selected row
has a quiet background outside list focus and a stronger background within it.
Monochrome selection uses bold outside focus and reverse within focus. Settings
also prefix the active field with `>`.

## Geometry and interaction

`computeMonitorLayout` is the geometry source for rendering and mouse hits.
The first row contains identity and repository scope, followed by tabs and
sections. The final two rows contain key hints and refresh/data status. Details
occupy about one third of the height, bounded to 5–16 rows. Their pinned title
stays visible while metadata and body scroll through the same content slice.

At 100 columns and above, the sidebar uses 22–28 cells. Smaller widths collapse
it into the header and retain keyboard repository navigation. Table projection
removes secondary fields before shrinking the title; details retain those
fields. Active sections and repository selections scroll into view. Below
60×16, the monitor asks the user to resize.

Cell-aware truncation, padding, and body wrapping handle ANSI styling and wide
Unicode text. Fetched table text is stripped of terminal controls before
rendering. Table headers, detail headings, borders, and footer rows are not
selectable data rows. Resizing recomputes geometry and keeps the cursor visible.

The fetch layer, host-qualified repository queries, schema-v1 configuration,
timeouts, refresh cancellation, and retained snapshots keep their existing
contracts. Startup restores saved sections instead of replacing them with
All open; switching PR/issue tabs continues to select All open.

## Regression and terminal validation

`monitorui_test.go` covers whole-screen bounds at 60×16, 80×24, 120×40, and
160×50; long Unicode titles; status text/colors; monochrome output; background
replies; scroll-to-end behavior; mouse origins; and custom sections. Existing
monitor tests cover refresh/error transitions, cancellation, settings, config,
clipboard/browser actions, and host-aware queries.

The issue's PR embeds current-head terminal captures using isolated config and
synthetic GitHub responses. Terminal input exercises repository and section
changes, filtering, detail scrolling, settings validation, help, refresh/error
recovery, and resize. Native Windows, macOS, and Linux checks complement the
repository's canonical cross-builds. The full Perfection audit remains required,
including race tests, analysis, coverage, mutation limits, and rendering budgets.
