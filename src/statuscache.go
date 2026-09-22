package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"gopkg.in/yaml.v3"
)

const (
	statusCacheSchemaVersion = 3
	statusCacheTTL           = time.Minute
	statusCacheDirectoryName = "status-cache-v3"
)

type statusCacheKey struct {
	RemoteFingerprint string `json:"remoteFingerprint"`
	MergedLimit       int    `json:"mergedLimit"`
	ColorEnabled      bool   `json:"colorEnabled"`
}

type statusCachedReference struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Text   string `json:"text,omitempty"`
}

type statusCachedIssue struct {
	Display         displayIssue            `json:"display"`
	PullRequestRefs []statusCachedReference `json:"pullRequestRefs,omitempty"`
	ParentRefs      []statusCachedReference `json:"parentRefs,omitempty"`
}

type statusCachedPullRequest struct {
	Display          displayPullRequest      `json:"display"`
	ChecksDowngraded bool                    `json:"checksDowngraded,omitempty"`
	IssueRefs        []statusCachedReference `json:"issueRefs,omitempty"`
	UpdatedAt        time.Time               `json:"updatedAt"`
	MergedAt         time.Time               `json:"mergedAt"`
}

type statusCacheEntry struct {
	Version                int                       `json:"version"`
	FetchedAt              time.Time                 `json:"fetchedAt"`
	Key                    statusCacheKey            `json:"key"`
	Repository             string                    `json:"repository"`
	RepositoryURL          string                    `json:"repositoryUrl,omitempty"`
	DefaultBranch          string                    `json:"defaultBranch"`
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
	files, err := os.ReadDir(directory)
	if err != nil {
		return statusCacheEntry{}, false
	}
	var newest statusCacheEntry
	found := false
	for _, file := range files {
		if !isStatusCacheFile(file) {
			continue
		}
		entry, readErr := readStatusCacheEntry(filepath.Join(directory, file.Name()))
		if readErr == nil && validStatusCacheEntry(entry, matches, now) && (!found || entry.FetchedAt.After(newest.FetchedAt)) {
			newest = entry
			found = true
		}
	}
	return newest, found
}

func isStatusCacheFile(file os.DirEntry) bool {
	return file.Type().IsRegular() && strings.HasPrefix(file.Name(), "status-") && strings.HasSuffix(file.Name(), ".json")
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
		!statusCacheSectionsComplete(entry) || entry.ShowMergedPullRequests != (expected.MergedLimit > 0) ||
		(entry.ShowMergedPullRequests && entry.MergedPullRequests == nil) {
		return false
	}
	age := now.Sub(entry.FetchedAt)
	return age >= 0 && age < statusCacheTTL
}

func statusCacheSectionsComplete(entry statusCacheEntry) bool {
	return entry.Repository != "" && entry.DefaultBranch != "" && entry.Issues != nil &&
		entry.PullRequests != nil && entry.PullRequestHeads != nil && entry.WorkflowRuns != nil
}

func saveStatusCacheIfSameIdentity(options statusOptions, colorEnabled bool, now time.Time, dashboard statusDashboard, pullRequestHeads map[string]bool, pullRequestsKnown bool, directory, fingerprint string) {
	currentDirectory, currentFingerprint, err := statusCacheDirectoryFunc()
	if err == nil && directory == currentDirectory && fingerprint == currentFingerprint {
		saveStatusCacheAt(options, colorEnabled, now, dashboard, pullRequestHeads, pullRequestsKnown, directory, fingerprint)
	}
}

func saveStatusCacheAt(options statusOptions, colorEnabled bool, now time.Time, dashboard statusDashboard, pullRequestHeads map[string]bool, pullRequestsKnown bool, directory, fingerprint string) {
	if !statusDashboardCacheable(dashboard) {
		return
	}
	entry := statusCacheEntry{
		Version:                statusCacheSchemaVersion,
		FetchedAt:              now,
		Key:                    statusCacheKey{RemoteFingerprint: fingerprint, MergedLimit: options.mergedLimit, ColorEnabled: colorEnabled},
		Repository:             dashboard.Repository,
		RepositoryURL:          dashboard.RepositoryURL,
		DefaultBranch:          dashboard.DefaultBranch,
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
	if dashboard.DefaultBranch == "" {
		return false
	}
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
	temporaryPath, err := writeStatusCacheTemp(directory, data)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporaryPath) }()
	finalName := strings.TrimPrefix(strings.TrimSuffix(filepath.Base(temporaryPath), ".tmp"), ".") + ".json"
	finalPath := filepath.Join(directory, finalName)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		return err
	}
	pruneStatusCacheFiles(directory, finalPath, now)
	return nil
}

