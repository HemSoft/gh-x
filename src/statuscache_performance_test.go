package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStatusCacheConcurrentReplacementIsComplete(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	options := statusOptions{}
	saveStatusCacheAt(options, false, now, statusDashboard{Repository: "seed", DefaultBranch: "seed"}, nil, true, directory, "fixture")
	key := statusCacheKey{RemoteFingerprint: "fixture"}
	path := filepath.Join(directory, statusCacheFileName(key))
	seed, err := readStatusCacheEntry(path)
	if err != nil {
		t.Fatal(err)
	}
	var writers sync.WaitGroup
	defer writers.Wait()
	writeErrors := make(chan error, 8)
	for writer := range 8 {
		writers.Go(func() {
			entry := seed
			entry.Repository = fmt.Sprintf("writer%d", writer)
			entry.DefaultBranch = entry.Repository
			for range 8 {
				if err := writeStatusCacheEntry(directory, entry, now); err != nil {
					writeErrors <- err
					return
				}
			}
		})
	}
	for range 100 {
		entry, err := readStatusCacheEntry(path)
		if err != nil || entry.Repository != entry.DefaultBranch || !validStatusCacheEntry(entry, key, now) {
			t.Fatalf("reader observed incomplete snapshot: %+v, %v", entry, err)
		}
	}
	writers.Wait()
	close(writeErrors)
	for err := range writeErrors {
		t.Fatal(err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 2 || files[0].Name() != ".status-cache.lock" {
		t.Fatalf("replacement leaked snapshots: %v, %v", files, err)
	}
}

func TestStatusPlaintextAuthenticationSkipsActiveKeyring(t *testing.T) {
	tests := []struct {
		name, config string
		probes       int
	}{
		{"active plaintext", "github.com:\n  user: alice\n  oauth_token: active-token\n  users:\n    alice:\n      oauth_token: active-token\n", 0},
		{"mixed fallback", "github.com:\n  user: alice\n  oauth_token: active-token\n  users:\n    alice: {}\n    bob:\n      oauth_token: fallback-token\n", 1},
		{"secure active", "github.com:\n  user: alice\n  users:\n    alice: {}\n", 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := isolateStatusCacheAuthentication(t)
			if err := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte(test.config), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			statusKeyringGetFunc = func(string, string) (string, error) { calls++; return "secure-token", nil }
			first, err := statusAuthContext()
			if err != nil || first == "" || calls != test.probes {
				t.Fatalf("calls=%d,err=%v", calls, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte(test.config+"  git_protocol: https\n"), 0600); err != nil {
				t.Fatal(err)
			}
			second, err := statusAuthContext()
			if err != nil || first == second {
				t.Fatal("config changes reused identity")
			}
		})
	}
}

func TestStatusPartialCacheRetriesOnlyFailedSections(t *testing.T) {
	tests := []struct {
		name   string
		failed int
	}{{"issues", 0}, {"open PRs", 1}, {"merged PRs", 2}, {"workflow runs", 3}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer saveStatusFuncs()()
			installStatusDashboardGitFixture()
			useStatusCacheDirectory(t, t.TempDir(), "owner/repo")
			now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
			statusNowFunc = func() time.Time { return now }
			statusRepoLabelFunc = func(string) string { return "owner/repo" }
			counts := [4]int{}
			broken := true
			fail := func(i int) error {
				counts[i]++
				if broken && i == test.failed {
					return errors.New("fixture section unavailable")
				}
				return nil
			}
			statusIssueListFunc = func(issueListOptions, time.Time) (issueListResult, error) { return issueListResult{}, fail(0) }
			statusPullRequestListFunc = func(o listOptions, _ time.Time) (pullRequestListResult, error) {
				i := 1
				if o.state == "merged" {
					i = 2
				}
				return pullRequestListResult{}, fail(i)
			}
			statusWorkflowRunListFunc = func(runListOptions, time.Time) (workflowRunListResult, error) {
				return workflowRunListResult{}, fail(3)
			}
			options := statusOptions{mergedLimit: 5}
			first, err := fetchStatusDashboard(false, options)
			if err != nil {
				t.Fatal(err)
			}
			if statusSectionErrors(first)[test.failed] == nil {
				t.Fatal("missing error")
			}
			second, err := fetchStatusDashboard(false, options)
			if err != nil || counts != [4]int{1, 1, 1, 1} || statusSectionErrors(second)[test.failed] == nil {
				t.Fatalf("warm counts=%v,err=%v", counts, err)
			}
			now = now.Add(5 * time.Second)
			broken = false
			recovered, err := fetchStatusDashboard(false, options)
			if err != nil || statusSectionErrors(recovered)[test.failed] != nil {
				t.Fatalf("recovery=%v", err)
			}
			want := [4]int{1, 1, 1, 1}
			want[test.failed] = 2
			if counts != want {
				t.Fatalf("retry fetched healthy sections: %v", counts)
			}
			now = now.Add(55 * time.Second)
			if _, err := fetchStatusDashboard(false, options); err != nil {
				t.Fatal(err)
			}
			for i := range counts {
				if counts[i] != 2 {
					t.Fatalf("section %d freshness extended incorrectly: %v", i, counts)
				}
			}
		})
	}
}

