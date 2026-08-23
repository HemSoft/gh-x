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
	cachedAccounts = map[string][]ghAccount{}
	cachedTokens = map[string]string{}
}

// resetRemoteCache clears the memoized git remote probe between scenarios.
func resetRemoteCache() {
	remoteMu.Lock()
	defer remoteMu.Unlock()
	cachedRemote = ""
	remoteResolved = false
}

func withFallbackStubs(t *testing.T, transport func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error), accounts []ghAccount, tokens map[string]string) *bytes.Buffer {
	t.Helper()
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")
	t.Setenv("GH_REPO", "")
	t.Setenv("GH_HOST", "")
	savedTransport := ghTransportFunc
	savedList := listAccountsFunc
	savedToken := accountTokenFunc
	savedWriter := accountWarningWriter
	savedRemote := gitRemoteURLFunc
	t.Cleanup(func() {
		ghTransportFunc = savedTransport
		listAccountsFunc = savedList
		accountTokenFunc = savedToken
		accountWarningWriter = savedWriter
		gitRemoteURLFunc = savedRemote
		resetAccountCache()
		resetRemoteCache()
	})
	resetAccountCache()
	resetRemoteCache()
	ghTransportFunc = transport
	listAccountsFunc = func(string) []ghAccount { return accounts }
	accountTokenFunc = func(login, _ string) (string, bool) {
		token, ok := tokens[login]
		return token, ok
	}
	gitRemoteURLFunc = func() string { return "" }
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

func TestExecGHFallsBackOnEnterpriseHostWithEnterpriseCredential(t *testing.T) {
	calls := 0
	notices := withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		if calls == 1 {
			return bytes.Buffer{}, *bytes.NewBufferString("HTTP 404: Not Found"), errors.New("exit status 1")
		}
		want := []string{
			"GH_ENTERPRISE_TOKEN=ent-token",
			"GITHUB_ENTERPRISE_TOKEN=ent-token",
		}
		if strings.Join(inv.ExtraEnv, "|") != strings.Join(want, "|") {
			t.Fatalf("expected enterprise credentials on retry, got %v", inv.ExtraEnv)
		}
		if got := targetHost(inv.Args); got != "ghe.example.com" {
			t.Fatalf("retry should keep the enterprise host, got %q", got)
		}
		return *bytes.NewBufferString("[]"), bytes.Buffer{}, nil
	}, nil, nil)
	listAccountsFunc = func(host string) []ghAccount {
		if host == "ghe.example.com" {
			return []ghAccount{{Login: "corp-lead", Active: true}, {Login: "corp-dev", Active: false}}
		}
		return []ghAccount{{Login: "personal", Active: true}, {Login: "other-personal", Active: false}}
	}
	accountTokenFunc = func(login, host string) (string, bool) {
		if login == "corp-dev" && host == "ghe.example.com" {
			return "ent-token", true
		}
		return "", false
	}

	out, _, err := execGH("pr", "list", "--repo", "GHE.Example.com/acme/widgets")
	if err != nil {
		t.Fatalf("expected enterprise fallback success, got %v", err)
	}
	if out.String() != "[]" {
		t.Fatalf("expected retry output, got %q", out.String())
	}
	if calls != 2 {
		t.Fatalf("github.com accounts must not be tried for an enterprise host, got %d calls", calls)
	}
	if !strings.Contains(notices.String(), "corp-dev (ghe.example.com)") {
		t.Fatalf("notice should name account and host, got %q", notices.String())
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
	for _, variable := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		t.Run(variable, func(t *testing.T) {
			calls := 0
			withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
				calls++
				return bytes.Buffer{}, *bytes.NewBufferString("Not Found"), errors.New("exit status 1")
			}, []ghAccount{{Login: "a", Active: true}, {Login: "b", Active: false}}, map[string]string{"b": "tok"})
			t.Setenv(variable, "explicit")

			_, _, _ = execGH("api", "repos/owner/private")
			if calls != 1 {
				t.Fatalf("%s override must disable fallback, got %d calls", variable, calls)
			}
		})
	}
}

