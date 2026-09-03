package behavior_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	fakeGHModeEnv     = "GH_X_BEHAVIOR_FAKE_GH"
	fakeGHScenarioEnv = "GH_X_BEHAVIOR_SCENARIO"
	fakeGHFixtureEnv  = "GH_X_BEHAVIOR_FIXTURES"
	fakeGHLogEnv      = "GH_X_BEHAVIOR_LOG"
)

var (
	repositoryRoot string
	ghXBinaryPath  string
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeGHModeEnv) == "1" {
		os.Exit(runFakeGH())
	}

	root, err := findRepositoryRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	repositoryRoot = root

	buildDir, err := os.MkdirTemp("", "gh-x-behavior-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create build directory: %v\n", err)
		os.Exit(1)
	}
	ghXBinaryPath = filepath.Join(buildDir, executableName("gh-x"))
	build := exec.Command("go", "build", "-o", ghXBinaryPath, "./src")
	build.Dir = repositoryRoot
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build gh-x: %v\n%s", err, output)
		os.Exit(1)
	}

	code := m.Run()
	if err := os.RemoveAll(buildDir); err != nil && code == 0 {
		fmt.Fprintf(os.Stderr, "remove build directory: %v\n", err)
		code = 1
	}
	os.Exit(code)
}

func TestCLIBehaviorSuccess(t *testing.T) {
	repository := newFixtureRepository(t)

	tests := []struct {
		name       string
		args       []string
		wantStdout []string
		wantCalls  []string
	}{
		{
			name: "pull request list",
			args: []string{"pr", "list", "--repo", "HemSoft/gh-x", "--json"},
			wantStdout: []string{
				`"number": 42`,
				`"title": "Exercise CLI behavior fixture"`,
				`"checks": "pass"`,
				`"issues": "#50"`,
			},
			wantCalls: []string{"pr list", "api --hostname github.com graphql", "api repos/HemSoft/gh-x/rules/branches/main"},
		},
		{
			name: "issue list",
			args: []string{"issue", "list", "--repo", "HemSoft/gh-x"},
			wantStdout: []string{
				"Issues for HemSoft/gh-x",
				"#50",
				"Behavior fixture issue",
				"enhancement",
			},
			wantCalls: []string{"issue list", "api --hostname github.com graphql"},
		},
		{
			name: "repository status",
			args: []string{"status"},
			wantStdout: []string{
				"Repository",
				"HemSoft/gh-x",
				"Open issues (1)",
				"Open pull requests (1)",
				"Recent workflow runs (1)",
			},
			wantCalls: []string{"issue list", "pr list", "run list"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runCLI(t, repository, "success", test.args...)
			if result.exitCode != 0 {
				t.Fatalf("exit code = %d, want 0\nstderr:\n%s", result.exitCode, result.stderr)
			}
			for _, text := range test.wantStdout {
				if !strings.Contains(result.stdout, text) {
					t.Fatalf("stdout does not contain %q:\n%s", text, result.stdout)
				}
			}
			for _, call := range test.wantCalls {
				if !strings.Contains(result.calls, call) {
					t.Fatalf("fake gh call log does not contain %q:\n%s", call, result.calls)
				}
			}
		})
	}
}

func TestCLIBehaviorGitHubFailure(t *testing.T) {
	result := runCLI(t, newFixtureRepository(t), "issue-list-error", "issue", "list", "--repo", "HemSoft/gh-x")

	if result.exitCode == 0 {
		t.Fatalf("exit code = 0, want nonzero\nstdout:\n%s", result.stdout)
	}
	for _, text := range []string{"Error: gh issue list", "fixture issue list failed"} {
		if !strings.Contains(result.stderr, text) {
			t.Fatalf("stderr does not contain %q:\n%s", text, result.stderr)
		}
	}
	if !strings.Contains(result.calls, "issue list") {
		t.Fatalf("fake gh call log does not contain issue list:\n%s", result.calls)
	}
}

type cliResult struct {
	stdout   string
	stderr   string
	calls    string
	exitCode int
}

func runCLI(t *testing.T, workingDirectory, scenario string, args ...string) cliResult {
	t.Helper()

	fakeGH := copyTestExecutable(t)
	callLog := filepath.Join(t.TempDir(), "gh-calls.log")
	command := exec.Command(ghXBinaryPath, args...)
	command.Dir = workingDirectory
	command.Env = replaceEnvironment(os.Environ(), map[string]string{
		"CLICOLOR":        "0",
		"GH_FORCE_TTY":    "0",
		"GH_PATH":         fakeGH,
		"GH_REPO":         "HemSoft/gh-x",
		"NO_COLOR":        "1",
		"TERM":            "dumb",
		fakeGHFixtureEnv:  filepath.Join(repositoryRoot, "tests", "behavior", "testdata"),
		fakeGHLogEnv:      callLog,
		fakeGHModeEnv:     "1",
		fakeGHScenarioEnv: scenario,
	})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()

	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("run gh-x: %v", err)
		}
		exitCode = exitError.ExitCode()
	}
	calls, readErr := os.ReadFile(callLog)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		t.Fatalf("read fake gh call log: %v", readErr)
	}

	return cliResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		calls:    string(calls),
		exitCode: exitCode,
	}
}

