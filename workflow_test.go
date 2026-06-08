package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestParseWorkflowListOptionsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseWorkflowListOptions(nil, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.repo != "" {
		t.Fatalf("expected empty repo, got %q", opts.repo)
	}
	if opts.all {
		t.Fatal("expected all=false by default")
	}
}

func TestParseWorkflowListOptionsFlags(t *testing.T) {
	var stderr bytes.Buffer
	args := []string{"--repo", "owner/repo", "--all"}
	opts, err := parseWorkflowListOptions(args, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.repo != "owner/repo" {
		t.Fatalf("expected repo owner/repo, got %q", opts.repo)
	}
	if !opts.all {
		t.Fatal("expected all=true when --all is passed")
	}
}

func TestParseWorkflowListOptionsShortFlags(t *testing.T) {
	var stderr bytes.Buffer
	args := []string{"-R", "owner/repo"}
	opts, err := parseWorkflowListOptions(args, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.repo != "owner/repo" {
		t.Fatalf("expected repo owner/repo, got %q", opts.repo)
	}
}

func TestParseWorkflowListOptionsHelp(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseWorkflowListOptions([]string{"-h"}, &stderr)
	if err != errHelpDisplayed {
		t.Fatalf("expected errHelpDisplayed, got %v", err)
	}
}

func TestParseWorkflowListOptionsUnexpectedArgs(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseWorkflowListOptions([]string{"extra"}, &stderr)
	if err == nil {
		t.Fatal("expected error for unexpected arguments")
	}
	if !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildWorkflowListArgs(t *testing.T) {
	t.Run("with all", func(t *testing.T) {
		args := buildWorkflowListArgs(workflowListOptions{all: true})
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--all") {
			t.Fatal("expected --all flag")
		}
		if !strings.Contains(joined, "--json") {
			t.Fatal("expected --json flag")
		}
	})

	t.Run("with repo", func(t *testing.T) {
		args := buildWorkflowListArgs(workflowListOptions{repo: "owner/repo", all: true})
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "--repo owner/repo") {
			t.Fatalf("expected --repo flag, got %q", joined)
		}
	})

	t.Run("without all", func(t *testing.T) {
		args := buildWorkflowListArgs(workflowListOptions{all: false})
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--all") {
			t.Fatal("expected no --all flag when all=false")
		}
	})
}

