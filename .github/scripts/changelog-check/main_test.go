package main

import (
	"strings"
	"testing"
)

const validChangelog = `# Changelog

GitHub Releases is the authoritative source for published release notes.

## [Unreleased]

## [1.2.3] - 2026-09-05

[Unreleased]: https://github.com/HemSoft/gh-x/compare/v1.2.3...HEAD
[1.2.3]: https://github.com/HemSoft/gh-x/releases/tag/v1.2.3
`

func TestValidateChangelog(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		tags     []string
		wantErr  string
	}{
		{name: "current latest release", contents: validChangelog, tags: []string{"v1.2.3", "v1.2.2"}},
		{name: "stale Unreleased base", contents: strings.Replace(validChangelog, "compare/v1.2.3", "compare/v1.2.2", 1), tags: []string{"v1.2.3"}, wantErr: "Unreleased comparison"},
		{name: "missing latest section", contents: strings.Replace(validChangelog, "## [1.2.3]", "## [1.2.2]", 1), tags: []string{"v1.2.3", "v1.2.2"}, wantErr: "no published section for latest release"},
		{name: "missing published tag", contents: validChangelog + "[9.9.9]: https://github.com/HemSoft/gh-x/releases/tag/v9.9.9\n", tags: []string{"v1.2.3"}, wantErr: "names missing tag v9.9.9"},
		{name: "wrong release link", contents: strings.Replace(validChangelog, "/releases/tag/v1.2.3", "/compare/v1.2.2...v1.2.3", 1), tags: []string{"v1.2.3"}, wantErr: "published changelog link 1.2.3"},
		{name: "invalid release date", contents: strings.Replace(validChangelog, "2026-09-05", "2026-99-99", 1), tags: []string{"v1.2.3"}, wantErr: "invalid date"},
		{name: "missing authority", contents: strings.Replace(validChangelog, "GitHub Releases is the authoritative source", "Release notes are recorded here", 1), tags: []string{"v1.2.3"}, wantErr: "authoritative source"},
		{name: "no semantic tags", contents: validChangelog, tags: []string{"latest"}, wantErr: "no semantic-version tags"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateChangelog(test.contents, test.tags)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateChangelog() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateChangelog() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestLatestSemanticTagSkipsInvalidTags(t *testing.T) {
	tags := []string{"nightly", "v1.2", "v1.2.3"}
	if got := latestSemanticTag(tags); got != "v1.2.3" {
		t.Fatalf("latestSemanticTag() = %q, want v1.2.3", got)
	}
}
