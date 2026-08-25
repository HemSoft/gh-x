package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestShouldSkipUpdateCheck(t *testing.T) {
	for _, cmd := range []string{"version", "-v", "--version"} {
		if !shouldSkipUpdateCheck(cmd) {
			t.Fatalf("expected skip for %q", cmd)
		}
	}
	for _, cmd := range []string{"pr", "list", "atm", "me", "help", "changelog"} {
		if shouldSkipUpdateCheck(cmd) {
			t.Fatalf("expected no skip for %q", cmd)
		}
	}
}

func TestResolveCommandKnown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolveCommand("help")
	if cmd.name != "help" {
		t.Fatalf("expected name 'help', got %q", cmd.name)
	}
	if !cmd.banner {
		t.Fatalf("expected banner true for help")
	}
	err := cmd.handler(nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "gh x") {
		t.Fatalf("expected usage output, got %q", stdout.String())
	}
}

func TestResolveCommandPr(t *testing.T) {
	cmd := resolveCommand("pr")
	if cmd.name != "pr" {
		t.Fatalf("expected name 'pr', got %q", cmd.name)
	}
	if !cmd.banner {
		t.Fatalf("expected banner true for pr")
	}
}

func TestResolveCommandIssue(t *testing.T) {
	cmd := resolveCommand("issue")
	if cmd.name != "issue" {
		t.Fatalf("expected name 'issue', got %q", cmd.name)
	}
	if !cmd.banner {
		t.Fatalf("expected banner true for issue")
	}
}

func TestResolveIssueCommandKnown(t *testing.T) {
	for _, name := range []string{"list", "help"} {
		cmd := resolveIssueCommand(name)
		if cmd.name != name {
			t.Fatalf("expected name %q, got %q", name, cmd.name)
		}
	}
}

func TestResolveIssueCommandUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolveIssueCommand("bogus")
	if cmd.name != "bogus" {
		t.Fatalf("expected name 'bogus', got %q", cmd.name)
	}
	err := cmd.handler(nil, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown issue subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning command, got %q", err.Error())
	}
	if !strings.Contains(stderr.String(), "gh x issue") {
		t.Fatalf("expected issue usage on stderr, got %q", stderr.String())
	}
}

func TestRunIssueNoArgs(t *testing.T) {
	savedFn := executeIssueListFunc
	defer func() { executeIssueListFunc = savedFn }()
	executeIssueListFunc = func(_ issueListOptions, _ io.Writer, _ time.Time) error { return nil }

	var stdout, stderr bytes.Buffer
	_, err := run([]string{"issue"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunIssueHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"issue", "help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for issue help, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "gh x issue") {
		t.Fatalf("expected issue usage on stdout, got %q", stdout.String())
	}
}

func TestRunIssueDashHelpDefaultsList(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"issue", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for issue --help, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x issue list") {
		t.Fatalf("expected issue list usage in stderr, got %q", stderr.String())
	}
}

func TestRunIssueUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"issue", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown issue subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning bogus, got %q", err.Error())
	}
}

func TestResolvePrCommandKnown(t *testing.T) {
	for _, name := range []string{"list", "atm", "me", "review", "changelog", "help"} {
		cmd := resolvePrCommand(name)
		if cmd.name != name {
			t.Fatalf("expected name %q, got %q", name, cmd.name)
		}
	}
}

func TestResolvePrCommandUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolvePrCommand("bogus")
	if cmd.name != "bogus" {
		t.Fatalf("expected name 'bogus', got %q", cmd.name)
	}
	err := cmd.handler(nil, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown pr subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning command, got %q", err.Error())
	}
	if !strings.Contains(stderr.String(), "gh x pr") {
		t.Fatalf("expected pr usage on stderr, got %q", stderr.String())
	}
}

