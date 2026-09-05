package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseCommitLogPreservesEmptyBodies(t *testing.T) {
	data := []byte("docs: first\x00\x00feat: second\x00Details\x00")
	subjects, bodies, err := parseCommitLog(data)
	if err != nil {
		t.Fatalf("parseCommitLog() error = %v", err)
	}
	if want := []string{"docs: first", "feat: second"}; !reflect.DeepEqual(subjects, want) {
		t.Fatalf("subjects = %#v, want %#v", subjects, want)
	}
	if want := []string{"", "Details"}; !reflect.DeepEqual(bodies, want) {
		t.Fatalf("bodies = %#v, want %#v", bodies, want)
	}
}

func TestParseCommitLogPreservesIndentedBreakingExample(t *testing.T) {
	subjects, bodies, err := parseCommitLog([]byte("docs: explain migration\x00    BREAKING CHANGE: illustrative text\x00"))
	if err != nil {
		t.Fatalf("parseCommitLog() error = %v", err)
	}
	if got := classifyBump(subjects, bodies); got != "patch" {
		t.Fatalf("classifyBump() after parse = %q, want patch", got)
	}
}

func TestClassifyBumpUsesSubjectsForConventionalTypes(t *testing.T) {
	tests := []struct {
		name     string
		subjects []string
		bodies   []string
		want     string
	}{
		{name: "body examples do not bump", subjects: []string{"docs: explain releases"}, bodies: []string{"Examples:\nfeat: add a feature\nfix!: break an API"}, want: "patch"},
		{name: "feature subject", subjects: []string{"feat(cli): add filtering"}, want: "minor"},
		{name: "breaking subject", subjects: []string{"fix(api)!: remove old field"}, want: "major"},
		{name: "breaking footer", subjects: []string{"fix: parse input"}, bodies: []string{"Details.\n\nBREAKING CHANGE: input is now strict"}, want: "major"},
		{name: "breaking example in body", subjects: []string{"docs: explain migration"}, bodies: []string{"Examples:\nBREAKING CHANGE: input is now strict\nUse --strict to migrate."}, want: "patch"},
		{name: "breaking prose before footer", subjects: []string{"fix: parse input"}, bodies: []string{"Discussion:\nBREAKING CHANGE: this sentence is illustrative.\n\nRefs: #52"}, want: "patch"},
		{name: "breaking footer followed by later trailer", subjects: []string{"fix: parse input"}, bodies: []string{"Details.\n\nBREAKING CHANGE: remove legacy mode\n\nRefs: #52"}, want: "major"},
		{name: "indented breaking example", subjects: []string{"docs: explain migration"}, bodies: []string{"Example:\n\n    BREAKING CHANGE: illustrative text"}, want: "patch"},
		{name: "breaking token in footer block", subjects: []string{"fix: parse input"}, bodies: []string{"Details.\n\nRefs: #52\nBREAKING-CHANGE: input is now strict"}, want: "major"},
		{name: "multi-paragraph breaking footer", subjects: []string{"fix: parse input"}, bodies: []string{"Details.\n\nBREAKING CHANGE: remove legacy mode\n\nUse --new-mode instead."}, want: "major"},
		{name: "major wins", subjects: []string{"feat: add filtering", "refactor!: replace protocol"}, want: "major"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyBump(test.subjects, test.bodies); got != test.want {
				t.Fatalf("classifyBump() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFirstSemanticTagSkipsNonSemanticTags(t *testing.T) {
	tags := []string{"vnext", "v999999999999999999999999999999.2.3", "v2.0.0"}
	if got, want := firstSemanticTag(tags), "v999999999999999999999999999999.2.3"; got != want {
		t.Fatalf("firstSemanticTag() = %q, want %q", got, want)
	}
}

func TestWriteOutputsIncludesReleaseTag(t *testing.T) {
	outputPath := filepath.Join(t.TempDir(), "github-output")
	t.Setenv("GITHUB_OUTPUT", outputPath)

	err := writeOutputs(map[string]string{
		"release_tag": "v1.2.3",
		"skip":        "true",
	})
	if err != nil {
		t.Fatalf("writeOutputs() error = %v", err)
	}

	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read outputs: %v", err)
	}
	if got, want := string(contents), "skip=true\nrelease_tag=v1.2.3\n"; got != want {
		t.Fatalf("outputs = %q, want %q", got, want)
	}
}

func TestReleaseNeededExaminesEveryPath(t *testing.T) {
	tests := []struct {
		name  string
		paths []string
		want  bool
	}{
		{name: "documentation only", paths: []string{"README.md", ".agents/skills/example/SKILL.md", "LICENSE"}, want: false},
		{name: "source change", paths: []string{"README.md", "src/main.go"}, want: true},
		{name: "rename source endpoint", paths: []string{"docs/old.md", "src/new.go"}, want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := releaseNeeded(test.paths); got != test.want {
				t.Fatalf("releaseNeeded() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestUpdateChangelog(t *testing.T) {
	contents := `# Changelog

## [Unreleased]

See changes since the latest release.

## [1.2.3] - 2026-09-04

- Previous release.

[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.3...HEAD
[1.2.3]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.3
`
	notes := "2026-09-05\n\n- Added the next release.\n"

	updated, changed, err := updateChangelog(contents, "v1.2.4", notes)
	if err != nil {
		t.Fatalf("updateChangelog() error = %v", err)
	}
	if !changed {
		t.Fatal("updateChangelog() changed = false, want true")
	}
	for _, want := range []string{
		"## [1.2.4] - 2026-09-05\n\n- Added the next release.",
		"[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.4...HEAD",
		"[1.2.4]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.4",
	} {
		if !strings.Contains(updated, want) {
			t.Fatalf("updated changelog does not contain %q:\n%s", want, updated)
		}
	}

	second, changed, err := updateChangelog(updated, "v1.2.4", notes)
	if err != nil || changed || second != updated {
		t.Fatalf("second update = changed %t, error %v; want unchanged", changed, err)
	}
}

func TestUpdateChangelogPreservesCRLF(t *testing.T) {
	contents := strings.ReplaceAll(`## [Unreleased]

## [1.2.3] - 2026-09-04

[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.3...HEAD
[1.2.3]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.3
`, "\n", "\r\n")

	updated, changed, err := updateChangelog(contents, "v1.2.4", "2026-09-05\n\n- Added the next release.\n")
	if err != nil {
		t.Fatalf("updateChangelog() error = %v", err)
	}
	if !changed {
		t.Fatal("updateChangelog() changed = false, want true")
	}
	if strings.Contains(strings.ReplaceAll(updated, "\r\n", ""), "\n") {
		t.Fatal("updateChangelog() introduced LF-only line endings")
	}
	for _, want := range []string{
		"## [1.2.4] - 2026-09-05\r\n\r\n- Added the next release.",
		"[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.4...HEAD\r\n[1.2.4]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.4",
	} {
		if !strings.Contains(updated, want) {
			t.Fatalf("updated changelog does not contain %q:\n%s", want, updated)
		}
	}
}

func TestUpdateChangelogUsesLocalLineEndings(t *testing.T) {
	contents := "# Changelog\r\n\n## [Unreleased]\n\n## [1.2.3] - 2026-09-04\n\n[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.3...HEAD\n[1.2.3]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.3\n"

	updated, changed, err := updateChangelog(contents, "v1.2.4", "2026-09-05\n\n- Added the next release.\n")
	if err != nil {
		t.Fatalf("updateChangelog() error = %v", err)
	}
	if !changed {
		t.Fatal("updateChangelog() changed = false, want true")
	}
	for _, want := range []string{
		"## [Unreleased]\n\n## [1.2.4] - 2026-09-05\n\n- Added the next release.\n\n## [1.2.3]",
		"[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.4...HEAD\n[1.2.4]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.4",
	} {
		if !strings.Contains(updated, want) {
			t.Fatalf("updated changelog does not contain %q:\n%s", want, updated)
		}
	}
}

func TestUpdateChangelogAcceptsCompletedHistoricalRelease(t *testing.T) {
	contents := `## [Unreleased]

## [1.2.4] - 2026-09-05

## [1.2.3] - 2026-09-04

[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.4...HEAD
[1.2.4]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.4
[1.2.3]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.3
`

	updated, changed, err := updateChangelog(contents, "v1.2.3", "2026-09-04\n\n- Historical release.\n")
	if err != nil || changed || updated != contents {
		t.Fatalf("historical update = changed %t, error %v; want unchanged", changed, err)
	}
}

func TestUpdateChangelogRejectsIncompleteExistingRelease(t *testing.T) {
	contents := "## [Unreleased]\n\n## [1.2.3] - 2026-09-04\n\n[Unreleased]: old\n"
	if _, _, err := updateChangelog(contents, "v1.2.3", "2026-09-04\n\n- Release.\n"); err == nil {
		t.Fatal("updateChangelog() error = nil, want incomplete-section error")
	}
}

func TestUpdateChangelogRejectsInvalidReleaseNotes(t *testing.T) {
	contents := "## [Unreleased]\n\n## [1.2.3] - 2026-09-04\n\n[Unreleased]: old\n"
	for _, notes := range []string{
		"not-a-date\n\n- Change\n",
		"2026-02-30\n\n- Impossible date\n",
		"2026-09-05\n\n",
	} {
		if _, _, err := updateChangelog(contents, "v1.2.4", notes); err == nil {
			t.Fatalf("updateChangelog(%q) error = nil, want error", notes)
		}
	}
}

func TestNextVersion(t *testing.T) {
	for _, test := range []struct {
		latest string
		bump   string
		want   string
	}{
		{latest: "", bump: "patch", want: "v0.0.1"},
		{latest: "v1.2.3", bump: "patch", want: "v1.2.4"},
		{latest: "v1.2.3", bump: "minor", want: "v1.3.0"},
		{latest: "v1.2.3", bump: "major", want: "v2.0.0"},
		{latest: "v999999999999999999999999999999.2.3", bump: "major", want: "v1000000000000000000000000000000.0.0"},
	} {
		if got, err := nextVersion(test.latest, test.bump); err != nil || got != test.want {
			t.Fatalf("nextVersion(%q, %q) = %q, %v; want %q", test.latest, test.bump, got, err, test.want)
		}
	}
}

func TestCreateReleaseArgsPinsValidatedSHA(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	want := []string{"release", "create", "v1.2.3", "dist/linux-amd64", "--title", "v1.2.3", "--target", sha, "--notes-file", "release-notes.md"}
	if got := createReleaseArgs("v1.2.3", sha, []string{"dist/linux-amd64"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("createReleaseArgs() = %#v, want %#v", got, want)
	}
}
