package main

import "time"

func maxStatusCacheTime(first, second time.Time) time.Time {
	if second.After(first) {
		return second
	}
	return first
}

// Writers hold the directory lock while merging and replacing the snapshot.
// Expired sections still participate: an older failure must not replace a
// newer success just because the success's reuse deadline has passed.
func mergeStatusCacheSections(incoming, previous statusCacheEntry, now time.Time) statusCacheEntry {
	if !statusCacheShapeMatches(previous, incoming.Key) || previous.FetchedAt.After(now) ||
		!statusCacheFailuresValid(previous) || !statusCacheSectionShapeValid(previous.Sections, now) || len(incoming.Sections) != 4 {
		return incoming
	}
	incoming.Sections = append([]statusCacheSection(nil), incoming.Sections...)
	failures := make([]string, statusFailureCount)
	copy(failures, incoming.Failures)
	for section := range incoming.Sections {
		if previous.Sections[section].FetchedAt.After(incoming.Sections[section].FetchedAt) {
			copyStatusCacheSection(&incoming, previous, section)
			copyStatusSectionFailures(failures, previous.Failures, section)
		}
	}
	incoming.Failures = compactStatusFailures(failures)
	incoming.FetchedAt = maxStatusCacheTime(incoming.FetchedAt, previous.FetchedAt)
	return incoming
}

func copyStatusCacheSection(destination *statusCacheEntry, source statusCacheEntry, section int) {
	destination.Sections[section] = source.Sections[section]
	switch section {
	case 0:
		destination.Issues = source.Issues
	case 1:
		destination.PullRequests = source.PullRequests
		destination.PullRequestHeads = source.PullRequestHeads
		destination.PullRequestsKnown = source.PullRequestsKnown
	case 2:
		destination.MergedPullRequests = source.MergedPullRequests
	case 3:
		destination.WorkflowRuns = source.WorkflowRuns
		destination.WorkflowRunsPerfect = source.WorkflowRunsPerfect
	}
}

func copyStatusSectionFailures(destination, source []string, section int) {
	first := section * 3
	last := min(first+3, statusFailureCount)
	for i := first; i < last; i++ {
		destination[i] = ""
		if len(source) != 0 {
			destination[i] = source[i]
		}
	}
}

func compactStatusFailures(failures []string) []string {
	for _, failure := range failures {
		if failure != "" {
			return failures
		}
	}
	return nil
}
