package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	gh "github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/term"
)

type meOptions struct {
	org   string
	limit int
	json  bool
}

func runMe(args []string, stdout io.Writer, stderr io.Writer) error {
	options, err := parseMeOptions(args, stderr)
	if err != nil {
		if errors.Is(err, errHelpDisplayed) {
			return nil
		}
		return err
	}

	return executeMe(options, stdout)
}

func parseMeOptions(args []string, stderr io.Writer) (meOptions, error) {
	options := meOptions{limit: 30}

	flags := flag.NewFlagSet("me", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprint(stderr, meUsage)
	}

	flags.StringVar(&options.org, "org", "", "Organization or user to search (default: inferred from current repo)")
	flags.StringVar(&options.org, "o", "", "Organization or user to search (default: inferred from current repo)")
	flags.IntVar(&options.limit, "limit", 30, "Maximum number of pull requests to show")
	flags.IntVar(&options.limit, "L", 30, "Maximum number of pull requests to show")
	flags.BoolVar(&options.json, "json", false, "Output enriched JSON instead of a table")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options, errHelpDisplayed
		}
		return options, err
	}

	if flags.NArg() > 0 {
		return options, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), ", "))
	}

	if options.limit < 1 {
		return options, errors.New("limit must be greater than zero")
	}

	return options, nil
}

const meUsage = `Usage:
  gh x pr me [flags]

Show all your open pull requests (authored + assigned) across an organization.

Flags:
  -o, --org string   Organization or user to search (default: inferred from current repo)
  -L, --limit int    Maximum number of pull requests to show (default 30)
      --json         Output enriched JSON instead of a table
`

func buildMeQueries(owner, login string) []string {
	qualifier := resolveOwnerQualifier(owner)
	return buildMeQueriesWithQualifier(qualifier, login)
}

func buildMeQueriesWithQualifier(qualifier, login string) []string {
	return []string{
		fmt.Sprintf("is:pr is:open author:%s %s", login, qualifier),
		fmt.Sprintf("is:pr is:open assignee:%s %s -author:%s", login, qualifier, login),
	}
}

// resolveOwnerQualifier returns "org:<owner>" for organizations
// or "user:<owner>" for personal accounts.
func resolveOwnerQualifier(owner string) string {
	stdout, _, err := gh.Exec("api", fmt.Sprintf("users/%s", owner), "--jq", ".type")
	if err != nil {
		return fmt.Sprintf("org:%s", owner)
	}
	userType := strings.TrimSpace(stdout.String())
	if strings.EqualFold(userType, "User") {
		return fmt.Sprintf("user:%s", owner)
	}
	return fmt.Sprintf("org:%s", owner)
}

func executeMe(options meOptions, stdout io.Writer) error {
	org, err := resolveAtmOrg(options.org)
	if err != nil {
		return fmt.Errorf("cannot determine organization: %w", err)
	}

	login, err := resolveCurrentUser()
	if err != nil {
		return fmt.Errorf("cannot determine current user: %w", err)
	}

	nodes, err := fetchMeNodes(org, login, options.limit)
	if err != nil {
		return err
	}

	return renderMeResults(nodes, stdout, org, login, options.limit, options.json, time.Now().UTC())
}

func fetchMeNodes(org, login string, limit int) ([]atmPullRequestNode, error) {
	queries := buildMeQueries(org, login)
	query := buildAtmMultiSearchQuery(queries, limit)
	stdoutBuf, stderrBuf, execErr := gh.Exec("api", "graphql", "-f", fmt.Sprintf("query=%s", query))
	if execErr != nil {
		return nil, wrapExecError(fmt.Errorf("GraphQL search failed: %w", execErr), stderrBuf.String())
	}
	return parseAtmMultiSearchResponse(stdoutBuf.Bytes())
}

func renderMeResults(nodes []atmPullRequestNode, stdout io.Writer, org, login string, limit int, jsonOutput bool, now time.Time) error {
	rendered := make([]displayPullRequest, 0, len(nodes))
	for _, node := range nodes {
		rendered = append(rendered, mapAtmNode(node, now))
	}

	sort.Slice(rendered, func(i, j int) bool {
		return rendered[i].updatedAt.After(rendered[j].updatedAt)
	})
	if len(rendered) > limit {
		rendered = rendered[:limit]
	}

	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(rendered)
	}

	return renderMeTable(stdout, org, login, rendered)
}

func renderMeTable(stdout io.Writer, org, login string, pullRequests []displayPullRequest) error {
	if len(pullRequests) == 0 {
		fmt.Fprintf(stdout, "No open PRs authored by or assigned to %s in %s.\n", login, org)
		return nil
	}

	fmt.Fprintf(stdout, "Open PRs for %s in %s\n\n", login, org)

	colorEnabled := term.FromEnv().IsColorEnabled()
	return renderAtmTableWithStyle(stdout, pullRequests, colorEnabled)
}
