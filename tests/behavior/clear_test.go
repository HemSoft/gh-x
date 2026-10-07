package behavior_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCLIBehaviorClearCapturedOutput(t *testing.T) {
	repository := newFixtureRepository(t)
	for _, test := range []struct {
		name, scenario string
		args           []string
		want           string
		json           bool
	}{
		{"root", "success", []string{"--clear", "status", "--refresh"}, "Repository", false},
		{"alias", "success", []string{"s", "--clear", "--refresh"}, "Repository", false},
		{"nested", "me-org", []string{"pr", "me", "--clear"}, "dependabot", false},
		{"JSON", "me-org", []string{"pr", "me", "--clear", "--json"}, `"checks": "pass"`, true},
		{"single dash JSON", "me-org", []string{"pr", "me", "--clear", "-json"}, `"checks": "pass"`, true},
		{"disabled", "success", []string{"--clear=false", "pr", "list", "--json"}, `"number": 42`, true},
		{"boolean and value flags", "success", []string{"pr", "list", "--draft", "--repo", "HemSoft/gh-x", "--clear", "--json"}, `"number": 42`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runCLI(t, repository, test.scenario, test.args...)
			if result.exitCode != 0 || !strings.Contains(result.stdout, test.want) {
				t.Fatalf("clear invocation failed: exit=%d stdout=%s stderr=%s", result.exitCode, result.stdout, result.stderr)
			}
			if strings.Contains(result.stdout+result.stderr, "\x1b[2J") || strings.Contains(result.calls, "--clear") {
				t.Fatalf("clear leaked into captured output or subprocesses: stdout=%q stderr=%q calls=%s", result.stdout, result.stderr, result.calls)
			}
			if test.json && !json.Valid([]byte(result.stdout)) {
				t.Fatalf("invalid JSON output: %q", result.stdout)
			}
		})
	}
}

func TestCLIBehaviorClearLiteralValue(t *testing.T) {
	result := runCLI(t, newFixtureRepository(t), "success", "--clear", "pr", "list", "--search", "--clear", "--json")
	if result.exitCode != 0 || !json.Valid([]byte(result.stdout)) || !strings.Contains(result.calls, "--search --clear") || strings.Count(result.calls, "--clear") != 1 {
		t.Fatalf("global clear must preserve the literal search value: exit=%d stdout=%s stderr=%s calls=%s", result.exitCode, result.stdout, result.stderr, result.calls)
	}
}

func TestCLIBehaviorClearBoundary(t *testing.T) {
	result := runCLI(t, newFixtureRepository(t), "success", "pr", "list", "--", "--clear")
	if result.exitCode == 0 || !strings.Contains(result.stderr, "unexpected arguments: --clear") || result.calls != "" {
		t.Fatalf("clear after -- must remain a positional argument: exit=%d stderr=%s calls=%s", result.exitCode, result.stderr, result.calls)
	}
}
