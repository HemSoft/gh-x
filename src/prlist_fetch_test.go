package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFetchPullRequestList(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		command := strings.Join(args, " ")
		if !strings.Contains(command, "pr list") || !strings.Contains(command, "--limit 30") || !strings.Contains(command, "--state open") {
			t.Fatalf("unexpected arguments: %s", command)
		}
		return *bytes.NewBufferString("[]"), bytes.Buffer{}, nil
	}

	result, err := fetchPullRequestList(listOptions{repo: "owner/repo", limit: 30, state: "open"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 0 || len(result.Rendered) != 0 {
		t.Fatalf("expected empty result, got %#v", result)
	}
}

func TestFetchPullRequestListError(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, *bytes.NewBufferString("offline"), errors.New("exit status 1")
	}

	_, err := fetchPullRequestList(defaultListOptions(), time.Now())
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("expected wrapped stderr, got %v", err)
	}
}

func TestResolveAuthorFromOrg_SearchError(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, bytes.Buffer{}, fmt.Errorf("network error")
	}

	result := resolveAuthorFromOrg("John Doe", "myorg")
	if result != "" {
		t.Fatalf("expected empty string on error, got %q", result)
	}
}

func TestResolveAuthorFromOrg_MatchFound(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	callCount := 0
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		callCount++
		var buf bytes.Buffer
		if callCount == 1 {
			// Search returns logins
			buf.WriteString("johndoe\noctocat\n")
			return buf, bytes.Buffer{}, nil
		}
		// Membership check: first login is a member
		if callCount == 2 {
			return buf, bytes.Buffer{}, nil // success = member
		}
		return buf, bytes.Buffer{}, fmt.Errorf("not a member")
	}

	result := resolveAuthorFromOrg("John Doe", "myorg")
	if result != "johndoe" {
		t.Fatalf("expected 'johndoe', got %q", result)
	}
}

func TestResolveAuthorFromOrg_NoMember(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	callCount := 0
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		callCount++
		var buf bytes.Buffer
		if callCount == 1 {
			buf.WriteString("user1\nuser2\n")
			return buf, bytes.Buffer{}, nil
		}
		// All membership checks fail
		return buf, bytes.Buffer{}, fmt.Errorf("not a member")
	}

	result := resolveAuthorFromOrg("Jane Smith", "myorg")
	if result != "" {
		t.Fatalf("expected empty string when no member found, got %q", result)
	}
}

func TestResolveAuthorFromOrg_EmptyLogins(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		var buf bytes.Buffer
		buf.WriteString("\nnull\n\n")
		return buf, bytes.Buffer{}, nil
	}

	result := resolveAuthorFromOrg("Nobody", "myorg")
	if result != "" {
		t.Fatalf("expected empty string for empty/null logins, got %q", result)
	}
}

func TestResolveAuthorLogin_WithSpaceAndOrg(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	callCount := 0
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		callCount++
		var buf bytes.Buffer
		if callCount == 1 {
			// Search returns one login
			buf.WriteString("jdoe\n")
			return buf, bytes.Buffer{}, nil
		}
		// Membership check succeeds
		return buf, bytes.Buffer{}, nil
	}

	login, err := resolveAuthorLogin("John Doe", "myorg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "jdoe" {
		t.Fatalf("expected 'jdoe', got %q", login)
	}
}

func TestResolveAuthorLogin_WithSpaceFallbackToSearch(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	callCount := 0
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		callCount++
		var buf bytes.Buffer
		if callCount == 1 {
			// Org search returns empty
			buf.WriteString("\n")
			return buf, bytes.Buffer{}, nil
		}
		// Global user search fallback
		buf.WriteString("globaluser")
		return buf, bytes.Buffer{}, nil
	}

	login, err := resolveAuthorLogin("John Doe", "myorg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "globaluser" {
		t.Fatalf("expected 'globaluser', got %q", login)
	}
}

func TestResolveAuthorLogin_WithSpaceNoOrg(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		var buf bytes.Buffer
		buf.WriteString("founduser")
		return buf, bytes.Buffer{}, nil
	}

	login, err := resolveAuthorLogin("John Doe", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if login != "founduser" {
		t.Fatalf("expected 'founduser', got %q", login)
	}
}

func TestResolveAuthorLogin_SearchFails(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, bytes.Buffer{}, fmt.Errorf("api error")
	}

	_, err := resolveAuthorLogin("John Doe", "")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestResolveAuthorLogin_SearchReturnsEmpty(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, bytes.Buffer{}, nil
	}

	_, err := resolveAuthorLogin("John Doe", "")
	if err == nil {
		t.Fatalf("expected error for empty result, got nil")
	}
}

