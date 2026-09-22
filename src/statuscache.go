package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	statusCacheSchemaVersion = 1
	statusCacheTTL           = time.Minute
	statusCacheDirectoryName = "status-cache-v1"
)

type statusCacheKey struct {
	RemoteFingerprint string `json:"remoteFingerprint"`
	MergedLimit       int    `json:"mergedLimit"`
	ColorEnabled      bool   `json:"colorEnabled"`
}

type statusCachedIssue struct {
	Display         displayIssue      `json:"display"`
	PullRequestRefs []linkedReference `json:"pullRequestRefs,omitempty"`
	ParentRefs      []linkedReference `json:"parentRefs,omitempty"`
}

type statusCachedPullRequest struct {
	Display          displayPullRequest `json:"display"`
	ChecksDowngraded bool               `json:"checksDowngraded,omitempty"`
	IssueRefs        []linkedReference  `json:"issueRefs,omitempty"`
	UpdatedAt        time.Time          `json:"updatedAt"`
	MergedAt         time.Time          `json:"mergedAt"`
}

type statusCacheEntry struct {
	Version                int                       `json:"version"`
	FetchedAt              time.Time                 `json:"fetchedAt"`
	Key                    statusCacheKey            `json:"key"`
	Repository             string                    `json:"repository"`
	RepositoryURL          string                    `json:"repositoryUrl,omitempty"`
	Issues                 []statusCachedIssue       `json:"issues"`
	PullRequests           []statusCachedPullRequest `json:"pullRequests"`
	PullRequestHeads       []string                  `json:"pullRequestHeads"`
	PullRequestsKnown      bool                      `json:"pullRequestsKnown"`
	ShowMergedPullRequests bool                      `json:"showMergedPullRequests"`
	MergedPullRequests     []statusCachedPullRequest `json:"mergedPullRequests"`
	WorkflowRuns           []displayWorkflowRun      `json:"workflowRuns"`
	WorkflowRunsPerfect    bool                      `json:"workflowRunsPerfect"`
}

var statusCacheDirectoryFunc = statusCacheDirectory

func loadStatusCache(options statusOptions, colorEnabled bool, now time.Time) (statusCacheEntry, bool) {
	directory, remoteFingerprint, err := statusCacheDirectoryFunc()
	if err != nil {
		return statusCacheEntry{}, false
	}
	matches := statusCacheKey{RemoteFingerprint: remoteFingerprint, MergedLimit: options.mergedLimit, ColorEnabled: colorEnabled}
	paths, err := filepath.Glob(filepath.Join(directory, "status-*.json"))
	if err != nil {
		return statusCacheEntry{}, false
	}
	var newest statusCacheEntry
	found := false
	for _, path := range paths {
		entry, readErr := readStatusCacheEntry(path)
		if readErr == nil && validStatusCacheEntry(entry, matches, now) && (!found || entry.FetchedAt.After(newest.FetchedAt)) {
			newest = entry
			found = true
		}
	}
	return newest, found
}

func readStatusCacheEntry(path string) (statusCacheEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return statusCacheEntry{}, err
	}
	var entry statusCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return statusCacheEntry{}, err
	}
	return entry, nil
}

func validStatusCacheEntry(entry statusCacheEntry, expected statusCacheKey, now time.Time) bool {
	if entry.Version != statusCacheSchemaVersion || entry.Key != expected || entry.FetchedAt.IsZero() ||
		entry.Repository == "" || entry.Issues == nil || entry.PullRequests == nil ||
		entry.PullRequestHeads == nil || entry.WorkflowRuns == nil ||
		entry.ShowMergedPullRequests != (expected.MergedLimit > 0) ||
		(entry.ShowMergedPullRequests && entry.MergedPullRequests == nil) {
		return false
	}
	age := now.Sub(entry.FetchedAt)
	return age >= 0 && age < statusCacheTTL
}

func saveStatusCache(options statusOptions, colorEnabled bool, now time.Time, dashboard statusDashboard, pullRequestHeads map[string]bool, pullRequestsKnown bool) {
	if !statusDashboardCacheable(dashboard) {
		return
	}
	directory, remoteFingerprint, err := statusCacheDirectoryFunc()
	if err != nil {
		return
	}
	entry := statusCacheEntry{
		Version:                statusCacheSchemaVersion,
		FetchedAt:              now,
		Key:                    statusCacheKey{RemoteFingerprint: remoteFingerprint, MergedLimit: options.mergedLimit, ColorEnabled: colorEnabled},
		Repository:             dashboard.Repository,
		RepositoryURL:          dashboard.RepositoryURL,
		Issues:                 cacheStatusIssues(dashboard.Issues),
		PullRequests:           cacheStatusPullRequests(dashboard.PullRequests),
		PullRequestHeads:       sortedStatusMapKeys(pullRequestHeads),
		PullRequestsKnown:      pullRequestsKnown,
		ShowMergedPullRequests: dashboard.ShowMergedPullRequests,
		MergedPullRequests:     cacheStatusPullRequests(dashboard.MergedPullRequests),
		WorkflowRuns:           append([]displayWorkflowRun{}, dashboard.WorkflowRuns...),
		WorkflowRunsPerfect:    dashboard.WorkflowRunsPerfect,
	}
	_ = writeStatusCacheEntry(directory, entry, now)
}

