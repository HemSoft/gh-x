package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultMonitorConfigSeedsRepoAndSections(t *testing.T) {
	cfg := defaultMonitorConfig("HemSoft/gh-x")
	if len(cfg.Repos) != 1 || cfg.Repos[0] != "HemSoft/gh-x" {
		t.Fatalf("expected seeded repo, got %v", cfg.Repos)
	}
	if cfg.Defaults.Limit != defaultMonitorLimit {
		t.Fatalf("expected default limit %d, got %d", defaultMonitorLimit, cfg.Defaults.Limit)
	}
	if _, err := parseMonitorInterval(cfg.Defaults.Interval); err != nil {
		t.Fatalf("starter interval should parse: %v", err)
	}
	if len(cfg.PRSections) == 0 || len(cfg.IssueSections) == 0 {
		t.Fatal("starter sections must not be empty")
	}
}

func TestLoadOrCreateMonitorConfigCreatesStarterFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdir", "config.yml")
	_, created, err := loadOrCreateMonitorConfig(path, "owner/repo", defaultGitHubHost)
	if err != nil {
		t.Fatalf("loadOrCreateMonitorConfig: %v", err)
	}
	if !created {
		t.Fatal("expected created=true for missing file")
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatalf("starter file missing or empty: %v", err)
	}
	if string(data)[:1] != "#" {
		t.Fatal("starter file should begin with a comment")
	}
	loaded, createdAgain, err := loadOrCreateMonitorConfig(path, "", defaultGitHubHost)
	if err != nil || createdAgain {
		t.Fatalf("reload should succeed without recreating: created=%v err=%v", createdAgain, err)
	}
	if len(loaded.Repos) != 1 || loaded.Repos[0] != "owner/repo" {
		t.Fatalf("round-trip lost repos: %+v", loaded.Repos)
	}
}

func TestLoadOrCreateMonitorConfigRejectsBadYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("repos: [broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadOrCreateMonitorConfig(path, "", defaultGitHubHost); err == nil {
		t.Fatal("expected YAML error")
	}
}

func TestLoadMonitorConfigQualifiesLegacyEnterpriseRepositories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg := defaultMonitorConfig("")
	cfg.Repos = []string{"Acme/Widgets", "Acme/Service"}
	if err := saveMonitorConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	loaded, _, err := loadOrCreateMonitorConfig(path, "", "ghe.example.com")
	if err != nil {
		t.Fatalf("load legacy enterprise config: %v", err)
	}
	want := []string{"ghe.example.com/Acme/Widgets", "ghe.example.com/Acme/Service"}
	for i := range want {
		if loaded.Repos[i] != want[i] {
			t.Fatalf("Repos[%d] = %q, want %q", i, loaded.Repos[i], want[i])
		}
	}
}

func TestLoadMonitorConfigDoesNotRewriteNewMixedHostRepositories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg := defaultMonitorConfig("")
	cfg.Repos = []string{"HemSoft/gh-x", "ghe.example.com/Acme/Widgets"}
	if err := saveMonitorConfig(path, cfg); err != nil {
		t.Fatal(err)
	}

	loaded, _, err := loadOrCreateMonitorConfig(path, "", "ghe.example.com")
	if err != nil {
		t.Fatalf("load mixed-host config: %v", err)
	}
	if loaded.Repos[0] != "HemSoft/gh-x" || loaded.Repos[1] != "ghe.example.com/Acme/Widgets" {
		t.Fatalf("new mixed-host config was rewritten: %v", loaded.Repos)
	}
}

func TestValidateMonitorConfigErrors(t *testing.T) {
	valid := defaultMonitorConfig("owner/repo")
	if err := validateMonitorConfig(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	noRepos := defaultMonitorConfig("")
	noRepos.Repos = nil
	if err := validateMonitorConfig(noRepos); err == nil {
		t.Fatal("expected repo error")
	}

	badRepo := defaultMonitorConfig("justone")
	if err := validateMonitorConfig(badRepo); err == nil {
		t.Fatal("expected malformed repo error")
	}

	badInterval := defaultMonitorConfig("owner/repo")
	badInterval.Defaults.Interval = "5s"
	if err := validateMonitorConfig(badInterval); err == nil {
		t.Fatal("expected interval error below minimum")
	}

	badSection := defaultMonitorConfig("owner/repo")
	badSection.PRSections[0].Filters = ""
	if err := validateMonitorConfig(badSection); err == nil {
		t.Fatal("expected section filter error")
	}
}

func TestParseMonitorRepositoryNormalizesHostOwnerAndName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  monitorRepository
		label string
	}{
		{
			name:  "github.com default",
			input: " HemSoft/gh-x ",
			want:  monitorRepository{Host: defaultGitHubHost, Owner: "HemSoft", Name: "gh-x"},
			label: "HemSoft/gh-x",
		},
		{
			name:  "explicit github.com",
			input: "GitHub.COM/HemSoft/gh-x",
			want:  monitorRepository{Host: defaultGitHubHost, Owner: "HemSoft", Name: "gh-x"},
			label: "HemSoft/gh-x",
		},
		{
			name:  "enterprise host",
			input: "GHE.Example.COM./Acme/Widgets",
			want:  monitorRepository{Host: "ghe.example.com", Owner: "Acme", Name: "Widgets"},
			label: "ghe.example.com/Acme/Widgets",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMonitorRepository(tc.input)
			if err != nil {
				t.Fatalf("parseMonitorRepository(%q): %v", tc.input, err)
			}
			if got != tc.want {
				t.Fatalf("parseMonitorRepository(%q) = %+v, want %+v", tc.input, got, tc.want)
			}
			if got.configValue() != tc.label {
				t.Fatalf("configValue() = %q, want %q", got.configValue(), tc.label)
			}
		})
	}
}