func TestResolveAuthorLogin_SearchReturnsNull(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		var buf bytes.Buffer
		buf.WriteString("null")
		return buf, bytes.Buffer{}, nil
	}

	_, err := resolveAuthorLogin("John Doe", "")
	if err == nil {
		t.Fatalf("expected error for null result, got nil")
	}
}

func TestFetchPRSupplemental_Empty(t *testing.T) {
	result, unavailable, err := fetchPRSupplemental("owner", "repo", "github.com", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil || unavailable != nil {
		t.Fatalf("expected nil for empty input, got %v", result)
	}
}

func TestFetchPRSupplemental_SingleBatch(t *testing.T) {
	saved := fetchPRSupplementalBatchFunc
	defer func() { fetchPRSupplementalBatchFunc = saved }()

	fetchPRSupplementalBatchFunc = func(owner, name, host string, prNumbers []int) (map[int]prSupplementalInfo, map[int]bool, error) {
		result := make(map[int]prSupplementalInfo)
		for _, n := range prNumbers {
			result[n] = prSupplementalInfo{AIReview: "clean"}
		}
		return result, nil, nil
	}

	result, unavailable, err := fetchPRSupplemental("owner", "repo", "github.com", []int{1, 2, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 || len(unavailable) != 0 {
		t.Fatalf("expected 3 results with none unavailable, got %d results, %d unavailable", len(result), len(unavailable))
	}
	for _, n := range []int{1, 2, 3} {
		if result[n].AIReview != "clean" {
			t.Fatalf("expected AIReview='clean' for PR %d", n)
		}
	}
}

func TestFetchPRSupplemental_MultipleBatches(t *testing.T) {
	saved := fetchPRSupplementalBatchFunc
	defer func() { fetchPRSupplementalBatchFunc = saved }()

	batchCalls := 0
	fetchPRSupplementalBatchFunc = func(owner, name, host string, prNumbers []int) (map[int]prSupplementalInfo, map[int]bool, error) {
		batchCalls++
		result := make(map[int]prSupplementalInfo)
		for _, n := range prNumbers {
			result[n] = prSupplementalInfo{Approvals: batchCalls}
		}
		return result, nil, nil
	}

	// Create 35 PRs to force 2 batches (batch size is 30)
	prs := make([]int, 35)
	for i := range prs {
		prs[i] = i + 1
	}

	result, unavailable, err := fetchPRSupplemental("owner", "repo", "github.com", prs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batchCalls != 2 {
		t.Fatalf("expected 2 batch calls, got %d", batchCalls)
	}
	if len(result) != 35 || len(unavailable) != 0 {
		t.Fatalf("expected 35 results with none unavailable, got %d results, %d unavailable", len(result), len(unavailable))
	}
}

func TestFetchPRSupplemental_BatchError(t *testing.T) {
	saved := fetchPRSupplementalBatchFunc
	defer func() { fetchPRSupplementalBatchFunc = saved }()

	fetchPRSupplementalBatchFunc = func(owner, name, host string, prNumbers []int) (map[int]prSupplementalInfo, map[int]bool, error) {
		return nil, map[int]bool{1: true, 2: true}, fmt.Errorf("graphql error")
	}

	result, unavailable, err := fetchPRSupplemental("owner", "repo", "github.com", []int{1, 2})
	if err == nil || err.Error() != "graphql error" {
		t.Fatalf("expected graphql error, got %v", err)
	}
	if len(result) != 0 || !unavailable[1] || !unavailable[2] {
		t.Fatalf("failed batches must keep every PR unavailable, got result=%v unavailable=%v", result, unavailable)
	}
}

// capturedCodexbarSupplementalResponse is the supplemental GraphQL response
// captured from HemSoft/codexbar-ios pull request #333 on 2026-09-09 while
// investigating issue #88. Comment prose past 90 characters is truncated;
// every field the parser consumes is verbatim: the closing relationship to
// issue #332, four review threads (one unresolved, Codex-authored), the
// completed current-head Codex review, and zero approvals.
const capturedCodexbarSupplementalResponse = `{
 "data": {
  "repository": {
   "pr333": {
    "number": 333,
    "headRefOid": "53b343204e072b22f6511ac68bf6284aa2c418c2",
    "closingIssuesReferences": {
     "totalCount": 1,
     "nodes": [
      {
       "number": 332,
       "url": "https://github.com/HemSoft/codexbar-ios/issues/332"
      }
     ]
    },
    "comments": {
     "totalCount": 6,
     "nodes": [
      {
       "body": "<!-- codex-pull-request-review-summary -->\n\n## Codex Review Summary\n\nThis comment shows th... [truncated]",
       "createdAt": "2026-09-09T04:26:19Z",
       "author": {
        "login": "chatgpt-codex-connector",
        "__typename": "Bot"
       }
      },
      {
       "body": "@coderabbitai review",
       "createdAt": "2026-09-09T04:26:34Z",
       "author": {
        "login": "HemSoft",
        "__typename": "User"
       }
      },
      {
       "body": "cursor review",
       "createdAt": "2026-09-09T04:26:35Z",
       "author": {
        "login": "HemSoft",
        "__typename": "User"
       }
      },
      {
       "body": "<!-- BUGBOT_FREE_TIER_DISABLED_UPSELL -->\nBugbot is not enabled for your account, so this ... [truncated]",
       "createdAt": "2026-09-09T04:26:39Z",
       "author": {
        "login": "cursor",
        "__typename": "Bot"
       }
      },
      {
       "body": "@codex review",
       "createdAt": "2026-09-09T05:20:29Z",
       "author": {
        "login": "HemSoft",
        "__typename": "User"
       }
      },
      {
       "body": "@coderabbitai review",
       "createdAt": "2026-09-09T05:20:30Z",
       "author": {
        "login": "HemSoft",
        "__typename": "User"
       }
      }
     ]
    },
    "reviewThreads": {
     "totalCount": 4,
     "nodes": [
      {
       "isResolved": true,
       "comments": {
        "nodes": [
         {
          "author": {
           "login": "chatgpt-codex-connector",
           "__typename": "Bot"
          }
         }
        ]
       }
      },
      {
       "isResolved": true,
       "comments": {
        "nodes": [
         {
          "author": {
           "login": "cubic-dev-ai",
           "__typename": "Bot"
          }
         }
        ]
       }
      },
      {
       "isResolved": true,
       "comments": {
        "nodes": [
         {
          "author": {
           "login": "cubic-dev-ai",
           "__typename": "Bot"
          }
         }
        ]
       }
      },
      {
       "isResolved": false,
       "comments": {
        "nodes": [
         {
          "author": {
           "login": "chatgpt-codex-connector",
           "__typename": "Bot"
          }
         }
        ]
       }
      }
     ]
    },
    "reviews": {
     "totalCount": 6,
     "nodes": [
      {
       "state": "COMMENTED",
       "submittedAt": "2026-09-09T04:30:06Z",
       "commit": {
        "oid": "c90b97e5274838c271b3ed3e110da2d49448c013"
       },
       "author": {
        "login": "chatgpt-codex-connector",
        "__typename": "Bot"
       },
       "comments": {
        "totalCount": 1
       }
      },
      {
       "state": "COMMENTED",
       "submittedAt": "2026-09-09T04:40:02Z",
       "commit": {
        "oid": "c90b97e5274838c271b3ed3e110da2d49448c013"
       },
       "author": {
        "login": "cubic-dev-ai",
        "__typename": "Bot"
       },
       "comments": {
        "totalCount": 2
       }
      },
      {
       "state": "COMMENTED",
       "submittedAt": "2026-09-09T05:20:13Z",
       "commit": {
        "oid": "53b343204e072b22f6511ac68bf6284aa2c418c2"
       },
       "author": {
        "login": "HemSoft",
        "__typename": "User"
       },
       "comments": {
        "totalCount": 1
       }
      },
      {
       "state": "COMMENTED",
       "submittedAt": "2026-09-09T05:20:16Z",
       "commit": {
        "oid": "53b343204e072b22f6511ac68bf6284aa2c418c2"
       },
       "author": {
        "login": "HemSoft",
        "__typename": "User"
       },
       "comments": {
        "totalCount": 1
       }
      },
      {
       "state": "COMMENTED",
       "submittedAt": "2026-09-09T05:20:18Z",
       "commit": {
        "oid": "53b343204e072b22f6511ac68bf6284aa2c418c2"
       },
       "author": {
        "login": "HemSoft",
        "__typename": "User"
       },
       "comments": {
        "totalCount": 1
       }
      },
      {
       "state": "COMMENTED",
       "submittedAt": "2026-09-09T05:25:08Z",
       "commit": {
        "oid": "53b343204e072b22f6511ac68bf6284aa2c418c2"
       },
       "author": {
        "login": "chatgpt-codex-connector",
        "__typename": "Bot"
       },
       "comments": {
        "totalCount": 1
       }
      }
     ]
    },
    "approvedReviews": {
     "nodes": []
    }
   }
  }
 }
}`

func TestEnrichRendersCapturedCodexbarFixture(t *testing.T) {
	infos, err := parseSupplementalResponse([]byte(capturedCodexbarSupplementalResponse))
	if err != nil {
		t.Fatalf("parseSupplementalResponse returned error: %v", err)
	}
	if _, ok := infos[333]; !ok {
		t.Fatalf("fixture must parse PR 333, got %v", infos)
	}

	now := time.Date(2026, 9, 9, 4, 40, 0, 0, time.UTC)
	prs := []pullRequest{{
		Number:    333,
		Title:     "Show reset times on Gemini coding quota metrics",
		State:     "OPEN",
		UpdatedAt: now,
		StatusCheckRollup: []checkItem{{
			Typename: "CheckRun", Name: "build", WorkflowName: "CI", Status: "IN_PROGRESS",
		}},
	}}
	rendered := enrichPullRequests(prs, prSupplementalData{Info: infos}, nil, nil, now)
	row := rendered[0]
	if row.Issues != "#332" {
		t.Fatalf("Issues = %q, want #332 from captured closing relationship", row.Issues)
	}
	if len(row.issueRefs) != 1 || row.issueRefs[0].Number != 332 {
		t.Fatalf("issueRefs = %#v, want issue #332", row.issueRefs)
	}
	if row.Comments != "3/4" {
		t.Fatalf("Comments = %q, want 3/4 from captured threads", row.Comments)
	}
	if row.AIReview != "fail" {
		t.Fatalf("AIReview = %q, want fail for the completed current-head Codex review with an unresolved finding", row.AIReview)
	}
	if row.Checks != "pending" {
		t.Fatalf("Checks = %q, want pending reported independently of enrichment", row.Checks)
	}

	// Table and JSON outputs must agree on the same rendered rows.
	var jsonBuf bytes.Buffer
	if err := renderListOutput(&jsonBuf, listOptions{json: true}, rendered); err != nil {
		t.Fatalf("renderListOutput(json) error: %v", err)
	}
	var decoded []displayPullRequest
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("JSON rows = %d, want 1", len(decoded))
	}
	if decoded[0].Issues != row.Issues || decoded[0].Comments != row.Comments ||
		decoded[0].AIReview != row.AIReview || decoded[0].Checks != row.Checks {
		t.Fatalf("JSON row %#v disagrees with table row %#v", decoded[0], row)
	}
}

func TestFetchPRSupplementalBatchRecoversHealthyAliasesFromPartialError(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		body := `{"data":{"repository":{"pr333":{"number":333,"headRefOid":"53b343204e072b22f6511ac68bf6284aa2c418c2","closingIssuesReferences":{"totalCount":1,"nodes":[{"number":332,"url":"https://github.com/HemSoft/codexbar-ios/issues/332"}]},"comments":{"totalCount":0,"nodes":[]},"reviewThreads":{"totalCount":0,"nodes":[]},"reviews":{"totalCount":0,"nodes":[]},"approvedReviews":{"nodes":[]}},"pr999":null}},"errors":[{"type":"NOT_FOUND","path":["repository","pr999"],"message":"Could not resolve to a PullRequest with the number of 999."}]}`
		return *bytes.NewBufferString(body), *bytes.NewBufferString("gh: Could not resolve to a PullRequest with the number of 99999.\n"), errors.New("exit status 1")
	}

	infos, unavailable, err := fetchPRSupplementalBatch("HemSoft", "codexbar-ios", "github.com", []int{333, 999})
	if err == nil {
		t.Fatal("partial GraphQL error must be carried for display")
	}
	if !strings.Contains(err.Error(), "Could not resolve to a PullRequest") {
		t.Fatalf("error should carry the gh diagnostic, got %v", err)
	}
	if len(infos[333].ClosingIssues) != 1 || infos[333].ClosingIssues[0].Number != 332 {
		t.Fatalf("healthy alias data must survive a partial batch error, got %#v", infos[333])
	}
	if !unavailable[999] {
		t.Fatalf("unparsed alias 999 must be unavailable, got %v", unavailable)
	}
	if unavailable[333] {
		t.Fatal("healthy alias 333 must not be marked unavailable")
	}
}

func TestFetchGraphQLKeepsGenuineFailureClosed(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, *bytes.NewBufferString("gh: You have exceeded a secondary rate limit. Please wait a few minutes before you try again.\n"), errors.New("exit status 1")
	}

	data, err := fetchGraphQL("github.com", "query { repository { id } }")
	if err == nil {
		t.Fatal("failure without a data envelope must return an error")
	}
	if data != nil {
		t.Fatalf("failure without a data envelope must not return data, got %s", data)
	}
	if !strings.Contains(err.Error(), "secondary rate limit") {
		t.Fatalf("error should carry the actionable gh diagnostic, got %v", err)
	}
}

func TestFetchSupplementalDataFailsClosedWhenRepoUnresolved(t *testing.T) {
	saved := ghExecFunc
	defer func() { ghExecFunc = saved }()
	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, bytes.Buffer{}, errors.New("repo resolution failed")
	}

	prs := []pullRequest{{Number: 7}, {Number: 9}}
	data, owner, name := fetchSupplementalData("no-slash", prs)
	if owner != "" || name != "" {
		t.Fatalf("owner/name = %q/%q, want empty", owner, name)
	}
	if !data.Unavailable[7] || !data.Unavailable[9] {
		t.Fatalf("unresolved repo must fail closed for every PR, got %v", data.Unavailable)
	}
	if data.Err == nil {
		t.Fatal("expected the resolution error to be carried for display")
	}
}

