package main

import (
	"bytes"
	"fmt"
	"testing"
)

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
	result, err := fetchPRSupplemental("owner", "repo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil for empty input, got %v", result)
	}
}

func TestFetchPRSupplemental_SingleBatch(t *testing.T) {
	saved := fetchPRSupplementalBatchFunc
	defer func() { fetchPRSupplementalBatchFunc = saved }()

	fetchPRSupplementalBatchFunc = func(owner, name string, prNumbers []int) (map[int]prSupplementalInfo, error) {
		result := make(map[int]prSupplementalInfo)
		for _, n := range prNumbers {
			result[n] = prSupplementalInfo{AIReview: "clean"}
		}
		return result, nil
	}

	result, err := fetchPRSupplemental("owner", "repo", []int{1, 2, 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 results, got %d", len(result))
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
	fetchPRSupplementalBatchFunc = func(owner, name string, prNumbers []int) (map[int]prSupplementalInfo, error) {
		batchCalls++
		result := make(map[int]prSupplementalInfo)
		for _, n := range prNumbers {
			result[n] = prSupplementalInfo{Approvals: batchCalls}
		}
		return result, nil
	}

	// Create 35 PRs to force 2 batches (batch size is 30)
	prs := make([]int, 35)
	for i := range prs {
		prs[i] = i + 1
	}

	result, err := fetchPRSupplemental("owner", "repo", prs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batchCalls != 2 {
		t.Fatalf("expected 2 batch calls, got %d", batchCalls)
	}
	if len(result) != 35 {
		t.Fatalf("expected 35 results, got %d", len(result))
	}
}

func TestFetchPRSupplemental_BatchError(t *testing.T) {
	saved := fetchPRSupplementalBatchFunc
	defer func() { fetchPRSupplementalBatchFunc = saved }()

	fetchPRSupplementalBatchFunc = func(owner, name string, prNumbers []int) (map[int]prSupplementalInfo, error) {
		return nil, fmt.Errorf("graphql error")
	}

	_, err := fetchPRSupplemental("owner", "repo", []int{1, 2})
	if err == nil || err.Error() != "graphql error" {
		t.Fatalf("expected graphql error, got %v", err)
	}
}