func TestNormalizeMonitorConfigCanonicalizesRepositories(t *testing.T) {
	cfg := defaultMonitorConfig("")
	cfg.Repos = []string{"GitHub.COM/HemSoft/gh-x", "GHE.Example.COM./Acme/Widgets"}
	normalizeMonitorConfig(cfg)

	want := []string{"HemSoft/gh-x", "ghe.example.com/Acme/Widgets"}
	for i := range want {
		if cfg.Repos[i] != want[i] {
			t.Fatalf("Repos[%d] = %q, want %q", i, cfg.Repos[i], want[i])
		}
	}
}

func TestParseMonitorIntervalBounds(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "10m", want: 10 * time.Minute},
		{in: " 1h ", want: time.Hour},
		{in: "5s", wantErr: true},
		{in: "7h", wantErr: true},
		{in: "nonsense", wantErr: true},
	}
	for _, tc := range cases {
		got, err := parseMonitorInterval(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseMonitorInterval(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Fatalf("parseMonitorInterval(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
		}
	}
}

func TestNormalizeMonitorConfigFillsDefaults(t *testing.T) {
	var cfg monitorConfig
	normalizeMonitorConfig(&cfg)
	if cfg.Defaults.Limit != defaultMonitorLimit {
		t.Fatalf("limit default not applied: %d", cfg.Defaults.Limit)
	}
	if !validMonitorIntervalText(cfg.Defaults.Interval) {
		t.Fatalf("interval default invalid: %q", cfg.Defaults.Interval)
	}
	if len(cfg.PRSections) == 0 || len(cfg.IssueSections) == 0 {
		t.Fatal("section defaults not applied")
	}
}

func TestSaveMonitorConfigRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg := defaultMonitorConfig("owner/repo")
	cfg.Repos = append(cfg.Repos, "other/repo2")
	cfg.PRSections[0].Limit = 50
	if err := saveMonitorConfig(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, _, err := loadOrCreateMonitorConfig(path, "", defaultGitHubHost)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded.Repos) != 2 {
		t.Fatalf("repos lost: %v", loaded.Repos)
	}
	if loaded.PRSections[0].Limit != 50 {
		t.Fatalf("section limit lost: %+v", loaded.PRSections[0])
	}
}

func TestMonitorSectionLimitClampsToMaximum(t *testing.T) {
	if got := monitorSectionLimit(monitorSection{Limit: 500}, 30); got != maximumMonitorFetch {
		t.Fatalf("section limit override not clamped: %d", got)
	}
	if got := monitorSectionLimit(monitorSection{}, 250); got != maximumMonitorFetch {
		t.Fatalf("global limit not clamped: %d", got)
	}
	if got := monitorSectionLimit(monitorSection{Limit: 5}, 30); got != 5 {
		t.Fatalf("section limit override ignored: %d", got)
	}
}

func TestMonitorStatePersistenceAndClamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := saveMonitorState(path, monitorSessionState{Tab: 1, SubTab: 2, RepoIndex: 3}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	state := loadMonitorState(path)
	if state.Tab != 1 || state.SubTab != 2 || state.RepoIndex != 3 {
		t.Fatalf("state round-trip mismatch: %+v", state)
	}
	clamped := clampMonitorState(state, 2, 2, 1, 2)
	if clamped.SubTab != 0 {
		t.Fatalf("issue sub-tab not clamped to bounds: %d", clamped.SubTab)
	}
	if clamped.RepoIndex != 0 {
		t.Fatalf("out-of-range repo index should reset to All: %d", clamped.RepoIndex)
	}
	if got := loadMonitorState(filepath.Join(t.TempDir(), "missing.json")); got != (monitorSessionState{}) {
		t.Fatalf("missing state should be zero value: %+v", got)
	}
	if err := saveMonitorState("", monitorSessionState{}); !errors.Is(err, nil) || err != nil {
		t.Fatalf("empty path should be a no-op, got %v", err)
	}
}