func TestSupplementalNotice(t *testing.T) {
	if got := supplementalNotice(nil); got != "" {
		t.Fatalf("supplementalNotice(nil) = %q, want empty", got)
	}
	long := errors.New("gh: " + strings.Repeat("boom ", 60))
	got := supplementalNotice(long)
	if strings.ContainsAny(got, "\n\r") {
		t.Fatalf("notice must stay single-line, got %q", got)
	}
	if len(got) > 200 {
		t.Fatalf("notice must stay short for display, got %d chars", len(got))
	}
}

func TestParseSupplementalNodeDropsPartialFieldFailure(t *testing.T) {
	// A partial GraphQL failure nulls the failed field inside an otherwise
	// valid PR object. The entry must not parse as available: unknown
	// threads would otherwise render as empty comments and a clean AI
	// review. Reproduces the Codex P1 finding on PR #89.
	raw := json.RawMessage(`{"number":333,"headRefOid":"53b343204e072b22f6511ac68bf6284aa2c418c2","closingIssuesReferences":{"totalCount":1,"nodes":[{"number":332,"url":"https://github.com/HemSoft/codexbar-ios/issues/332"}]},"comments":{"totalCount":2,"nodes":[{"body":"hi","author":{"login":"user","__typename":"User"}}]},"reviewThreads":null,"reviews":{"nodes":[]},"approvedReviews":{"nodes":[]}}`)

	envelope := `{"data":{"repository":{"pr333":` + string(raw) + `}},"errors":[{"type":"NOT_FOUND","path":["repository","pr333","reviewThreads"],"message":"Field failed"}]}`
	infos, parseErr := parseSupplementalResponse([]byte(envelope))
	if parseErr != nil {
		t.Fatalf("envelope with partial errors must still parse: %v", parseErr)
	}
	if _, present := infos[333]; present {
		t.Fatalf("PR 333 must stay out of the parsed set, got %#v", infos[333])
	}

	unavailable := unavailablePRNumbers([]int{333}, infos)
	if !unavailable[333] {
		t.Fatalf("PR 333 with a failed field must be unavailable, got %v", unavailable)
	}

	// The enrich path then renders unknown columns instead of empty data.
	now := time.Date(2026, 9, 9, 4, 40, 0, 0, time.UTC)
	rendered := enrichPullRequests(
		[]pullRequest{{Number: 333, State: "OPEN", UpdatedAt: now}},
		prSupplementalData{Info: infos, Unavailable: unavailable, Err: fmt.Errorf("gh api graphql: Field failed")},
		nil, nil, now,
	)
	if rendered[0].Comments != "?" || rendered[0].AIReview != "?" || rendered[0].Issues != "?" {
		t.Fatalf("partial field failure must render unknown, got issues=%q comments=%q ai=%q",
			rendered[0].Issues, rendered[0].Comments, rendered[0].AIReview)
	}
}

