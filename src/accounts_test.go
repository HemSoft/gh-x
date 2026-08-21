package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func resetAccountCache() {
	accountsMu.Lock()
	defer accountsMu.Unlock()
	cachedAccounts = nil
	cachedTokens = map[string]string{}
}

func withFallbackStubs(t *testing.T, transport func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error), accounts []ghAccount, tokens map[string]string) *bytes.Buffer {
	t.Helper()
	savedTransport := ghTransportFunc
	savedList := listAccountsFunc
	savedToken := accountTokenFunc
	savedWriter := accountWarningWriter
	t.Cleanup(func() {
		ghTransportFunc = savedTransport
		listAccountsFunc = savedList
		accountTokenFunc = savedToken
		accountWarningWriter = savedWriter
		resetAccountCache()
	})
	resetAccountCache()
	ghTransportFunc = transport
	listAccountsFunc = func() []ghAccount { return accounts }
	accountTokenFunc = func(login string) (string, bool) {
		token, ok := tokens[login]
		return token, ok
	}
	notices := &bytes.Buffer{}
	accountWarningWriter = notices
	return notices
}

func TestExecGHFallsBackToAlternateAccount(t *testing.T) {
	calls := 0
	notices := withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		if calls == 1 {
			if len(inv.ExtraEnv) != 0 {
				t.Fatal("first attempt must not carry an injected token")
			}
			return bytes.Buffer{}, *bytes.NewBufferString("HTTP 404: Not Found"), errors.New("exit status 1")
		}
		if len(inv.ExtraEnv) != 1 || inv.ExtraEnv[0] != "GH_TOKEN=alt-token" {
			t.Fatalf("expected GH_TOKEN on retry, got %v", inv.ExtraEnv)
		}
		return *bytes.NewBufferString("[]"), bytes.Buffer{}, nil
	},
		[]ghAccount{{Login: "primary", Active: true}, {Login: "secondary", Active: false}},
		map[string]string{"secondary": "alt-token"},
	)

	out, _, err := execGH("pr", "list")
	if err != nil {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if out.String() != "[]" {
		t.Fatalf("expected retry output, got %q", out.String())
	}
	if calls != 2 {
		t.Fatalf("expected two transport calls, got %d", calls)
	}
	if !strings.Contains(notices.String(), "secondary") {
		t.Fatalf("expected fallback notice naming the account, got %q", notices.String())
	}
}

func TestExecGHPreservesOriginalErrorWhenFallbackFails(t *testing.T) {
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, *bytes.NewBufferString("Not Found (HTTP 404)"), errors.New("boom")
	},
		[]ghAccount{{Login: "primary", Active: true}, {Login: "secondary", Active: false}},
		map[string]string{"secondary": "alt-token"},
	)

	_, stderr, err := execGH("api", "repos/owner/private")
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected original error preserved, got %v", err)
	}
	if !strings.Contains(stderr.String(), "404") {
		t.Fatalf("expected original stderr preserved, got %q", stderr.String())
	}
}

func TestExecGHSkipsFallbackWithoutAlternates(t *testing.T) {
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		return bytes.Buffer{}, *bytes.NewBufferString("Not Found"), errors.New("exit status 1")
	}, []ghAccount{{Login: "solo", Active: true}}, map[string]string{})

	_, _, err := execGH("api", "repos/owner/private")
	if err == nil {
		t.Fatal("expected failure without alternates")
	}
	if calls != 1 {
		t.Fatalf("fallback must not run with a single account, got %d calls", calls)
	}
}

func TestExecGHSkipsAuthCommands(t *testing.T) {
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		return bytes.Buffer{}, *bytes.NewBufferString("Not Found"), errors.New("exit status 1")
	}, []ghAccount{{Login: "a", Active: true}, {Login: "b", Active: false}}, map[string]string{"b": "tok"})

	_, _, _ = execGH("auth", "status")
	if calls != 1 {
		t.Fatalf("auth commands must never fall back, got %d calls", calls)
	}
}

func TestExecGHSkipsFallbackWhenTokenEnvOverrideSet(t *testing.T) {
	t.Setenv("GH_TOKEN", "explicit")
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		return bytes.Buffer{}, *bytes.NewBufferString("Not Found"), errors.New("exit status 1")
	}, []ghAccount{{Login: "a", Active: true}, {Login: "b", Active: false}}, map[string]string{"b": "tok"})

	_, _, _ = execGH("api", "repos/owner/private")
	if calls != 1 {
		t.Fatalf("explicit GH_TOKEN override must disable fallback, got %d calls", calls)
	}
}

