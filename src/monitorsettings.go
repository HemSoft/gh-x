package main

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// monitorField indexes focusable fields in the settings overlay.
const (
	monitorFieldRepos = iota
	monitorFieldLimit
	monitorFieldInterval
	monitorFieldCount
)

type monitorSettingsModel struct {
	active   bool
	focus    int
	repos    textarea.Model
	limit    textinput.Model
	interval textinput.Model
	errText  string
}

func newMonitorSettingsModel() monitorSettingsModel {
	repos := textarea.New()
	repos.Placeholder = "owner/repo (one per line)"
	repos.SetWidth(46)
	repos.SetHeight(6)

	limit := textinput.New()
	limit.Placeholder = strconv.Itoa(defaultMonitorLimit)

	interval := textinput.New()
	interval.Placeholder = formatMonitorInterval(defaultMonitorInterval)

	return monitorSettingsModel{repos: repos, limit: limit, interval: interval}
}

func (m *monitorSettingsModel) load(cfg *monitorConfig) {
	m.repos.SetValue(strings.Join(cfg.Repos, "\n"))
	m.limit.SetValue(strconv.Itoa(cfg.Defaults.Limit))
	m.interval.SetValue(cfg.Defaults.Interval)
	m.focus = monitorFieldRepos
	m.errText = ""
	m.refocus()
}

func (m *monitorSettingsModel) open(cfg *monitorConfig) {
	m.load(cfg)
	m.active = true
}

func (m *monitorSettingsModel) close() {
	m.active = false
	m.blurAll()
}

func (m *monitorSettingsModel) blurAll() {
	m.repos.Blur()
	m.limit.Blur()
	m.interval.Blur()
}

func (m *monitorSettingsModel) refocus() {
	m.blurAll()
	switch m.focus {
	case monitorFieldRepos:
		m.repos.Focus()
	case monitorFieldLimit:
		m.limit.Focus()
	default:
		m.interval.Focus()
	}
}

func (m *monitorSettingsModel) cycleFocus(delta int) {
	m.focus = (m.focus + delta + monitorFieldCount) % monitorFieldCount
	m.refocus()
}

// updateSettingsKey routes keys while the settings overlay is open.
// Returns apply=true when the user committed the form. Enter inserts
// newlines inside the multi-line repos field; elsewhere it commits.
func updateSettingsKey(m *monitorSettingsModel, msg tea.KeyPressMsg) (applied bool) {
	switch msg.String() {
	case "esc":
		m.close()
	case "tab":
		m.cycleFocus(1)
	case "shift+tab":
		m.cycleFocus(-1)
	case "ctrl+s":
		return true
	case "enter":
		if m.focus == monitorFieldRepos {
			m.routeInput(msg)
			return false
		}
		return true
	default:
		m.routeInput(msg)
	}
	return false
}

func (m *monitorSettingsModel) routeInput(msg tea.KeyPressMsg) {
	var cmd tea.Cmd
	switch m.focus {
	case monitorFieldRepos:
		m.repos, cmd = m.repos.Update(msg)
	case monitorFieldLimit:
		m.limit, cmd = m.limit.Update(msg)
	default:
		m.interval, cmd = m.interval.Update(msg)
	}
	_ = cmd
}

// applySettings parses the form onto cfg and validates it.
func applySettings(settings *monitorSettingsModel, cfg *monitorConfig) error {
	repos := parseMonitorRepoLines(settings.repos.Value())
	if err := validateMonitorRepos(repos); err != nil {
		settings.errText = err.Error()
		return err
	}
	cfg.Repos = repos

	limit, err := strconv.Atoi(strings.TrimSpace(settings.limit.Value()))
	if err != nil || limit < 1 || limit > maximumMonitorFetch {
		settings.errText = fmt.Sprintf("limit must be 1-%d", maximumMonitorFetch)
		return fmt.Errorf("invalid limit")
	}
	cfg.Defaults.Limit = limit

	interval, err := parseMonitorInterval(settings.interval.Value())
	if err != nil {
		settings.errText = err.Error()
		return err
	}
	cfg.Defaults.Interval = formatMonitorInterval(interval)
	return nil
}

func parseMonitorRepoLines(value string) []string {
	lines := strings.Split(value, "\n")
	repos := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			repos = append(repos, trimmed)
		}
	}
	return repos
}
