package main

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	semverTag       = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)
	breakingSubject = regexp.MustCompile(`^[a-z][a-z0-9-]*(?:\([^\r\n()]+\))?!:`)
	featureSubject  = regexp.MustCompile(`^feat(?:\([^\r\n()]+\))?:`)
	footerSeparator = regexp.MustCompile(`\n[\t ]*\n+`)
	footerToken     = regexp.MustCompile(`^(?:BREAKING CHANGE|[A-Za-z0-9-]+)(?:: | #)`)
	breakingFooter  = regexp.MustCompile(`(?m)^BREAKING(?: CHANGE|-CHANGE): `)
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: release-plan <check|version|notes|create>")
	}

	var err error
	switch os.Args[1] {
	case "check":
		err = runCheck()
	case "version":
		err = runVersion()
	case "notes":
		err = runNotes()
	case "create":
		err = runCreate()
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fail(err.Error())
	}
}

func runCheck() error {
	releaseSHA, err := requiredSHA("RELEASE_SHA")
	if err != nil {
		return err
	}

	remoteMain, err := gitOutput("ls-remote", "origin", "refs/heads/main")
	if err != nil {
		return err
	}
	fields := strings.Fields(remoteMain)
	if len(fields) == 0 {
		return errors.New("origin did not report refs/heads/main")
	}
	if releaseSHA != fields[0] {
		fmt.Fprintf(os.Stdout, "A newer main commit superseded %s - skipping auto-release\n", releaseSHA)
		return writeOutputs(map[string]string{"skip": "true"})
	}

	pointingTags, err := gitOutput("tag", "--points-at", "HEAD")
	if err != nil {
		return err
	}
	for _, tag := range nonEmptyLines(pointingTags) {
		if semverTag.MatchString(tag) {
			fmt.Fprintln(os.Stdout, "HEAD already has a release tag - skipping auto-release")
			return writeOutputs(map[string]string{"skip": "true"})
		}
	}

	latest, err := latestReachableTag()
	if err != nil {
		return err
	}
	versionBase, err := latestSemanticTag()
	if err != nil {
		return err
	}
	rangeStart := latest
	if rangeStart == "" {
		rangeStart, err = gitOutputWithInput("", "hash-object", "-t", "tree", "--stdin")
		if err != nil {
			return err
		}
	}
	changed, err := gitOutput("diff", "--no-renames", "--name-only", rangeStart, "HEAD")
	if err != nil {
		return err
	}
	if !releaseNeeded(nonEmptyLines(changed)) {
		fmt.Fprintln(os.Stdout, "Only documentation or agent metadata changed - skipping auto-release")
		return writeOutputs(map[string]string{"latest": latest, "skip": "true", "version_base": versionBase})
	}

	return writeOutputs(map[string]string{"latest": latest, "skip": "false", "version_base": versionBase})
}

func runVersion() error {
	latest := os.Getenv("LATEST_TAG")
	versionBase := os.Getenv("VERSION_BASE_TAG")
	rangeSpec := "HEAD"
	if latest != "" {
		if !semverTag.MatchString(latest) {
			return fmt.Errorf("could not parse latest tag %q", latest)
		}
		rangeSpec = latest + "..HEAD"
	}
	if versionBase != "" && !semverTag.MatchString(versionBase) {
		return fmt.Errorf("could not parse version base tag %q", versionBase)
	}

	logData, err := gitBytes("log", "-z", "--format=%s%x00%b", rangeSpec)
	if err != nil {
		return err
	}
	subjects, bodies, err := parseCommitLog(logData)
	if err != nil {
		return err
	}
	bump := classifyBump(subjects, bodies)
	next, err := nextVersion(versionBase, bump)
	if err != nil {
		return err
	}
	if err := writeOutputs(map[string]string{"tag": next}); err != nil {
		return err
	}
	previous := versionBase
	if previous == "" {
		previous = "v0.0.0"
	}
	fmt.Fprintf(os.Stdout, "Auto-releasing as %s (%s bump, previous: %s)\n", next, bump, previous)
	return nil
}