func writeStatusCacheTemp(directory string, data []byte) (string, error) {
	file, err := os.CreateTemp(directory, ".status-*.tmp")
	if err != nil {
		return "", err
	}
	path := file.Name()
	complete := false
	defer func() {
		if !complete {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	complete = true
	return path, nil
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
	remoteConfig, err := statusRemoteConfig()
	if err != nil {
		return "", "", err
	}
	branchRemote, err := statusBranchRemote()
	if err != nil {
		return "", "", fmt.Errorf("active branch remote: %w", err)
	}
	authContext, err := statusAuthContext()
	if err != nil {
		return "", "", fmt.Errorf("GitHub authentication context: %w", err)
	}
	return filepath.Join(filepath.Clean(commonDirectory), "gh-x", statusCacheDirectoryName), statusTargetFingerprint(remoteConfig, branchRemote, authContext), nil
}

func statusRemoteConfig() (string, error) {
	config, err := statusCommandFunc("git", "config", "--includes", "--null", "--get-regexp", `^(remote\..*\.(url|gh-resolved)|url\..*)$`)
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", fmt.Errorf("git remote configuration: %w", err)
		}
	}
	if config == "" && strings.TrimSpace(os.Getenv("GH_REPO")) == "" {
		return "", errors.New("no GitHub repository target")
	}
	return config, nil
}

func statusBranchRemote() (string, error) {
	branch, err := statusCommandFunc("git", "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil // Detached HEAD uses the repository's default remote.
		}
		return "", err
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "", errors.New("active branch unavailable")
	}
	remote, err := statusCommandFunc("git", "config", "--includes", "--get", "branch."+branch+".remote")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", nil // No branch-specific remote is configured.
		}
		return "", err
	}
	return strings.TrimSpace(remote), nil
}

func statusRemoteFingerprint(remoteURL string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(remoteURL)))
}

// gh repo set-default writes remote.*.gh-resolved in local Git config.
// Hash all remotes, the gh auth configuration, and environment overrides;
// neither tokens nor authentication files are persisted in cache entries.
func statusTargetFingerprint(remoteConfig, branchRemote, authContext string) string {
	return statusRemoteFingerprint(strings.Join([]string{
		remoteConfig, branchRemote, authContext, os.Getenv("GH_REPO"), os.Getenv("GH_HOST"),
		os.Getenv("GH_TOKEN"), os.Getenv("GITHUB_TOKEN"),
		os.Getenv("GH_ENTERPRISE_TOKEN"), os.Getenv("GITHUB_ENTERPRISE_TOKEN"),
	}, "\x00"))
}

type statusAuthHost struct {
	User  string         `yaml:"user"`
	Users map[string]any `yaml:"users"`
}

var statusKeyringGetFunc = func(service, user string) (string, error) {
	type result struct {
		token string
		err   error
	}
	response := make(chan result, 1)
	go func() {
		token, err := keyring.Get(service, user)
		response <- result{token, err}
	}()
	select {
	case outcome := <-response:
		return outcome.token, outcome.err
	case <-time.After(2 * time.Second):
		return "", errors.New("GitHub credential store timed out")
	}
}

