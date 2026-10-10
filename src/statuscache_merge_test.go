package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func statusMergeFixture(at time.Time, label string) statusCacheEntry {
	sections := make([]statusCacheSection, 4)
	for i := range sections {
		sections[i] = statusCacheSection{FetchedAt: at, RetryAfter: at.Add(time.Minute)}
	}
	return statusCacheEntry{Version: statusCacheSchemaVersion, FetchedAt: at,
		Key: statusCacheKey{RemoteFingerprint: "fixture", MergedLimit: 5}, Repository: "owner/repo", DefaultBranch: "main",
		Sections: sections, Issues: []statusCachedIssue{{Display: displayIssue{Title: label}}},
		PullRequests: []statusCachedPullRequest{{Display: displayPullRequest{Title: label}}}, PullRequestHeads: []string{label},
		PullRequestsKnown: true, ShowMergedPullRequests: true,
		MergedPullRequests: []statusCachedPullRequest{{Display: displayPullRequest{Title: label}}},
		WorkflowRuns:       []statusCachedWorkflowRun{{Display: displayWorkflowRun{Workflow: label}}}, WorkflowRunsPerfect: true}
}

func TestStatusCachePublicationKeepsNewestSection(t *testing.T) {
	tests := []struct {
		name            string
		section         int
		previousFailure bool
		expired         bool
	}{{"newer issues success", 0, false, false}, {"newer issues failure", 0, true, false},
		{"expired newer success", 0, false, true}, {"open PR metadata", 1, true, false},
		{"merged PR metadata", 2, true, false}, {"workflow metadata", 3, true, false}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer saveStatusFuncs()()
			oldTime := time.Unix(1000, 0)
			newTime := oldTime.Add(time.Second)
			writeTime := newTime.Add(2 * time.Second)
			if test.expired {
				writeTime = newTime.Add(2 * time.Minute)
			}
			statusNowFunc = func() time.Time { return writeTime }
			incoming := statusMergeFixture(oldTime, "older")
			previous := statusMergeFixture(oldTime.Add(-time.Second), "persisted")
			previous.Sections[test.section] = statusCacheSection{FetchedAt: newTime, RetryAfter: newTime.Add(time.Minute)}
			previous.PullRequestsKnown = false
			previous.WorkflowRunsPerfect = false
			if test.previousFailure {
				previous.Failures = make([]string, statusFailureCount)
				previous.Failures[test.section*3] = "newer rate limit failure"
				previous.Sections[test.section].RetryAfter = newTime.Add(time.Hour)
			} else {
				incoming.Failures = make([]string, statusFailureCount)
				incoming.Failures[test.section*3] = "older rate limit failure"
				incoming.Sections[test.section].RetryAfter = oldTime.Add(time.Hour)
			}
			directory := t.TempDir()
			if err := writeStatusCacheEntry(directory, previous, oldTime); err != nil {
				t.Fatal(err)
			}
			if err := writeStatusCacheEntry(directory, incoming, oldTime); err != nil {
				t.Fatal(err)
			}
			got, err := readStatusCacheEntry(filepath.Join(directory, statusCacheFileName(incoming.Key)))
			if err != nil {
				t.Fatal(err)
			}
			expected := incoming
			expected.Sections = append([]statusCacheSection(nil), incoming.Sections...)
			copyStatusCacheSection(&expected, previous, test.section)
			expected.Failures = previous.Failures
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("publication lost newest section: got=%+v want=%+v", got, expected)
			}
		})
	}
}

func TestStatusCacheMergeRejectsInvalidPrevious(t *testing.T) {
	now := time.Unix(1000, 0)
	tests := []struct {
		name   string
		change func(*statusCacheEntry)
	}{
		{"wrong key", func(e *statusCacheEntry) { e.Key.RemoteFingerprint = "other" }},
		{"future snapshot", func(e *statusCacheEntry) { e.FetchedAt = now.Add(time.Second) }},
		{"invalid errors", func(e *statusCacheEntry) { e.Failures = []string{"bad"} }},
		{"future section", func(e *statusCacheEntry) { e.Sections[0].FetchedAt = now.Add(time.Second) }},
		{"legacy incoming", func(e *statusCacheEntry) { e.Sections = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			incoming := statusMergeFixture(now.Add(-time.Second), "incoming")
			previous := statusMergeFixture(now, "previous")
			if test.name == "legacy incoming" {
				test.change(&incoming)
			} else {
				test.change(&previous)
			}
			if got := mergeStatusCacheSections(incoming, previous, now); !reflect.DeepEqual(got, incoming) {
				t.Fatalf("invalid cache participated: %+v", got)
			}
		})
	}
}

func TestStatusCachePublicationSkipsContendedLock(t *testing.T) {
	directory := t.TempDir()
	lock := flock.New(filepath.Join(directory, ".status-cache.lock"), flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		t.Fatalf("lock=%t, err=%v", locked, err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	entry := statusMergeFixture(time.Unix(1000, 0), "incoming")
	if err := writeStatusCacheEntry(directory, entry, entry.FetchedAt); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, statusCacheFileName(entry.Key))
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("contended writer published: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writeStatusCacheEntry(directory, entry, entry.FetchedAt); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !json.Valid(data) {
		t.Fatalf("unlocked writer failed: %v", err)
	}
}

func TestStatusCacheMergeSnapshotTime(t *testing.T) {
	now := time.Unix(1000, 0)
	incoming := statusMergeFixture(now.Add(-time.Second), "incoming")
	previous := statusMergeFixture(now, "newest")
	got := mergeStatusCacheSections(incoming, previous, now)
	if !reflect.DeepEqual(got, previous) {
		t.Fatalf("newest snapshot not preserved: %+v", got)
	}
	if got := mergeStatusCacheSections(previous, incoming, now); !reflect.DeepEqual(got, previous) {
		t.Fatalf("older snapshot replaced newest: %+v", got)
	}
	incoming.FetchedAt = now
	incoming.Sections = append([]statusCacheSection(nil), previous.Sections...)
	if got := mergeStatusCacheSections(incoming, previous, now); !reflect.DeepEqual(got, incoming) {
		t.Fatalf("equal-clock refresh did not replace: %+v", got)
	}
}