func runNotes() error {
	releaseTag := os.Getenv("RELEASE_TAG")
	if !semverTag.MatchString(releaseTag) {
		return fmt.Errorf("invalid RELEASE_TAG %q", releaseTag)
	}
	latest := os.Getenv("LATEST_TAG")
	rangeSpec := "HEAD"
	if latest != "" {
		if !semverTag.MatchString(latest) {
			return fmt.Errorf("invalid LATEST_TAG %q", latest)
		}
		rangeSpec = latest + "..HEAD"
	}
	subjects, err := gitOutput("log", "--format=- %s", "--no-merges", rangeSpec)
	if err != nil {
		return err
	}
	notes := time.Now().UTC().Format("2006-01-02") + "\n\n" + subjects + "\n"
	if err := os.WriteFile("release-notes.md", []byte(notes), 0o644); err != nil {
		return fmt.Errorf("write release notes: %w", err)
	}
	fmt.Fprintf(os.Stdout, "Generated release notes for %s\n%s", releaseTag, notes)
	return nil
}

func runCreate() error {
	releaseTag := os.Getenv("RELEASE_TAG")
	if !semverTag.MatchString(releaseTag) {
		return fmt.Errorf("invalid RELEASE_TAG %q", releaseTag)
	}
	releaseSHA, err := requiredSHA("RELEASE_SHA")
	if err != nil {
		return err
	}
	assets, err := filepath.Glob("dist/*")
	if err != nil {
		return fmt.Errorf("find release assets: %w", err)
	}
	if len(assets) == 0 {
		return errors.New("no release assets found in dist")
	}
	sort.Strings(assets)

	view := exec.Command("gh", "release", "view", releaseTag, "--json", "tagName")
	if err := view.Run(); err == nil {
		if err := runCommand("git", "fetch", "--force", "--tags", "origin"); err != nil {
			return err
		}
		tagSHA, err := gitOutput("rev-list", "-n", "1", releaseTag)
		if err != nil {
			return err
		}
		if tagSHA != releaseSHA {
			return fmt.Errorf("release %s targets %s, not validated SHA %s", releaseTag, tagSHA, releaseSHA)
		}
		fmt.Fprintf(os.Stdout, "Release %s already exists - uploading assets with --clobber\n", releaseTag)
		return runCommand("gh", append([]string{"release", "upload", releaseTag}, append(assets, "--clobber")...)...)
	} else if _, ok := err.(*exec.ExitError); !ok {
		return fmt.Errorf("inspect release %s: %w", releaseTag, err)
	}

	return runCommand("gh", createReleaseArgs(releaseTag, releaseSHA, assets)...)
}

