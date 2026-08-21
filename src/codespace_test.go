package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseCodespaceListOptionsDefaults(t *testing.T) {
	var stderr bytes.Buffer
	opts, err := parseCodespaceListOptions(nil, &stderr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.org != "" {
		t.Fatalf("expected empty org, got %q", opts.org)
	}
	if opts.repo != "" {
		t.Fatalf("expected empty repo, got %q", opts.repo)
	}
}

func TestParseCodespaceListOptionsFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantOrg  string
		wantRepo string
	}{
		{
			name:     "long org flag",
			args:     []string{"--org", "HemSoft"},
			wantOrg:  "HemSoft",
			wantRepo: "",
		},
		{
			name:     "short org flag",
			args:     []string{"-o", "acme"},
			wantOrg:  "acme",
			wantRepo: "",
		},
		{
			name:     "long repo flag",
			args:     []string{"--repo", "owner/repo"},
			wantOrg:  "",
			wantRepo: "owner/repo",
		},
		{
			name:     "short repo flag",
			args:     []string{"-R", "owner/repo"},
			wantOrg:  "",
			wantRepo: "owner/repo",
		},
		{
			name:     "both flags",
			args:     []string{"--org", "acme", "--repo", "acme/app"},
			wantOrg:  "acme",
			wantRepo: "acme/app",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, err := parseCodespaceListOptions(tc.args, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.org != tc.wantOrg {
				t.Fatalf("expected org %q, got %q", tc.wantOrg, opts.org)
			}
			if opts.repo != tc.wantRepo {
				t.Fatalf("expected repo %q, got %q", tc.wantRepo, opts.repo)
			}
		})
	}
}

func TestParseCodespaceListOptionsHelp(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseCodespaceListOptions([]string{"-h"}, &stderr)
	if err != errHelpDisplayed {
		t.Fatalf("expected errHelpDisplayed, got %v", err)
	}
}

func TestParseCodespaceListOptionsUnexpectedArgs(t *testing.T) {
	var stderr bytes.Buffer
	_, err := parseCodespaceListOptions([]string{"extra"}, &stderr)
	if err == nil {
		t.Fatal("expected error for unexpected arguments")
	}
	if !strings.Contains(err.Error(), "unexpected arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDecodeCodespaces(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int
		wantErr bool
	}{
		{
			name:  "single entry",
			input: `{"display_name":"cs-one","repository":{"full_name":"org/repo"},"git_status":{"ref":"main"},"state":"Available","machine":{"name":"basic","display_name":"2 cores"}}`,
			want:  1,
		},
		{
			name:  "multiple entries",
			input: `{"display_name":"cs-one","repository":{"full_name":"org/repo"},"git_status":{"ref":"main"},"state":"Available","machine":{"name":"basic","display_name":"2 cores"}}` + "\n" + `{"display_name":"cs-two","repository":{"full_name":"org/other"},"git_status":{"ref":"dev"},"state":"Shutdown","machine":{"name":"std","display_name":"4 cores"}}`,
			want:  2,
		},
		{
			name:  "empty input",
			input: "",
			want:  0,
		},
		{
			name:    "invalid json",
			input:   `{"display_name": broken`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := strings.NewReader(tc.input)
			entries, err := decodeCodespaces(r)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(entries) != tc.want {
				t.Fatalf("expected %d entries, got %d", tc.want, len(entries))
			}
		})
	}
}