func statusDashboardCacheable(dashboard statusDashboard) bool {
	errorsToCheck := []error{
		dashboard.IssuesErr,
		dashboard.IssuesRelErr,
		dashboard.IssuesHierarchyErr,
		dashboard.PullRequestsErr,
		dashboard.PullRequestsSuppErr,
		dashboard.RequiredChecksErr,
		dashboard.MergedPullRequestsErr,
		dashboard.MergedPullRequestsSuppErr,
		dashboard.MergedRequiredChecksErr,
		dashboard.WorkflowRunsErr,
	}
	for _, err := range errorsToCheck {
		if err != nil {
			return false
		}
	}
	return true
}

func writeStatusCacheEntry(directory string, entry statusCacheEntry, now time.Time) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(directory, 0o700); err != nil && !errors.Is(err, os.ErrPermission) {
		return err
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".status-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	finalName := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(temporaryPath), ".tmp"), ".") + ".json"
	finalPath := filepath.Join(directory, finalName)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return err
	}
	removeTemporary = false
	pruneStatusCacheFiles(directory, finalPath, now)
	return nil
}

func pruneStatusCacheFiles(directory, currentPath string, now time.Time) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		if entry.IsDir() || path == currentPath || (!strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".tmp")) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && now.Sub(info.ModTime()) >= 2*statusCacheTTL {
			_ = os.Remove(path)
		}
	}
}

func statusCacheDirectory() (string, string, error) {
	commonDirectory, err := statusCommandFunc("git", "rev-parse", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("git common directory: %w", err)
	}
	commonDirectory = strings.TrimSpace(commonDirectory)
	if commonDirectory == "" {
		return "", "", errors.New("git common directory is empty")
	}
	if !filepath.IsAbs(commonDirectory) {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		commonDirectory = filepath.Join(workingDirectory, commonDirectory)
	}
	remoteURL, err := statusCommandFunc("git", "remote", "get-url", "origin")
	if err != nil {
		return "", "", fmt.Errorf("git origin URL: %w", err)
	}
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return "", "", errors.New("git origin URL is empty")
	}
	return filepath.Join(filepath.Clean(commonDirectory), "gh-x", statusCacheDirectoryName), statusRemoteFingerprint(remoteURL), nil
}

func statusRemoteFingerprint(remoteURL string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(remoteURL)))
}

func cacheStatusIssues(issues []displayIssue) []statusCachedIssue {
	cached := make([]statusCachedIssue, len(issues))
	for index, issue := range issues {
		cached[index] = statusCachedIssue{
			Display:         issue,
			PullRequestRefs: issue.pullRequestRefs,
			ParentRefs:      issue.parentRefs,
		}
	}
	return cached
}

func restoreStatusIssues(cached []statusCachedIssue) []displayIssue {
	issues := make([]displayIssue, len(cached))
	for index, issue := range cached {
		issues[index] = issue.Display
		issues[index].pullRequestRefs = issue.PullRequestRefs
		issues[index].parentRefs = issue.ParentRefs
	}
	return issues
}

func cacheStatusPullRequests(pullRequests []displayPullRequest) []statusCachedPullRequest {
	cached := make([]statusCachedPullRequest, len(pullRequests))
	for index, pullRequest := range pullRequests {
		cached[index] = statusCachedPullRequest{
			Display:          pullRequest,
			ChecksDowngraded: pullRequest.checksDowngraded,
			IssueRefs:        pullRequest.issueRefs,
			UpdatedAt:        pullRequest.updatedAt,
			MergedAt:         pullRequest.mergedAt,
		}
	}
	return cached
}

func restoreStatusPullRequests(cached []statusCachedPullRequest) []displayPullRequest {
	pullRequests := make([]displayPullRequest, len(cached))
	for index, pullRequest := range cached {
		pullRequests[index] = pullRequest.Display
		pullRequests[index].checksDowngraded = pullRequest.ChecksDowngraded
		pullRequests[index].issueRefs = pullRequest.IssueRefs
		pullRequests[index].updatedAt = pullRequest.UpdatedAt
		pullRequests[index].mergedAt = pullRequest.MergedAt
	}
	return pullRequests
}

func sortedStatusMapKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key, included := range values {
		if included {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func statusStringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return set
}