func TestExecGHFallsBackDespiteEnterpriseToken(t *testing.T) {
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		if calls == 1 {
			return bytes.Buffer{}, *bytes.NewBufferString("Not Found"), errors.New("exit status 1")
		}
		return bytes.Buffer{}, bytes.Buffer{}, nil
	}, []ghAccount{{Login: "a", Active: true}, {Login: "b", Active: false}}, map[string]string{"b": "tok"})
	t.Setenv("GH_ENTERPRISE_TOKEN", "ghs_enterprise")

	if _, _, err := execGH("api", "repos/owner/private"); err != nil {
		t.Fatalf("enterprise token must not disable github.com fallback, got %v", err)
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

// resetFallbackNotes clears the per-process notice dedupe between cases.
func resetFallbackNotes() {
	notifiedMu.Lock()
	defer notifiedMu.Unlock()
	notifiedLogins = map[string]bool{}
}

func TestExpandMeReference(t *testing.T) {
	saved := ghTransportFunc
	t.Cleanup(func() { ghTransportFunc = saved })
	ghTransportFunc = func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		if strings.Join(inv.Args, " ") != "api user --jq .login" {
			t.Fatalf("unexpected resolution call: %s", strings.Join(inv.Args, " "))
		}
		return *bytes.NewBufferString("active-login\n"), bytes.Buffer{}, nil
	}

	got, err := expandMeReference("@me")
	if err != nil || got != "active-login" {
		t.Fatalf(`expandMeReference("@me") = %q, %v; want active-login`, got, err)
	}
	got, err = expandMeReference("@ME")
	if err != nil || got != "active-login" {
		t.Fatalf(`expandMeReference("@ME") = %q, %v; want active-login`, got, err)
	}
	got, err = expandMeReference("me")
	if err != nil || got != "me" {
		t.Fatalf(`literal login "me" must pass through, got %q, %v`, got, err)
	}
	got, err = expandMeReference("someone-else")
	if err != nil || got != "someone-else" {
		t.Fatalf("non-me value must pass through, got %q, %v", got, err)
	}
}

