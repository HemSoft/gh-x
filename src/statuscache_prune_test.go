package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStatusCachePruningPreservesExtendedCooldown(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		prepare  func(*statusCacheEntry)
		corrupt  bool
		retained bool
	}{
		{name: "retry-after deadline", prepare: func(e *statusCacheEntry) {
			e.Failures = make([]string, statusFailureCount)
			e.Failures[0] = "rate limit; Retry-After: 600"
			e.Sections[0].RetryAfter = e.Sections[0].FetchedAt.Add(10 * time.Minute)
		}, retained: true},
		{name: "reset deadline", prepare: func(e *statusCacheEntry) {
			e.Failures = make([]string, statusFailureCount)
			e.Failures[9] = "rate limit reset"
			e.Sections[3].RetryAfter = now.Add(time.Minute)
		}, retained: true},
		{name: "expired snapshot"},
		{name: "deadline boundary", prepare: func(e *statusCacheEntry) { e.Sections[0].RetryAfter = now }},
		{name: "corrupt snapshot", corrupt: true},
		{name: "future snapshot", prepare: func(e *statusCacheEntry) { e.FetchedAt = now.Add(time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			previous := statusMergeFixture(now.Add(-3*time.Minute), "old")
			if test.prepare != nil {
				test.prepare(&previous)
			}
			data, err := json.Marshal(previous)
			if err != nil {
				t.Fatal(err)
			}
			if test.corrupt {
				data = []byte("corrupt")
			}
			oldPath := filepath.Join(directory, statusCacheFileName(previous.Key))
			if err := os.WriteFile(oldPath, data, 0600); err != nil {
				t.Fatal(err)
			}
			age := now.Add(-3 * time.Minute)
			if err := os.Chtimes(oldPath, age, age); err != nil {
				t.Fatal(err)
			}
			current := statusMergeFixture(now, "current")
			current.Key.MergedLimit = 10
			current.Key.ColorEnabled = true
			if err := writeStatusCacheEntry(directory, current, now); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(oldPath)
			if test.retained {
				if err != nil {
					t.Fatalf("option/color write discarded cooldown: %v", err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("expired snapshot retained: %v", err)
			}
			if test.retained {
				cached, ok := loadStatusCacheAt(statusOptions{mergedLimit: 5}, false, now, directory, "fixture")
				if !ok || cached.Failures == nil {
					t.Fatalf("cooldown not reusable: %+v, %t", cached, ok)
				}
			}
		})
	}
}