func latestReachableTag() (string, error) {
	tags, err := gitOutput("tag", "--merged", "HEAD", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	return firstSemanticTag(nonEmptyLines(tags)), nil
}

func latestSemanticTag() (string, error) {
	tags, err := gitOutput("tag", "--list", "v*", "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	return firstSemanticTag(nonEmptyLines(tags)), nil
}

func firstSemanticTag(tags []string) string {
	for _, tag := range tags {
		if semverTag.MatchString(tag) {
			return tag
		}
	}
	return ""
}

func releaseNeeded(paths []string) bool {
	for _, path := range paths {
		if strings.HasSuffix(path, ".md") || strings.HasPrefix(path, ".agents/") || path == "LICENSE" {
			continue
		}
		return true
	}
	return false
}

func parseCommitLog(data []byte) ([]string, []string, error) {
	parts := bytes.Split(data, []byte{0})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	if len(parts)%2 != 0 {
		return nil, nil, errors.New("git log returned an incomplete subject/body pair")
	}
	subjects := make([]string, 0, len(parts)/2)
	bodies := make([]string, 0, len(parts)/2)
	for i := 0; i < len(parts); i += 2 {
		subjects = append(subjects, strings.TrimSpace(string(parts[i])))
		bodies = append(bodies, strings.TrimRight(string(parts[i+1]), "\r\n"))
	}
	return subjects, bodies, nil
}

func classifyBump(subjects, bodies []string) string {
	for _, subject := range subjects {
		if breakingSubject.MatchString(subject) {
			return "major"
		}
	}
	for _, body := range bodies {
		if hasBreakingFooter(body) {
			return "major"
		}
	}
	for _, subject := range subjects {
		if featureSubject.MatchString(subject) {
			return "minor"
		}
	}
	return "patch"
}

func hasBreakingFooter(body string) bool {
	normalized := strings.ReplaceAll(strings.TrimRight(body, " \t\r\n"), "\r\n", "\n")
	if normalized == "" {
		return false
	}
	sections := footerSeparator.Split(normalized, -1)
	footerSection := sections[len(sections)-1]
	firstLine, _, _ := strings.Cut(footerSection, "\n")
	return footerToken.MatchString(firstLine) && breakingFooter.MatchString(footerSection)
}

func nextVersion(latest, bump string) (string, error) {
	if latest == "" {
		latest = "v0.0.0"
	}
	matches := semverTag.FindStringSubmatch(latest)
	if matches == nil {
		return "", fmt.Errorf("could not parse latest tag %q", latest)
	}
	major, ok := new(big.Int).SetString(matches[1], 10)
	if !ok {
		return "", fmt.Errorf("could not parse major version in %q", latest)
	}
	minor, ok := new(big.Int).SetString(matches[2], 10)
	if !ok {
		return "", fmt.Errorf("could not parse minor version in %q", latest)
	}
	patch, ok := new(big.Int).SetString(matches[3], 10)
	if !ok {
		return "", fmt.Errorf("could not parse patch version in %q", latest)
	}
	one := big.NewInt(1)
	switch bump {
	case "major":
		major.Add(major, one)
		minor.SetInt64(0)
		patch.SetInt64(0)
	case "minor":
		minor.Add(minor, one)
		patch.SetInt64(0)
	case "patch":
		patch.Add(patch, one)
	default:
		return "", fmt.Errorf("unknown bump %q", bump)
	}
	return fmt.Sprintf("v%s.%s.%s", major.String(), minor.String(), patch.String()), nil
}

func createReleaseArgs(tag, sha string, assets []string) []string {
	args := []string{"release", "create", tag}
	args = append(args, assets...)
	return append(args, "--title", tag, "--target", sha, "--notes-file", "release-notes.md")
}

func requiredSHA(name string) (string, error) {
	value := os.Getenv(name)
	if matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, value); !matched {
		return "", fmt.Errorf("%s must be a full lowercase commit SHA", name)
	}
	return value, nil
}

func writeOutputs(values map[string]string) error {
	path := os.Getenv("GITHUB_OUTPUT")
	if path == "" {
		return errors.New("GITHUB_OUTPUT is not set")
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open GITHUB_OUTPUT: %w", err)
	}
	defer file.Close()
	for _, key := range []string{"latest", "skip", "tag", "version_base"} {
		if value, ok := values[key]; ok {
			if _, err := fmt.Fprintf(file, "%s=%s\n", key, value); err != nil {
				return fmt.Errorf("write GITHUB_OUTPUT: %w", err)
			}
		}
	}
	return nil
}

func nonEmptyLines(value string) []string {
	var result []string
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func gitOutput(args ...string) (string, error) {
	data, err := gitBytes(args...)
	return strings.TrimSpace(string(data)), err
}

func gitOutputWithInput(input string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Stdin = strings.NewReader(input)
	data, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(data)))
	}
	return strings.TrimSpace(string(data)), nil
}

func gitBytes(args ...string) ([]byte, error) {
	command := exec.Command("git", args...)
	data, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func runCommand(name string, args ...string) error {
	command := exec.Command(name, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