func TestExpandSearchReferences(t *testing.T) {
	saved := ghTransportFunc
	t.Cleanup(func() { ghTransportFunc = saved })
	ghTransportFunc = func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		return *bytes.NewBufferString("active-login\n"), bytes.Buffer{}, nil
	}

	cases := []struct{ in, want string }{
		{"is:pr review-requested:@me", "is:pr review-requested:active-login"},
		{"author:@ME assignee:@Me", "author:active-login assignee:active-login"},
		{"author:@me assignee:@me", "author:active-login assignee:active-login"},
		{"mentions:@ME OR involves:@me", "mentions:active-login OR involves:active-login"},
		{"commented-by:x reviewed-by:@me", "commented-by:x reviewed-by:active-login"},
		{"label:@me", "label:@me"},
		{"notify @me", "notify @me"},
		{"user:@megalomaniac", "user:@megalomaniac"},
		{"@meeting", "@meeting"},
		{"x@me.com", "x@me.com"},
	}
	for _, tc := range cases {
		got, err := expandSearchReferences(tc.in)
		if err != nil {
			t.Fatalf("expandSearchReferences(%q) error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("expandSearchReferences(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExecGHActiveNeverFallsBack(t *testing.T) {
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		return bytes.Buffer{}, *bytes.NewBufferString("Not Found"), errors.New("exit status 1")
	}, []ghAccount{{Login: "a", Active: true}, {Login: "b", Active: false}}, map[string]string{"b": "tok"})

	_, _, err := execGHActive("api", "user")
	if err == nil {
		t.Fatal("expected the active-account error to surface")
	}
	if calls != 1 {
		t.Fatalf("identity-pinned execution must not retry, got %d calls", calls)
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
	if len(accounts["github.com"]) != 2 {
		t.Fatalf("expected 2 github.com accounts, got %#v", accounts)
	}
	if accounts["github.com"][0].Login != "HemSoft" || !accounts["github.com"][0].Active {
		t.Fatalf("unexpected first account %#v", accounts["github.com"][0])
	}
	if accounts["github.com"][1].Login != "fhemmerrelias" || accounts["github.com"][1].Active {
		t.Fatalf("unexpected second account %#v", accounts["github.com"][1])
	}
	if len(accounts["ghe.example.com"]) != 1 || accounts["ghe.example.com"][0].Login != "enterprise-only" {
		t.Fatalf("enterprise host entries must survive parsing, got %#v", accounts["ghe.example.com"])
	}
}

func TestParseAuthStatusJSONInvalidPayload(t *testing.T) {
	if accounts := parseAuthStatusJSON([]byte("not json")); len(accounts) != 0 {
		t.Fatalf("expected no accounts for invalid JSON, got %#v", accounts)
	}
}

func TestTargetHost(t *testing.T) {
	t.Setenv("GH_HOST", "")
	resetRemoteCache()
	savedRemote := gitRemoteURLFunc
	t.Cleanup(func() { gitRemoteURLFunc = savedRemote; resetRemoteCache() })
	gitRemoteURLFunc = func() string { return "" }

	cases := []struct {
		name string
		args []string
		env  string
		want string
	}{
		{"no repo flag", []string{"pr", "list"}, "", defaultGitHubHost},
		{"plain owner/repo stays public", []string{"pr", "list", "--repo", "o/r"}, "", defaultGitHubHost},
		{"host-prefixed -R wins", []string{"pr", "view", "42", "-R", "ghe.corp.io/o/r"}, "", "ghe.corp.io"},
		{"host-prefixed --repo wins over GH_HOST", []string{"api", "repos/o/p", "--repo", "A.B.C/x/y"}, "other.host", "a.b.c"},
		{"GH_HOST used without repo flag", []string{"issue", "list"}, "ghe.mycorp.net", "ghe.mycorp.net"},
		{"dangling repo flag ignored", []string{"repo", "--repo"}, "", defaultGitHubHost},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GH_HOST", tc.env)
			t.Setenv("GH_REPO", "")
			if got := targetHost(tc.args); got != tc.want {
				t.Fatalf("targetHost(%v) with GH_HOST=%q = %q, want %q", tc.args, tc.env, got, tc.want)
			}
		})
	}
}

func TestTargetHostUsesRepoEnvAndGitRemote(t *testing.T) {
	t.Setenv("GH_HOST", "")
	resetRemoteCache()
	savedRemote := gitRemoteURLFunc
	t.Cleanup(func() { gitRemoteURLFunc = savedRemote; resetRemoteCache() })

	t.Run("GH_REPO with host prefix", func(t *testing.T) {
		t.Setenv("GH_REPO", "ghe.example.com/o/r")
		gitRemoteURLFunc = func() string { return "https://github.com/o/r.git" }
		resetRemoteCache()
		if got := targetHost([]string{"pr", "list"}); got != "ghe.example.com" {
			t.Fatalf("GH_REPO should win over the local remote: %q", got)
		}
	})

	t.Run("git remote infers enterprise host", func(t *testing.T) {
		t.Setenv("GH_REPO", "")
		gitRemoteURLFunc = func() string { return "git@ghe.internal.acme.io:acme/widgets.git" }
		resetRemoteCache()
		if got := targetHost([]string{"pr", "list"}); got != "ghe.internal.acme.io" {
			t.Fatalf("remote host inference failed: %q", got)
		}
	})

	t.Run("github.com remote beats GH_HOST", func(t *testing.T) {
		gitRemoteURLFunc = func() string { return "https://github.com/o/r.git" }
		resetRemoteCache()
		t.Setenv("GH_HOST", "fallback.host")
		if got := targetHost([]string{"pr", "list"}); got != defaultGitHubHost {
			t.Fatalf("a resolved github.com remote must not defer to GH_HOST, got %q", got)
		}
	})

	t.Run("unresolvable remote falls through to GH_HOST", func(t *testing.T) {
		gitRemoteURLFunc = func() string { return "/dev/null/not/a/remote" }
		resetRemoteCache()
		t.Setenv("GH_HOST", "fallback.host")
		if got := targetHost([]string{"pr", "list"}); got != "fallback.host" {
			t.Fatalf("expected GH_HOST fallback, got %q", got)
		}
	})

	t.Run("no signals anywhere lands on github.com", func(t *testing.T) {
		t.Setenv("GH_HOST", "")
		gitRemoteURLFunc = func() string { return "" }
		resetRemoteCache()
		if got := targetHost(nil); got != defaultGitHubHost {
			t.Fatalf("default wrong: %q", got)
		}
	})
}

func TestHostFromRemoteURL(t *testing.T) {
	cases := map[string]string{
		"https://ghe.example.com/acme/widgets.git": "ghe.example.com",
		"ssh://git@ghe.example.com/acme/widgets":   "ghe.example.com",
		"git@ghe.example.com:acme/widgets.git":     "ghe.example.com",
		"ghe.example.com:acme/widgets":             "ghe.example.com",
		"github.com:o/r":                           "github.com",
		"git@ghe.example.com.:acme/widgets.git":    "ghe.example.com",
		"https://GHE.Example.COM./acme/widgets":    "ghe.example.com",
		"ghe.example.com/acme/widgets":             "ghe.example.com",
		"http://GHE.Example.COM/acme/widgets":      "ghe.example.com",
		"https://github.com/o/r":                   "github.com",
		"workserver:acme/widgets.git":              "",
		"ghe.example.com..:acme/widgets":           "",
		"":                                         "",
		"/just/a/path":                             "",
	}
	for input, want := range cases {
		if got := hostFromRemoteURL(input); got != want {
			t.Errorf("hostFromRemoteURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCredentialEnvFor(t *testing.T) {
	if got := credentialEnvFor(defaultGitHubHost, "tok"); strings.Join(got, "|") != "GH_TOKEN=tok" {
		t.Fatalf("github.com credential wrong: %v", got)
	}
	got := credentialEnvFor("ghe.example.com", "tok")
	want := "GH_ENTERPRISE_TOKEN=tok|GITHUB_ENTERPRISE_TOKEN=tok"
	if strings.Join(got, "|") != want {
		t.Fatalf("enterprise credential wrong: %v", got)
	}
}

func TestFallbackEligibleRespectsHostOverrides(t *testing.T) {
	args := []string{"pr", "list", "--repo", "ghe.example.com/o/r"}
	if !fallbackEligible(args, "Not Found (HTTP 404)") {
		t.Fatal("clean environment should allow enterprise fallback")
	}
	t.Setenv("GH_ENTERPRISE_TOKEN", "explicit")
	if fallbackEligible(args, "Not Found (HTTP 404)") {
		t.Fatal("GH_ENTERPRISE_TOKEN must disable enterprise fallback")
	}
	publicArgs := []string{"pr", "list", "--repo", "o/r"}
	if !fallbackEligible(publicArgs, "Not Found (HTTP 404)") {
		t.Fatal("enterprise token must not block github.com fallback")
	}
}

func TestAccountsAreCachedPerHost(t *testing.T) {
	authStatusCalls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		authStatusCalls++
		payload := `{"hosts":{
			"github.com":[{"login":"pub","active":true,"state":"success"}],
			"ghe.example.com":[{"login":"ent","active":true,"state":"success"}]
		}}`
		return *bytes.NewBufferString(payload), bytes.Buffer{}, nil
	}, nil, nil)
	listAccountsFunc = listAccounts

	first := listAccounts("ghe.example.com")
	second := listAccounts("github.com")
	listAccounts("ghe.example.com")

	if authStatusCalls != 1 {
		t.Fatalf("one auth status probe should serve every host, got %d", authStatusCalls)
	}
	if len(first) != 1 || first[0].Login != "ent" || len(second) != 1 || second[0].Login != "pub" {
		t.Fatalf("per-host results mixed: ghe=%#v github=%#v", first, second)
	}
}

func TestDefaultAccountTokenTargetsHost(t *testing.T) {
	var seenHostname []string
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		for i, arg := range inv.Args {
			if arg == "--hostname" && i+1 < len(inv.Args) {
				seenHostname = append(seenHostname, inv.Args[i+1])
			}
		}
		return *bytes.NewBufferString("tok\n"), bytes.Buffer{}, nil
	}, nil, nil)
	accountTokenFunc = defaultAccountToken

	if token, ok := accountTokenFunc("corp-dev", "ghe.example.com"); !ok || token != "tok" {
		t.Fatalf("token lookup failed: %q %v", token, ok)
	}
	if len(seenHostname) != 1 || seenHostname[0] != "ghe.example.com" {
		t.Fatalf("token request must target the resolved host, saw %v", seenHostname)
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
	resetFallbackNotes()
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