func TestFetchPRSupplementalSynthesizesPartialPayloadError(t *testing.T) {
	saved := fetchPRSupplementalBatchFunc
	defer func() { fetchPRSupplementalBatchFunc = saved }()

	// A recovered payload that omitted the alias renders ? rows; the wrapper
	// must still produce a diagnostic instead of failing silently.
	fetchPRSupplementalBatchFunc = func(owner, name, host string, prNumbers []int) (map[int]prSupplementalInfo, map[int]bool, error) {
		return nil, map[int]bool{5: true}, nil
	}

	result, unavailable, err := fetchPRSupplemental("owner", "repo", "github.com", []int{5, 6})
	if err == nil {
		t.Fatal("unavailable aliases without an underlying error must synthesize a diagnostic")
	}
	if !strings.Contains(err.Error(), "1 of 2 requested pull requests") {
		t.Fatalf("synthesized error should count unavailable aliases, got %v", err)
	}
	if !unavailable[5] || len(result) != 0 {
		t.Fatalf("expected PR 5 unavailable, got result=%v unavailable=%v", result, unavailable)
	}
}

func TestRequiredChecksError(t *testing.T) {
	if got := requiredChecksError(nil); got != nil {
		t.Fatalf("requiredChecksError(nil) = %v, want nil", got)
	}
	err := requiredChecksError(map[string]error{"main": errors.New("unavailable"), "develop": errors.New("unavailable")})
	text := err.Error()
	if !strings.Contains(text, "base develop") || !strings.Contains(text, "base main") || !strings.Contains(text, "unavailable") {
		t.Fatalf("requiredChecksError text = %q, want both branches and reasons", text)
	}
	if strings.Index(text, "develop") > strings.Index(text, "main") {
		t.Fatalf("branches must be listed deterministically, got %q", text)
	}
}