func TestDecodeCodespacesFieldMapping(t *testing.T) {
	input := `{"display_name":"my-space","repository":{"full_name":"acme/app"},"git_status":{"ref":"feature"},"state":"Available","machine":{"name":"basicLinux32gb","display_name":"2 cores, 8 GB RAM, 32 GB storage"}}`
	entries, err := decodeCodespaces(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.DisplayName != "my-space" {
		t.Fatalf("expected display_name %q, got %q", "my-space", e.DisplayName)
	}
	if e.Repository.FullName != "acme/app" {
		t.Fatalf("expected repository %q, got %q", "acme/app", e.Repository.FullName)
	}
	if e.GitStatus.Ref != "feature" {
		t.Fatalf("expected ref %q, got %q", "feature", e.GitStatus.Ref)
	}
	if e.State != "Available" {
		t.Fatalf("expected state %q, got %q", "Available", e.State)
	}
	if e.Machine.Name != "basicLinux32gb" {
		t.Fatalf("expected machine name %q, got %q", "basicLinux32gb", e.Machine.Name)
	}
}

func TestFilterCodespaces(t *testing.T) {
	entries := []codespaceEntry{
		{
			DisplayName: "cs-one",
			Repository:  codespaceRepo{FullName: "HemSoft/gh-x"},
			GitStatus:   codespaceGit{Ref: "main"},
			State:       "Available",
		},
		{
			DisplayName: "cs-two",
			Repository:  codespaceRepo{FullName: "HemSoft/other-repo"},
			GitStatus:   codespaceGit{Ref: "feature"},
			State:       "Shutdown",
		},
		{
			DisplayName: "cs-three",
			Repository:  codespaceRepo{FullName: "acme/app"},
			GitStatus:   codespaceGit{Ref: "main"},
			State:       "Available",
		},
	}

	tests := []struct {
		name    string
		options codespaceListOptions
		want    int
	}{
		{
			name:    "no filter returns all",
			options: codespaceListOptions{},
			want:    3,
		},
		{
			name:    "filter by org",
			options: codespaceListOptions{org: "HemSoft"},
			want:    2,
		},
		{
			name:    "filter by org case insensitive",
			options: codespaceListOptions{org: "hemsoft"},
			want:    2,
		},
		{
			name:    "filter by repo",
			options: codespaceListOptions{repo: "HemSoft/gh-x"},
			want:    1,
		},
		{
			name:    "filter by repo case insensitive",
			options: codespaceListOptions{repo: "hemsoft/GH-X"},
			want:    1,
		},
		{
			name:    "filter by org with no matches",
			options: codespaceListOptions{org: "nonexistent"},
			want:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := filterCodespaces(entries, tc.options)
			if len(result) != tc.want {
				t.Fatalf("expected %d results, got %d", tc.want, len(result))
			}
		})
	}
}

func TestExecuteCodespaceListEmpty(t *testing.T) {
	original := fetchCodespacesFunc
	defer func() { fetchCodespacesFunc = original }()
	fetchCodespacesFunc = func() ([]codespaceEntry, error) {
		return nil, nil
	}

	var stdout bytes.Buffer
	err := executeCodespaceList(codespaceListOptions{}, &stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No codespaces found") {
		t.Fatalf("expected 'No codespaces found', got %q", stdout.String())
	}
}

func TestExecuteCodespaceListWithEntries(t *testing.T) {
	original := fetchCodespacesFunc
	defer func() { fetchCodespacesFunc = original }()
	fetchCodespacesFunc = func() ([]codespaceEntry, error) {
		return []codespaceEntry{
			{
				DisplayName: "my-codespace",
				Repository:  codespaceRepo{FullName: "HemSoft/gh-x"},
				GitStatus:   codespaceGit{Ref: "main"},
				State:       "Available",
				CreatedAt:   time.Now().Add(-2 * time.Hour),
				Machine: codespaceMachine{
					Name:        "basicLinux32gb",
					DisplayName: "2 cores, 8 GB RAM, 32 GB storage",
				},
			},
		}, nil
	}

	var stdout bytes.Buffer
	err := executeCodespaceList(codespaceListOptions{}, &stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "my-codespace") {
		t.Fatalf("expected display name in output, got %q", output)
	}
	if !strings.Contains(output, "HemSoft/gh-x") {
		t.Fatalf("expected repository in output, got %q", output)
	}
	if !strings.Contains(output, "main") {
		t.Fatalf("expected branch in output, got %q", output)
	}
	if !strings.Contains(output, "Available") {
		t.Fatalf("expected state in output, got %q", output)
	}
	if !strings.Contains(output, "basicLinux32gb") {
		t.Fatalf("expected machine name in output, got %q", output)
	}
}

