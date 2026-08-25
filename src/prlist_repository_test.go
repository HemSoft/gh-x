package main

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestResolveRepoOverride(t *testing.T) {
	tests := []struct {
		input     string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{"owner/repo", "owner", "repo", false},
		{"github.com/owner/repo", "owner", "repo", false},
		{"noslash", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			owner, name, err := resolveRepo(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tc.wantOwner || name != tc.wantName {
				t.Fatalf("got %s/%s, want %s/%s", owner, name, tc.wantOwner, tc.wantName)
			}
		})
	}
}

func TestResolveRepoUsesGHRepo(t *testing.T) {
	t.Setenv("GH_REPO", "ghe.example.com/env-owner/env-repo")

	owner, name, err := resolveRepo("")
	if err != nil {
		t.Fatalf("resolveRepo returned error: %v", err)
	}
	if owner != "env-owner" || name != "env-repo" {
		t.Fatalf("resolveRepo with GH_REPO = %s/%s, want env-owner/env-repo", owner, name)
	}
}

func TestResolveAuthorLogin_NoSpace(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"octocat", "octocat"},
		{"@octocat", "octocat"},
		{"some-user", "some-user"},
		{"@some-user", "some-user"},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got, err := resolveAuthorLogin(tc.input, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveAuthorLoginFunc_Integration(t *testing.T) {
	// Verify that runList resolves the author via resolveAuthorLoginFunc and
	// passes the resolved login to executeListFunc.
	saved := resolveAuthorLoginFunc
	t.Cleanup(func() { resolveAuthorLoginFunc = saved })

	var calledWith string
	var calledOrg string
	resolveAuthorLoginFunc = func(author, org string) (string, error) {
		calledWith = author
		calledOrg = org
		return "resolved-login", nil
	}

	savedExec := executeListFunc
	t.Cleanup(func() { executeListFunc = savedExec })
	var receivedAuthor string
	executeListFunc = func(options listOptions, _ io.Writer) error {
		receivedAuthor = options.author
		return nil
	}

	// Call through runList which invokes executeListFunc (the mock).
	err := runList([]string{"--author", "Trey Walters", "--repo", "test-org/test-repo"}, io.Discard, io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calledWith != "Trey Walters" {
		t.Fatalf("resolveAuthorLoginFunc called with %q, want %q", calledWith, "Trey Walters")
	}
	if calledOrg != "test-org" {
		t.Fatalf("resolveAuthorLoginFunc called with org %q, want %q", calledOrg, "test-org")
	}
	if receivedAuthor != "resolved-login" {
		t.Fatalf("executeListFunc received author %q, want %q", receivedAuthor, "resolved-login")
	}
}

func TestParseRepoViewResponse(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOwner string
		wantName  string
		wantErr   bool
	}{
		{
			name:      "valid response",
			input:     `{"name":"gh-x","owner":{"login":"HemSoft"}}`,
			wantOwner: "HemSoft",
			wantName:  "gh-x",
		},
		{
			name:    "missing owner login",
			input:   `{"name":"gh-x","owner":{"login":""}}`,
			wantErr: true,
		},
		{
			name:    "missing name",
			input:   `{"name":"","owner":{"login":"HemSoft"}}`,
			wantErr: true,
		},
		{
			name:    "invalid JSON",
			input:   `{broken`,
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			owner, name, err := parseRepoViewResponse([]byte(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got owner=%q name=%q", owner, name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tc.wantOwner || name != tc.wantName {
				t.Fatalf("got (%q, %q), want (%q, %q)", owner, name, tc.wantOwner, tc.wantName)
			}
		})
	}
}

func TestUniqueBaseBranches(t *testing.T) {
	tests := []struct {
		name   string
		prs    []pullRequest
		expect []string
	}{
		{
			name:   "empty",
			prs:    nil,
			expect: nil,
		},
		{
			name:   "single branch",
			prs:    []pullRequest{{BaseRefName: "main"}},
			expect: []string{"main"},
		},
		{
			name:   "duplicates removed",
			prs:    []pullRequest{{BaseRefName: "main"}, {BaseRefName: "develop"}, {BaseRefName: "main"}},
			expect: []string{"main", "develop"},
		},
		{
			name:   "preserves order",
			prs:    []pullRequest{{BaseRefName: "beta"}, {BaseRefName: "alpha"}},
			expect: []string{"beta", "alpha"},
		},
		{
			name:   "empty base name preserved",
			prs:    []pullRequest{{BaseRefName: ""}, {BaseRefName: "main"}},
			expect: []string{"", "main"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := uniqueBaseBranches(tc.prs)
			if !reflect.DeepEqual(got, tc.expect) {
				t.Fatalf("uniqueBaseBranches() = %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestWrapExecError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		stderr  string
		wantSub string
	}{
		{
			name:    "with stderr",
			err:     fmt.Errorf("search failed"),
			stderr:  "rate limited",
			wantSub: "search failed: rate limited",
		},
		{
			name:    "empty stderr",
			err:     fmt.Errorf("search failed"),
			stderr:  "",
			wantSub: "search failed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := wrapExecError(tc.err, tc.stderr)
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("wrapExecError() = %q, want substring %q", err.Error(), tc.wantSub)
			}
		})
	}
}
