package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestBacklogPraisePool(t *testing.T) {
	if len(backlogPraises) < 12 {
		t.Fatalf("backlogPraises has %d messages, want at least 12", len(backlogPraises))
	}

	required := []string{
		"✅ Backlog cleared!",
		"✅ Good job, here is a pony!",
	}
	for _, want := range required {
		found := false
		for _, praise := range backlogPraises {
			if praise == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("backlogPraises is missing %q", want)
		}
	}
}

func TestBacklogPraiseSelectionBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		index int
		want  string
	}{
		{name: "first", index: 0, want: backlogPraises[0]},
		{name: "last", index: len(backlogPraises) - 1, want: backlogPraises[len(backlogPraises)-1]},
		{name: "negative falls back", index: -1, want: backlogPraises[0]},
		{name: "upper bound falls back", index: len(backlogPraises), want: backlogPraises[0]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := backlogPraiseAt(tc.index); got != tc.want {
				t.Fatalf("backlogPraiseAt(%d) = %q, want %q", tc.index, got, tc.want)
			}
		})
	}
}

func TestWorkflowPerfectionPraisePool(t *testing.T) {
	if len(workflowPerfectionPraises) < 3 {
		t.Fatalf("workflowPerfectionPraises has %d messages, want at least 3", len(workflowPerfectionPraises))
	}
	for _, want := range []string{
		"✨ Five for five. Flawless.",
		"✨ CI perfection: five straight successes.",
	} {
		if !containsString(workflowPerfectionPraises, want) {
			t.Fatalf("workflowPerfectionPraises is missing %q", want)
		}
	}
}

func TestWorkflowPerfectionPraiseSelectionBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		index int
		want  string
	}{
		{name: "first", index: 0, want: workflowPerfectionPraises[0]},
		{name: "last", index: len(workflowPerfectionPraises) - 1, want: workflowPerfectionPraises[len(workflowPerfectionPraises)-1]},
		{name: "negative falls back", index: -1, want: workflowPerfectionPraises[0]},
		{name: "upper bound falls back", index: len(workflowPerfectionPraises), want: workflowPerfectionPraises[0]},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflowPerfectionPraiseAt(tc.index); got != tc.want {
				t.Fatalf("workflowPerfectionPraiseAt(%d) = %q, want %q", tc.index, got, tc.want)
			}
		})
	}
}