func TestExecuteCodespaceListFiltered(t *testing.T) {
	original := fetchCodespacesFunc
	defer func() { fetchCodespacesFunc = original }()
	fetchCodespacesFunc = func() ([]codespaceEntry, error) {
		return []codespaceEntry{
			{
				DisplayName: "cs-match",
				Repository:  codespaceRepo{FullName: "acme/app"},
				GitStatus:   codespaceGit{Ref: "main"},
				State:       "Available",
				CreatedAt:   time.Now(),
				Machine:     codespaceMachine{Name: "basicLinux32gb", DisplayName: "2 cores, 8 GB RAM"},
			},
			{
				DisplayName: "cs-nomatch",
				Repository:  codespaceRepo{FullName: "other/repo"},
				GitStatus:   codespaceGit{Ref: "dev"},
				State:       "Shutdown",
				CreatedAt:   time.Now(),
				Machine:     codespaceMachine{Name: "standardLinux32gb", DisplayName: "4 cores, 16 GB RAM"},
			},
		}, nil
	}

	var stdout bytes.Buffer
	err := executeCodespaceList(codespaceListOptions{org: "acme"}, &stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "cs-match") {
		t.Fatalf("expected matching codespace in output, got %q", output)
	}
	if strings.Contains(output, "cs-nomatch") {
		t.Fatalf("expected non-matching codespace to be filtered out, got %q", output)
	}
}

func TestCodespaceStateCell(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, false)

	tests := []struct {
		state string
		want  string
	}{
		{"Available", "Available"},
		{"Shutdown", "Shutdown"},
		{"Starting", "Starting"},
		{"Unknown", "Unknown"},
	}

	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			cell := codespaceStateCell(styler, tc.state)
			if cell.text != tc.want {
				t.Fatalf("expected text %q, got %q", tc.want, cell.text)
			}
		})
	}
}

func TestCodespaceStateCellColored(t *testing.T) {
	var buf bytes.Buffer
	styler := newTableStyler(&buf, true)

	tests := []struct {
		state     string
		wantText  string
		wantColor bool
	}{
		{"Available", "Available", true},
		{"Shutdown", "Shutdown", true},
		{"Shutting Down", "Shutting Down", true},
		{"Starting", "Starting", true},
		{"Rebuilding", "Rebuilding", true},
		{"Unknown", "Unknown", false},
	}

	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			cell := codespaceStateCell(styler, tc.state)
			if cell.text != tc.wantText {
				t.Fatalf("expected text %q, got %q", tc.wantText, cell.text)
			}
			if tc.wantColor && cell.styled == cell.text {
				t.Fatalf("expected styled output to differ from plain text for state %q", tc.state)
			}
			if !tc.wantColor && cell.styled != cell.text {
				t.Fatalf("expected plain output for unknown state %q, got styled %q", tc.state, cell.styled)
			}
		})
	}
}

func TestRenderCodespaceTable(t *testing.T) {
	now := time.Now()
	codespaces := []codespaceEntry{
		{
			DisplayName: "dev-space",
			Repository:  codespaceRepo{FullName: "org/repo"},
			GitStatus:   codespaceGit{Ref: "feature-branch"},
			State:       "Available",
			CreatedAt:   now.Add(-48 * time.Hour),
			Machine: codespaceMachine{
				Name:        "basicLinux32gb",
				DisplayName: "2 cores, 8 GB RAM, 32 GB storage",
			},
		},
	}

	var stdout bytes.Buffer
	err := renderCodespaceTable(&stdout, codespaces, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := stdout.String()
	expectedParts := []string{"NAME", "REPOSITORY", "BRANCH", "STATE", "CREATED", "MACHINE", "SPECS"}
	for _, part := range expectedParts {
		if !strings.Contains(output, part) {
			t.Fatalf("expected header %q in output, got %q", part, output)
		}
	}

	if !strings.Contains(output, "dev-space") {
		t.Fatalf("expected codespace name in output, got %q", output)
	}
	if !strings.Contains(output, "feature-branch") {
		t.Fatalf("expected branch in output, got %q", output)
	}
}

func TestRunCodespaceCmdHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"dash h shows codespace usage", []string{"-h"}, "gh x codespace <command>"},
		{"double dash help shows codespace usage", []string{"--help"}, "gh x codespace <command>"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := runCodespaceCmd(tc.args, &stdout, &stderr)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("expected %q in output, got %q", tc.want, stdout.String())
			}
		})
	}
}