func TestRunPrNoArgs(t *testing.T) {
	// With no args, pr defaults to "list" subcommand
	savedExecuteList := executeListFunc
	defer func() { executeListFunc = savedExecuteList }()
	executeListFunc = func(_ listOptions, _ io.Writer) error { return nil }

	var stdout, stderr bytes.Buffer
	_, err := run([]string{"pr"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(stdout.String(), "Pull request commands") {
		t.Fatal("expected no pr-level usage on stdout when defaulting to list")
	}
	if strings.Contains(stderr.String(), "Pull request commands") {
		t.Fatal("expected no pr-level usage on stderr when defaulting to list")
	}
}

func TestRunPrHelp(t *testing.T) {
	// "pr help" (no dash) still shows pr-level usage
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"pr", "help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for pr help, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "gh x pr") {
		t.Fatalf("expected pr usage on stdout, got %q", stdout.String())
	}
}

func TestRunPrDashHelpDefaultsList(t *testing.T) {
	// "pr --help" routes to "list --help" since --help is a flag
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"pr", "--help"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected no error for pr --help, got: %v", err)
	}
	if !strings.Contains(stderr.String(), "gh x pr list") {
		t.Fatalf("expected pr list usage in stderr, got %q", stderr.String())
	}
}

func TestRunPrUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := run([]string{"pr", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown pr subcommand")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected error mentioning bogus, got %q", err.Error())
	}
}

func TestLooksLikeFlag(t *testing.T) {
	tests := []struct {
		arg  string
		want bool
	}{
		{"--help", true},
		{"-h", true},
		{"--limit", true},
		{"-L", true},
		{"list", false},
		{"atm", false},
		{"help", false},
	}
	for _, tc := range tests {
		if got := looksLikeFlag(tc.arg); got != tc.want {
			t.Fatalf("looksLikeFlag(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

func TestLooksLikeNumber(t *testing.T) {
	tests := []struct {
		arg  string
		want bool
	}{
		{"363", true},
		{"1", true},
		{"0", true},
		{"", false},
		{"abc", false},
		{"12a", false},
		{"-1", false},
		{"--repo", false},
	}
	for _, tc := range tests {
		if got := looksLikeNumber(tc.arg); got != tc.want {
			t.Fatalf("looksLikeNumber(%q) = %v, want %v", tc.arg, got, tc.want)
		}
	}
}

func TestRunView_MissingNumber(t *testing.T) {
	err := runView([]string{"--repo", "org/repo"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestWritePrUsage(t *testing.T) {
	var buf bytes.Buffer
	writePrUsage(&buf)
	if !strings.Contains(buf.String(), "gh x pr") {
		t.Fatalf("expected pr usage, got %q", buf.String())
	}
	for _, cmd := range []string{"list", "me", "atm", "review", "changelog"} {
		if !strings.Contains(buf.String(), cmd) {
			t.Fatalf("expected pr usage to mention %q", cmd)
		}
	}
}

func TestResolveCommandUnknown(t *testing.T) {
	var stdout, stderr bytes.Buffer
	cmd := resolveCommand("nonexistent")
	if cmd.name != "nonexistent" {
		t.Fatalf("expected name 'nonexistent', got %q", cmd.name)
	}
	err := cmd.handler(nil, &stdout, &stderr)
	if err == nil {
		t.Fatalf("expected error for unknown command")
	}
	if !strings.Contains(err.Error(), "nonexistent") {
		t.Fatalf("expected error mentioning command, got %q", err.Error())
	}
}

func TestErrorHint(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		wantSub string
	}{
		{
			name:    "401 bad credentials",
			err:     fmt.Errorf("gh execution failed: exit status 1: HTTP 401: Bad credentials (https://api.github.com/graphql)"),
			wantSub: "gh auth login",
		},
		{
			name:    "403 SAML",
			err:     fmt.Errorf("HTTP 403: Resource protected by SAML enforcement"),
			wantSub: "SSO",
		},
		{
			name:    "403 generic",
			err:     fmt.Errorf("HTTP 403: forbidden"),
			wantSub: "gh auth status",
		},
		{
			name:    "DNS resolution failure",
			err:     fmt.Errorf("could not resolve host api.github.com"),
			wantSub: "internet connection",
		},
		{
			name:    "timeout",
			err:     fmt.Errorf("request timed out"),
			wantSub: "timed out",
		},
		{
			name:    "404 not found",
			err:     fmt.Errorf("HTTP 404: Not Found"),
			wantSub: "not found",
		},
		{
			name:    "unrelated error no hint",
			err:     fmt.Errorf("decode json: unexpected EOF"),
			wantSub: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hint := errorHint(tc.err)
			if tc.wantSub == "" {
				if hint != "" {
					t.Fatalf("expected no hint, got %q", hint)
				}
				return
			}
			if !strings.Contains(hint, tc.wantSub) {
				t.Fatalf("errorHint() = %q, want substring %q", hint, tc.wantSub)
			}
		})
	}
}