// runAuxiliaryNoticeListExec drives executeList through fetchPullRequestList
// with a mocked gh subprocess, returning both output buffers.
func runAuxiliaryNoticeListExec(t *testing.T, options listOptions) (bytes.Buffer, bytes.Buffer) {
	t.Helper()
	saved := ghExecFunc
	t.Cleanup(func() { ghExecFunc = saved })

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		joined := strings.Join(args, " ")
		out := bytes.Buffer{}
		switch {
		case strings.Contains(joined, "pr list"):
			out.WriteString(`[{"number":42,"title":"Auxiliary diagnostics","state":"OPEN","updatedAt":"2026-09-09T05:00:00Z","headRefName":"feature","baseRefName":"main","url":"https://github.com/owner/repo/pull/42"}]`)
		case args[0] == "api" && len(args) > 1 && strings.HasPrefix(args[1], "repos/"):
			out.WriteString("not-json")
		case args[0] == "api" && strings.Contains(joined, " graphql"):
			return bytes.Buffer{}, *bytes.NewBufferString("gh: You have exceeded a secondary rate limit. Please wait a few minutes before you try again.\n"), errors.New("exit status 1")
		default:
			out.WriteString("[]")
		}
		return out, bytes.Buffer{}, nil
	}

	var stdout, stderr bytes.Buffer
	if err := executeList(options, &stdout, &stderr); err != nil {
		t.Fatalf("executeList error: %v", err)
	}
	return stdout, stderr
}

