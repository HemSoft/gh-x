package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStatusCacheEntryValidation(t *testing.T) {
	now := time.Date(2026, 9, 21, 3, 30, 0, 0, time.UTC)
	key := statusCacheKey{RemoteFingerprint: statusRemoteFingerprint("https://github.com/owner/repo.git"), MergedLimit: 5, ColorEnabled: true}
	complete := func(at time.Time, cacheKey statusCacheKey, version int) statusCacheEntry {
		return statusCacheEntry{
			Version: version, FetchedAt: at, Key: cacheKey, Repository: "owner/repo",
			Issues: []statusCachedIssue{}, PullRequests: []statusCachedPullRequest{},
			PullRequestHeads: []string{}, WorkflowRuns: []displayWorkflowRun{},
			ShowMergedPullRequests: true, MergedPullRequests: []statusCachedPullRequest{},
		}
	}
	tests := []struct {
		name    string
		entry   statusCacheEntry
		expects bool
	}{
		{name: "fresh", entry: complete(now.Add(-59*time.Second), key, statusCacheSchemaVersion), expects: true},
		{name: "expiry boundary", entry: complete(now.Add(-statusCacheTTL), key, statusCacheSchemaVersion)},
		{name: "future", entry: complete(now.Add(time.Second), key, statusCacheSchemaVersion)},
		{name: "wrong schema", entry: complete(now, key, statusCacheSchemaVersion+1)},
		{name: "wrong remote", entry: complete(now, statusCacheKey{RemoteFingerprint: statusRemoteFingerprint("https://github.com/owner/other.git"), MergedLimit: 5, ColorEnabled: true}, statusCacheSchemaVersion)},
		{name: "wrong merged limit", entry: complete(now, statusCacheKey{RemoteFingerprint: key.RemoteFingerprint, MergedLimit: 2, ColorEnabled: true}, statusCacheSchemaVersion)},
		{name: "wrong color mode", entry: complete(now, statusCacheKey{RemoteFingerprint: key.RemoteFingerprint, MergedLimit: 5}, statusCacheSchemaVersion)},
		{name: "incomplete cache", entry: statusCacheEntry{Version: statusCacheSchemaVersion, FetchedAt: now, Key: key}},
		{name: "missing workflows", entry: func() statusCacheEntry {
			entry := complete(now, key, statusCacheSchemaVersion)
			entry.WorkflowRuns = nil
			return entry
		}()},
		{name: "missing merged rows", entry: func() statusCacheEntry {
			entry := complete(now, key, statusCacheSchemaVersion)
			entry.MergedPullRequests = nil
			return entry
		}()},
		{name: "wrong merged visibility", entry: func() statusCacheEntry {
			entry := complete(now, key, statusCacheSchemaVersion)
			entry.ShowMergedPullRequests = false
			return entry
		}()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validStatusCacheEntry(test.entry, key, now); got != test.expects {
				t.Fatalf("validStatusCacheEntry() = %v, want %v", got, test.expects)
			}
		})
	}
}

func TestStatusCacheRoundTripPreservesRenderedMetadata(t *testing.T) {
	directory := t.TempDir()
	remoteURL := "git@github.com:owner/repo.git"
	useStatusCacheDirectory(t, directory, remoteURL)
	now := time.Date(2026, 9, 21, 3, 30, 0, 0, time.UTC)
	issueRef := linkedReference{Number: 42, URL: "https://github.com/owner/repo/pull/42"}
	parentRef := linkedReference{Number: 7, URL: "https://github.com/owner/repo/issues/7"}
	pullRequestRef := linkedReference{Number: 9, URL: "https://github.com/owner/repo/issues/9"}
	clean := true
	dashboard := statusDashboard{
		Repository:    "owner/repo",
		RepositoryURL: "https://github.com/owner/repo",
		Issues: []displayIssue{{
			Number: 8, PullRequests: "#42", Parent: "#7", Title: "Cached issue",
			pullRequestRefs: []linkedReference{issueRef}, parentRefs: []linkedReference{parentRef},
		}},
		PullRequests: []displayPullRequest{{
			Number: 42, Issues: "#9", Title: "Cached PR", AIClean: &clean,
			checksDowngraded: true, issueRefs: []linkedReference{pullRequestRef}, updatedAt: now.Add(-time.Hour),
		}},
		ShowMergedPullRequests: true,
		MergedPullRequests: []displayPullRequest{{
			Number: 41, Title: "Merged PR", mergedAt: now.Add(-2 * time.Hour),
		}},
		WorkflowRuns:        []displayWorkflowRun{{ID: "100", Title: "CI"}},
		WorkflowRunsPerfect: true,
	}
	options := statusOptions{mergedLimit: 5}
	saveStatusCache(options, true, now, dashboard, map[string]bool{"feature/cache": true}, true)

	cached, ok := loadStatusCache(options, true, now.Add(10*time.Second))
	if !ok {
		t.Fatal("fresh cache was not loaded")
	}
	var restored statusDashboard
	heads, known := applyStatusCache(&restored, cached)
	if !known || !heads["feature/cache"] {
		t.Fatalf("cached pull request metadata = %#v, known=%v", heads, known)
	}
	if !reflect.DeepEqual(restored.Issues[0].pullRequestRefs, []linkedReference{issueRef}) || !reflect.DeepEqual(restored.Issues[0].parentRefs, []linkedReference{parentRef}) {
		t.Fatalf("issue references were not restored: %#v", restored.Issues[0])
	}
	if !restored.PullRequests[0].checksDowngraded || !reflect.DeepEqual(restored.PullRequests[0].issueRefs, []linkedReference{pullRequestRef}) || !restored.PullRequests[0].updatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("pull request metadata was not restored: %#v", restored.PullRequests[0])
	}
	if !restored.MergedPullRequests[0].mergedAt.Equal(now.Add(-2*time.Hour)) || !restored.WorkflowRunsPerfect {
		t.Fatalf("merged or workflow metadata was not restored: %#v", restored)
	}
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0].Name(), ".json") {
		t.Fatalf("cache write was not a single finalized snapshot: %#v", files)
	}
	data, err := os.ReadFile(filepath.Join(directory, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "authorization", "header"} {
		if strings.Contains(strings.ToLower(string(data)), forbidden) {
			t.Fatalf("cache contains forbidden credential field %q", forbidden)
		}
	}
}

