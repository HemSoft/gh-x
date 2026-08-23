package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// monitorSessionState remembers the last UI selections across runs.
type monitorSessionState struct {
	Tab       int `json:"tab"`
	SubTab    int `json:"subTab"`
	RepoIndex int `json:"repoIndex"`
}

func loadMonitorState(path string) monitorSessionState {
	state := monitorSessionState{}
	data, err := os.ReadFile(path)
	if err != nil {
		return state
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return monitorSessionState{}
	}
	return state
}

// saveMonitorState is swappable in tests.
var saveMonitorState = saveMonitorStateFile

// saveMonitorState persists selections; failures are non-fatal by design.
func saveMonitorStateFile(path string, state monitorSessionState) error {
	if path == "" {
		return nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode session state: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write session state: %w", err)
	}
	return nil
}

// clampMonitorState keeps restored selections inside current bounds.
func clampMonitorState(state monitorSessionState, tabs, prSections, issueSections, repos int) monitorSessionState {
	state.Tab = clampBoundedIndex(state.Tab, tabs)
	state.RepoIndex = clampBoundedIndex(state.RepoIndex, repos)
	state.SubTab = clampStoredSubTab(state.SubTab, state.Tab, prSections, issueSections)
	return state
}

// clampBoundedIndex resets indexes that fall outside a positive bound.
func clampBoundedIndex(value, bound int) int {
	if value < 0 || value >= maxInt(bound, 1) {
		return 0
	}
	return value
}

// clampStoredSubTab clamps against the section count of the stored tab.
func clampStoredSubTab(value, tab, prSections, issueSections int) int {
	bound := prSections
	if tab == monitorTabIssues && issueSections > 0 {
		bound = issueSections
	}
	if bound <= 0 {
		return 0
	}
	return clampBoundedIndex(value, bound)
}
