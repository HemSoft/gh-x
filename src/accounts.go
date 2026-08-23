package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

// defaultGitHubHost is the public GitHub host; every other resolved host is
// treated as an Enterprise Server installation.
const defaultGitHubHost = "github.com"

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
// environment entries. GH_PATH takes precedence, matching gh's own resolution.
func runGHCmd(inv ghInvocation) (bytes.Buffer, bytes.Buffer, error) {
	path := os.Getenv("GH_PATH")
	if path == "" {
		var lookErr error
		if path, lookErr = exec.LookPath("gh"); lookErr != nil {
			return bytes.Buffer{}, bytes.Buffer{}, fmt.Errorf("gh CLI not found in PATH")
		}
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

var (
	notifiedMu     sync.Mutex
	notifiedLogins = map[string]bool{}
)

// noteFallback prints the alternate-account notice once per login per process
// so multi-call commands do not repeat it.
func noteFallback(login, host string) {
	notifiedMu.Lock()
	defer notifiedMu.Unlock()
	if notifiedLogins[login] {
		return
	}
	notifiedLogins[login] = true
	fmt.Fprintf(accountWarningWriter, "[gh-x] note: retried as %s (%s) after an access failure\n", login, host)
}

// execGH runs a gh command and retries with another logged-in account's token
// when the active account cannot access the target repository. The retry
// authenticates against the same host the original command targeted.
func execGH(args ...string) (bytes.Buffer, bytes.Buffer, error) {
	stdout, stderr, err := ghTransportFunc(ghInvocation{Args: args})
	if err == nil || !fallbackEligible(args, stderr.String()) {
		return stdout, stderr, err
	}

	host := targetHost(args)
	for _, login := range fallbackAccountLoginsFor(host) {
		token, ok := accountTokenFunc(login, host)
		if !ok || token == "" {
			continue
		}
		retry := ghInvocation{
			Args:     args,
			ExtraEnv: credentialEnvFor(host, token),
		}
		retryOut, retryErrs, retryErr := ghTransportFunc(retry)
		if retryErr == nil {
			noteFallback(login, host)
			return retryOut, retryErrs, nil
		}
	}
	return stdout, stderr, err
}

// execGHActive runs a gh command as the active account with no fallback.
// Identity-scoped flows use it so a retry can never switch the account that a
// query's embedded login refers to, on any host.
func execGHActive(args ...string) (bytes.Buffer, bytes.Buffer, error) {
	return ghTransportFunc(ghInvocation{Args: args})
}

// targetHost resolves which GitHub host a command targets, following gh's own
// precedence: an explicit HOST/OWNER/REPO value on --repo/-R, then the
// GH_REPO environment variable, then the current repository's git remote,
// then GH_HOST, then github.com.
func targetHost(args []string) string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "--repo" && args[i] != "-R" {
			continue
		}
		if host := hostFromRepoValue(args[i+1]); host != "" {
			return host
		}
	}
	if host := hostFromRepoValue(os.Getenv("GH_REPO")); host != "" {
		return host
	}
	if host := hostFromRemoteURL(cachedRemoteURL()); host != "" {
		return host
	}
	if host := strings.TrimSpace(os.Getenv("GH_HOST")); host != "" {
		return strings.ToLower(host)
	}
	return defaultGitHubHost
}

// gitRemoteURLFunc reads the current repository's origin URL so commands run
// inside an Enterprise Server checkout target that host by default; tests
// replace it.
var gitRemoteURLFunc = defaultGitRemoteURL

var (
	remoteMu       sync.Mutex
	cachedRemote   string
	remoteResolved bool
)

// cachedRemoteURL memoizes the git remote probe for this process.
func cachedRemoteURL() string {
	remoteMu.Lock()
	defer remoteMu.Unlock()
	if !remoteResolved {
		cachedRemote = gitRemoteURLFunc()
		remoteResolved = true
	}
	return cachedRemote
}

// defaultGitRemoteURL reads the origin remote of the current repository.
func defaultGitRemoteURL() string {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

var (
	remoteSchemeHost = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://(?:[^@/]+@)?([^/:?#]+)`)
	remoteScpHost    = regexp.MustCompile(`^(?:[^/@]+@)?([^:/]+):`)
	remotePlainHost  = regexp.MustCompile(`^([^/:@]+\.[^/:@]+)/`)
)

// plausibleRemoteHost rejects SSH host aliases such as "workserver", which
// have no dotted hostname, so a local alias is never treated as a GitHub
// hostname during remote-based inference.
func plausibleRemoteHost(host string) bool {
	return strings.Contains(host, ".") &&
		!strings.HasPrefix(host, ".") &&
		!strings.HasSuffix(host, ".")
}

// normalizeRemoteHost lowercases and strips the DNS root dot from absolute
// names like "ghe.example.com.". Malformed names that still end in a dot
// afterwards stay invalid on purpose.
func normalizeRemoteHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), "."))
}

// hostFromRepoValue extracts the host from HOST/OWNER/REPO values. Plain
// OWNER/REPO values return "" because their host comes from elsewhere.
func hostFromRepoValue(value string) string {
	parts := strings.Split(strings.Trim(strings.TrimSpace(value), "/"), "/")
	if len(parts) < 3 || parts[0] == "" {
		return ""
	}
	return strings.ToLower(parts[0])
}

