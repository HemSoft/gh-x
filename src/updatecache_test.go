package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAutomaticUpdateCache(t *testing.T) {
	tests := []struct {
		name, content string
		age           time.Duration
		hit           bool
	}{
		{name: "fresh", content: `{"latest":"v1.2.3"}`, age: time.Minute, hit: true},
		{name: "expired", content: `{"latest":"v1.2.3"}`, age: time.Hour},
		{name: "future", content: `{"latest":"v1.2.3"}`, age: -time.Second},
		{name: "invalid tag", content: `{"latest":"garbage"}`},
		{name: "corrupt", content: `{`},
		{name: "missing"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "entry.json")
			now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
			if test.content != "" {
				data := []byte(test.content)
				if test.content != `{` {
					data = []byte(`{"latest":"` + map[bool]string{true: "garbage", false: "v1.2.3"}[test.name == "invalid tag"] + `","fetchedAt":"` + now.Add(-test.age).Format(time.RFC3339Nano) + `"}`)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, hit := loadUpdateCache(path, now)
			if hit != test.hit {
				t.Fatalf("hit=%v, want %v", hit, test.hit)
			}
		})
	}
}

func TestAutomaticUpdateFetchAndIdentity(t *testing.T) {
	savedPath, savedNow, savedFetch := updateCachePathFunc, updateNowFunc, fetchLatestReleaseFunc
	t.Cleanup(func() { updateCachePathFunc, updateNowFunc, fetchLatestReleaseFunc = savedPath, savedNow, savedFetch })
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	updateNowFunc = func() time.Time { return now }
	path := filepath.Join(t.TempDir(), "first", "entry.json")
	updateCachePathFunc = func() string { return path }
	calls := 0
	fetchLatestReleaseFunc = func(string, string) (string, error) { calls++; return "v2.3.4", nil }
	for i := 0; i < 2; i++ {
		if tag, err := fetchAutomaticUpdate(); err != nil || tag != "v2.3.4" {
			t.Fatalf("tag=%q,err=%v", tag, err)
		}
	}
	if calls != 1 {
		t.Fatalf("warm cache fetched %d times", calls)
	}
	now = now.Add(time.Hour)
	if _, err := fetchAutomaticUpdate(); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expiry fetched %d times", calls)
	}
	oldPath := path
	path = filepath.Join(t.TempDir(), "second.json")
	fetchLatestReleaseFunc = func(string, string) (string, error) { path = oldPath; return "v3.4.5", nil }
	if _, err := fetchAutomaticUpdate(); err != nil {
		t.Fatal(err)
	}
	if tag, _ := loadUpdateCache(oldPath, now); tag != "v2.3.4" {
		t.Fatal("identity switch wrote under another identity")
	}
	path = ""
	fetchLatestReleaseFunc = func(string, string) (string, error) { return "", errors.New("offline") }
	if _, err := fetchAutomaticUpdate(); err == nil {
		t.Fatal("fetch error hidden")
	}
}

func TestUpdateCachePathIsolation(t *testing.T) {
	isolateStatusCacheAuthentication(t)
	t.Setenv("GH_X_CACHE_DIR", t.TempDir())
	t.Setenv("GH_TOKEN", "token-a")
	first := updateCachePath()
	t.Setenv("GH_TOKEN", "token-b")
	second := updateCachePath()
	if first == "" || second == "" || first == second {
		t.Fatal("update cache not isolated by token")
	}
	if err := os.WriteFile(filepath.Join(statusCLIConfigDir(), "hosts.yml"), []byte("invalid: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if path := updateCachePath(); path != "" {
		t.Fatal("unknown identity allowed update cache")
	}
}
