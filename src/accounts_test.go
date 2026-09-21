package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ghconfig "github.com/cli/go-gh/v2/pkg/config"
)

const (
	ghHelperModeEnv       = "GH_X_TEST_HELPER_MODE"
	ghHelperStartedEnv    = "GH_X_TEST_HELPER_STARTED"
	ghHelperCompletedEnv  = "GH_X_TEST_HELPER_COMPLETED"
	ghHelperSleepDuration = 30 * time.Second
)

func TestRunGHCmdHelperProcess(t *testing.T) {
	mode := os.Getenv(ghHelperModeEnv)
	if mode == "" {
		return
	}
	if started := os.Getenv(ghHelperStartedEnv); started != "" {
		if err := os.WriteFile(started, []byte("started"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "success" {
		if _, err := os.Stdout.WriteString("helper success"); err != nil {
			t.Fatal(err)
		}
		return
	}
	time.Sleep(ghHelperSleepDuration)
	if completed := os.Getenv(ghHelperCompletedEnv); completed != "" {
		if err := os.WriteFile(completed, []byte("completed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunGHCmdHonorsSuccessDeadlineAndCancellation(t *testing.T) {
	t.Setenv("GH_PATH", os.Args[0])
	helperArgs := []string{"-test.run=^TestRunGHCmdHelperProcess$"}

	successCtx, successCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer successCancel()
	stdout, _, err := runGHCmd(ghInvocation{
		Context: successCtx,
		Args:    helperArgs,
		ExtraEnv: []string{
			ghHelperModeEnv + "=success",
		},
	})
	if err != nil || !strings.Contains(stdout.String(), "helper success") {
		t.Fatalf("successful helper = %q, %v", stdout.String(), err)
	}

	deadlineCtx, deadlineCancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer deadlineCancel()
	_, _, err = runGHCmd(ghInvocation{
		Context:  deadlineCtx,
		Args:     helperArgs,
		ExtraEnv: []string{ghHelperModeEnv + "=sleep"},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}

	started := filepath.Join(t.TempDir(), "started")
	completed := filepath.Join(t.TempDir(), "completed")
	cancelCtx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, _, runErr := runGHCmd(ghInvocation{
			Context: cancelCtx,
			Args:    helperArgs,
			ExtraEnv: []string{
				ghHelperModeEnv + "=sleep",
				ghHelperStartedEnv + "=" + started,
				ghHelperCompletedEnv + "=" + completed,
			},
		})
		result <- runErr
	}()
	waitForFile(t, started, time.Second)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	if _, err := os.Stat(completed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled child reached completion marker: %v", err)
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func TestConfiguredTimeout(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", want: 7 * time.Second},
		{name: "configured", value: "250ms", want: 250 * time.Millisecond},
		{name: "invalid", value: "soon", wantErr: true},
		{name: "whitespace", value: "  ", wantErr: true},
		{name: "zero", value: "0s", wantErr: true},
		{name: "negative", value: "-1s", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GH_X_TEST_TIMEOUT", test.value)
			got, err := configuredTimeout("GH_X_TEST_TIMEOUT", 7*time.Second)
			if (err != nil) != test.wantErr {
				t.Fatalf("configuredTimeout() error = %v, wantErr %v", err, test.wantErr)
			}
			if !test.wantErr && got != test.want {
				t.Fatalf("configuredTimeout() = %v, want %v", got, test.want)
			}
		})
	}
}

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
	cachedRemoteHost = ""
	remoteResolved = false
}

func withSSHConfigHostStub(t *testing.T, stub func(string) string) {
	t.Helper()
	saved := sshConfigHostFunc
	sshConfigHostFunc = func(_ context.Context, host string) string { return stub(host) }
	t.Cleanup(func() { sshConfigHostFunc = saved })
}

func withKnownGitHubHostStub(t *testing.T, stub func(string) bool) {
	t.Helper()
	saved := knownGitHubHostFunc
	knownGitHubHostFunc = stub
	t.Cleanup(func() { knownGitHubHostFunc = saved })
}

func withRemoteURLStub(t *testing.T, remote string) {
	t.Helper()
	saved := gitRemoteURLFunc
	gitRemoteURLFunc = func(context.Context) string { return remote }
	resetRemoteCache()
	t.Cleanup(func() {
		gitRemoteURLFunc = saved
		resetRemoteCache()
	})
}

func withFallbackStubs(t *testing.T, transport func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error), accounts []ghAccount, tokens map[string]string) *bytes.Buffer {
	t.Helper()
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_ENTERPRISE_TOKEN", "")
	t.Setenv("GITHUB_ENTERPRISE_TOKEN", "")
	t.Setenv("GH_REPO", "")
	t.Setenv("GH_HOST", "")
	t.Setenv(githubCommandTimeoutEnv, "")
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
	listAccountsFunc = func(context.Context, string) []ghAccount { return accounts }
	accountTokenFunc = func(_ context.Context, login, _ string) (string, bool) {
		token, ok := tokens[login]
		return token, ok
	}
	gitRemoteURLFunc = func(context.Context) string { return "" }
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

func TestExecGHSearchListFallsBackWhenEmptyResultHidesPrivateRepo(t *testing.T) {
	tests := []struct {
		name                string
		probeError          error
		probeStderr         string
		explicitToken       bool
		firstAlternateBlind bool
		wantFallback        bool
		wantCalls           int
		wantOutput          string
	}{
		{
			name:                "inaccessible repository retries search",
			probeError:          errors.New("exit status 1"),
			probeStderr:         "GraphQL: Could not resolve to a Repository with the name 'acme/private'. (repository)",
			firstAlternateBlind: true,
			wantFallback:        true,
			wantCalls:           5,
			wantOutput:          `[{"number":116}]`,
		},
		{
			name:       "accessible repository keeps legitimate empty result",
			wantCalls:  2,
			wantOutput: "[]",
		},
		{
			name:          "explicit token keeps fallback disabled",
			probeError:    errors.New("exit status 1"),
			probeStderr:   "GraphQL: Could not resolve to a Repository with the name 'acme/private'. (repository)",
			explicitToken: true,
			wantCalls:     2,
			wantOutput:    "[]",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			accounts := []ghAccount{{Login: "primary", Active: true}}
			if test.firstAlternateBlind {
				accounts = append(accounts, ghAccount{Login: "blind-secondary"})
			}
			accounts = append(accounts, ghAccount{Login: "search-secondary"})
			notices := withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
				calls++
				switch calls {
				case 1:
					if strings.Join(inv.Args, " ") != "pr list --repo acme/private --state merged --search sort:updated-desc --json number" {
						t.Fatalf("unexpected search call: %v", inv.Args)
					}
					return *bytes.NewBufferString("[]"), bytes.Buffer{}, nil
				case 2:
					if strings.Join(inv.Args, " ") != "repo view acme/private --json nameWithOwner" {
						t.Fatalf("unexpected access probe: %v", inv.Args)
					}
					if test.probeError != nil {
						return bytes.Buffer{}, *bytes.NewBufferString(test.probeStderr), test.probeError
					}
					return *bytes.NewBufferString(`{"nameWithOwner":"acme/private"}`), bytes.Buffer{}, nil
				case 3:
					if test.firstAlternateBlind {
						if strings.Join(inv.ExtraEnv, "|") != "GH_TOKEN=blind-token" {
							t.Fatalf("expected blind alternate account token, got %v", inv.ExtraEnv)
						}
						return *bytes.NewBufferString("[]"), bytes.Buffer{}, nil
					}
					if strings.Join(inv.ExtraEnv, "|") != "GH_TOKEN=alt-token" {
						t.Fatalf("expected alternate account token, got %v", inv.ExtraEnv)
					}
					return *bytes.NewBufferString(`[{"number":116}]`), bytes.Buffer{}, nil
				case 4:
					if strings.Join(inv.Args, " ") != "repo view acme/private --json nameWithOwner" || strings.Join(inv.ExtraEnv, "|") != "GH_TOKEN=blind-token" {
						t.Fatalf("unexpected blind alternate access probe: args=%v env=%v", inv.Args, inv.ExtraEnv)
					}
					return bytes.Buffer{}, *bytes.NewBufferString(test.probeStderr), test.probeError
				case 5:
					if strings.Join(inv.ExtraEnv, "|") != "GH_TOKEN=alt-token" {
						t.Fatalf("expected accessible alternate account token, got %v", inv.ExtraEnv)
					}
					return *bytes.NewBufferString(`[{"number":116}]`), bytes.Buffer{}, nil
				default:
					t.Fatalf("unexpected transport call %d: %v", calls, inv.Args)
					return bytes.Buffer{}, bytes.Buffer{}, nil
				}
			},
				accounts,
				map[string]string{"blind-secondary": "blind-token", "search-secondary": "alt-token"},
			)
			if test.explicitToken {
				t.Setenv("GH_TOKEN", "explicit-token")
			}

			out, _, err := execGH("pr", "list", "--repo", "acme/private", "--state", "merged", "--search", "sort:updated-desc", "--json", "number")
			if err != nil {
				t.Fatalf("search list error = %v", err)
			}
			if out.String() != test.wantOutput {
				t.Fatalf("search list output = %q, want %q", out.String(), test.wantOutput)
			}
			if calls != test.wantCalls {
				t.Fatalf("transport calls = %d, want %d", calls, test.wantCalls)
			}
			if test.wantFallback && !strings.Contains(notices.String(), "search-secondary") {
				t.Fatalf("expected fallback notice, got %q", notices.String())
			}
			if !test.wantFallback && notices.Len() != 0 {
				t.Fatalf("unexpected fallback notice %q", notices.String())
			}
		})
	}
}

func TestExecGHContextSkipsFallbackAfterCancellation(t *testing.T) {
	fallbackCalls := 0
	withFallbackStubs(t, func(ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, *bytes.NewBufferString("HTTP 404: Not Found"), errors.New("exit status 1")
	}, nil, nil)
	listAccountsFunc = func(context.Context, string) []ghAccount {
		fallbackCalls++
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := execGHContext(ctx, "pr", "list")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command error = %v", err)
	}
	if fallbackCalls != 0 {
		t.Fatalf("fallback discovery ran %d times after cancellation", fallbackCalls)
	}
}

func TestExecGHContextBoundsFallbackHostDiscovery(t *testing.T) {
	fallbackCalls := 0
	withFallbackStubs(t, func(ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		return bytes.Buffer{}, *bytes.NewBufferString("HTTP 404: Not Found"), errors.New("exit status 1")
	}, nil, nil)
	gitRemoteURLFunc = func(ctx context.Context) string {
		<-ctx.Done()
		return ""
	}
	listAccountsFunc = func(context.Context, string) []ghAccount {
		fallbackCalls++
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, _, err := execGHContext(ctx, "pr", "list")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("host discovery deadline error = %v", err)
	}
	if fallbackCalls != 0 {
		t.Fatalf("account discovery ran %d times after host timeout", fallbackCalls)
	}
}

func TestExecGHContextBoundsAccountRetry(t *testing.T) {
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		if calls == 1 {
			return bytes.Buffer{}, *bytes.NewBufferString("HTTP 404: Not Found"), errors.New("exit status 1")
		}
		<-inv.Context.Done()
		return bytes.Buffer{}, bytes.Buffer{}, githubContextError(inv.Context.Err())
	}, []ghAccount{{Login: "primary", Active: true}, {Login: "secondary", Active: false}}, map[string]string{"secondary": "token"})

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	_, _, err := execGHContext(ctx, "pr", "list")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("account retry error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("account retry transport calls = %d, want 2", calls)
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
	listAccountsFunc = func(_ context.Context, host string) []ghAccount {
		if host == "ghe.example.com" {
			return []ghAccount{{Login: "corp-lead", Active: true}, {Login: "corp-dev", Active: false}}
		}
		return []ghAccount{{Login: "personal", Active: true}, {Login: "other-personal", Active: false}}
	}
	accountTokenFunc = func(_ context.Context, login, host string) (string, bool) {
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
			"GHE.Example.COM.": [
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

func TestTargetHostResolvesDottedSSHRemoteAlias(t *testing.T) {
	t.Setenv("GH_REPO", "")
	withKnownGitHubHostStub(t, func(host string) bool { return host == defaultGitHubHost })
	t.Setenv("GH_HOST", "ghe.fallback.example")
	savedRemote := gitRemoteURLFunc
	savedResolver := sshConfigHostFunc
	t.Cleanup(func() {
		gitRemoteURLFunc = savedRemote
		sshConfigHostFunc = savedResolver
		resetRemoteCache()
	})
	gitRemoteURLFunc = func(context.Context) string {
		return "git@github.com-hemsoft:HemSoft/codexbar-ios.git"
	}
	resolverCalls := 0
	sshConfigHostFunc = func(_ context.Context, host string) string {
		resolverCalls++
		if host != "github.com-hemsoft" {
			t.Fatalf("SSH resolver host = %q, want github.com-hemsoft", host)
		}
		return defaultGitHubHost
	}
	resetRemoteCache()

	for range 2 {
		if got := targetHost(nil); got != defaultGitHubHost {
			t.Fatalf("targetHost() = %q, want %q from SSH configuration", got, defaultGitHubHost)
		}
	}
	if resolverCalls != 1 {
		t.Fatalf("SSH resolver calls = %d, want 1", resolverCalls)
	}
}

func TestTargetHostExplicitSourcesPrecedeSSHRemote(t *testing.T) {
	savedRemote := gitRemoteURLFunc
	remoteCalls := 0
	gitRemoteURLFunc = func(context.Context) string {
		remoteCalls++
		return "git@github.com-hemsoft:HemSoft/codexbar-ios.git"
	}
	withSSHConfigHostStub(t, func(host string) string {
		t.Fatalf("SSH resolver unexpectedly called for %q", host)
		return ""
	})
	t.Cleanup(func() {
		gitRemoteURLFunc = savedRemote
		resetRemoteCache()
	})

	tests := []struct {
		name string
		args []string
		repo string
		want string
	}{
		{name: "hostname argument", args: []string{"api", "--hostname", "ghe.arg.example", "graphql"}, want: "ghe.arg.example"},
		{name: "host-prefixed repository argument", args: []string{"pr", "list", "--repo", "ghe.repo.example/acme/widgets"}, want: "ghe.repo.example"},
		{name: "host-prefixed GH_REPO", args: []string{"pr", "list"}, repo: "ghe.env.example/acme/widgets", want: "ghe.env.example"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GH_REPO", test.repo)
			t.Setenv("GH_HOST", "ghe.fallback.example")
			resetRemoteCache()
			if got := targetHost(test.args); got != test.want {
				t.Fatalf("targetHost() = %q, want %q", got, test.want)
			}
		})
	}
	if remoteCalls != 0 {
		t.Fatalf("git remote probes = %d, want 0 for explicit sources", remoteCalls)
	}
}

func TestTargetHost(t *testing.T) {
	t.Setenv("GH_HOST", "")
	resetRemoteCache()
	savedRemote := gitRemoteURLFunc
	t.Cleanup(func() { gitRemoteURLFunc = savedRemote; resetRemoteCache() })
	gitRemoteURLFunc = func(context.Context) string { return "" }

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
		{"explicit hostname wins over GH_HOST", []string{"api", "--hostname", "GitHub.COM.", "graphql"}, "ghe.mycorp.net", defaultGitHubHost},
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
	withSSHConfigHostStub(t, func(host string) string { return host })
	withKnownGitHubHostStub(t, func(host string) bool {
		return host == defaultGitHubHost || host == "ghe.internal.acme.io"
	})
	t.Cleanup(func() { gitRemoteURLFunc = savedRemote; resetRemoteCache() })

	t.Run("GH_REPO with host prefix", func(t *testing.T) {
		t.Setenv("GH_REPO", "ghe.example.com/o/r")
		gitRemoteURLFunc = func(context.Context) string { return "https://github.com/o/r.git" }
		resetRemoteCache()
		if got := targetHost([]string{"pr", "list"}); got != "ghe.example.com" {
			t.Fatalf("GH_REPO should win over the local remote: %q", got)
		}
	})

	t.Run("git remote infers enterprise host", func(t *testing.T) {
		t.Setenv("GH_REPO", "")
		gitRemoteURLFunc = func(context.Context) string { return "git@ghe.internal.acme.io:acme/widgets.git" }
		resetRemoteCache()
		if got := targetHost([]string{"pr", "list"}); got != "ghe.internal.acme.io" {
			t.Fatalf("remote host inference failed: %q", got)
		}
	})

	t.Run("github.com remote beats GH_HOST", func(t *testing.T) {
		gitRemoteURLFunc = func(context.Context) string { return "https://github.com/o/r.git" }
		resetRemoteCache()
		t.Setenv("GH_HOST", "fallback.host")
		if got := targetHost([]string{"pr", "list"}); got != defaultGitHubHost {
			t.Fatalf("a resolved github.com remote must not defer to GH_HOST, got %q", got)
		}
	})

	t.Run("unresolvable remote falls through to GH_HOST", func(t *testing.T) {
		gitRemoteURLFunc = func(context.Context) string { return "/dev/null/not/a/remote" }
		resetRemoteCache()
		t.Setenv("GH_HOST", "fallback.host")
		if got := targetHost([]string{"pr", "list"}); got != "fallback.host" {
			t.Fatalf("expected GH_HOST fallback, got %q", got)
		}
	})

	t.Run("no signals anywhere lands on github.com", func(t *testing.T) {
		t.Setenv("GH_HOST", "")
		gitRemoteURLFunc = func(context.Context) string { return "" }
		resetRemoteCache()
		if got := targetHost(nil); got != defaultGitHubHost {
			t.Fatalf("default wrong: %q", got)
		}
	})
}

func TestHostFromRemoteURL(t *testing.T) {
	withSSHConfigHostStub(t, func(host string) string { return host })
	withKnownGitHubHostStub(t, func(host string) bool {
		return host == defaultGitHubHost || host == "ghe.example.com"
	})
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
		if got := hostFromRemoteURLContext(context.Background(), input); got != want {
			t.Errorf("hostFromRemoteURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHostFromRemoteURLResolvesSSHAliasesOnly(t *testing.T) {
	resolverCalls := 0
	withKnownGitHubHostStub(t, func(host string) bool {
		return host == defaultGitHubHost || host == "ghe.example.com"
	})
	withSSHConfigHostStub(t, func(host string) string {
		resolverCalls++
		switch host {
		case "github.com-hemsoft", "GitHub.com-hemsoft", "workserver":
			return defaultGitHubHost
		case "github.com-443":
			return publicGitHubSSHHost
		case "ghe.example-alias":
			return "ghe.example.com"
		default:
			return host
		}
	})

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "scp dotted alias", value: "git@github.com-hemsoft:HemSoft/codexbar-ios.git", want: defaultGitHubHost},
		{name: "case-sensitive SSH alias", value: "git@GitHub.com-hemsoft:HemSoft/codexbar-ios.git", want: defaultGitHubHost},
		{name: "scp plain alias", value: "workserver:acme/widgets.git", want: defaultGitHubHost},
		{name: "ssh scheme alias", value: "ssh://git@github.com-hemsoft/HemSoft/codexbar-ios.git", want: defaultGitHubHost},
		{name: "git plus ssh alias", value: "git+ssh://git@github.com-hemsoft/HemSoft/codexbar-ios.git", want: defaultGitHubHost},
		{name: "ssh plus git alias", value: "ssh+git://git@github.com-hemsoft/HemSoft/codexbar-ios.git", want: defaultGitHubHost},
		{name: "public SSH over 443 alias", value: "git@github.com-443:HemSoft/codexbar-ios.git", want: defaultGitHubHost},
		{name: "genuine enterprise ssh host", value: "git@ghe.example.com:acme/widgets.git", want: "ghe.example.com"},
		{name: "enterprise SSH alias", value: "git@ghe.example-alias:acme/widgets.git", want: "ghe.example.com"},
		{name: "canonical public host over SSH", value: "git@github.com:HemSoft/codexbar-ios.git", want: defaultGitHubHost},
		{name: "https host is not an SSH alias", value: "https://github.com-hemsoft/HemSoft/codexbar-ios.git", want: "github.com-hemsoft"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := hostFromRemoteURLContext(context.Background(), test.value); got != test.want {
				t.Fatalf("hostFromRemoteURL(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
	if resolverCalls != 8 {
		t.Fatalf("SSH resolver calls = %d, want 8", resolverCalls)
	}
}

func TestNewSSHConfigCommandBoundsPipeDrain(t *testing.T) {
	cmd := newSSHConfigCommand(context.Background(), "GitHub.com-hemsoft")
	if cmd.WaitDelay != sshConfigWaitDelay {
		t.Fatalf("ssh WaitDelay = %s, want %s", cmd.WaitDelay, sshConfigWaitDelay)
	}
	if got := strings.Join(cmd.Args, "|"); got != "ssh|-G|--|GitHub.com-hemsoft" {
		t.Fatalf("ssh command args = %q", got)
	}
}

func TestParseSSHConfigHost(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "configured public host", output: "host github.com-hemsoft\nhostname GitHub.COM.\nuser git\n", want: defaultGitHubHost},
		{name: "configured enterprise host", output: "hostname ghe.example.com\n", want: "ghe.example.com"},
		{name: "missing hostname", output: "user git\nport 22\n"},
		{name: "malformed hostname", output: "hostname\nhostname too many fields\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseSSHConfigHost([]byte(test.output)); got != test.want {
				t.Fatalf("parseSSHConfigHost() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConfiguredGitHubHosts(t *testing.T) {
	saved := ghConfigReadFunc
	t.Cleanup(func() { ghConfigReadFunc = saved })
	ghConfigReadFunc = func() (*ghconfig.Config, error) {
		return ghconfig.ReadFromString("hosts:\n  GHE.Example.COM.:\n    user: enterprise-user\n"), nil
	}
	if got := configuredGitHubHosts(); len(got) != 1 || got[0] != "GHE.Example.COM." {
		t.Fatalf("configuredGitHubHosts() = %#v", got)
	}
	ghConfigReadFunc = func() (*ghconfig.Config, error) {
		return ghconfig.ReadFromString("editor: vim\n"), nil
	}
	if got := configuredGitHubHosts(); got != nil {
		t.Fatalf("configuredGitHubHosts() without hosts = %#v, want nil", got)
	}
	ghConfigReadFunc = func() (*ghconfig.Config, error) { return nil, errors.New("broken config") }
	if got := configuredGitHubHosts(); got != nil {
		t.Fatalf("configuredGitHubHosts() after read failure = %#v, want nil", got)
	}
}

func TestKnownGitHubHost(t *testing.T) {
	t.Setenv("GH_HOST", "Env.GHE.Example.")
	saved := configuredGitHubHostsFunc
	configuredGitHubHostsFunc = func() []string { return []string{"GHE.Example.COM."} }
	t.Cleanup(func() { configuredGitHubHostsFunc = saved })

	for _, host := range []string{defaultGitHubHost, "GHE.Example.COM.", "env.ghe.example"} {
		if !knownGitHubHost(host) {
			t.Errorf("knownGitHubHost(%q) = false, want true", host)
		}
	}
	if knownGitHubHost("ssh.ghe.example.com") {
		t.Fatal("an unknown SSH transport endpoint must not be an API host")
	}
}

func TestConfiguredSSHHostPreservesKnownAPIHosts(t *testing.T) {
	withKnownGitHubHostStub(t, func(host string) bool {
		return host == defaultGitHubHost || host == "ghe.example.com"
	})
	resolverCalls := 0
	withSSHConfigHostStub(t, func(string) string {
		resolverCalls++
		return "ssh.transport.example"
	})
	for _, host := range []string{defaultGitHubHost, "ghe.example.com"} {
		if got := configuredSSHHostContext(context.Background(), host); got != host {
			t.Fatalf("configuredSSHHost(%q) = %q, want known API host unchanged", host, got)
		}
	}
	if resolverCalls != 0 {
		t.Fatalf("SSH resolver calls = %d, want 0 for known API hosts", resolverCalls)
	}
}

func TestConfiguredSSHHostRejectsUnknownTransportHost(t *testing.T) {
	withKnownGitHubHostStub(t, func(host string) bool { return host == defaultGitHubHost })
	withSSHConfigHostStub(t, func(string) string { return "ssh.transport.example" })
	if got := configuredSSHHostContext(context.Background(), "github.com-hemsoft"); got != "github.com-hemsoft" {
		t.Fatalf("configuredSSHHost() = %q, want original alias when resolved host is not a known API host", got)
	}
}

func TestConfiguredSSHHostFallsBack(t *testing.T) {
	withKnownGitHubHostStub(t, func(string) bool { return false })
	withSSHConfigHostStub(t, func(string) string { return "" })
	if got := configuredSSHHostContext(context.Background(), "ghe.example.com"); got != "ghe.example.com" {
		t.Fatalf("configuredSSHHost() = %q, want original Enterprise host", got)
	}
	if got := configuredSSHHostContext(context.Background(), "workserver"); got != "workserver" {
		t.Fatalf("configuredSSHHost() = %q, want unresolved alias", got)
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
	if _, eligible := fallbackHost(context.Background(), args, "Not Found (HTTP 404)"); !eligible {
		t.Fatal("clean environment should allow enterprise fallback")
	}
	t.Setenv("GH_ENTERPRISE_TOKEN", "explicit")
	if _, eligible := fallbackHost(context.Background(), args, "Not Found (HTTP 404)"); eligible {
		t.Fatal("GH_ENTERPRISE_TOKEN must disable enterprise fallback")
	}
	publicArgs := []string{"pr", "list", "--repo", "o/r"}
	if _, eligible := fallbackHost(context.Background(), publicArgs, "Not Found (HTTP 404)"); !eligible {
		t.Fatal("enterprise token must not block github.com fallback")
	}
}

func TestAccountDiscoveryDoesNotCacheContextFailure(t *testing.T) {
	calls := 0
	withFallbackStubs(t, func(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
		calls++
		if calls == 1 {
			<-inv.Context.Done()
			return bytes.Buffer{}, bytes.Buffer{}, githubContextError(inv.Context.Err())
		}
		return *bytes.NewBufferString(`{"hosts":{"github.com":[{"login":"ready","active":true,"state":"success"}]}}`), bytes.Buffer{}, nil
	}, nil, nil)
	listAccountsFunc = listAccounts

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if accounts := listAccounts(ctx, defaultGitHubHost); len(accounts) != 0 {
		t.Fatalf("timed-out discovery returned accounts: %v", accounts)
	}
	accounts := listAccounts(context.Background(), defaultGitHubHost)
	if calls != 2 || len(accounts) != 1 || accounts[0].Login != "ready" {
		t.Fatalf("discovery retry = calls %d, accounts %v", calls, accounts)
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

	first := listAccounts(context.Background(), "GHE.Example.COM.")
	second := listAccounts(context.Background(), "github.com")
	listAccounts(context.Background(), "ghe.example.com")

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

	if token, ok := accountTokenFunc(context.Background(), "corp-dev", "ghe.example.com"); !ok || token != "tok" {
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
