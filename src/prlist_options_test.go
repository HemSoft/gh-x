package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func TestBuildListArgsIncludesFilters(t *testing.T) {
	options := listOptions{
		repo:      "HemSoft/gh-x",
		limit:     50,
		state:     "all",
		author:    "@me",
		assignee:  "octocat",
		app:       "dependabot",
		base:      "main",
		head:      "feature/demo",
		search:    "review:required",
		draftOnly: true,
		labels:    stringSliceFlag{"bug", "urgent"},
	}

	got := buildListArgs(options)
	want := []string{
		"pr", "list",
		"--json", jsonFields,
		"--repo", "HemSoft/gh-x",
		"--limit", "50",
		"--state", "all",
		"--author", "@me",
		"--assignee", "octocat",
		"--app", "dependabot",
		"--base", "main",
		"--head", "feature/demo",
		"--search", "review:required",
		"--draft",
		"--label", "bug",
		"--label", "urgent",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected arguments\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestParseListOptionsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseListOptions(nil, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.limit != 30 {
		t.Fatalf("expected limit 30, got %d", opts.limit)
	}
	if opts.state != "open" {
		t.Fatalf("expected state 'open', got %q", opts.state)
	}
}

func TestParseListOptionsAllFlags(t *testing.T) {
	var stderr bytes.Buffer
	args := []string{
		"--repo", "owner/repo",
		"--limit", "50",
		"--state", "all",
		"--author", "@me",
		"--assignee", "bob",
		"--app", "dependabot",
		"--base", "main",
		"--head", "feature",
		"--search", "bug",
		"--draft",
		"--label", "bug",
		"--label", "urgent",
		"--json",
	}
	opts, err := parseListOptions(args, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.repo != "owner/repo" {
		t.Fatalf("expected repo 'owner/repo', got %q", opts.repo)
	}
	if opts.limit != 50 {
		t.Fatalf("expected limit 50, got %d", opts.limit)
	}
	if opts.state != "all" {
		t.Fatalf("expected state 'all', got %q", opts.state)
	}
	if opts.author != "@me" {
		t.Fatalf("expected author '@me', got %q", opts.author)
	}
	if opts.assignee != "bob" {
		t.Fatalf("expected assignee 'bob', got %q", opts.assignee)
	}
	if opts.app != "dependabot" {
		t.Fatalf("expected app 'dependabot', got %q", opts.app)
	}
	if opts.base != "main" {
		t.Fatalf("expected base 'main', got %q", opts.base)
	}
	if opts.head != "feature" {
		t.Fatalf("expected head 'feature', got %q", opts.head)
	}
	if opts.search != "bug" {
		t.Fatalf("expected search 'bug', got %q", opts.search)
	}
	if !opts.draftOnly {
		t.Fatalf("expected draftOnly true")
	}
	if !opts.json {
		t.Fatalf("expected json true")
	}
	if len(opts.labels) != 2 || opts.labels[0] != "bug" || opts.labels[1] != "urgent" {
		t.Fatalf("expected labels [bug urgent], got %v", opts.labels)
	}
}

func TestParseListOptionsShortFlags(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseListOptions([]string{"-R", "o/r", "-L", "10", "-s", "closed", "-A", "@me", "-B", "main", "-H", "dev", "-S", "fix", "-d", "-w", "-l", "p1"}, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.repo != "o/r" || opts.limit != 10 || opts.state != "closed" {
		t.Fatalf("short flags not parsed: repo=%q limit=%d state=%q", opts.repo, opts.limit, opts.state)
	}
	if opts.author != "@me" || opts.base != "main" || opts.head != "dev" || opts.search != "fix" {
		t.Fatalf("short filters not parsed")
	}
	if !opts.draftOnly || !opts.web {
		t.Fatalf("short bools not parsed")
	}
	if len(opts.labels) != 1 || opts.labels[0] != "p1" {
		t.Fatalf("short label not parsed: %v", opts.labels)
	}
}

func TestParseListOptionsInvalidLimit(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseListOptions([]string{"--limit", "0"}, &stderr)
	if err == nil {
		t.Fatalf("expected error for zero limit")
	}
}

func TestParseListOptionsWebAndJSON(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseListOptions([]string{"--web", "--json"}, &stderr)
	if err == nil {
		t.Fatalf("expected error for --web and --json together")
	}
}

func TestParseListOptionsUnexpectedArgs(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseListOptions([]string{"extra"}, &stderr)
	if err == nil {
		t.Fatalf("expected error for unexpected args")
	}
}

func TestParseListOptionsHelp(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseListOptions([]string{"--help"}, &stderr)
	if err != errHelpDisplayed {
		t.Fatalf("expected errHelpDisplayed, got %v", err)
	}
}

func TestDefaultListOptions(t *testing.T) {
	opts := defaultListOptions()
	if opts.limit != 30 || opts.state != "open" {
		t.Fatalf("unexpected defaults: limit=%d state=%q", opts.limit, opts.state)
	}
}

func TestWriteListUsage(t *testing.T) {
	var buf bytes.Buffer
	writeListUsage(&buf)
	if !strings.Contains(buf.String(), "gh x pr list") {
		t.Fatalf("expected list usage, got %q", buf.String())
	}
}

func TestStringSliceFlag(t *testing.T) {
	var s stringSliceFlag
	if s.String() != "" {
		t.Fatalf("expected empty string, got %q", s.String())
	}
	if err := s.Set("bug"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := s.Set("feature"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.String() != "bug,feature" {
		t.Fatalf("expected 'bug,feature', got %q", s.String())
	}
}

func TestAppendNonEmpty(t *testing.T) {
	args := []string{"base"}
	args = appendNonEmpty(args, "--flag", "val")
	if len(args) != 3 {
		t.Fatalf("expected 3, got %d", len(args))
	}
	args = appendNonEmpty(args, "--empty", "")
	if len(args) != 3 {
		t.Fatalf("expected 3 (no append for empty), got %d", len(args))
	}
}

func TestParseListOptionsValidLimit(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseListOptions([]string{"--limit", "1"}, &stderr)
	if err != nil {
		t.Fatalf("expected no error for limit=1, got: %v", err)
	}
	if opts.limit != 1 {
		t.Fatalf("expected limit 1, got %d", opts.limit)
	}
}