func TestExecuteListRendersAuxiliaryNotices(t *testing.T) {
	stdout, stderr := runAuxiliaryNoticeListExec(t, listOptions{repo: "owner/repo", limit: 30, state: "open"})
	table := stdout.String()
	if !strings.Contains(table, "#42") {
		t.Fatalf("table should render the listed PR:\n%s", table)
	}
	if !strings.Contains(table, "Supplemental data unavailable: gh api graphql: gh: You have exceeded a secondary rate limit.") {
		t.Fatalf("table should print the supplemental diagnostic:\n%s", table)
	}
	if !strings.Contains(table, "Required check rules unavailable: base main: required check rules: malformed response") {
		t.Fatalf("table should print the required-checks diagnostic:\n%s", table)
	}
	if stderr.Len() != 0 {
		t.Fatalf("table mode should keep diagnostics on stdout, got stderr %q", stderr.String())
	}
}

func TestExecuteListAuxiliaryNoticesGoToStderrInJSONMode(t *testing.T) {
	stdout, stderr := runAuxiliaryNoticeListExec(t, listOptions{repo: "owner/repo", limit: 30, state: "open", json: true})
	var decoded []displayPullRequest
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("JSON stdout must decode as the row array: %v\n%s", err, stdout.String())
	}
	if len(decoded) != 1 || decoded[0].Number != 42 || decoded[0].AIReview != "?" {
		t.Fatalf("JSON rows must carry the listed PR with unknown supplemental columns, got %#v", decoded)
	}
	if !strings.Contains(stderr.String(), "Supplemental data unavailable:") {
		t.Fatalf("JSON stderr should carry the supplemental diagnostic:\n%s", stderr.String())
	}
	if !strings.Contains(stderr.String(), "Required check rules unavailable:") {
		t.Fatalf("JSON stderr should carry the required-checks diagnostic:\n%s", stderr.String())
	}
}

func TestRunViewRendersSupplementalDataAndNotices(t *testing.T) {
	saved := ghExecFunc
	t.Cleanup(func() { ghExecFunc = saved })

	ghExecFunc = func(args ...string) (bytes.Buffer, bytes.Buffer, error) {
		joined := strings.Join(args, " ")
		out := bytes.Buffer{}
		switch {
		case strings.Contains(joined, "pr view 333"):
			out.WriteString(`{"number":333,"title":"Show reset times on Gemini coding quota metrics","state":"OPEN","updatedAt":"2026-09-09T05:00:00Z","headRefName":"issue-332-gemini","baseRefName":"main","url":"https://github.com/HemSoft/codexbar-ios/pull/333"}`)
		case args[0] == "api" && len(args) > 1 && strings.HasPrefix(args[1], "repos/"):
			out.WriteString("[]")
		case args[0] == "api" && strings.Contains(joined, " graphql"):
			out.WriteString(capturedCodexbarSupplementalResponse)
		default:
			out.WriteString("[]")
		}
		return out, bytes.Buffer{}, nil
	}

	var stdout, stderr bytes.Buffer
	if err := runView([]string{"333", "--repo", "HemSoft/codexbar-ios"}, &stdout, &stderr); err != nil {
		t.Fatalf("runView error: %v", err)
	}
	table := stdout.String()
	if !strings.Contains(table, "#333") || !strings.Contains(table, "#332") {
		t.Fatalf("runView should render the PR with its captured relationship:\n%s", table)
	}
	if !strings.Contains(table, "3/4") || !strings.Contains(table, "fail") {
		t.Fatalf("runView should render captured thread and AI columns:\n%s", table)
	}
	if stderr.Len() != 0 {
		t.Fatalf("complete enrichment should print no diagnostics, got stderr %q", stderr.String())
	}
}

