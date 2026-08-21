package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	gh "github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/term"
	"github.com/muesli/termenv"
)

type codespaceListOptions struct {
	org  string
	repo string
}

type codespaceEntry struct {
	DisplayName string           `json:"display_name"`
	Repository  codespaceRepo    `json:"repository"`
	GitStatus   codespaceGit     `json:"git_status"`
	State       string           `json:"state"`
	CreatedAt   time.Time        `json:"created_at"`
	Machine     codespaceMachine `json:"machine"`
	LastUsedAt  time.Time        `json:"last_used_at"`
}

type codespaceRepo struct {
	FullName string `json:"full_name"`
}

type codespaceGit struct {
	Ref string `json:"ref"`
}

type codespaceMachine struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

func runCodespaceList(args []string, stdout io.Writer, stderr io.Writer) error {
	options, err := parseCodespaceListOptions(args, stderr)
	if err != nil {
		if errors.Is(err, errHelpDisplayed) {
			return nil
		}
		return err
	}
	return executeCodespaceList(options, stdout)
}

func parseCodespaceListOptions(args []string, stderr io.Writer) (codespaceListOptions, error) {
	var options codespaceListOptions

	flags := flag.NewFlagSet("codespace list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		writeCodespaceListUsage(stderr)
	}

	flags.StringVar(&options.org, "org", "", "Filter by organization")
	flags.StringVar(&options.org, "o", "", "Filter by organization")
	flags.StringVar(&options.repo, "repo", "", "Filter by repository (OWNER/REPO)")
	flags.StringVar(&options.repo, "R", "", "Filter by repository (OWNER/REPO)")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return options, errHelpDisplayed
		}
		return options, err
	}

	if flags.NArg() > 0 {
		return options, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), ", "))
	}

	return options, nil
}

// fetchCodespacesFunc is swapped in tests to avoid real API calls.
var fetchCodespacesFunc = fetchCodespaces

func fetchCodespaces() ([]codespaceEntry, error) {
	stdoutBuf, stderrBuf, err := gh.Exec("api", "/user/codespaces", "--paginate", "--jq", ".codespaces[]")
	if err != nil {
		return nil, wrapExecError(fmt.Errorf("gh api codespaces: %w", err), stderrBuf.String())
	}
	return decodeCodespaces(&stdoutBuf)
}

func decodeCodespaces(r io.Reader) ([]codespaceEntry, error) {
	var entries []codespaceEntry
	dec := json.NewDecoder(r)
	for {
		var entry codespaceEntry
		if err := dec.Decode(&entry); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parsing codespaces response: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func filterCodespaces(entries []codespaceEntry, options codespaceListOptions) []codespaceEntry {
	if options.org == "" && options.repo == "" {
		return entries
	}

	var filtered []codespaceEntry
	for _, cs := range entries {
		if options.repo != "" && !strings.EqualFold(cs.Repository.FullName, options.repo) {
			continue
		}
		if options.org != "" {
			parts := strings.SplitN(cs.Repository.FullName, "/", 2)
			if len(parts) < 2 || !strings.EqualFold(parts[0], options.org) {
				continue
			}
		}
		filtered = append(filtered, cs)
	}
	return filtered
}

func executeCodespaceList(options codespaceListOptions, stdout io.Writer) error {
	codespaces, err := fetchCodespacesFunc()
	if err != nil {
		return err
	}

	codespaces = filterCodespaces(codespaces, options)

	if len(codespaces) == 0 {
		fmt.Fprintln(stdout, "No codespaces found.")
		return nil
	}

	colorEnabled := term.FromEnv().IsColorEnabled()
	return renderCodespaceTable(stdout, codespaces, colorEnabled)
}

func codespaceStateCell(styler tableStyler, state string) tableCell {
	switch strings.ToLower(state) {
	case "available":
		return styler.colored(state, termenv.ANSIGreen)
	case "shutdown", "shutting down":
		return styler.dim(state)
	case "starting", "rebuilding":
		return styler.colored(state, termenv.ANSIYellow)
	default:
		return styler.plain(state)
	}
}

func renderCodespaceTable(stdout io.Writer, codespaces []codespaceEntry, colorEnabled bool) error {
	styler := newTableStyler(stdout, colorEnabled)
	now := time.Now()

	headers := []tableCell{
		styler.dim("NAME"),
		styler.dim("REPOSITORY"),
		styler.dim("BRANCH"),
		styler.dim("STATE"),
		styler.dim("CREATED"),
		styler.dim("MACHINE"),
		styler.dim("SPECS"),
	}

	rows := make([][]tableCell, len(codespaces))
	for i, cs := range codespaces {
		rows[i] = []tableCell{
			styler.plain(cs.DisplayName),
			styler.plain(cs.Repository.FullName),
			styler.colored(cs.GitStatus.Ref, termenv.ANSICyan),
			codespaceStateCell(styler, cs.State),
			styler.dim(formatRelativeTime(cs.CreatedAt, now)),
			styler.plain(cs.Machine.Name),
			styler.dim(cs.Machine.DisplayName),
		}
	}

	colWidths := computeColumnWidths(headers, rows)

	flexibleCols := []int{0, 1, 2, 6}
	colWidths = fitColumnsToTerminal(colWidths, flexibleCols, getTerminalWidth())
	rows = truncateCells(rows, colWidths, flexibleCols)

	writeRow(stdout, headers, colWidths)
	for _, row := range rows {
		writeRow(stdout, row, colWidths)
	}

	return nil
}

func writeCodespaceUsage(w io.Writer) {
	fmt.Fprint(w, codespaceUsage)
}

func writeCodespaceListUsage(w io.Writer) {
	fmt.Fprint(w, codespaceListUsage)
}

const codespaceUsage = `Usage:
  gh x codespace <command> [flags]

Available commands:
  list        List your codespaces with machine specs

Flags:
  -h, --help  Show help

`

const codespaceListUsage = `Usage:
  gh x codespace list [flags]

Flags:
  -o, --org string   Filter by organization
  -R, --repo string  Filter by repository (OWNER/REPO)
  -h, --help         Show help

Examples:
  gh x codespace list
  gh x codespace list --org HemSoft
  gh x codespace list --repo HemSoft/gh-x

`
