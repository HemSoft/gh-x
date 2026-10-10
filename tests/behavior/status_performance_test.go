package behavior_test

import (
	"strings"
	"testing"
)

func TestCLIBehaviorReleaseStatusCache(t *testing.T) {
	saved := ghXBinaryPath
	ghXBinaryPath = ghXReleaseBinaryPath
	t.Cleanup(func() { ghXBinaryPath = saved })
	repository := newFixtureRepository(t)
	cold := runCLI(t, repository, "success", "status", "--refresh")
	if cold.exitCode != 0 || !strings.Contains(cold.calls, "releases/latest") || !strings.Contains(cold.stdout, "#43") {
		t.Fatalf("release cold status incomplete: %+v", cold)
	}
	warm := runCLI(t, repository, "success", "status")
	if warm.exitCode != 0 || warm.calls != "" || !strings.Contains(warm.stdout, "#42") || !strings.Contains(warm.stdout, "#50") {
		t.Fatalf("release warm status made requests or lost rows: %+v", warm)
	}
	t.Logf("release status cold=%v warm=%v gh-cold=%d gh-warm=%d", cold.elapsed, warm.elapsed, len(strings.Split(strings.TrimSpace(cold.calls), "\n")), strings.Count(warm.calls, "\n"))
}