func statusAuthContext() (string, error) {
	configDirectory := statusCLIConfigDir()
	if configDirectory == "" {
		return "", errors.New("GitHub CLI config directory unavailable")
	}
	content, err := os.ReadFile(filepath.Join(configDirectory, "hosts.yml"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil // An environment token can authenticate without a hosts file.
	}
	if err != nil {
		return "", err // Do not reuse a snapshot if the active account is unknown.
	}
	var hosts map[string]statusAuthHost
	if err := yaml.Unmarshal(content, &hosts); err != nil {
		return "", err
	}
	keyringContext, err := statusKeyringContext(hosts)
	if err != nil {
		return "", err // Never reuse a snapshot if the effective token is unknown.
	}
	return statusRemoteFingerprint(string(content) + "\x00" + keyringContext), nil
}

func statusKeyringContext(hosts map[string]statusAuthHost) (string, error) {
	hostnames := make([]string, 0, len(hosts))
	for hostname := range hosts {
		hostnames = append(hostnames, hostname)
	}
	sort.Strings(hostnames)
	var identities []string
	for _, hostname := range hostnames {
		if statusHasEnvironmentToken(hostname) {
			continue // An explicit token also prevents fallback-account retries.
		}
		for _, user := range statusAuthUsernames(hosts[hostname]) {
			identity, err := statusKeyringTokenFingerprint("gh:"+hostname, user)
			if err != nil {
				return "", err
			}
			identities = append(identities, hostname+"/"+user+":"+identity)
		}
	}
	return strings.Join(identities, "\x00"), nil
}

func statusAuthUsernames(host statusAuthHost) []string {
	users := map[string]bool{"": true, host.User: true}
	for user := range host.Users {
		users[user] = true
	}
	names := make([]string, 0, len(users))
	for user := range users {
		names = append(names, user)
	}
	sort.Strings(names)
	return names
}

func statusKeyringTokenFingerprint(service, user string) (string, error) {
	token, err := statusKeyringGetFunc(service, user)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return statusRemoteFingerprint(token), nil
}

func statusHasEnvironmentToken(host string) bool {
	if strings.EqualFold(host, "github.com") || strings.EqualFold(host, "ghe.com") ||
		strings.HasSuffix(strings.ToLower(host), ".ghe.com") {
		return os.Getenv("GH_TOKEN") != "" || os.Getenv("GITHUB_TOKEN") != ""
	}
	return os.Getenv("GH_ENTERPRISE_TOKEN") != "" || os.Getenv("GITHUB_ENTERPRISE_TOKEN") != ""
}

func statusCLIConfigDir() string {
	if directory := os.Getenv("GH_CONFIG_DIR"); directory != "" {
		return directory
	}
	if directory := os.Getenv("XDG_CONFIG_HOME"); directory != "" {
		return filepath.Join(directory, "gh")
	}
	if runtime.GOOS == "windows" && os.Getenv("AppData") != "" {
		return filepath.Join(os.Getenv("AppData"), "GitHub CLI")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "gh")
}

func cacheStatusReferences(refs []linkedReference) []statusCachedReference {
	cached := make([]statusCachedReference, len(refs))
	for i, ref := range refs {
		cached[i] = statusCachedReference(ref)
	}
	return cached
}

func restoreStatusReferences(cached []statusCachedReference) []linkedReference {
	refs := make([]linkedReference, len(cached))
	for i, ref := range cached {
		refs[i] = linkedReference(ref)
	}
	return refs
}

func cacheStatusIssues(issues []displayIssue) []statusCachedIssue {
	cached := make([]statusCachedIssue, len(issues))
	for index, issue := range issues {
		cached[index] = statusCachedIssue{
			Display:         issue,
			PullRequestRefs: cacheStatusReferences(issue.pullRequestRefs),
			ParentRefs:      cacheStatusReferences(issue.parentRefs),
		}
	}
	return cached
}

func restoreStatusIssues(cached []statusCachedIssue) []displayIssue {
	issues := make([]displayIssue, len(cached))
	for index, issue := range cached {
		issues[index] = issue.Display
		issues[index].pullRequestRefs = restoreStatusReferences(issue.PullRequestRefs)
		issues[index].parentRefs = restoreStatusReferences(issue.ParentRefs)
	}
	return issues
}

func cacheStatusPullRequests(pullRequests []displayPullRequest) []statusCachedPullRequest {
	cached := make([]statusCachedPullRequest, len(pullRequests))
	for index, pullRequest := range pullRequests {
		cached[index] = statusCachedPullRequest{
			Display:          pullRequest,
			ChecksDowngraded: pullRequest.checksDowngraded,
			IssueRefs:        cacheStatusReferences(pullRequest.issueRefs),
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
		pullRequests[index].issueRefs = restoreStatusReferences(pullRequest.IssueRefs)
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