func copyTestExecutable(t *testing.T) string {
	t.Helper()

	source, err := os.Executable()
	if err != nil {
		t.Fatalf("resolve test executable: %v", err)
	}
	target := filepath.Join(t.TempDir(), executableName("fake-gh"))
	input, err := os.Open(source)
	if err != nil {
		t.Fatalf("open test executable: %v", err)
	}
	t.Cleanup(func() {
		if err := input.Close(); err != nil {
			t.Errorf("close test executable: %v", err)
		}
	})
	output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatalf("create fake gh: %v", err)
	}
	if _, err := io.Copy(output, input); err != nil {
		if closeErr := output.Close(); closeErr != nil {
			t.Fatalf("copy fake gh: %v; close fake gh: %v", err, closeErr)
		}
		t.Fatalf("copy fake gh: %v", err)
	}
	if err := output.Close(); err != nil {
		t.Fatalf("close fake gh: %v", err)
	}
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatalf("mark fake gh executable: %v", err)
	}
	return target
}

func newFixtureRepository(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	runGit(t, directory, "init", "-b", "main")
	runGit(t, directory, "config", "user.name", "Behavior Tests")
	runGit(t, directory, "config", "user.email", "behavior@example.invalid")
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture repository file: %v", err)
	}
	runGit(t, directory, "add", "README.md")
	runGit(t, directory, "-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	runGit(t, directory, "remote", "add", "origin", "https://github.com/HemSoft/gh-x.git")
	runGit(t, directory, "update-ref", "refs/remotes/origin/main", "HEAD")
	runGit(t, directory, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return directory
}

func runGit(t *testing.T, directory string, args ...string) {
	t.Helper()

	command := exec.Command("git", args...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func findRepositoryRoot() (string, error) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve behavior test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..")), nil
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func replaceEnvironment(current []string, replacements map[string]string) []string {
	result := make([]string, 0, len(current)+len(replacements))
	for _, entry := range current {
		name, _, _ := strings.Cut(entry, "=")
		replaced := false
		for replacement := range replacements {
			if strings.EqualFold(name, replacement) {
				replaced = true
				break
			}
		}
		if !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range replacements {
		result = append(result, name+"="+value)
	}
	return result
}

func runFakeGH() int {
	args := os.Args[1:]
	if err := appendFakeGHCall(args); err != nil {
		fmt.Fprintf(os.Stderr, "record fake gh call: %v\n", err)
		return 2
	}
	if os.Getenv(fakeGHScenarioEnv) == "issue-list-error" && hasCommandPrefix(args, "issue", "list") {
		fmt.Fprintln(os.Stderr, "fixture issue list failed")
		return 23
	}

	fixture := ""
	switch {
	case hasCommandPrefix(args, "pr", "list"):
		fixture = "pr-list.json"
	case hasCommandPrefix(args, "issue", "list"):
		fixture = "issue-list.json"
	case hasCommandPrefix(args, "run", "list"):
		fixture = "workflow-runs.json"
	case hasCommandPrefix(args, "api") && strings.Contains(strings.Join(args, " "), "rules/branches/main"):
		fixture = "required-checks.json"
	case hasCommandPrefix(args, "api") && strings.Contains(strings.Join(args, " "), "closedByPullRequestsReferences"):
		fixture = "issue-relationships.json"
	case hasCommandPrefix(args, "api") && strings.Contains(strings.Join(args, " "), "pullRequest(number: 42)"):
		fixture = "pr-supplemental.json"
	default:
		fmt.Fprintf(os.Stderr, "unsupported fake gh call: %s\n", strings.Join(args, " "))
		return 2
	}

	data, err := os.ReadFile(filepath.Join(os.Getenv(fakeGHFixtureEnv), fixture))
	if err != nil {
		fmt.Fprintf(os.Stderr, "read fixture %s: %v\n", fixture, err)
		return 2
	}
	if _, err := os.Stdout.Write(data); err != nil {
		fmt.Fprintf(os.Stderr, "write fixture %s: %v\n", fixture, err)
		return 2
	}
	return 0
}

func appendFakeGHCall(args []string) error {
	logFile, err := os.OpenFile(os.Getenv(fakeGHLogEnv), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintln(logFile, strings.Join(args, " ")); err != nil {
		_ = logFile.Close()
		return err
	}
	return logFile.Close()
}

func hasCommandPrefix(args []string, prefix ...string) bool {
	if len(args) < len(prefix) {
		return false
	}
	for index := range prefix {
		if args[index] != prefix[index] {
			return false
		}
	}
	return true
}
