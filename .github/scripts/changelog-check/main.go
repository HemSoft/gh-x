package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const repositoryURL = "https://github.com/HemSoft/gh-x"

var (
	semverTag        = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	publishedHeading = regexp.MustCompile(`(?m)^## \[([0-9]+\.[0-9]+\.[0-9]+)\]([^\r\n]*)\r?$`)
	versionLink      = regexp.MustCompile(`(?m)^\[([0-9]+\.[0-9]+\.[0-9]+)\]: (\S+)$`)
	unreleasedLink   = regexp.MustCompile(`(?m)^\[Unreleased\]: (\S+)$`)
)

func main() {
	contents, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		fail(err)
	}

	latestOutput, err := exec.Command(
		"gh",
		"release",
		"view",
		"--repo",
		"HemSoft/gh-x",
		"--json",
		"tagName",
		"--jq",
		".tagName",
	).Output()
	if err != nil {
		fail(fmt.Errorf("find latest published GitHub Release: %w", err))
	}
	latest := strings.TrimSpace(string(latestOutput))

	releaseOutput, err := exec.Command(
		"gh",
		"api",
		"--paginate",
		"repos/HemSoft/gh-x/releases",
		"--jq",
		".[] | select(.draft == false and .prerelease == false) | .tag_name",
	).Output()
	if err != nil {
		fail(fmt.Errorf("list published GitHub Releases: %w", err))
	}

	releasedTags := strings.Fields(string(releaseOutput))
	if err := validateChangelog(string(contents), latest, releasedTags); err != nil {
		fail(err)
	}

	fmt.Fprintf(os.Stdout, "CHANGELOG.md matches latest release %s\n", latest)
}

func validateChangelog(contents, latest string, releasedTags []string) error {
	if !semverTag.MatchString(latest) {
		return errors.New("repository latest GitHub Release is not semantic-versioned")
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
	if err := validatePublishedHeadings(headings); err != nil {
		return err
	}
	if !containsVersion(headings, latestVersion) {
		return fmt.Errorf("CHANGELOG.md has no published section for latest release %s", latest)
	}
	return validateVersionLinks(contents, releasedTags, headings, latestVersion)
}

func validatePublishedHeadings(headings [][]string) error {
	for _, heading := range headings {
		date, ok := strings.CutPrefix(heading[2], " - ")
		if !ok {
			return fmt.Errorf("published changelog section %s has invalid date %q", heading[1], strings.TrimSpace(heading[2]))
		}
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return fmt.Errorf("published changelog section %s has invalid date %q", heading[1], date)
		}
	}
	return nil
}

func validateVersionLinks(contents string, releasedTags []string, headings [][]string, latestVersion string) error {
	releaseSet := make(map[string]bool, len(releasedTags))
	for _, tag := range releasedTags {
		releaseSet[tag] = true
	}

	links := versionLink.FindAllStringSubmatch(contents, -1)
	linkByVersion := make(map[string]string, len(links))
	for _, link := range links {
		version, url := link[1], link[2]
		tag := "v" + version
		if !releaseSet[tag] {
			return fmt.Errorf("published changelog link %s names missing GitHub Release %s", version, tag)
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
		return fmt.Errorf("latest release v%s has no version link", latestVersion)
	}

	return nil
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