// hostFromRemoteURL extracts a hostname from https, ssh, scp-style (with or
// without an ssh user), or bare host/path git remote URLs. SSH aliases and
// anything unrecognizable return "" so inference degrades to the next signal.
func hostFromRemoteURL(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	for _, pattern := range []*regexp.Regexp{remoteSchemeHost, remoteScpHost, remotePlainHost} {
		matches := pattern.FindStringSubmatch(value)
		if len(matches) != 2 || matches[1] == "" {
			continue
		}
		host := normalizeRemoteHost(matches[1])
		if plausibleRemoteHost(host) {
			return host
		}
	}
	return ""
}

// credentialEnvFor returns the environment entries that authenticate one gh
// subprocess against host with token. github.com uses GH_TOKEN; Enterprise
// Server hosts use both enterprise variable spellings so either gh build
// honors them.
func credentialEnvFor(host, token string) []string {
	if host == defaultGitHubHost {
		return []string{"GH_TOKEN=" + token}
	}
	return []string{
		"GH_ENTERPRISE_TOKEN=" + token,
		"GITHUB_ENTERPRISE_TOKEN=" + token,
	}
}

// fallbackEligible reports whether a failure should trigger an alternate
// account attempt. Auth-plane commands never fall back, explicit token
// environment overrides for the target host are respected, and only
// access-shaped errors qualify.
func fallbackEligible(args []string, stderr string) bool {
	if len(args) == 0 || args[0] == "auth" {
		return false
	}
	if explicitTokenSet(targetHost(args)) {
		return false
	}
	return isAccessError(stderr)
}

// explicitTokenSet reports whether the caller pinned credentials for host via
// the environment; fallback must respect that choice instead of second-guessing it.
func explicitTokenSet(host string) bool {
	if host == defaultGitHubHost {
		return os.Getenv("GH_TOKEN") != "" || os.Getenv("GITHUB_TOKEN") != ""
	}
	return os.Getenv("GH_ENTERPRISE_TOKEN") != "" || os.Getenv("GITHUB_ENTERPRISE_TOKEN") != ""
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

// ghAccount is one authenticated GitHub CLI identity on some host.
type ghAccount struct {
	Login  string
	Active bool
}

// listAccountsFunc resolves an account list for one host; tests replace it.
var listAccountsFunc = listAccounts

// accountTokenFunc resolves one account's token for a host; tests replace it.
var accountTokenFunc = defaultAccountToken

var (
	accountsMu     sync.Mutex
	cachedAccounts = map[string][]ghAccount{}
	cachedTokens   = map[string]string{}
)

// listAccounts discovers logged-in accounts once per process: one auth status
// probe is parsed and cached for every reported host. An empty result is
// cached too so a broken auth state cannot cause repeated probing.
func listAccounts(host string) []ghAccount {
	accountsMu.Lock()
	defer accountsMu.Unlock()
	if cached, ok := cachedAccounts[host]; ok {
		return cached
	}
	stdout, _, err := ghTransportFunc(ghInvocation{Args: []string{"auth", "status", "--json", "hosts"}})
	if err != nil {
		cachedAccounts[host] = []ghAccount{}
		return cachedAccounts[host]
	}
	for parsedHost, parsedAccounts := range parseAuthStatusJSON(stdout.Bytes()) {
		if len(parsedAccounts) > 0 {
			cachedAccounts[parsedHost] = parsedAccounts
		}
	}
	if _, probed := cachedAccounts[host]; !probed {
		cachedAccounts[host] = []ghAccount{}
	}
	return cachedAccounts[host]
}

// parseAuthStatusJSON reads `gh auth status --json hosts` into per-host
// account lists keyed by hostname. Only successful logins apply; expired or
// pending entries are skipped because their tokens cannot authenticate.
func parseAuthStatusJSON(data []byte) map[string][]ghAccount {
	var payload struct {
		Hosts map[string][]struct {
			Login  string `json:"login"`
			Active bool   `json:"active"`
			State  string `json:"state"`
		} `json:"hosts"`
	}
	byHost := map[string][]ghAccount{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return byHost
	}
	for host, entries := range payload.Hosts {
		for _, entry := range entries {
			if entry.Login == "" {
				continue
			}
			if entry.State != "" && entry.State != "success" {
				continue
			}
			byHost[host] = append(byHost[host], ghAccount{Login: entry.Login, Active: entry.Active})
		}
	}
	return byHost
}

// fallbackAccountLoginsFor lists non-active accounts on host in discovery order.
func fallbackAccountLoginsFor(host string) []string {
	logins := []string{}
	for _, account := range listAccountsFunc(host) {
		if !account.Active {
			logins = append(logins, account.Login)
		}
	}
	return logins
}

// defaultAccountToken resolves one account's token on host via the gh CLI and
// caches it for this invocation of the extension. Failures are not cached.
func defaultAccountToken(login, host string) (string, bool) {
	accountsMu.Lock()
	defer accountsMu.Unlock()
	cacheKey := login + "@" + host
	if token, ok := cachedTokens[cacheKey]; ok {
		return token, true
	}
	stdout, _, err := ghTransportFunc(ghInvocation{Args: []string{
		"auth", "token", "--user", login, "--hostname", host,
	}})
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", false
	}
	cachedTokens[cacheKey] = token
	return token, true
}
