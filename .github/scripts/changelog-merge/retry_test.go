package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestReleaseRetryPreservesUnchangedHead(t *testing.T) {
	data, err := os.ReadFile("../../workflows/auto-release.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow struct {
		Jobs map[string]struct{ Steps []struct{ Run string } }
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var script string
	for _, step := range workflow.Jobs["release"].Steps {
		start := strings.Index(step.Run, `branch="chore/changelog-`)
		end := strings.Index(step.Run, "pr_url=$(gh pr list")
		if start >= 0 && end > start {
			script = step.Run[start:end]
		}
	}
	if script == "" {
		t.Fatal("missing changelog branch publication block")
	}
	dir := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit("init", "--bare", "remote.git")
	runGit("init", "-b", "main")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.invalid")
	runGit("remote", "add", "origin", "./remote.git")
	write("CHANGELOG.md", "old\n")
	runGit("add", "CHANGELOG.md")
	runGit("commit", "-m", "base")
	base := runGit("rev-parse", "HEAD")
	bash := "bash"
	if runtime.GOOS == "windows" {
		bash = "C:/Program Files/Git/bin/bash.exe"
	}
	publish := func(notes, date string) string {
		t.Helper()
		runGit("checkout", "--detach", "-f", base)
		write("CHANGELOG.md", notes)
		cmd := exec.Command(bash, "-eu", "-o", "pipefail", "-c", script)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "RELEASE_TAG=v1.2.3", "GIT_COMMITTER_DATE="+date)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("publish: %v\n%s", err, out)
		}
		head := strings.Fields(runGit("ls-remote", "origin", "refs/heads/chore/changelog-1.2.3"))[0]
		runGit("checkout", "--detach", "-f", base)
		runGit("branch", "-D", "chore/changelog-1.2.3")
		return head
	}
	first := publish("released\n", "2026-09-05T12:00:00Z")
	if retry := publish("released\n", "2026-09-05T13:00:00Z"); retry != first {
		t.Fatal("unchanged retry replaced the reviewed head and reset its request deadline")
	}
	// Main advancing must not recreate an otherwise unchanged changelog PR.
	runGit("checkout", "--detach", "-f", base)
	write("product.txt", "new main content\n")
	runGit("add", "product.txt")
	runGit("commit", "-m", "advance main")
	base = runGit("rev-parse", "HEAD")
	if retry := publish("released\n", "2026-09-05T14:00:00Z"); retry != first {
		t.Fatal("main advancing invalidated unchanged changelog review evidence")
	}
	if changed := publish("corrected release\n", "2026-09-05T15:00:00Z"); changed == first {
		t.Fatal("changed changelog must create a new review head")
	}
}
