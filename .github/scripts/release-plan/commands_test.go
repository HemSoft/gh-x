package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func fixtureGit(t *testing.T, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func releaseRepository(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	t.Setenv("GIT_AUTHOR_NAME", "Release Fixture")
	t.Setenv("GIT_COMMITTER_NAME", "Release Fixture")
	t.Setenv("GIT_AUTHOR_EMAIL", "fixture@example.invalid")
	t.Setenv("GIT_COMMITTER_EMAIL", "fixture@example.invalid")
	t.Chdir(base)
	fixtureGit(t, "init", "--bare", "remote")
	fixtureGit(t, "init", "-b", "main", "checkout")
	t.Chdir(filepath.Join(base, "checkout"))
	fixtureGit(t, "remote", "add", "origin", filepath.Join(base, "remote"))
	return filepath.Join(base, "outputs")
}

func commitReleaseFixture(t *testing.T, path, subject string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(subject), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, "add", path)
	fixtureGit(t, "commit", "-m", subject)
	fixtureGit(t, "push", "origin", "main")
	return fixtureGit(t, "rev-parse", "HEAD")
}

func TestRunCheckReleaseDecisions(t *testing.T) {
	tests := []struct {
		name       string
		tagged     bool
		path       string
		superseded bool
		expected   string
	}{
		{name: "first product release", path: "main.go", expected: "skip=false"},
		{name: "tagged head reconciliation", tagged: true, expected: "release_tag=v1.2.3"},
		{name: "documentation only", path: "README.md", expected: "skip=true"},
		{name: "new product change", path: "main.go", expected: "skip=false"},
		{name: "superseded commit", superseded: true, expected: "skip=true"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := releaseRepository(t)
			head := commitReleaseFixture(t, "README.md", "docs: initial")
			if test.tagged || test.name != "first product release" {
				fixtureGit(t, "tag", "v1.2.3")
			}
			if test.path != "" {
				head = commitReleaseFixture(t, test.path, "fix: product")
			}
			if test.superseded {
				head = strings.Repeat("a", 40)
			}
			t.Setenv("RELEASE_SHA", head)
			t.Setenv("GITHUB_OUTPUT", output)
			if err := run([]string{"check"}); err != nil {
				t.Fatal(err)
			}
			bytes, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(bytes), test.expected+"\n") {
				t.Fatalf("outputs = %s, want %s", bytes, test.expected)
			}
		})
	}
}

func TestReleaseCommandsPersistVersionNotesAndChangelog(t *testing.T) {
	output := releaseRepository(t)
	commitReleaseFixture(t, "README.md", "docs: initial")
	fixtureGit(t, "tag", "v1.2.3")
	commitReleaseFixture(t, "main.go", "feat: new command")
	t.Setenv("LATEST_TAG", "v1.2.3")
	t.Setenv("VERSION_BASE_TAG", "v1.2.3")
	t.Setenv("GITHUB_OUTPUT", output)
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(output)
	if err != nil || string(bytes) != "tag=v1.3.0\n" {
		t.Fatalf("version output = %s, %v", bytes, err)
	}
	t.Setenv("RELEASE_TAG", "v1.3.0")
	if err := run([]string{"notes"}); err != nil {
		t.Fatal(err)
	}
	notes, err := os.ReadFile("release-notes.md")
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := parseReleaseNotes(string(notes))
	if err != nil || body != "- feat: new command" {
		t.Fatalf("notes = %s, %v", notes, err)
	}
	original := "## [Unreleased]\n\n## [1.2.3] - 2026-01-01\n\n[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.3...HEAD\n"
	if err := os.WriteFile("CHANGELOG.md", []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := run([]string{"changelog"}); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(changed), "## [1.3.0]") != 1 || !strings.Contains(string(changed), body) {
		t.Fatalf("changelog = %s", changed)
	}
}

func TestReleaseCommandErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing"}, {name: "unknown", args: []string{"other"}},
		{name: "invalid check", args: []string{"check"}}, {name: "invalid notes", args: []string{"notes"}},
		{name: "invalid create", args: []string{"create"}}, {name: "invalid changelog", args: []string{"changelog"}},
		{name: "invalid version", args: []string{"version"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("RELEASE_SHA", "bad")
			t.Setenv("RELEASE_TAG", "bad")
			t.Setenv("LATEST_TAG", "bad")
			if err := run(test.args); err == nil {
				t.Fatal("invalid invocation succeeded")
			}
		})
	}
}

func releaseCLI(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	source := filepath.Join(bin, "fixture.go")
	program := `package main
import("encoding/json";"fmt";"os";"strings")
func main() {
 args:=os.Args[1:]
 file,err:=os.OpenFile(os.Getenv("RELEASE_FIXTURE_LOG"),os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600)
 if err!=nil { panic(err) };if err=json.NewEncoder(file).Encode(args);err!=nil { panic(err) };if err=file.Close();err!=nil { panic(err) }
 if strings.Join(args[:2]," ")=="release view" { if os.Getenv("RELEASE_FIXTURE_RESPONSE")=="missing" { os.Exit(1) };fmt.Print(os.Getenv("RELEASE_FIXTURE_RESPONSE")) }
 if args[0]=="attestation" && os.Getenv("RELEASE_FIXTURE_FAIL_VERIFY")=="true" { os.Exit(1) }
}`
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	name := "gh"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output, err := exec.Command("go", "build", "-o", filepath.Join(bin, name), source).CombinedOutput()
	if err != nil {
		t.Fatalf("build isolated gh fixture: %v: %s", err, output)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	log := filepath.Join(bin, "calls.jsonl")
	t.Setenv("RELEASE_FIXTURE_LOG", log)
	return log
}

func TestCreateAndReconcileReleaseCommands(t *testing.T) {
	tests := []struct {
		name, response string
		failVerify     bool
		wantCommands   [][]string
		wantErr        string
	}{
		{name: "new release", response: "missing", wantCommands: [][]string{{"release", "view"}, {"release", "create"}}},
		{name: "existing draft with missing asset", response: `{"tagName":"v1.2.3","assets":[],"isDraft":true}`, wantCommands: [][]string{{"release", "view"}, {"attestation", "verify"}, {"release", "upload"}, {"release", "edit"}}},
		{name: "invalid existing response", response: "invalid", wantErr: "decode release"},
		{name: "wrong tag", response: `{"tagName":"v9.9.9"}`, wantErr: "lookup returned tag"},
		{name: "failed attestation", response: `{"tagName":"v1.2.3","assets":[]}`, failVerify: true, wantErr: "verify release attestation"},
		{name: "conflicting published digest", response: `{"tagName":"v1.2.3","assets":[{"name":"linux-amd64","digest":"sha256:wrong"}]}`, wantErr: "refusing to replace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			releaseRepository(t)
			head := commitReleaseFixture(t, "README.md", "docs: initial")
			fixtureGit(t, "tag", "v1.2.3")
			fixtureGit(t, "push", "origin", "v1.2.3")
			if err := os.Mkdir("dist", 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("dist/linux-amd64", []byte("attested fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			log := releaseCLI(t)
			t.Setenv("RELEASE_TAG", "v1.2.3")
			t.Setenv("RELEASE_SHA", head)
			t.Setenv("GITHUB_REPOSITORY", "HemSoft/gh-x")
			t.Setenv("RELEASE_FIXTURE_RESPONSE", test.response)
			t.Setenv("RELEASE_FIXTURE_FAIL_VERIFY", fmtBool(test.failVerify))
			err := run([]string{"create"})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want %s", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			var commands [][]string
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var args []string
				if err := json.Unmarshal([]byte(line), &args); err != nil {
					t.Fatal(err)
				}
				commands = append(commands, args[:2])
			}
			if !reflect.DeepEqual(commands, test.wantCommands) {
				t.Fatalf("commands = %v, want %v", commands, test.wantCommands)
			}
			if test.response == "missing" && !strings.Contains(string(data), head) {
				t.Fatal("release creation did not pin source SHA")
			}
		})
	}
}

func fmtBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
