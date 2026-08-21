package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseRunListOptionsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseRunListOptions(nil, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.limit != 20 {
		t.Fatalf("expected default limit 20, got %d", opts.limit)
	}
	if opts.status != "" || opts.workflow != "" || opts.branch != "" {
		t.Fatalf("expected empty default filters")
	}
}

func TestParseRunListOptionsFlags(t *testing.T) {
	var stderr bytes.Buffer
	args := []string{
		"--limit", "50",
		"--status", "failure",
		"--workflow", "CI",
		"--branch", "main",
		"--event", "push",
		"--user", "octocat",
		"--repo", "owner/repo",
	}
	opts, err := parseRunListOptions(args, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.limit != 50 {
		t.Fatalf("expected limit 50, got %d", opts.limit)
	}
	if opts.status != "failure" {
		t.Fatalf("expected status failure, got %q", opts.status)
	}
	if opts.workflow != "CI" {
		t.Fatalf("expected workflow CI, got %q", opts.workflow)
	}
	if opts.branch != "main" {
		t.Fatalf("expected branch main, got %q", opts.branch)
	}
	if opts.event != "push" {
		t.Fatalf("expected event push, got %q", opts.event)
	}
	if opts.user != "octocat" {
		t.Fatalf("expected user octocat, got %q", opts.user)
	}
	if opts.repo != "owner/repo" {
		t.Fatalf("expected repo owner/repo, got %q", opts.repo)
	}
}

func TestParseRunListOptionsShortFlags(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseRunListOptions([]string{"-L", "10", "-s", "success", "-w", "Deploy", "-b", "release", "-e", "push", "-u", "dev"}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.limit != 10 || opts.status != "success" || opts.workflow != "Deploy" || opts.branch != "release" || opts.event != "push" || opts.user != "dev" {
		t.Fatalf("short flags not parsed: %+v", opts)
	}
}

func TestParseRunListOptionsHelp(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseRunListOptions([]string{"--help"}, &stderr)
	if err != errHelpDisplayed {
		t.Fatalf("expected errHelpDisplayed, got %v", err)
	}
}

func TestParseRunListOptionsInvalidLimit(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseRunListOptions([]string{"--limit", "0"}, &stderr)
	if err == nil {
		t.Fatal("expected error for zero limit")
	}
}

func TestParseRunListOptionsUnexpectedArgs(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseRunListOptions([]string{"extra"}, &stderr)
	if err == nil {
		t.Fatal("expected error for unexpected args")
	}
}

func TestBuildRunListArgs(t *testing.T) {
	tests := []struct {
		name    string
		options runListOptions
		want    []string
	}{
		{
			name:    "defaults",
			options: runListOptions{limit: 20},
			want:    []string{"run", "list", "--json", runJSONFields, "--limit", "20"},
		},
		{
			name: "all filters",
			options: runListOptions{
				repo:     "owner/repo",
				limit:    50,
				status:   "failure",
				workflow: "CI",
				branch:   "main",
				event:    "push",
				user:     "dev",
			},
			want: []string{
				"run", "list", "--json", runJSONFields,
				"--repo", "owner/repo",
				"--limit", "50",
				"--status", "failure",
				"--workflow", "CI",
				"--branch", "main",
				"--event", "push",
				"--user", "dev",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := buildRunListArgs(tc.options)
			if len(got) != len(tc.want) {
				t.Fatalf("length mismatch: got %d, want %d\ngot:  %v\nwant: %v", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("arg[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestResolveRunStatus(t *testing.T) {
	tests := []struct {
		status     string
		conclusion string
		want       string
	}{
		{"completed", "success", "✓"},
		{"completed", "failure", "X"},
		{"completed", "timed_out", "X"},
		{"completed", "startup_failure", "X"},
		{"completed", "cancelled", "!"},
		{"completed", "skipped", "—"},
		{"completed", "neutral", "—"},
		{"completed", "action_required", "!"},
		{"completed", "unknown", "·"},
		{"in_progress", "", "*"},
		{"queued", "", "○"},
		{"requested", "", "○"},
		{"waiting", "", "○"},
		{"pending", "", "○"},
		{"stale", "", "·"},
	}
	for _, tc := range tests {
		t.Run(tc.status+"/"+tc.conclusion, func(t *testing.T) {
			got := resolveRunStatus(tc.status, tc.conclusion)
			if got != tc.want {
				t.Fatalf("resolveRunStatus(%q, %q) = %q, want %q", tc.status, tc.conclusion, got, tc.want)
			}
		})
	}
}

func TestFormatElapsed(t *testing.T) {
	now := time.Date(2026, 5, 12, 5, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		status    string
		startedAt time.Time
		updatedAt time.Time
		want      string
	}{
		{"completed short", "completed", now.Add(-45 * time.Second), now, "45s"},
		{"completed minutes", "completed", now.Add(-2*time.Minute - 30*time.Second), now, "2m30s"},
		{"completed exact minutes", "completed", now.Add(-5 * time.Minute), now, "5m"},
		{"completed hours", "completed", now.Add(-1*time.Hour - 15*time.Minute), now, "1h15m"},
		{"completed exact hours", "completed", now.Add(-2 * time.Hour), now, "2h"},
		{"in_progress", "in_progress", now.Add(-30 * time.Second), time.Time{}, "30s"},
		{"queued", "queued", now.Add(-10 * time.Second), time.Time{}, "-"},
		{"zero start", "completed", time.Time{}, now, "-"},
		{"end before start", "completed", now, now.Add(-time.Minute), "-"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatElapsed(tc.status, tc.startedAt, tc.updatedAt, now)
			if got != tc.want {
				t.Fatalf("formatElapsed(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestBuildDisplayWorkflowRun(t *testing.T) {
	now := time.Date(2026, 5, 12, 5, 10, 0, 0, time.UTC)
	r := workflowRun{
		DatabaseID:   25714589506,
		DisplayTitle: "feat: add pr subcommand group for multi-command architecture (#3)",
		WorkflowName: "Copilot Setup Steps",
		HeadBranch:   "v0.4.0",
		Event:        "push",
		Status:       "completed",
		Conclusion:   "success",
		URL:          "https://github.com/HemSoft/gh-x/actions/runs/25714589506",
		CreatedAt:    now.Add(-5 * time.Minute),
		StartedAt:    now.Add(-5 * time.Minute),
		UpdatedAt:    now.Add(-4*time.Minute - 50*time.Second),
	}

	got := buildDisplayWorkflowRun(r, now)

	if got.Status != "✓" {
		t.Fatalf("expected ✓ status, got %q", got.Status)
	}
	if got.ID != "25714589506" {
		t.Fatalf("expected ID 25714589506, got %q", got.ID)
	}
	if got.URL != r.URL {
		t.Fatalf("expected URL preserved, got %q", got.URL)
	}
	if got.Event != "push" {
		t.Fatalf("expected push event, got %q", got.Event)
	}
	if len(got.Title) > 43 {
		t.Fatalf("expected title truncated, got %q (len=%d)", got.Title, len(got.Title))
	}
	if got.Elapsed != "10s" {
		t.Fatalf("expected 10s elapsed, got %q", got.Elapsed)
	}
	if got.Age != "5m" {
		t.Fatalf("expected 5m age, got %q", got.Age)
	}
}

func TestBuildDisplayWorkflowRunInProgress(t *testing.T) {
	now := time.Date(2026, 5, 12, 5, 10, 0, 0, time.UTC)
	r := workflowRun{
		DatabaseID:   12345,
		DisplayTitle: "Running build",
		WorkflowName: "CI",
		HeadBranch:   "main",
		Event:        "push",
		Status:       "in_progress",
		Conclusion:   "",
		URL:          "https://github.com/org/repo/actions/runs/12345",
		CreatedAt:    now.Add(-2 * time.Minute),
		StartedAt:    now.Add(-2 * time.Minute),
		UpdatedAt:    now.Add(-30 * time.Second),
	}

	got := buildDisplayWorkflowRun(r, now)
	if got.Status != "*" {
		t.Fatalf("expected * for in_progress, got %q", got.Status)
	}
	if got.Elapsed != "2m" {
		t.Fatalf("expected 2m elapsed for in_progress (using now), got %q", got.Elapsed)
	}
}

func TestRenderRunTableNoColor(t *testing.T) {
	var buf bytes.Buffer
	runs := []displayWorkflowRun{
		{Status: "✓", Title: "Build PR", Workflow: "CI", Branch: "main", Event: "push", ID: "12345", URL: "https://example.com", Elapsed: "45s", Age: "2h"},
	}
	err := renderRunTable(&buf, runs, false)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if strings.Contains(output, "\x1b[") {
		t.Fatal("expected no ANSI escape codes when color is disabled")
	}
	if !strings.Contains(output, "12345") {
		t.Fatal("expected run ID in output")
	}
	if !strings.Contains(output, "Build PR") {
		t.Fatal("expected title in output")
	}
	if !strings.Contains(output, "CI") {
		t.Fatal("expected workflow in output")
	}
	if strings.Contains(output, "copied") {
		t.Fatal("clipboard hint should not appear when color is disabled")
	}
}

func TestRenderRunTableWithColor(t *testing.T) {
	var buf bytes.Buffer
	runs := []displayWorkflowRun{
		{Status: "✓", Title: "Build", Workflow: "CI", Branch: "main", Event: "push", ID: "99", URL: "https://example.com", Elapsed: "10s", Age: "1m"},
	}
	err := renderRunTable(&buf, runs, true)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "\x1b[") {
		t.Fatal("expected ANSI escape codes when color is enabled")
	}
	if !strings.Contains(output, "gh run view 99") {
		t.Fatal("expected clipboard hint with run ID")
	}
	if !strings.Contains(output, "\x1b]52;c;") {
		t.Fatal("expected OSC 52 clipboard sequence")
	}
}

func TestRenderRunTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := renderRunTable(&buf, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No workflow runs found") {
		t.Fatal("expected empty message")
	}
}

func TestRenderRunTableAlignment(t *testing.T) {
	var buf bytes.Buffer
	// Use ASCII status chars to avoid multi-byte UTF-8 alignment issues in byte-level test
	runs := []displayWorkflowRun{
		{Status: "X", Title: "Short", Workflow: "CI", Branch: "main", Event: "push", ID: "1", URL: "", Elapsed: "5s", Age: "1m"},
		{Status: "X", Title: "Much longer title here", Workflow: "Deploy Pipeline", Branch: "feature/long-branch", Event: "pull_request", ID: "999999", URL: "", Elapsed: "2m30s", Age: "3d"},
	}
	err := renderRunTable(&buf, runs, false)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines (header + 2 rows), got %d", len(lines))
	}

	if !strings.Contains(lines[0], "Title") || !strings.Contains(lines[0], "Workflow") {
		t.Fatal("expected header labels")
	}

	// Verify Title column alignment (all ASCII status chars, consistent byte widths)
	headerTitleIdx := strings.Index(lines[0], "Title")
	row1TitleIdx := strings.Index(lines[1], "Short")
	row2TitleIdx := strings.Index(lines[2], "Much")
	if headerTitleIdx != row1TitleIdx || headerTitleIdx != row2TitleIdx {
		t.Fatalf("Title column misaligned: header=%d row1=%d row2=%d", headerTitleIdx, row1TitleIdx, row2TitleIdx)
	}
}

func TestRenderRunTableOSC8Link(t *testing.T) {
	var buf bytes.Buffer
	runs := []displayWorkflowRun{
		{Status: "✓", Title: "Test", Workflow: "CI", Branch: "main", Event: "push", ID: "42", URL: "https://github.com/org/repo/actions/runs/42", Elapsed: "5s", Age: "1m"},
	}
	err := renderRunTable(&buf, runs, true)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "\x1b]8;;https://github.com/org/repo/actions/runs/42\x1b\\") {
		t.Fatal("expected OSC 8 hyperlink for run ID")
	}
}

func TestWriteOSC52(t *testing.T) {
	var buf bytes.Buffer
	writeOSC52(&buf, "gh run view 12345")
	output := buf.String()
	// "gh run view 12345" → base64 = "Z2ggcnVuIHZpZXcgMTIzNDU="
	if !strings.Contains(output, "\x1b]52;c;Z2ggcnVuIHZpZXcgMTIzNDU=\x07") {
		t.Fatalf("expected OSC 52 with base64-encoded command, got %q", output)
	}
}

func TestRenderRunTableClipboardFirstRun(t *testing.T) {
	var buf bytes.Buffer
	runs := []displayWorkflowRun{
		{Status: "✓", Title: "First", Workflow: "CI", Branch: "main", Event: "push", ID: "111", URL: "", Elapsed: "5s", Age: "1m"},
		{Status: "X", Title: "Second", Workflow: "CI", Branch: "dev", Event: "push", ID: "222", URL: "", Elapsed: "10s", Age: "2m"},
	}
	err := renderRunTable(&buf, runs, true)
	if err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if !strings.Contains(output, "gh run view 111") {
		t.Fatal("expected clipboard hint for first (most recent) run")
	}
	if strings.Contains(output, "gh run view 222") {
		t.Fatal("should only copy first run's command, not second")
	}
}

func TestRunRunCmdRouting(t *testing.T) {
	// "run help" (no dash) still shows run-level usage
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"run", "help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for run help, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "gh x run") {
		t.Fatalf("expected run usage on stdout, got %q", stdout.String())
	}
}

func TestRunRunCmdNoArgs(t *testing.T) {
	// With no args, run defaults to "list" subcommand
	savedExecuteRunList := executeRunListFunc
	defer func() { executeRunListFunc = savedExecuteRunList }()
	executeRunListFunc = func(_ runListOptions, _ io.Writer, _ time.Time) error { return nil }

	var stdout, stderr bytes.Buffer
	_, err := run([]string{"run"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout.String(), "Workflow run commands") {
		t.Fatal("expected no run-level usage on stdout when defaulting to list")
	}
	if strings.Contains(stderr.String(), "Workflow run commands") {
		t.Fatal("expected no run-level usage on stderr when defaulting to list")
	}
}

func TestRunRunCmdHelp(t *testing.T) {
	// "run --help" routes to "list --help" since --help is a flag
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"run", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for run --help, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x run list") {
		t.Fatalf("expected run list usage in stderr, got %q", stderr.String())
	}
}

func TestRunRunCmdUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"run", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown run subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning bogus, got %q", err.Error())
	}
}

func TestRunRunCmdListHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"run", "list", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for run list --help, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x run list") {
		t.Fatalf("expected run list usage in stderr, got: %q", stderr.String())
	}
}

func TestResolveRunCommandKnown(t *testing.T) {
	for _, name := range []string{"list", "help"} {
		cmd := resolveRunCommand(name)
		if cmd.name != name {
			t.Fatalf("expected name %q, got %q", name, cmd.name)
		}
	}
}

func TestResolveRunCommandUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolveRunCommand("bogus")
	err := cmd.handler(nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown run subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning bogus, got %q", err.Error())
	}
	if !strings.Contains(stderr.String(), "gh x run") {
		t.Fatalf("expected run usage on stderr, got %q", stderr.String())
	}
}

func TestWriteRunUsage(t *testing.T) {
	var buf bytes.Buffer
	writeRunUsage(&buf)
	if !strings.Contains(buf.String(), "gh x run") {
		t.Fatalf("expected run usage, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), "list") {
		t.Fatalf("expected run usage to mention list")
	}
}

func TestWriteRunListUsage(t *testing.T) {
	var buf bytes.Buffer
	writeRunListUsage(&buf)
	if !strings.Contains(buf.String(), "gh x run list") {
		t.Fatalf("expected run list usage, got %q", buf.String())
	}
}

func TestRootUsageMentionsRun(t *testing.T) {
	if !strings.Contains(rootUsage, "run") {
		t.Fatal("root usage should mention run command")
	}
}

func TestRunStatusCellColors(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, true)

	tests := []struct {
		status string
		expect string
	}{
		{"✓", "✓"},
		{"X", "X"},
		{"*", "*"},
		{"!", "!"},
		{"—", "—"},
		{"○", "○"},
		{"·", "·"},
	}
	for _, tc := range tests {
		cell := styler.runStatusCell(tc.status)
		if cell.text != tc.expect {
			t.Fatalf("runStatusCell(%q).text = %q, want %q", tc.status, cell.text, tc.expect)
		}
	}
}

func TestRunIDCellPlainWidth(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, true)
	cell := styler.runIDCell("25714589506", "https://example.com/runs/25714589506")
	if cell.text != "25714589506" {
		t.Fatalf("expected plain text ID for width calc, got %q", cell.text)
	}
	if !strings.Contains(cell.styled, "\x1b]8;;") {
		t.Fatal("expected OSC 8 in styled output")
	}
}
