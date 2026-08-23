package main

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/browser"
)

type monitorEditorDoneMsg struct {
	err error
}

// monitorAction describes one executable action for the selected row.
func (m monitorModel) openSelectedInBrowser() tea.Model {
	row, ok := m.selectedRow()
	if !ok || row.URL == "" {
		return m
	}
	b := browser.New("", os.Stdout, os.Stderr)
	if err := b.Browse(row.URL); err != nil {
		m.refreshErr = "open browser: " + err.Error()
	}
	return m
}

func (m monitorModel) copySelectedURL(checkoutCommand bool) (tea.Model, tea.Cmd) {
	row, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	payload := row.URL
	if checkoutCommand {
		payload = checkoutCommandFor(row)
	}
	return m, tea.SetClipboard(payload)
}

func checkoutCommandFor(row monitorRow) string {
	if row.Kind == monitorKindIssue {
		return "gh issue develop " + strconv.Itoa(row.Number) + " -R " + row.Repo
	}
	return "gh pr checkout " + strconv.Itoa(row.Number) + " -R " + row.Repo
}

// editConfigCmd suspends the TUI, opens $EDITOR on the config, reloads after.
func (m monitorModel) editConfigCmd() (tea.Model, tea.Cmd) {
	editor := resolveMonitorEditor()
	if editor == "" {
		m.refreshErr = "set $EDITOR or $VISUAL to edit the config"
		return m, nil
	}
	command := exec.Command(editor, m.configPath)
	return m, tea.ExecProcess(command, func(err error) tea.Msg {
		return monitorEditorDoneMsg{err: err}
	})
}

func resolveMonitorEditor() string {
	if editor := os.Getenv("VISUAL"); editor != "" {
		return editor
	}
	if editor := os.Getenv("EDITOR"); editor != "" {
		return editor
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "vi"
}

// handleEditorDone reloads the config after the editor exits.
func (m monitorModel) handleEditorDone(msg monitorEditorDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.refreshErr = "editor: " + msg.err.Error()
		return m, nil
	}
	cfg, _, err := loadOrCreateMonitorConfig(m.configPath, monitorSeedRepo(), monitorRepoHostFunc())
	if err != nil {
		m.refreshErr = "reload config: " + err.Error()
		return m, nil
	}
	*m.cfg = *cfg
	m.interval = parseMonitorIntervalOrDefault(cfg.Defaults.Interval, defaultMonitorInterval)
	m.clampSelections()
	return m.startRefresh()
}
