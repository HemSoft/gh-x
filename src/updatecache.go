package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	ghauth "github.com/cli/go-gh/v2/pkg/auth"
)

const automaticUpdateTTL = time.Hour

type updateCacheEntry struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Latest    string    `json:"latest"`
}

var updateCachePathFunc = updateCachePath
var updateNowFunc = time.Now

func updateCachePath() string {
	directory := os.Getenv("GH_X_CACHE_DIR")
	if directory == "" {
		var err error
		directory, err = os.UserCacheDir()
		if err != nil {
			return ""
		}
	}
	auth, err := statusAuthContext()
	if err != nil {
		return ""
	}
	apiHost, _ := ghauth.DefaultHost()
	// gh api uses the CLI default host; access-error retries select accounts
	// through the repository-derived target. Keep both contexts in the identity.
	identity := statusTargetFingerprint(repoOwner+"/"+repoName, apiHost+"\x00"+targetHost(nil), auth)
	return filepath.Join(directory, "gh-x", "updates-v1", identity+".json")
}

func fetchAutomaticUpdate() (string, error) {
	path := updateCachePathFunc()
	now := updateNowFunc()
	if latest, ok := loadUpdateCache(path, now); ok {
		return latest, nil
	}
	latest, err := fetchLatestReleaseFunc(repoOwner, repoName)
	if err == nil && validUpdateTag(latest) && path != "" {
		// A concurrent account/config change must not publish under the old identity.
		if path == updateCachePathFunc() {
			_ = saveUpdateCache(path, updateCacheEntry{FetchedAt: now, Latest: latest})
		}
	}
	return latest, err
}

func loadUpdateCache(path string, now time.Time) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var entry updateCacheEntry
	if json.Unmarshal(data, &entry) != nil || !validUpdateTag(entry.Latest) {
		return "", false
	}
	age := now.Sub(entry.FetchedAt)
	return entry.Latest, !entry.FetchedAt.IsZero() && age >= 0 && age < automaticUpdateTTL
}

func validUpdateTag(tag string) bool {
	_, ok := parseSemanticVersion(strings.TrimPrefix(tag, "v"))
	return ok
}

func saveUpdateCache(path string, entry updateCacheEntry) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	temporary, err := writeStatusCacheTemp(directory, data)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary) }()
	return os.Rename(temporary, path)
}
