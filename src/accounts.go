package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// ghInvocation describes one gh subprocess execution.
type ghInvocation struct {
	Args     []string
	Stdin    []byte
	ExtraEnv []string
}

// ghTransportFunc is the lowest-level seam for executing gh; tests replace it
// to simulate command output and failures.
var ghTransportFunc = runGHCmd

// accountWarningWriter receives multi-account fallback notices. stderr keeps
// --json stdout machine-readable.
var accountWarningWriter io.Writer = os.Stderr

// runGHCmd executes the gh binary with inherited environment plus any extra
// environment entries.
func runGHCmd(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return bytes.Buffer{}, bytes.Buffer{}, fmt.Errorf("gh CLI not found in PATH")
	}
	cmd := exec.Command(path, inv.Args...)
	if len(inv.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(inv.Stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if len(inv.ExtraEnv) > 0 {
		cmd.Env = append(os.Environ(), inv.ExtraEnv...)
	}
	if err := cmd.Run(); err != nil {
		return stdout, stderr, err
	}
	return stdout, stderr, nil
}

// execGH runs a gh command and retries with another logged-in account's token
// when the active account cannot access the target repository.
func execGH(args ...string) (bytes.Buffer, bytes.Buffer, error) {
	return execGHWithInput(args, nil)
}

// execGHWithInput is execGH with stdin data, e.g. API POST payloads.
func execGHWithInput(args []string, stdin []byte) (bytes.Buffer, bytes.Buffer, error) {
	inv := ghInvocation{Args: args, Stdin: stdin}
	stdout, stderr, err := ghTransportFunc(inv)
	if err == nil || !fallbackEligible(args, stderr.String()) {
		return stdout, stderr, err
	}

	for _, login := range fallbackAccountLogins() {
		token, ok := accountTokenFunc(login)
		if !ok || token == "" {
			continue
		}
		retry := ghInvocation{
			Args:     args,
			Stdin:    stdin,
			ExtraEnv: []string{"GH_TOKEN=" + token},
		}
		retryOut, retryErrs, retryErr := ghTransportFunc(retry)
		if retryErr == nil {
			fmt.Fprintf(accountWarningWriter, "[gh-x] note: retried as %s after an access failure\n", login)
			return retryOut, retryErrs, nil
		}
	}
	return stdout, stderr, err
}

// fallbackEligible reports whether a failure should trigger an alternate
// account attempt. Auth-plane commands never fall back, explicit token
// environment overrides are respected, and only access-shaped errors qualify.
func fallbackEligible(args []string, stderr string) bool {
	if len(args) == 0 || args[0] == "auth" {
		return false
	}
	if os.Getenv("GH_TOKEN") != "" || os.Getenv("GITHUB_TOKEN") != "" || os.Getenv("GH_ENTERPRISE_TOKEN") != "" {
		return false
	}
	return isAccessError(stderr)
}

var accessErrorMarkers = []string{
	"http 403",
	"http 404",
	"(403)",
	"(404)",
	"forbidden",
	"not accessible",
	"resource protected",
	"bad credentials",
	"not found",
	"not_found",
	"could not resolve",
}

// isAccessError matches failure text that plausibly indicates the active
// account cannot see the target rather than a broken request.
func isAccessError(stderr string) bool {
	lowered := strings.ToLower(stderr)
	for _, marker := range accessErrorMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// ghAccount is one authenticated GitHub CLI identity on github.com.
type ghAccount struct {
	Login  string
	Active bool
}

var listAccountsFunc = listAccounts

// accountTokenFunc resolves one account's token; tests replace it.
var accountTokenFunc = defaultAccountToken

var (
	accountsMu     sync.Mutex
	cachedAccounts []ghAccount
	cachedTokens   = map[string]string{}
)

// listAccounts discovers logged-in accounts once per process. An empty result
// is cached so a broken auth state cannot cause repeated probing.
func listAccounts() []ghAccount {
	accountsMu.Lock()
	defer accountsMu.Unlock()
	if cachedAccounts != nil {
		return cachedAccounts
	}
	stdout, _, err := ghTransportFunc(ghInvocation{Args: []string{"auth", "status", "--json", "hosts"}})
	if err != nil {
		cachedAccounts = []ghAccount{}
		return cachedAccounts
	}
	cachedAccounts = parseAuthStatusJSON(stdout.Bytes())
	return cachedAccounts
}

// parseAuthStatusJSON reads `gh auth status --json hosts`. Only successful
// github.com accounts apply because GH_TOKEN governs that host.
func parseAuthStatusJSON(data []byte) []ghAccount {
	var payload struct {
		Hosts map[string][]struct {
			Login  string `json:"login"`
			Active bool   `json:"active"`
			State  string `json:"state"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return []ghAccount{}
	}
	entries := payload.Hosts["github.com"]
	accounts := make([]ghAccount, 0, len(entries))
	for _, entry := range entries {
		if entry.Login == "" {
			continue
		}
		if entry.State != "" && entry.State != "success" {
			continue
		}
		accounts = append(accounts, ghAccount{Login: entry.Login, Active: entry.Active})
	}
	return accounts
}

// fallbackAccountLogins lists non-active accounts in discovery order.
func fallbackAccountLogins() []string {
	logins := []string{}
	for _, account := range listAccountsFunc() {
		if !account.Active {
			logins = append(logins, account.Login)
		}
	}
	return logins
}

// defaultAccountToken resolves one account's token via the gh CLI and caches
// it for this invocation of the extension. Failures are not cached.
func defaultAccountToken(login string) (string, bool) {
	accountsMu.Lock()
	defer accountsMu.Unlock()
	if token, ok := cachedTokens[login]; ok {
		return token, true
	}
	stdout, _, err := ghTransportFunc(ghInvocation{Args: []string{"auth", "token", "--user", login}})
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", false
	}
	cachedTokens[login] = token
	return token, true
}