func TestOpenPullRequestBacklogEligibility(t *testing.T) {
	tests := []struct {
		name    string
		options listOptions
		want    bool
	}{
		{name: "default open backlog", options: defaultListOptions(), want: true},
		{name: "repository selection", options: listOptions{state: "open", repo: "owner/repo"}, want: true},
		{name: "limit", options: listOptions{state: "open", limit: 5}, want: true},
		{name: "JSON display", options: listOptions{state: "open", json: true}, want: true},
		{name: "web display", options: listOptions{state: "open", web: true}, want: true},
		{name: "closed state", options: listOptions{state: "closed"}},
		{name: "all states", options: listOptions{state: "all"}},
		{name: "author", options: listOptions{state: "open", author: "octocat"}},
		{name: "assignee", options: listOptions{state: "open", assignee: "octocat"}},
		{name: "app", options: listOptions{state: "open", app: "dependabot"}},
		{name: "base", options: listOptions{state: "open", base: "main"}},
		{name: "head", options: listOptions{state: "open", head: "feature"}},
		{name: "search", options: listOptions{state: "open", search: "review:required"}},
		{name: "draft", options: listOptions{state: "open", draftOnly: true}},
		{name: "label", options: listOptions{state: "open", labels: stringSliceFlag{"bug"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOpenPullRequestBacklog(tc.options); got != tc.want {
				t.Fatalf("isOpenPullRequestBacklog() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOpenIssueBacklogEligibility(t *testing.T) {
	tests := []struct {
		name    string
		options issueListOptions
		want    bool
	}{
		{name: "default open backlog", options: issueListOptions{state: "open"}, want: true},
		{name: "repository selection", options: issueListOptions{state: "open", repo: "owner/repo"}, want: true},
		{name: "limit", options: issueListOptions{state: "open", limit: 5}, want: true},
		{name: "web display", options: issueListOptions{state: "open", web: true}, want: true},
		{name: "closed state", options: issueListOptions{state: "closed"}},
		{name: "all states", options: issueListOptions{state: "all"}},
		{name: "author", options: issueListOptions{state: "open", author: "octocat"}},
		{name: "assignee", options: issueListOptions{state: "open", assignee: "octocat"}},
		{name: "milestone", options: issueListOptions{state: "open", milestone: "next"}},
		{name: "search", options: issueListOptions{state: "open", search: "no:assignee"}},
		{name: "label", options: issueListOptions{state: "open", labels: stringSliceFlag{"bug"}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isOpenIssueBacklog(tc.options); got != tc.want {
				t.Fatalf("isOpenIssueBacklog() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEmptyBacklogMessages(t *testing.T) {
	useBacklogPraiseIndex(t, 1)
	praise := backlogPraises[1]

	tests := []struct {
		name        string
		render      func(io.Writer) error
		want        string
		praiseCount int
	}{
		{
			name: "pull request open backlog",
			render: func(w io.Writer) error {
				return renderTableWithStyle(w, defaultListOptions(), nil, false)
			},
			want: praise, praiseCount: 1,
		},
		{
			name: "pull request repository and limit are display only",
			render: func(w io.Writer) error {
				return renderTableWithStyle(w, listOptions{state: "open", repo: "owner/repo", limit: 5}, nil, false)
			},
			want: praise, praiseCount: 1,
		},
		{
			name: "pull request historical state",
			render: func(w io.Writer) error {
				return renderTableWithStyle(w, listOptions{state: "closed"}, nil, false)
			},
			want: "No pull requests found.",
		},
		{
			name: "pull request author filter",
			render: func(w io.Writer) error {
				return renderTableWithStyle(w, listOptions{state: "open", author: "octocat"}, nil, false)
			},
			want: "No pull requests found.",
		},
		{
			name: "issue open backlog",
			render: func(w io.Writer) error {
				return renderIssueTable(w, nil, issueListOptions{state: "open"}, false)
			},
			want: praise, praiseCount: 1,
		},
		{
			name: "issue repository and limit are display only",
			render: func(w io.Writer) error {
				return renderIssueTable(w, nil, issueListOptions{state: "open", repo: "owner/repo", limit: 5}, false)
			},
			want: praise, praiseCount: 1,
		},
		{
			name: "issue historical state",
			render: func(w io.Writer) error {
				return renderIssueTable(w, nil, issueListOptions{state: "closed"}, false)
			},
			want: "No issues found.",
		},
		{
			name: "issue label filter",
			render: func(w io.Writer) error {
				return renderIssueTable(w, nil, issueListOptions{state: "open", labels: stringSliceFlag{"bug"}}, false)
			},
			want: "No issues found.",
		},
		{
			name: "status issue section",
			render: func(w io.Writer) error {
				return renderStatusIssueSection(w, newTableStyler(w, false), statusDashboard{})
			},
			want: praise, praiseCount: 1,
		},
		{
			name: "status pull request section",
			render: func(w io.Writer) error {
				return renderStatusPullRequestSection(w, newTableStyler(w, false), statusDashboard{})
			},
			want: praise, praiseCount: 1,
		},
		{
			name: "status both sections",
			render: func(w io.Writer) error {
				return renderStatus(w, statusDashboard{}, false)
			},
			want: praise, praiseCount: 2,
		},
		{
			name: "structured output",
			render: func(w io.Writer) error {
				return renderListOutput(w, listOptions{state: "open", json: true}, []displayPullRequest{})
			},
			want: "[]\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := tc.render(&output); err != nil {
				t.Fatal(err)
			}
			if got := output.String(); !strings.Contains(got, tc.want) {
				t.Fatalf("output %q does not contain %q", got, tc.want)
			}
			if count := strings.Count(output.String(), praise); count != tc.praiseCount {
				t.Fatalf("praise count = %d, want %d in %q", count, tc.praiseCount, output.String())
			}
		})
	}
}

func useBacklogPraiseIndex(t *testing.T, index int) {
	t.Helper()
	original := backlogPraiseIndex
	backlogPraiseIndex = func(limit int) int {
		if limit != len(backlogPraises) {
			t.Fatalf("selector limit = %d, want %d", limit, len(backlogPraises))
		}
		return index
	}
	t.Cleanup(func() { backlogPraiseIndex = original })
}

func useWorkflowPerfectionPraiseIndex(t *testing.T, index int) {
	t.Helper()
	original := workflowPerfectionPraiseIndex
	workflowPerfectionPraiseIndex = func(limit int) int {
		if limit != len(workflowPerfectionPraises) {
			t.Fatalf("selector limit = %d, want %d", limit, len(workflowPerfectionPraises))
		}
		return index
	}
	t.Cleanup(func() { workflowPerfectionPraiseIndex = original })
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
