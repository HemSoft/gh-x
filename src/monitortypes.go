package main

import (
	"strconv"
	"time"
)

// Tab order for the monitor TUI.
const (
	monitorTabPRs    = 0
	monitorTabIssues = 1
	monitorTabCount  = 2
)

// monitorRowKind distinguishes table rows and detail rendering.
type monitorRowKind string

const (
	monitorKindPR    monitorRowKind = "pr"
	monitorKindIssue monitorRowKind = "issue"
)

// monitorRow is the uniform display record for both PRs and issues.
// Enriched PR fields are empty for issues.
type monitorRow struct {
	Kind      monitorRowKind
	Number    int
	Title     string
	Author    string
	State     string
	Review    string // PR review decision symbol
	Approvals int
	Checks    string
	Comments  string // resolved/total or "-"
	AIReview  string
	AIClean   *bool
	Branch    string
	Assignees string
	Labels    []string
	Milestone string
	Body      string
	Updated   string
	UpdatedAt time.Time
	URL       string
	Repo      string // nameWithOwner
}

// key identifies a row across refreshes.
func (r monitorRow) key() string {
	return r.Repo + "#" + string(r.Kind) + "#" + strconv.Itoa(r.Number)
}