func TestLoadStatusCacheIgnoresCorruptAndIncompatibleFiles(t *testing.T) {
	directory := t.TempDir()
	remoteURL := "https://github.com/owner/repo.git"
	useStatusCacheDirectory(t, directory, remoteURL)
	now := time.Date(2026, 9, 21, 3, 30, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(directory, "status-corrupt.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	incompatible := statusCacheEntry{
		Version:   statusCacheSchemaVersion + 1,
		FetchedAt: now,
		Key:       statusCacheKey{RemoteFingerprint: statusRemoteFingerprint(remoteURL), MergedLimit: 5},
	}
	if err := writeStatusCacheEntry(directory, incompatible, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := loadStatusCache(statusOptions{mergedLimit: 5}, false, now); ok {
		t.Fatal("corrupt or incompatible cache must be ignored")
	}
}

func TestSaveStatusCacheSkipsFailedSnapshots(t *testing.T) {
	directory := t.TempDir()
	useStatusCacheDirectory(t, directory, "https://github.com/owner/repo.git")
	dashboard := statusDashboard{IssuesErr: errors.New("offline")}
	saveStatusCache(statusOptions{mergedLimit: 5}, false, time.Now().UTC(), dashboard, nil, false)
	files, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("failed status snapshot wrote cache files: %#v", files)
	}
}

func TestLoadStatusCacheKeepsRepositoriesAndOptionsIsolated(t *testing.T) {
	now := time.Date(2026, 9, 21, 3, 30, 0, 0, time.UTC)
	options := statusOptions{mergedLimit: 5}
	firstDirectory := t.TempDir()
	useStatusCacheDirectory(t, firstDirectory, "https://github.com/owner/first.git")
	saveStatusCache(options, false, now, statusDashboard{Repository: "owner/first", ShowMergedPullRequests: true}, nil, true)

	if cached, ok := loadStatusCache(options, false, now); !ok || cached.Repository != "owner/first" {
		t.Fatalf("first repository cache = %#v, hit=%v", cached, ok)
	}
	statusCacheDirectoryFunc = func() (string, string, error) {
		return firstDirectory, statusRemoteFingerprint("https://github.com/owner/second.git"), nil
	}
	if _, ok := loadStatusCache(options, false, now); ok {
		t.Fatal("different remote reused the first repository cache")
	}
	statusCacheDirectoryFunc = func() (string, string, error) {
		return t.TempDir(), statusRemoteFingerprint("https://github.com/owner/first.git"), nil
	}
	if _, ok := loadStatusCache(options, false, now); ok {
		t.Fatal("different repository directory reused the first cache")
	}
	statusCacheDirectoryFunc = func() (string, string, error) {
		return firstDirectory, statusRemoteFingerprint("https://github.com/owner/first.git"), nil
	}
	if _, ok := loadStatusCache(statusOptions{mergedLimit: 2}, false, now); ok {
		t.Fatal("different merged limit reused an incompatible cache")
	}
}

func useStatusCacheDirectory(t *testing.T, directory, remoteURL string) {
	t.Helper()
	saved := statusCacheDirectoryFunc
	statusCacheDirectoryFunc = func() (string, string, error) {
		return directory, statusRemoteFingerprint(remoteURL), nil
	}
	t.Cleanup(func() {
		statusCacheDirectoryFunc = saved
	})
}
