package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseIssueListOptionsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseIssueListOptions(nil, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.limit != 30 {
		t.Fatalf("expected default limit 30, got %d", opts.limit)
	}
	if opts.state != "open" {
		t.Fatalf("expected default state open, got %q", opts.state)
	}
	if opts.author != "" || opts.assignee != "" || opts.milestone != "" || opts.search != "" {
		t.Fatalf("expected empty default filters")
	}
	if opts.web {
		t.Fatalf("expected web to be false by default")
	}
	if len(opts.labels) != 0 {
		t.Fatalf("expected no default labels")
	}
}

func TestParseIssueListOptionsFlags(t *testing.T) {
	var stderr bytes.Buffer
	args := []string{
		"--limit", "50",
		"--state", "all",
		"--author", "octocat",
		"--assignee", "hubot",
		"--milestone", "v1.0",
		"--search", "is:pinned",
		"--repo", "owner/repo",
		"--label", "bug",
		"--label", "urgent",
		"--web",
	}
	opts, err := parseIssueListOptions(args, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.limit != 50 {
		t.Fatalf("expected limit 50, got %d", opts.limit)
	}
	if opts.state != "all" {
		t.Fatalf("expected state all, got %q", opts.state)
	}
	if opts.author != "octocat" {
		t.Fatalf("expected author octocat, got %q", opts.author)
	}
	if opts.assignee != "hubot" {
		t.Fatalf("expected assignee hubot, got %q", opts.assignee)
	}
	if opts.milestone != "v1.0" {
		t.Fatalf("expected milestone v1.0, got %q", opts.milestone)
	}
	if opts.search != "is:pinned" {
		t.Fatalf("expected search is:pinned, got %q", opts.search)
	}
	if opts.repo != "owner/repo" {
		t.Fatalf("expected repo owner/repo, got %q", opts.repo)
	}
	if len(opts.labels) != 2 || opts.labels[0] != "bug" || opts.labels[1] != "urgent" {
		t.Fatalf("expected labels [bug urgent], got %v", opts.labels)
	}
	if !opts.web {
		t.Fatalf("expected web to be true")
	}
}

func TestParseIssueListOptionsShortFlags(t *testing.T) {
	var stderr bytes.Buffer
	args := []string{
		"-L", "10",
		"-s", "closed",
		"-A", "alice",
		"-a", "bob",
		"-m", "Sprint 1",
		"-S", "label:bug",
		"-R", "org/repo",
		"-l", "enhancement",
		"-w",
	}
	opts, err := parseIssueListOptions(args, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.limit != 10 {
		t.Fatalf("expected limit 10, got %d", opts.limit)
	}
	if opts.state != "closed" {
		t.Fatalf("expected state closed, got %q", opts.state)
	}
	if opts.author != "alice" {
		t.Fatalf("expected author alice, got %q", opts.author)
	}
	if opts.assignee != "bob" {
		t.Fatalf("expected assignee bob, got %q", opts.assignee)
	}
	if opts.milestone != "Sprint 1" {
		t.Fatalf("expected milestone Sprint 1, got %q", opts.milestone)
	}
	if opts.search != "label:bug" {
		t.Fatalf("expected search label:bug, got %q", opts.search)
	}
	if opts.repo != "org/repo" {
		t.Fatalf("expected repo org/repo, got %q", opts.repo)
	}
	if len(opts.labels) != 1 || opts.labels[0] != "enhancement" {
		t.Fatalf("expected labels [enhancement], got %v", opts.labels)
	}
	if !opts.web {
		t.Fatalf("expected web to be true")
	}
}

func TestParseIssueListOptionsHelp(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseIssueListOptions([]string{"--help"}, &stderr)
	if err != errHelpDisplayed {
		t.Fatalf("expected errHelpDisplayed, got %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x issue list") {
		t.Fatalf("expected usage text in stderr, got %q", stderr.String())
	}
}

func TestParseIssueListOptionsLimitValidation(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseIssueListOptions([]string{"--limit", "0"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "limit must be greater than zero") {
		t.Fatalf("expected limit validation error, got %v", err)
	}
}

func TestParseIssueListOptionsUnexpectedArgs(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseIssueListOptions([]string{"extra"}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("expected unexpected arguments error, got %v", err)
	}
}

func TestBuildIssueListArgs(t *testing.T) {
	tests := []struct {
		name     string
		options  issueListOptions
		contains []string
		excludes []string
	}{
		{
			name:     "defaults",
			options:  issueListOptions{limit: 30, state: "open"},
			contains: []string{"issue", "list", "--json", "--limit", "30", "--state", "open"},
			excludes: []string{"--web", "--author", "--assignee", "--milestone", "--search", "--label"},
		},
		{
			name: "all flags",
			options: issueListOptions{
				repo: "org/repo", limit: 10, state: "all",
				author: "alice", assignee: "bob", milestone: "v1",
				search: "is:pinned", labels: stringSliceFlag{"bug", "wip"},
			},
			contains: []string{
				"--repo", "org/repo", "--limit", "10", "--state", "all",
				"--author", "alice", "--assignee", "bob", "--milestone", "v1",
				"--search", "is:pinned", "--label", "bug", "--label", "wip",
			},
		},
		{
			name:     "web mode",
			options:  issueListOptions{limit: 30, state: "open", web: true},
			contains: []string{"issue", "list", "--web"},
			excludes: []string{"--json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildIssueListArgs(tt.options)
			joined := strings.Join(args, " ")
			for _, c := range tt.contains {
				if !strings.Contains(joined, c) {
					t.Errorf("expected args to contain %q, got %v", c, args)
				}
			}
			for _, e := range tt.excludes {
				if strings.Contains(joined, e) {
					t.Errorf("expected args to not contain %q, got %v", e, args)
				}
			}
		})
	}
}

func TestBuildDisplayIssue(t *testing.T) {
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		entry  issueEntry
		expect displayIssue
	}{
		{
			name: "basic open issue",
			entry: issueEntry{
				Number:    42,
				Title:     "Fix the bug",
				Author:    &author{Login: "octocat", Name: "The Octocat"},
				State:     "OPEN",
				Labels:    []issueLabel{{Name: "bug"}, {Name: "urgent"}},
				Assignees: []issueAssignee{{Login: "alice"}, {Login: "bob"}},
				UpdatedAt: now.Add(-2 * time.Hour),
				URL:       "https://github.com/org/repo/issues/42",
			},
			expect: displayIssue{
				Number:    42,
				Title:     "Fix the bug",
				Author:    "The Octocat",
				State:     "open",
				Labels:    "bug, urgent",
				Assignees: "alice, bob",
				Updated:   "2h",
				URL:       "https://github.com/org/repo/issues/42",
			},
		},
		{
			name: "closed issue no labels no assignees",
			entry: issueEntry{
				Number:    7,
				Title:     "Closed thing",
				Author:    &author{Login: "bot"},
				State:     "CLOSED",
				Labels:    nil,
				Assignees: nil,
				UpdatedAt: now.Add(-24 * time.Hour),
				URL:       "https://github.com/org/repo/issues/7",
			},
			expect: displayIssue{
				Number:    7,
				Title:     "Closed thing",
				Author:    "bot",
				State:     "closed",
				Labels:    "",
				Assignees: "",
				Updated:   "1d",
				URL:       "https://github.com/org/repo/issues/7",
			},
		},
		{
			name: "nil author",
			entry: issueEntry{
				Number:    99,
				Title:     "Ghost issue",
				Author:    nil,
				State:     "OPEN",
				UpdatedAt: now,
				URL:       "https://github.com/org/repo/issues/99",
			},
			expect: displayIssue{
				Number:  99,
				Title:   "Ghost issue",
				Author:  "-",
				State:   "open",
				Updated: "0s",
				URL:     "https://github.com/org/repo/issues/99",
			},
		},
		{
			name: "empty login author",
			entry: issueEntry{
				Number:    100,
				Title:     "Empty author",
				Author:    &author{Login: ""},
				State:     "OPEN",
				UpdatedAt: now,
				URL:       "https://github.com/org/repo/issues/100",
			},
			expect: displayIssue{
				Number:  100,
				Title:   "Empty author",
				Author:  "-",
				State:   "open",
				Updated: "0s",
				URL:     "https://github.com/org/repo/issues/100",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildDisplayIssue(tt.entry, now)
			if got.Number != tt.expect.Number {
				t.Errorf("Number: got %d, want %d", got.Number, tt.expect.Number)
			}
			if got.Title != tt.expect.Title {
				t.Errorf("Title: got %q, want %q", got.Title, tt.expect.Title)
			}
			if got.Author != tt.expect.Author {
				t.Errorf("Author: got %q, want %q", got.Author, tt.expect.Author)
			}
			if got.State != tt.expect.State {
				t.Errorf("State: got %q, want %q", got.State, tt.expect.State)
			}
			if got.Labels != tt.expect.Labels {
				t.Errorf("Labels: got %q, want %q", got.Labels, tt.expect.Labels)
			}
			if got.Assignees != tt.expect.Assignees {
				t.Errorf("Assignees: got %q, want %q", got.Assignees, tt.expect.Assignees)
			}
			if got.Updated != tt.expect.Updated {
				t.Errorf("Updated: got %q, want %q", got.Updated, tt.expect.Updated)
			}
			if got.URL != tt.expect.URL {
				t.Errorf("URL: got %q, want %q", got.URL, tt.expect.URL)
			}
		})
	}
}

func TestNormalizeIssueState(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"OPEN", "open"},
		{"CLOSED", "closed"},
		{"open", "open"},
		{"closed", "closed"},
		{"", "-"},
		{"UNKNOWN", "unknown"},
	}
	for _, tt := range tests {
		got := normalizeIssueState(tt.input)
		if got != tt.want {
			t.Errorf("normalizeIssueState(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestJoinLabels(t *testing.T) {
	tests := []struct {
		labels []issueLabel
		want   string
	}{
		{nil, ""},
		{[]issueLabel{}, ""},
		{[]issueLabel{{Name: "bug"}}, "bug"},
		{[]issueLabel{{Name: "bug"}, {Name: "wip"}, {Name: "help wanted"}}, "bug, wip, help wanted"},
	}
	for _, tt := range tests {
		got := joinLabels(tt.labels)
		if got != tt.want {
			t.Errorf("joinLabels(%v) = %q, want %q", tt.labels, got, tt.want)
		}
	}
}

func TestJoinAssignees(t *testing.T) {
	tests := []struct {
		assignees []issueAssignee
		want      string
	}{
		{nil, ""},
		{[]issueAssignee{}, ""},
		{[]issueAssignee{{Login: "alice"}}, "alice"},
		{[]issueAssignee{{Login: "alice"}, {Login: "bob"}}, "alice, bob"},
	}
	for _, tt := range tests {
		got := joinAssignees(tt.assignees)
		if got != tt.want {
			t.Errorf("joinAssignees(%v) = %q, want %q", tt.assignees, got, tt.want)
		}
	}
}

func TestIssueStateCell(t *testing.T) {
	styler := newTableStyler(io.Discard, false)
	tests := []struct {
		state string
	}{
		{"open"},
		{"closed"},
		{"unknown"},
	}
	for _, tt := range tests {
		cell := styler.issueStateCell(tt.state)
		if cell.text != tt.state {
			t.Errorf("issueStateCell(%q).text = %q", tt.state, cell.text)
		}
	}
}

func TestRenderIssueTableEmpty(t *testing.T) {
	var buf bytes.Buffer
	err := renderIssueTable(&buf, nil, issueListOptions{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No issues found.") {
		t.Fatalf("expected empty message, got %q", buf.String())
	}
}

func TestRenderIssueTableSingleIssue(t *testing.T) {
	issues := []displayIssue{
		{
			Number:    1,
			Title:     "First issue",
			Author:    "alice",
			State:     "open",
			Labels:    "bug",
			Assignees: "bob",
			Updated:   "2h ago",
			URL:       "https://github.com/org/repo/issues/1",
		},
	}
	var buf bytes.Buffer
	err := renderIssueTable(&buf, issues, issueListOptions{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "#1") {
		t.Errorf("expected #1 in output")
	}
	if !strings.Contains(output, "First issue") {
		t.Errorf("expected title in output")
	}
	if !strings.Contains(output, "alice") {
		t.Errorf("expected author in output")
	}
	if !strings.Contains(output, "open") {
		t.Errorf("expected state in output")
	}
	if !strings.Contains(output, "bug") {
		t.Errorf("expected labels in output")
	}
	if !strings.Contains(output, "bob") {
		t.Errorf("expected assignees in output")
	}
	if !strings.Contains(output, "2h ago") {
		t.Errorf("expected updated time in output")
	}
}

func TestRenderIssueTableMultipleIssues(t *testing.T) {
	issues := []displayIssue{
		{Number: 10, Title: "Issue A", Author: "alice", State: "open", Labels: "bug", Updated: "1h", URL: "https://github.com/org/repo/issues/10"},
		{Number: 9, Title: "Issue B", Author: "bob", State: "closed", Labels: "", Updated: "3d", URL: "https://github.com/org/repo/issues/9"},
		{Number: 8, Title: "Issue C", Author: "charlie", State: "open", Labels: "feat, help wanted", Assignees: "dave", Updated: "5m", URL: "https://github.com/org/repo/issues/8"},
	}
	var buf bytes.Buffer
	err := renderIssueTable(&buf, issues, issueListOptions{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	// Should contain all three issue numbers
	for _, num := range []string{"#10", "#9", "#8"} {
		if !strings.Contains(output, num) {
			t.Errorf("expected %s in output", num)
		}
	}
}

func TestRenderIssueTableClipboard(t *testing.T) {
	issues := []displayIssue{
		{Number: 42, Title: "Test", Author: "me", State: "open", Updated: "now", URL: "https://github.com/org/repo/issues/42"},
	}

	t.Run("no color no clipboard", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderIssueTable(&buf, issues, issueListOptions{}, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(buf.String(), "copied") {
			t.Errorf("clipboard hint should not appear without color")
		}
	})

	t.Run("with color includes clipboard", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderIssueTable(&buf, issues, issueListOptions{}, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(buf.String(), "gh issue view 42") {
			t.Errorf("expected clipboard command in output")
		}
		if !strings.Contains(buf.String(), "copied") {
			t.Errorf("expected clipboard hint in output")
		}
	})

	t.Run("with repo includes repo flag", func(t *testing.T) {
		var buf bytes.Buffer
		err := renderIssueTable(&buf, issues, issueListOptions{repo: "owner/other"}, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(buf.String(), "gh issue view 42 --repo owner/other") {
			t.Errorf("expected repo flag in clipboard command, got %q", buf.String())
		}
	})
}

func TestRenderIssueTableHeaders(t *testing.T) {
	issues := []displayIssue{
		{Number: 1, Title: "X", Author: "a", State: "open", Updated: "1h"},
	}
	var buf bytes.Buffer
	err := renderIssueTable(&buf, issues, issueListOptions{}, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	for _, h := range []string{"#", "Title", "Author", "State", "Labels", "Assignees", "Updated"} {
		if !strings.Contains(output, h) {
			t.Errorf("expected header %q in output: %q", h, output)
		}
	}
}

func TestExecuteIssueListHappyPath(t *testing.T) {
	origFetch := fetchIssuesFunc
	defer func() { fetchIssuesFunc = origFetch }()

	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	fetchIssuesFunc = func(_ issueListOptions) ([]issueEntry, error) {
		return []issueEntry{
			{
				Number:    42,
				Title:     "Fix the bug",
				Author:    &author{Login: "octocat", Name: "The Octocat"},
				State:     "OPEN",
				Labels:    []issueLabel{{Name: "bug"}},
				Assignees: []issueAssignee{{Login: "alice"}},
				UpdatedAt: now.Add(-2 * time.Hour),
				URL:       "https://github.com/org/repo/issues/42",
			},
		}, nil
	}

	var buf bytes.Buffer
	err := executeIssueList(issueListOptions{limit: 30, state: "open"}, &buf, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "#42") {
		t.Errorf("expected issue number in output, got %q", output)
	}
	if !strings.Contains(output, "Fix the bug") {
		t.Errorf("expected title in output")
	}
}

func TestExecuteIssueListFetchError(t *testing.T) {
	origFetch := fetchIssuesFunc
	defer func() { fetchIssuesFunc = origFetch }()

	fetchIssuesFunc = func(_ issueListOptions) ([]issueEntry, error) {
		return nil, fmt.Errorf("network error")
	}

	var buf bytes.Buffer
	err := executeIssueList(issueListOptions{limit: 30, state: "open"}, &buf, time.Now())
	if err == nil || !strings.Contains(err.Error(), "network error") {
		t.Fatalf("expected network error, got %v", err)
	}
}

func TestExecuteIssueListEmpty(t *testing.T) {
	origFetch := fetchIssuesFunc
	defer func() { fetchIssuesFunc = origFetch }()

	fetchIssuesFunc = func(_ issueListOptions) ([]issueEntry, error) {
		return nil, nil
	}

	var buf bytes.Buffer
	err := executeIssueList(issueListOptions{limit: 30, state: "open"}, &buf, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "No issues found.") {
		t.Fatalf("expected empty message, got %q", buf.String())
	}
}

func TestIssueRouting(t *testing.T) {
	original := executeIssueListFunc
	defer func() { executeIssueListFunc = original }()

	var capturedOptions issueListOptions
	executeIssueListFunc = func(opts issueListOptions, _ io.Writer, _ time.Time) error {
		capturedOptions = opts
		return nil
	}

	t.Run("no args defaults to list", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runIssueCmd(nil, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedOptions.state != "open" {
			t.Errorf("expected default state open, got %q", capturedOptions.state)
		}
	})

	t.Run("flag args default to list", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runIssueCmd([]string{"--state", "closed"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedOptions.state != "closed" {
			t.Errorf("expected state closed, got %q", capturedOptions.state)
		}
	})

	t.Run("explicit list subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runIssueCmd([]string{"list", "--state", "all"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if capturedOptions.state != "all" {
			t.Errorf("expected state all, got %q", capturedOptions.state)
		}
	})

	t.Run("help subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runIssueCmd([]string{"help"}, &stdout, &stderr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stdout.String(), "Issue commands") {
			t.Errorf("expected issue usage in output, got %q", stdout.String())
		}
	})

	t.Run("unknown subcommand", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := runIssueCmd([]string{"bogus"}, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "unknown issue subcommand") {
			t.Fatalf("expected unknown subcommand error, got %v", err)
		}
	})
}

func TestExecuteIssueListAuthorResolution(t *testing.T) {
	origFetch := fetchIssuesFunc
	defer func() { fetchIssuesFunc = origFetch }()
	origResolve := resolveAuthorLoginFunc
	defer func() { resolveAuthorLoginFunc = origResolve }()

	var resolvedAuthor string
	resolveAuthorLoginFunc = func(author, org string) (string, error) {
		resolvedAuthor = author
		return "resolved-user", nil
	}

	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	fetchIssuesFunc = func(opts issueListOptions) ([]issueEntry, error) {
		if opts.author != "resolved-user" {
			t.Fatalf("expected resolved author, got %q", opts.author)
		}
		return []issueEntry{
			{Number: 1, Title: "Test", State: "OPEN", UpdatedAt: now},
		}, nil
	}

	var buf bytes.Buffer
	err := executeIssueList(issueListOptions{
		limit:  30,
		state:  "open",
		author: "John Doe",
		repo:   "myorg/myrepo",
	}, &buf, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolvedAuthor != "John Doe" {
		t.Fatalf("expected resolveAuthorLoginFunc called with 'John Doe', got %q", resolvedAuthor)
	}
}

func TestExecuteIssueListAuthorResolutionError(t *testing.T) {
	origResolve := resolveAuthorLoginFunc
	defer func() { resolveAuthorLoginFunc = origResolve }()

	resolveAuthorLoginFunc = func(author, org string) (string, error) {
		return "", fmt.Errorf("resolve failed")
	}

	var buf bytes.Buffer
	err := executeIssueList(issueListOptions{
		limit:  30,
		state:  "open",
		author: "Unknown Person",
		repo:   "org/repo",
	}, &buf, time.Now())
	if err == nil || !strings.Contains(err.Error(), "resolve failed") {
		t.Fatalf("expected resolve error, got %v", err)
	}
}
