package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRunVersionUpToDate(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v1.2.3", nil
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "gh-x v1.2.3 © 2026 HemSoft Developments") {
		t.Fatalf("expected version line, got %q", out)
	}
	if !strings.Contains(out, "gh extension install HemSoft/gh-x") {
		t.Fatalf("expected install command, got %q", out)
	}
	if !strings.Contains(out, "✓ Up to date") {
		t.Fatalf("expected up-to-date indicator, got %q", out)
	}
}

func TestRunVersionUpdateAvailable(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v2.0.0", nil
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "↑ v2.0.0 available") {
		t.Fatalf("expected update indicator, got %q", out)
	}
	if !strings.Contains(out, "gh extension upgrade gh-x") {
		t.Fatalf("expected upgrade command, got %q", out)
	}
}

func TestRunVersionAheadOfLatestRelease(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v0.2.2", nil
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "v0.2.3")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "✓ Up to date") {
		t.Fatalf("expected ahead-of-release build to be current, got %q", out)
	}
	if strings.Contains(out, "v0.2.2 available") {
		t.Fatalf("older release must not be advertised as an update, got %q", out)
	}
}

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		name               string
		candidate, current string
		want               bool
	}{
		{name: "major", candidate: "v2.0.0", current: "v1.9.9", want: true},
		{name: "minor", candidate: "v1.3.0", current: "v1.2.9", want: true},
		{name: "patch", candidate: "v1.2.4", current: "v1.2.3", want: true},
		{name: "equal", candidate: "v1.2.3", current: "v1.2.3", want: false},
		{name: "older", candidate: "v0.2.2", current: "v0.2.3", want: false},
		{name: "without prefix", candidate: "1.2.4", current: "1.2.3", want: true},
		{name: "invalid candidate", candidate: "latest", current: "v1.2.3", want: false},
		{name: "invalid current", candidate: "v1.2.3", current: "dev", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNewerVersion(tc.candidate, tc.current); got != tc.want {
				t.Fatalf("isNewerVersion(%q, %q) = %t, want %t", tc.candidate, tc.current, got, tc.want)
			}
		})
	}
}

func TestRunVersionDevBuild(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v0.5.0", nil
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "dev")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "gh-x dev © 2026 HemSoft Developments") {
		t.Fatalf("expected dev version, got %q", out)
	}
	if !strings.Contains(out, "⚙ Dev build · latest release: v0.5.0") {
		t.Fatalf("expected dev build indicator, got %q", out)
	}
}

func TestRunVersionAPIError(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "", fmt.Errorf("network error")
	}

	var buf bytes.Buffer
	err := runVersionTestable(&buf, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "⚠ Could not check for updates") {
		t.Fatalf("expected error fallback, got %q", out)
	}
}

func TestPrintBanner(t *testing.T) {
	oldVersion := version
	oldDate := buildDate
	defer func() { version = oldVersion; buildDate = oldDate }()

	version = "v1.2.3"
	buildDate = "2026-05-10"
	var buf bytes.Buffer
	printBanner(&buf)
	if got := buf.String(); got != "gh-x v1.2.3 (2026-05-10) © 2026 HemSoft Developments\n" {
		t.Fatalf("unexpected banner: %q", got)
	}
}

func TestPrintBannerNoDate(t *testing.T) {
	oldVersion := version
	oldDate := buildDate
	defer func() { version = oldVersion; buildDate = oldDate }()

	version = "v1.2.3"
	buildDate = ""
	var buf bytes.Buffer
	printBanner(&buf)
	if got := buf.String(); got != "gh-x v1.2.3 © 2026 HemSoft Developments\n" {
		t.Fatalf("unexpected banner without date: %q", got)
	}
}

func TestFormatVersion(t *testing.T) {
	if got := formatVersion("v1.0.0", "2026-05-10"); got != "v1.0.0 (2026-05-10)" {
		t.Fatalf("expected date in parens, got %q", got)
	}
	if got := formatVersion("v1.0.0", ""); got != "v1.0.0" {
		t.Fatalf("expected no parens when date empty, got %q", got)
	}
}

func TestBannerOnRootUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, _ = run(nil, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "gh-x") {
		t.Fatalf("expected banner on stderr for root usage, got %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Available Commands") {
		t.Fatalf("expected usage on stdout, got %q", stdout.String())
	}
}

func TestBannerOnHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, _ = run([]string{"--help"}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "gh-x") {
		t.Fatalf("expected banner on stderr for help, got %q", stderr.String())
	}
}

func TestNoBannerOnVersion(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v1.0.0", nil
	}

	for _, arg := range []string{"version", "--version", "-v"} {
		var stdout, stderr bytes.Buffer
		_, _ = run([]string{arg}, &stdout, &stderr)
		if strings.Contains(stderr.String(), "gh-x") {
			t.Fatalf("run(%q) should not print banner to stderr, got %q", arg, stderr.String())
		}
	}
}

func TestBannerOnUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, _ = run([]string{"bogus"}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "gh-x") {
		t.Fatalf("expected banner on stderr for unknown command, got %q", stderr.String())
	}
}