func TestWorkflowURL(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		wf      workflowEntry
		want    string
	}{
		{
			name:    "yml workflow",
			repoURL: "https://github.com/owner/repo",
			wf:      workflowEntry{ID: 123, Path: ".github/workflows/ci.yml"},
			want:    "https://github.com/owner/repo/actions/workflows/ci.yml",
		},
		{
			name:    "yaml workflow",
			repoURL: "https://github.com/owner/repo",
			wf:      workflowEntry{ID: 456, Path: ".github/workflows/deploy.yaml"},
			want:    "https://github.com/owner/repo/actions/workflows/deploy.yaml",
		},
		{
			name:    "dynamic workflow uses ID",
			repoURL: "https://github.com/owner/repo",
			wf:      workflowEntry{ID: 789, Path: "dynamic/copilot-pull-request-reviewer/copilot-pull-request-reviewer"},
			want:    "https://github.com/owner/repo/actions/workflows/789",
		},
		{
			name:    "yml workflow with special characters is escaped",
			repoURL: "https://github.com/owner/repo",
			wf:      workflowEntry{ID: 999, Path: ".github/workflows/my build #1.yml"},
			want:    "https://github.com/owner/repo/actions/workflows/my%20build%20%231.yml",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := workflowURL(tc.repoURL, tc.wf)
			if got != tc.want {
				t.Fatalf("workflowURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatWorkflowTriggers(t *testing.T) {
	yaml := `name: Nightly
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
    types: [opened, synchronize, reopened]
  schedule:
    - cron: "15 6 * * 1-5"
  workflow_dispatch:
  workflow_run:
    workflows: ["CI"]
    types: [completed]
`

	got := formatWorkflowTriggers([]byte(yaml))
	want := "push (branches: main), pull_request (branches: main; types: opened, synchronize, reopened), schedule: weekdays at 06:15 UTC, manual, after workflow run (workflows: CI; types: completed)"
	if got != want {
		t.Fatalf("formatWorkflowTriggers() = %q, want %q", got, want)
	}
}

func TestFormatWorkflowTriggersScalarAndSequence(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "scalar",
			yaml: "on: push\n",
			want: "push",
		},
		{
			name: "sequence",
			yaml: "on: [push, workflow_dispatch]\n",
			want: "push, manual",
		},
		{
			name: "missing",
			yaml: "name: No Trigger\n",
			want: "unknown",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatWorkflowTriggers([]byte(tc.yaml))
			if got != tc.want {
				t.Fatalf("formatWorkflowTriggers() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormatScheduleTriggerHumanReadable(t *testing.T) {
	tests := []struct {
		name string
		cron string
		want string
	}{
		{name: "daily", cron: "50 10 * * *", want: "schedule: daily at 10:50 UTC"},
		{name: "hourly", cron: "0 * * * *", want: "schedule: hourly at minute 00 UTC"},
		{name: "weekdays", cron: "15 6 * * 1-5", want: "schedule: weekdays at 06:15 UTC"},
		{name: "weekly", cron: "0 14 * * 1", want: "schedule: weekly on Monday at 14:00 UTC"},
		{name: "custom", cron: "*/20 8-17 * * 1-5", want: "schedule: custom schedule (*/20 8-17 * * 1-5)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			yaml := fmt.Sprintf("on:\n  schedule:\n    - cron: %q\n", tc.cron)
			got := formatWorkflowTriggers([]byte(yaml))
			if got != tc.want {
				t.Fatalf("formatWorkflowTriggers() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderWorkflowTableNoColor(t *testing.T) {
	workflows := []workflowEntry{
		{ID: 100, Name: "Auto Release", State: "active", Path: ".github/workflows/auto-release.yml", Triggers: "push (branches: main)"},
		{ID: 200, Name: "CI Quality Gates", State: "active", Path: ".github/workflows/ci.yml", Triggers: "pull_request"},
		{ID: 300, Name: "Disabled Flow", State: "disabled_manually", Path: ".github/workflows/disabled.yml", Triggers: "schedule: daily at 05:00 UTC"},
	}
	var buf bytes.Buffer
	err := renderWorkflowTable(&buf, workflows, "https://github.com/owner/repo", false)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "NAME") {
		t.Fatal("expected NAME header")
	}
	if !strings.Contains(output, "STATE") {
		t.Fatal("expected STATE header")
	}
	if !strings.Contains(output, "ID") {
		t.Fatal("expected ID header")
	}
	if !strings.Contains(output, "TRIGGERS") {
		t.Fatal("expected TRIGGERS header")
	}
	if !strings.Contains(output, "Auto Release") {
		t.Fatal("expected workflow name")
	}
	if !strings.Contains(output, "active") {
		t.Fatal("expected active state")
	}
	if !strings.Contains(output, "100") {
		t.Fatal("expected workflow ID")
	}
	if !strings.Contains(output, "disabled_manually") {
		t.Fatal("expected disabled_manually state")
	}
	if !strings.Contains(output, "schedule: daily at 05:00 UTC") {
		t.Fatal("expected schedule trigger")
	}
	if strings.Contains(output, "\x1b") {
		t.Fatal("expected no ANSI escape sequences in no-color mode")
	}
}

func TestRenderWorkflowTableWithColor(t *testing.T) {
	workflows := []workflowEntry{
		{ID: 100, Name: "CI", State: "active", Path: ".github/workflows/ci.yml"},
	}
	var buf bytes.Buffer
	err := renderWorkflowTable(&buf, workflows, "https://github.com/owner/repo", true)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "\x1b[") {
		t.Fatal("expected ANSI codes when color enabled")
	}
}

func TestRenderWorkflowTableClickableHeaders(t *testing.T) {
	workflows := []workflowEntry{
		{ID: 100, Name: "CI", State: "active", Path: ".github/workflows/ci.yml"},
	}
	var buf bytes.Buffer
	err := renderWorkflowTable(&buf, workflows, "https://github.com/owner/repo", true)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	// Assert the actual OSC-8 hyperlink opening sequence wraps header text,
	// not just the URL substring which could also match row-level workflow URLs.
	osc8Open := "\x1b]8;;https://github.com/owner/repo/actions\x1b\\"
	if !strings.Contains(output, osc8Open) {
		t.Fatal("expected OSC-8 hyperlink sequence wrapping header text with actions URL")
	}
}

func TestRenderWorkflowTableClickableIDs(t *testing.T) {
	workflows := []workflowEntry{
		{ID: 100, Name: "CI", State: "active", Path: ".github/workflows/ci.yml"},
	}
	var buf bytes.Buffer
	err := renderWorkflowTable(&buf, workflows, "https://github.com/owner/repo", true)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	// Assert the actual OSC-8 hyperlink opening sequence wraps the workflow ID,
	// not just the URL substring.
	osc8Open := "\x1b]8;;https://github.com/owner/repo/actions/workflows/ci.yml\x1b\\"
	if !strings.Contains(output, osc8Open) {
		t.Fatal("expected OSC-8 hyperlink sequence wrapping workflow ID")
	}
}

func TestWorkflowStateCellColors(t *testing.T) {
	styler := newTableStyler(&bytes.Buffer{}, true)
	tests := []struct {
		state    string
		wantAnsi bool
	}{
		{"active", true},
		{"disabled_manually", true},
		{"disabled_inactivity", true},
		{"disabled_fork", true},
		{"unknown", true}, // dim still uses ANSI
	}
	for _, tc := range tests {
		cell := styler.workflowStateCell(tc.state)
		hasAnsi := strings.Contains(cell.styled, "\x1b[")
		if hasAnsi != tc.wantAnsi {
			t.Fatalf("workflowStateCell(%q): hasAnsi=%v, want %v", tc.state, hasAnsi, tc.wantAnsi)
		}
	}
}

func TestRunWorkflowCmdRouting(t *testing.T) {
	// "workflow --help" routes to "list --help" since --help is a flag
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"workflow", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for workflow --help, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x workflow list") {
		t.Fatalf("expected workflow list usage in stderr, got: %q", stderr.String())
	}
}

func TestRunWorkflowCmdNoArgs(t *testing.T) {
	// With no args, workflow defaults to "list" subcommand
	defer saveWorkflowFuncs()()
	fetchWorkflowsFunc = func(_ workflowListOptions) ([]workflowEntry, error) { return nil, nil }
	isColorEnabledFunc = func() bool { return false }

	var stdout, stderr bytes.Buffer
	_, err := run([]string{"workflow"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout.String(), "Workflow commands for gh-x") {
		t.Fatal("expected no workflow-level usage on stdout when defaulting to list")
	}
	if strings.Contains(stderr.String(), "Workflow commands for gh-x") {
		t.Fatal("expected no workflow-level usage on stderr when defaulting to list")
	}
}

func TestRunWorkflowCmdHelp(t *testing.T) {
	// "workflow help" (no dash) still shows workflow-level usage
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"workflow", "help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for workflow help, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "gh x workflow") {
		t.Fatalf("expected workflow usage on stdout, got: %q", stdout.String())
	}
}

func TestRunWorkflowCmdUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"workflow", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown workflow subcommand")
	}
	if !strings.Contains(err.Error(), `unknown workflow subcommand "bogus"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunWorkflowListHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"workflow", "list", "-h"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for workflow list -h, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x workflow list") {
		t.Fatalf("expected workflow list usage in stderr, got: %q", stderr.String())
	}
}

func TestRootUsageMentionsWorkflow(t *testing.T) {
	if !strings.Contains(rootUsage, "workflow") {
		t.Fatal("root usage should mention workflow subcommand")
	}
}

func TestDimLinkCell(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, true)
	cell := styler.dimLinkCell("TEXT", "https://example.com")
	if cell.text != "TEXT" {
		t.Fatalf("expected text 'TEXT', got %q", cell.text)
	}
	if !strings.Contains(cell.styled, "https://example.com") {
		t.Fatal("expected URL in styled output")
	}
	if !strings.Contains(cell.styled, "\x1b]8;;") {
		t.Fatal("expected OSC 8 link sequence")
	}
}

func TestDimLinkCellNoURL(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, true)
	cell := styler.dimLinkCell("TEXT", "")
	if strings.Contains(cell.styled, "\x1b]8;;") {
		t.Fatal("expected no OSC 8 link for empty URL")
	}
}

// saveWorkflowFuncs saves the current function variables and returns a restore function.
func saveWorkflowFuncs() func() {
	savedFetch := fetchWorkflowsFunc
	savedFetchTriggers := fetchWorkflowTriggersFunc
	savedResolve := resolveRepoURLFunc
	savedColor := isColorEnabledFunc
	return func() {
		fetchWorkflowsFunc = savedFetch
		fetchWorkflowTriggersFunc = savedFetchTriggers
		resolveRepoURLFunc = savedResolve
		isColorEnabledFunc = savedColor
	}
}

func TestExecuteWorkflowListEmpty(t *testing.T) {
	defer saveWorkflowFuncs()()
	fetchWorkflowsFunc = func(_ workflowListOptions) ([]workflowEntry, error) {
		return nil, nil
	}

	var buf bytes.Buffer
	err := executeWorkflowList(workflowListOptions{}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No workflows found.") {
		t.Fatalf("expected empty message, got %q", buf.String())
	}
}

func TestExecuteWorkflowListFetchError(t *testing.T) {
	defer saveWorkflowFuncs()()
	fetchWorkflowsFunc = func(_ workflowListOptions) ([]workflowEntry, error) {
		return nil, fmt.Errorf("network error")
	}

	var buf bytes.Buffer
	err := executeWorkflowList(workflowListOptions{}, &buf)
	if err == nil || !strings.Contains(err.Error(), "network error") {
		t.Fatalf("expected network error, got %v", err)
	}
}

func TestExecuteWorkflowListNoColor(t *testing.T) {
	defer saveWorkflowFuncs()()
	fetchWorkflowsFunc = func(_ workflowListOptions) ([]workflowEntry, error) {
		return []workflowEntry{
			{ID: 1, Name: "CI", State: "active", Path: ".github/workflows/ci.yml"},
		}, nil
	}
	isColorEnabledFunc = func() bool { return false }

	var buf bytes.Buffer
	err := executeWorkflowList(workflowListOptions{}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "CI") {
		t.Fatal("expected workflow name in output")
	}
}

func TestExecuteWorkflowListWithColor(t *testing.T) {
	defer saveWorkflowFuncs()()
	fetchWorkflowsFunc = func(_ workflowListOptions) ([]workflowEntry, error) {
		return []workflowEntry{
			{ID: 42, Name: "Deploy", State: "active", Path: ".github/workflows/deploy.yml"},
		}, nil
	}
	isColorEnabledFunc = func() bool { return true }
	resolveRepoURLFunc = func(_ string) (string, error) {
		return "https://github.com/owner/repo", nil
	}

	var buf bytes.Buffer
	err := executeWorkflowList(workflowListOptions{}, &buf)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "Deploy") {
		t.Fatal("expected workflow name in output")
	}
}

func TestExecuteWorkflowListResolveURLError(t *testing.T) {
	defer saveWorkflowFuncs()()
	fetchWorkflowsFunc = func(_ workflowListOptions) ([]workflowEntry, error) {
		return []workflowEntry{
			{ID: 1, Name: "CI", State: "active", Path: ".github/workflows/ci.yml"},
		}, nil
	}
	isColorEnabledFunc = func() bool { return true }
	resolveRepoURLFunc = func(_ string) (string, error) {
		return "", fmt.Errorf("repo not found")
	}

	var buf bytes.Buffer
	err := executeWorkflowList(workflowListOptions{}, &buf)
	if err == nil || !strings.Contains(err.Error(), "repo not found") {
		t.Fatalf("expected repo not found error, got %v", err)
	}
}