func TestStatusRetryDelay(t *testing.T) {
	now := time.Unix(1000, 0)
	tests := []struct {
		name string
		err  error
		want time.Duration
	}{
		{"healthy", nil, time.Minute}, {"failure", errors.New("offline"), 5 * time.Second},
		{"rate limit", errors.New("HTTP 429"), time.Minute},
		{"retry after", errors.New("rate limit exceeded; Retry-After: 120"), 2 * time.Minute},
		{"reset", errors.New("rate limit; X-RateLimit-Reset: 1180"), 3 * time.Minute},
		{"invalid retry", errors.New("retry-after: 999999999999999999999"), 5 * time.Second},
		{"invalid reset", errors.New("x-ratelimit-reset: 999999999999999999999"), 5 * time.Second},
		{"past reset", errors.New("x-ratelimit-reset: 900"), 5 * time.Second},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := statusRetryDelay(test.err, now); got != test.want {
				t.Fatalf("delay=%v,want %v", got, test.want)
			}
		})
	}
}

func TestStatusCacheSectionValidation(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name         string
		sections     []statusCacheSection
		valid, fetch bool
	}{
		{name: "missing"},
		{name: "zero", sections: make([]statusCacheSection, 4)},
		{name: "healthy", valid: true, sections: []statusCacheSection{{now, now.Add(time.Minute)}, {now, now.Add(time.Minute)}, {now, now.Add(time.Minute)}, {now, now.Add(time.Minute)}}},
		{name: "mixed expiry", valid: true, fetch: true, sections: []statusCacheSection{{now, now}, {now, now.Add(time.Minute)}, {now, now.Add(time.Minute)}, {now, now.Add(time.Minute)}}},
		{name: "future", sections: []statusCacheSection{{now.Add(time.Second), now.Add(time.Minute)}, {now, now}, {now, now}, {now, now}}},
		{name: "reversed", sections: []statusCacheSection{{now, now.Add(-time.Second)}, {now, now}, {now, now}, {now, now}}},
		{name: "expired", fetch: true, sections: []statusCacheSection{{now, now}, {now, now}, {now, now}, {now, now}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validStatusCacheSections(test.sections, now); got != test.valid {
				t.Fatalf("valid=%v", got)
			}
			if test.valid && statusSectionsNeedFetch(test.sections, now) != test.fetch {
				t.Fatal("wrong refresh decision")
			}
		})
	}
}

func TestStatusFailedRefreshReplacesSuccessfulSnapshot(t *testing.T) {
	defer saveStatusFuncs()()
	installStatusDashboardGitFixture()
	useStatusCacheDirectory(t, t.TempDir(), "owner/repo")
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	statusNowFunc = func() time.Time { return now }
	statusRepoLabelFunc = func(string) string { return "owner/repo" }
	failed := false
	calls := 0
	statusIssueListFunc = func(issueListOptions, time.Time) (issueListResult, error) {
		calls++
		if failed {
			return issueListResult{}, errors.New("issue fetch unavailable")
		}
		return issueListResult{}, nil
	}
	statusPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) { return pullRequestListResult{}, nil }
	statusWorkflowRunListFunc = func(runListOptions, time.Time) (workflowRunListResult, error) { return workflowRunListResult{}, nil }
	if _, err := fetchStatusDashboard(false, statusOptions{}); err != nil {
		t.Fatal(err)
	}
	failed = true
	if _, err := fetchStatusDashboard(false, statusOptions{refresh: true}); err != nil {
		t.Fatal(err)
	}
	dashboard, err := fetchStatusDashboard(false, statusOptions{})
	if err != nil || dashboard.IssuesErr == nil || calls != 2 {
		t.Fatalf("failed refresh hidden or repeated: err=%v issues=%v calls=%d", err, dashboard.IssuesErr, calls)
	}
}

func TestStatusRateLimitCooldownOutlivesHealthySections(t *testing.T) {
	defer saveStatusFuncs()()
	installStatusDashboardGitFixture()
	useStatusCacheDirectory(t, t.TempDir(), "owner/repo")
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	statusNowFunc = func() time.Time { return now }
	statusRepoLabelFunc = func(string) string { return "owner/repo" }
	issueCalls, runCalls := 0, 0
	limited := true
	statusIssueListFunc = func(issueListOptions, time.Time) (issueListResult, error) {
		issueCalls++
		if limited {
			return issueListResult{}, errors.New("HTTP 429; Retry-After: 120")
		}
		return issueListResult{}, nil
	}
	statusPullRequestListFunc = func(listOptions, time.Time) (pullRequestListResult, error) { return pullRequestListResult{}, nil }
	statusWorkflowRunListFunc = func(runListOptions, time.Time) (workflowRunListResult, error) {
		runCalls++
		return workflowRunListResult{}, nil
	}
	if _, err := fetchStatusDashboard(false, statusOptions{}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(70 * time.Second)
	dashboard, err := fetchStatusDashboard(false, statusOptions{})
	if err != nil || dashboard.IssuesErr == nil || issueCalls != 1 || runCalls != 2 {
		t.Fatalf("cooldown/freshness lost: issues=%d runs=%d err=%v", issueCalls, runCalls, err)
	}
	now = now.Add(50 * time.Second)
	limited = false
	dashboard, err = fetchStatusDashboard(false, statusOptions{})
	if err != nil || dashboard.IssuesErr != nil || issueCalls != 2 || runCalls != 2 {
		t.Fatalf("cooldown recovery wrong: issues=%d runs=%d err=%v", issueCalls, runCalls, err)
	}
}