func TestUpgradeNoticeShown(t *testing.T) {
	oldVersion := version
	defer func() { version = oldVersion }()
	version = "v1.0.0"

	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v2.0.0", nil
	}

	var stdout, stderr bytes.Buffer
	updateCh, _ := run(nil, &stdout, &stderr)
	showUpdateNotice(&stderr, updateCh, updateSuccessTimeout)
	if !strings.Contains(stderr.String(), "↑ v2.0.0 available") {
		t.Fatalf("expected upgrade notice on stderr, got %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "gh extension upgrade gh-x") {
		t.Fatalf("expected upgrade command on stderr, got %q", stderr.String())
	}
}

func TestNoUpgradeNoticeWhenCurrent(t *testing.T) {
	oldVersion := version
	defer func() { version = oldVersion }()
	version = "v1.0.0"

	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v1.0.0", nil
	}

	var stdout, stderr bytes.Buffer
	updateCh, _ := run(nil, &stdout, &stderr)
	// Drain the channel so the async goroutine completes before deferred
	// cleanup restores fetchLatestReleaseFunc (prevents data race).
	if updateCh != nil {
		for range updateCh {
		}
	}
	if strings.Contains(stderr.String(), "available") {
		t.Fatalf("should not show upgrade notice when up-to-date, got %q", stderr.String())
	}
}

func TestNoUpgradeNoticeWhenLocalBuildIsNewer(t *testing.T) {
	oldVersion := version
	defer func() { version = oldVersion }()
	version = "v0.2.3"

	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v0.2.2", nil
	}

	var stdout, stderr bytes.Buffer
	updateCh, _ := run(nil, &stdout, &stderr)
	showUpdateNotice(&stderr, updateCh, updateSuccessTimeout)
	if strings.Contains(stderr.String(), "available") {
		t.Fatalf("older release must not be advertised as an upgrade, got %q", stderr.String())
	}
}

func TestNoUpgradeNoticeOnVersionCmd(t *testing.T) {
	oldVersion := version
	defer func() { version = oldVersion }()
	version = "v1.0.0"

	apiCalls := 0
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		apiCalls++
		return "v2.0.0", nil
	}

	var stdout, stderr bytes.Buffer
	_, _ = run([]string{"version"}, &stdout, &stderr)
	if apiCalls != 1 {
		t.Fatalf("expected 1 API call (from version cmd only), got %d", apiCalls)
	}
}

func TestShowUpdateNotice(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "v2.0.0"
	close(ch)

	var buf bytes.Buffer
	showUpdateNotice(&buf, ch, 500*time.Millisecond)
	if !strings.Contains(buf.String(), "↑ v2.0.0 available") {
		t.Fatalf("expected upgrade notice, got %q", buf.String())
	}
}

func TestShowUpdateNoticeNil(t *testing.T) {
	var buf bytes.Buffer
	showUpdateNotice(&buf, nil, 500*time.Millisecond)
	if buf.Len() != 0 {
		t.Fatalf("expected no output for nil channel, got %q", buf.String())
	}
}

func TestShowUpdateNoticeTimeout(t *testing.T) {
	// Channel that never receives — simulates a slow API call.
	ch := make(chan string, 1)
	var buf bytes.Buffer
	start := time.Now()
	showUpdateNotice(&buf, ch, 10*time.Millisecond)
	elapsed := time.Since(start)
	if buf.Len() != 0 {
		t.Fatalf("expected no output on timeout, got %q", buf.String())
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("expected short timeout, took %v", elapsed)
	}
}

func TestRunErrorGetsLongerUpdateTimeout(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()

	oldVersion := version
	defer func() { version = oldVersion }()

	// Override timeouts to small values so the test runs fast.
	oldSuccess, oldError := updateSuccessTimeout, updateErrorTimeout
	defer func() { updateSuccessTimeout, updateErrorTimeout = oldSuccess, oldError }()
	updateSuccessTimeout = 10 * time.Millisecond
	updateErrorTimeout = 200 * time.Millisecond

	// Use a gate channel to make timing deterministic: the fetch blocks until
	// we release it, which we do after the success timeout has certainly elapsed.
	gate := make(chan struct{})
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		<-gate
		return "v99.0.0", nil
	}
	version = "v1.0.0"

	var stdout, stderr bytes.Buffer
	updateCh, err := run([]string{"bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for bogus command")
	}

	// Release the fetch after the success timeout has elapsed but well within
	// the error timeout window. Derived from updateSuccessTimeout to stay
	// resilient if the constants change.
	time.Sleep(2 * updateSuccessTimeout)
	close(gate)

	showUpdateNotice(&stderr, updateCh, updateErrorTimeout)
	if !strings.Contains(stderr.String(), "v99.0.0 available") {
		t.Fatalf("expected update notice in error output, got %q", stderr.String())
	}
}

func TestRunVersionRouting(t *testing.T) {
	orig := fetchLatestReleaseFunc
	defer func() { fetchLatestReleaseFunc = orig }()
	fetchLatestReleaseFunc = func(owner, repo string) (string, error) {
		return "v1.0.0", nil
	}

	for _, arg := range []string{"version", "--version", "-v"} {
		var buf bytes.Buffer
		_, err := run([]string{arg}, &buf, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("run(%q) returned error: %v", arg, err)
		}
		if !strings.Contains(buf.String(), "gh-x") {
			t.Fatalf("run(%q) missing version output: %q", arg, buf.String())
		}
	}
}