func TestParseSupplementalResponseDropsIncompleteConnectionObjects(t *testing.T) {
	tests := []struct {
		name    string
		field   string
		payload string
	}{
		{name: "missing totalCount", field: "reviewThreads", payload: `{"nodes":[]}`},
		{name: "null totalCount", field: "reviews", payload: `{"totalCount":null,"nodes":[]}`},
		{name: "missing nodes", field: "comments", payload: `{"totalCount":0}`},
		{name: "null nodes", field: "approvedReviews", payload: `{"nodes":null}`},
		{name: "empty object", field: "reviewThreads", payload: `{}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := fmt.Sprintf(`{"data":{"repository":{"pr9":{"number":9,"comments":{"totalCount":0,"nodes":[]},"reviewThreads":{"totalCount":0,"nodes":[]},"reviews":{"totalCount":0,"nodes":[]},"approvedReviews":{"nodes":[]},%q:%s}}}}`, test.field, test.payload)
			infos, err := parseSupplementalResponse([]byte(envelope))
			if err != nil {
				t.Fatalf("parseSupplementalResponse error: %v", err)
			}
			if _, present := infos[9]; present {
				t.Fatalf("PR 9 with incomplete %s must not parse as available: %#v", test.field, infos[9])
			}
		})
	}
}

func TestIncompleteConnectionsCarryDiagnostic(t *testing.T) {
	saved := fetchPRSupplementalBatchFunc
	defer func() { fetchPRSupplementalBatchFunc = saved }()

	// A PR with more threads than one page returns a truncated connection:
	// the PR stays rendered with its healthy fields while AI data is ?.
	fetchPRSupplementalBatchFunc = func(owner, name, host string, prNumbers []int) (map[int]prSupplementalInfo, map[int]bool, error) {
		return map[int]prSupplementalInfo{
			7: {AIReview: "?", Incomplete: true},
		}, nil, nil
	}

	data, _, _ := fetchSupplementalData("owner/repo", []pullRequest{{Number: 7}})
	if data.Err == nil {
		t.Fatal("truncated connections must produce a diagnostic when no fetch error exists")
	}
	if !strings.Contains(data.Err.Error(), "truncated supplemental connections for pull request(s) 7") {
		t.Fatalf("diagnostic should name the affected PR, got %v", data.Err)
	}
	if data.Unavailable[7] {
		t.Fatal("truncated PR stays rendered with healthy fields, not unavailable")
	}
}

func TestEnrichPullRequestsDowngradesChecksOnFailedRules(t *testing.T) {
	now := time.Date(2026, 9, 9, 4, 40, 0, 0, time.UTC)
	prs := []pullRequest{{
		Number:      42,
		Title:       "Unverifiable required checks",
		State:       "OPEN",
		UpdatedAt:   now,
		BaseRefName: "main",
		StatusCheckRollup: []checkItem{{
			Typename: "CheckRun", Name: "build", WorkflowName: "CI", Status: "COMPLETED", Conclusion: "SUCCESS",
		}},
	}}

	rendered := enrichPullRequests(prs, prSupplementalData{}, nil, map[string]error{"main": errors.New("required check rules: malformed response")}, now)
	if rendered[0].Checks != "pending" {
		t.Fatalf("Checks = %q, want pending when required rules cannot be fetched", rendered[0].Checks)
	}
	if !rendered[0].checksDowngraded {
		t.Fatal("expected the failed-rules downgrade to be recorded")
	}
}

func TestParseSupplementalResponseDropsBrokenThreadNodes(t *testing.T) {
	tests := []struct {
		name       string
		brokenNode string
	}{
		{name: "null thread node", brokenNode: `null`},
		{name: "missing isResolved", brokenNode: `{"comments":{"nodes":[{"author":{"login":"bot[bot]","__typename":"Bot"}}]}}`},
		{name: "null isResolved", brokenNode: `{"isResolved":null,"comments":{"nodes":[]}}`},
		{name: "missing comments", brokenNode: `{"isResolved":false}`},
		{name: "null comments", brokenNode: `{"isResolved":false,"comments":null}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := fmt.Sprintf(`{"data":{"repository":{"pr9":{"number":9,"comments":{"totalCount":0,"nodes":[]},"reviewThreads":{"totalCount":1,"nodes":[%s]},"reviews":{"totalCount":0,"nodes":[]},"approvedReviews":{"nodes":[]}}}}}`, test.brokenNode)
			infos, err := parseSupplementalResponse([]byte(envelope))
			if err != nil {
				t.Fatalf("parseSupplementalResponse error: %v", err)
			}
			if _, present := infos[9]; present {
				t.Fatalf("PR 9 with broken thread node %s must not parse as available: %#v", test.brokenNode, infos[9])
			}
		})
	}
}

