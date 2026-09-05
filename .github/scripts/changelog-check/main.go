package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

const repositoryURL = "https://github.com/HemSoft/gh-x"

var (
	semverTag        = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	publishedHeading = regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$`)
	versionLink      = regexp.MustCompile(`(?m)^\[([0-9]+\.[0-9]+\.[0-9]+)\]: (\S+)$`)
	unreleasedLink   = regexp.MustCompile(`(?m)^\[Unreleased\]: (\S+)$`)
)

func main() {
	contents, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		fail(err)
	}

	tagOutput, err := exec.Command("git", "tag", "--list", "v*", "--sort=-v:refname").Output()
	if err != nil {
		fail(fmt.Errorf("list repository tags: %w", err))
	}

	tags := strings.Fields(string(tagOutput))
	if err := validateChangelog(string(contents), tags); err != nil {
		fail(err)
	}

	fmt.Fprintf(os.Stdout, "CHANGELOG.md matches latest release %s\n", latestSemanticTag(tags))
}

func validateChangelog(contents string, tags []string) error {
	latest := latestSemanticTag(tags)
	if latest == "" {
		return errors.New("repository has no semantic-version tags")
	}

	if !strings.Contains(contents, "GitHub Releases is the authoritative source") {
		return errors.New("CHANGELOG.md must name GitHub Releases as the authoritative source")
	}

	expectedUnreleased := repositoryURL + "/compare/" + latest + "...HEAD"
	match := unreleasedLink.FindStringSubmatch(contents)
	if len(match) == 0 {
		return errors.New("CHANGELOG.md has no Unreleased comparison link")
	}
	if match[1] != expectedUnreleased {
		return fmt.Errorf("Unreleased comparison is %q; want %q", match[1], expectedUnreleased)
	}

	latestVersion := strings.TrimPrefix(latest, "v")
	headings := publishedHeading.FindAllStringSubmatch(contents, -1)
	if !containsVersion(headings, latestVersion) {
		return fmt.Errorf("CHANGELOG.md has no published section for latest release %s", latest)
	}

	tagSet := make(map[string]bool, len(tags))
	for _, tag := range tags {
		tagSet[tag] = true
	}

	links := versionLink.FindAllStringSubmatch(contents, -1)
	linkByVersion := make(map[string]string, len(links))
	for _, link := range links {
		version, url := link[1], link[2]
		tag := "v" + version
		if !tagSet[tag] {
			return fmt.Errorf("published changelog link %s names missing tag %s", version, tag)
		}
		expected := repositoryURL + "/releases/tag/" + tag
		if url != expected {
			return fmt.Errorf("published changelog link %s is %q; want %q", version, url, expected)
		}
		linkByVersion[version] = url
	}

	for _, heading := range headings {
		if _, ok := linkByVersion[heading[1]]; !ok {
			return fmt.Errorf("published changelog section %s has no version link", heading[1])
		}
	}

	if _, ok := linkByVersion[latestVersion]; !ok {
		return fmt.Errorf("latest release %s has no version link", latest)
	}

	return nil
}

func latestSemanticTag(tags []string) string {
	for _, tag := range tags {
		if semverTag.MatchString(tag) {
			return tag
		}
	}
	return ""
}

func containsVersion(matches [][]string, version string) bool {
	for _, match := range matches {
		if match[1] == version {
			return true
		}
	}
	return false
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "changelog check failed:", err)
	os.Exit(1)
}