func TestExecGHNoRetryForNonAccessErrors(t *testing.T) {
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		return bytes.Buffer{}, *bytes.NewBufferString("flag provided but not defined"), errors.New("exit status 1")
	}, []ghAccount{{Login: "a", Active: true}, {Login: "b", Active: false}}, map[string]string{"b": "tok"})

	_, _, _ = execGH("pr", "list")
	if calls != 1 {
		t.Fatalf("non-access errors must not fall back, got %d calls", calls)
	}
}

func TestExecGHWithInputFallsBackOnPost(t *testing.T) {
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		if calls == 2 && string(inv.Stdin) != "payload" {
			t.Fatalf("retry must replay stdin, got %q", string(inv.Stdin))
		}
		if calls == 1 {
			return bytes.Buffer{}, *bytes.NewBufferString("Resource not accessible by integration"), errors.New("exit status 1")
		}
		return bytes.Buffer{}, bytes.Buffer{}, nil
	},
		[]ghAccount{{Login: "a", Active: true}, {Login: "b", Active: false}},
		map[string]string{"b": "tok"},
	)

	if _, _, err := execGHWithInput([]string{"api", "repos/o/n/pulls/1/reviews"}, []byte("payload")); err != nil {
		t.Fatalf("expected POST fallback success, got %v", err)
	}
}

func TestParseAuthStatusJSON(t *testing.T) {
	data := []byte(`{
		"hosts": {
			"github.com": [
				{"login": "HemSoft", "active": true, "state": "success"},
				{"login": "", "active": false, "state": "success"},
				{"login": "expired", "active": false, "state": "unauthenticated"},
				{"login": "fhemmerrelias", "active": false, "state": "success"}
			],
			"ghe.example.com": [
				{"login": "enterprise-only", "active": true, "state": "success"}
			]
		}
	}`)

	accounts := parseAuthStatusJSON(data)
	if len(accounts) != 2 {
		t.Fatalf("expected 2 github.com accounts, got %#v", accounts)
	}
	if accounts[0].Login != "HemSoft" || !accounts[0].Active {
		t.Fatalf("unexpected first account %#v", accounts[0])
	}
	if accounts[1].Login != "fhemmerrelias" || accounts[1].Active {
		t.Fatalf("unexpected second account %#v", accounts[1])
	}
}

func TestParseAuthStatusJSONInvalidPayload(t *testing.T) {
	if accounts := parseAuthStatusJSON([]byte("not json")); len(accounts) != 0 {
		t.Fatalf("expected no accounts for invalid JSON, got %#v", accounts)
	}
}

func TestIsAccessError(t *testing.T) {
	cases := []struct {
		stderr string
		want   bool
	}{
		{"gh: Not Found (HTTP 404)", true},
		{"GraphQL: Resource not accessible by integration", true},
		{"Could not resolve to a PullRequest with the number 42.", true},
		{"GET /repos/o/p: 403 forbidden []", true},
		{"api: Bad credentials (HTTP 401)", true},
		{"required_status_checks rules malformed", false},
		{"flag provided but not defined: -watch", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isAccessError(tc.stderr); got != tc.want {
			t.Errorf("isAccessError(%q) = %v, want %v", tc.stderr, got, tc.want)
		}
	}
}

func TestDiscoveryAndTokenResolveOncePerProcess(t *testing.T) {
	authStatusCalls, tokenCalls, listCalls := 0, 0, 0
	notices := withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		switch {
		case len(inv.Args) > 1 && inv.Args[0] == "auth" && inv.Args[1] == "status":
			authStatusCalls++
			payload := `{"hosts":{"github.com":[{"login":"a","active":true,"state":"success"},{"login":"b","active":false,"state":"success"}]}}`
			return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
		case len(inv.Args) > 0 && inv.Args[0] == "auth":
			tokenCalls++
			return *bytes.NewBufferString("tok-123\n"), bytes.Buffer{}, nil
		default:
			listCalls++
			if listCalls%2 == 1 {
				return bytes.Buffer{}, *bytes.NewBufferString("Not Found"), errors.New("exit status 1")
			}
			return bytes.Buffer{}, bytes.Buffer{}, nil
		}
	}, nil, nil)
	listAccountsFunc = listAccounts
	accountTokenFunc = defaultAccountToken

	for i := 0; i < 2; i++ {
		if _, _, err := execGH("pr", "list"); err != nil {
			t.Fatalf("command %d: expected fallback success, got %v", i, err)
		}
	}

	if authStatusCalls != 1 {
		t.Fatalf("expected discovery once per process, got %d", authStatusCalls)
	}
	if tokenCalls != 1 {
		t.Fatalf("expected token resolution once per account, got %d", tokenCalls)
	}
	if !strings.Contains(notices.String(), "b") {
		t.Fatalf("expected fallback notices, got %q", notices.String())
	}
}
