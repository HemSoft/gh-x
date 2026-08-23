package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	xterm "golang.org/x/term"
)

const monitorUsage = `Usage:
  gh x monitor [flags]

Aliases:
  m

Launch a full-terminal dashboard monitoring the repositories configured in
your gh-x config. Left sidebar lists repos with open counts, top tabs switch
PRs / Issues, section sub-tabs filter views, and the bottom pane shows details
of the selected row. Data refreshes on an interval you can change with ` + "`s`" + `.

Flags:
  -h, --help          Show this help
      --print-query   Print the batched GraphQL query and exit

Examples:
  gh x monitor
  gh x m
`

// monitorTTYFunc is swappable in tests.
var monitorTTYFunc = func() bool {
	return xterm.IsTerminal(int(os.Stdin.Fd())) && xterm.IsTerminal(int(os.Stdout.Fd()))
}

// newMonitorProgramFunc is swappable in tests.
var newMonitorProgramFunc = func(model monitorModel) monitorProgram {
	return tea.NewProgram(model)
}

// monitorProgram hides the concrete tea.Program behind a tiny interface.
type monitorProgram interface {
	Run() (tea.Model, error)
}

func runMonitorCmd(args []string, stdout io.Writer, stderr io.Writer) error {
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			fmt.Fprint(stdout, monitorUsage)
			return nil
		case "--print-query":
			return printMonitorQuery(stdout)
		default:
			return fmt.Errorf("unknown argument %q; see gh x monitor --help", arg)
		}
	}
	if !monitorTTYFunc() {
		return errors.New("monitor requires an interactive terminal")
	}

	model, err := bootstrapMonitorModel()
	if err != nil {
		return err
	}
	program := newMonitorProgramFunc(model)
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("monitor session: %w", err)
	}
	return nil
}

// printMonitorQuery renders the batched GraphQL document without running it.
func printMonitorQuery(stdout io.Writer) error {
	configPath, err := monitorConfigPath()
	if err != nil {
		return err
	}
	cfg, _, err := loadOrCreateMonitorConfig(configPath, monitorSeedRepo())
	if err != nil {
		return err
	}
	query, err := buildMonitorGraphQLQuery(cfg)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, query)
	return nil
}

// bootstrapMonitorModel loads (or creates) config and session state.
func bootstrapMonitorModel() (monitorModel, error) {
	configPath, err := monitorConfigPath()
	if err != nil {
		return monitorModel{}, err
	}
	statePath, err := monitorStatePath()
	if err != nil {
		return monitorModel{}, err
	}
	cfg, _, err := loadOrCreateMonitorConfig(configPath, monitorSeedRepo())
	if err != nil {
		return monitorModel{}, err
	}
	state := loadMonitorState(statePath)
	if _, statErr := os.Stat(statePath); statErr != nil {
		// Virgin launch: no saved selections yet, so start on the broadest
		// section instead of the first (often narrowest) one.
		if state.Tab == monitorTabIssues && len(cfg.IssueSections) > 0 {
			state.SubTab = len(cfg.IssueSections) - 1
		} else if len(cfg.PRSections) > 0 {
			state.SubTab = len(cfg.PRSections) - 1
		}
	}
	state = clampMonitorState(state, monitorTabCount,
		len(cfg.PRSections), len(cfg.IssueSections), len(cfg.Repos)+1)
	return newMonitorModel(cfg, configPath, statePath, state), nil
}

// monitorSeedRepo returns owner/repo of the current directory when available;
// failures are fine because the starter config documents where to add repos.
func monitorSeedRepo() string {
	owner, name, err := resolveRepo("")
	if err != nil {
		return ""
	}
	return owner + "/" + name
}