func TestTruncatedThreadsRenderUnknownComments(t *testing.T) {
	now := time.Date(2026, 9, 9, 4, 40, 0, 0, time.UTC)
	prs := []pullRequest{{Number: 8, State: "OPEN", UpdatedAt: now}}
	rendered := enrichPullRequests(prs, prSupplementalData{Info: map[int]prSupplementalInfo{
		8: {Threads: reviewThreadInfo{Total: 101, Resolved: 100}, ThreadsTruncated: true, AIReview: "pass"},
	}}, nil, nil, now)
	if rendered[0].Comments != "?" {
		t.Fatalf("Comments = %q, want ? for a truncated thread page", rendered[0].Comments)
	}
	if rendered[0].AIReview != "pass" {
		t.Fatalf("healthy AI data must stay rendered beside truncated threads, got %q", rendered[0].AIReview)
	}
}

func TestEvidenceAmbiguityCarriesOwnDiagnostic(t *testing.T) {
	saved := fetchPRSupplementalBatchFunc
	defer func() { fetchPRSupplementalBatchFunc = saved }()

	fetchPRSupplementalBatchFunc = func(owner, name, host string, prNumbers []int) (map[int]prSupplementalInfo, map[int]bool, error) {
		return map[int]prSupplementalInfo{
			3: {EvidenceAmbiguous: true},
		}, nil, nil
	}

	data, _, _ := fetchSupplementalData("owner/repo", []pullRequest{{Number: 3}})
	if data.Err == nil {
		t.Fatal("evidence ambiguity must produce a diagnostic when no fetch error exists")
	}
	if !strings.Contains(data.Err.Error(), "cannot order AI review evidence for pull request(s) 3") {
		t.Fatalf("diagnostic should name ambiguity specifically, got %v", data.Err)
	}
	if strings.Contains(data.Err.Error(), "truncated supplemental connections") {
		t.Fatalf("ambiguity alone must not be reported as truncation, got %v", data.Err)
	}
}

func TestJoinSupplementalReasons(t *testing.T) {
	if joinSupplementalReasons(nil, nil) != nil {
		t.Fatal("no reasons must join to nil")
	}
	joined := joinSupplementalReasons(nil, errors.New("b"))
	if joined == nil || joined.Error() != "b" {
		t.Fatalf("single reason must survive joining, got %v", joined)
	}
	joined = joinSupplementalReasons(errors.New("a"), errors.New("b"))
	if joined == nil || !strings.Contains(joined.Error(), "a; b") {
		t.Fatalf("multiple reasons must join with ; got %v", joined)
	}
}

func TestEnrichPullRequestsKeepsReviewStateOnFailedRules(t *testing.T) {
	now := time.Date(2026, 9, 9, 4, 40, 0, 0, time.UTC)
	prs := []pullRequest{{
		Number:      50,
		State:       "OPEN",
		UpdatedAt:   now,
		BaseRefName: "main",
		StatusCheckRollup: []checkItem{{
			Typename: "CheckRun", Name: "cubic · AI code reviewer", WorkflowName: "Cubic",
			Status: "IN_PROGRESS",
		}},
	}}

	rendered := enrichPullRequests(prs, prSupplementalData{}, nil, map[string]error{"main": errors.New("required check rules: malformed response")}, now)
	if rendered[0].Checks != "review" {
		t.Fatalf("Checks = %q, want review kept when rules fetch fails while an AI reviewer runs", rendered[0].Checks)
	}

	rendered[0].Checks = "pass"
	downgradeChecksIfMissing(&rendered[0], nil, map[string]error{"main": errors.New("offline")}, "main", nil)
	if rendered[0].Checks != "pending" || !rendered[0].checksDowngraded {
		t.Fatalf("a pass under failed rules must still downgrade to pending, got %q", rendered[0].Checks)
	}
}
