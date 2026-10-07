package main

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestExtractTerminalOptions(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    []string
		clear   bool
		json    bool
		wantErr bool
	}{
		{"empty", nil, []string{}, false, false, false},
		{"before command", []string{"--clear", "status"}, []string{"status"}, true, false, false},
		{"alias", []string{"s", "--clear"}, []string{"s"}, true, false, false},
		{"between commands", []string{"pr", "--clear", "me"}, []string{"pr", "me"}, true, false, false},
		{"after nested command", []string{"pr", "me", "--clear"}, []string{"pr", "me"}, true, false, false},
		{"disabled", []string{"--clear=false", "status"}, []string{"status"}, false, false, false},
		{"last clear wins", []string{"--clear", "status", "--clear=false"}, []string{"status"}, false, false, false},
		{"last clear enables", []string{"--clear=false", "status", "--clear=true"}, []string{"status"}, true, false, false},
		{"JSON", []string{"pr", "me", "--clear", "--json"}, []string{"pr", "me", "--json"}, true, true, false},
		{"single dash JSON", []string{"pr", "me", "--clear", "-json"}, []string{"pr", "me", "-json"}, true, true, false},
		{"JSON disabled", []string{"pr", "list", "--json=false", "--clear"}, []string{"pr", "list", "--json=false"}, true, false, false},
		{"JSON last wins", []string{"pr", "atm", "--json", "--clear", "--json=false"}, []string{"pr", "atm", "--json", "--json=false"}, true, false, false},
		{"value literal", []string{"pr", "list", "--search", "--clear"}, []string{"pr", "list", "--search", "--clear"}, false, false, false},
		{"value and flag", []string{"pr", "list", "--clear", "--search", "--clear"}, []string{"pr", "list", "--search", "--clear"}, true, false, false},
		{"JSON literal", []string{"pr", "list", "--clear", "--search", "--json"}, []string{"pr", "list", "--search", "--json"}, true, false, false},
		{"inline value", []string{"pr", "list", "--search=--clear", "--clear"}, []string{"pr", "list", "--search=--clear"}, true, false, false},
		{"repeatable value", []string{"issue", "--label", "--clear", "--clear"}, []string{"issue", "--label", "--clear"}, true, false, false},
		{"short value", []string{"pr", "list", "-a", "--clear", "--clear"}, []string{"pr", "list", "-a", "--clear"}, true, false, false},
		{"short boolean", []string{"pr", "atm", "-a", "--clear"}, []string{"pr", "atm", "-a"}, true, false, false},
		{"workflow boolean", []string{"workflow", "-a", "--clear"}, []string{"workflow", "-a"}, true, false, false},
		{"run workflow value", []string{"run", "-w", "--clear", "--clear"}, []string{"run", "-w", "--clear"}, true, false, false},
		{"PR review positional", []string{"pr", "review", "42", "--instructions", "--clear", "--clear"}, []string{"pr", "review", "42", "--instructions", "--clear"}, true, false, false},
		{"numeric view", []string{"pr", "42", "-R", "--clear", "--clear"}, []string{"pr", "42", "-R", "--clear"}, true, false, false},
		{"explicit view", []string{"pr", "view", "42", "--repo", "--clear", "--clear"}, []string{"pr", "view", "42", "--repo", "--clear"}, true, false, false},
		{"changelog value", []string{"changelog", "--version", "--clear", "--clear"}, []string{"changelog", "--version", "--clear"}, true, false, false},
		{"PR changelog value", []string{"pr", "changelog", "--version", "--clear", "--clear"}, []string{"pr", "changelog", "--version", "--clear"}, true, false, false},
		{"codespace value", []string{"codespace", "list", "--org", "--clear", "--clear"}, []string{"codespace", "list", "--org", "--clear"}, true, false, false},
		{"boundary", []string{"--clear", "pr", "review", "--", "--clear"}, []string{"pr", "review", "--", "--clear"}, true, false, false},
		{"boundary used as value", []string{"pr", "list", "--search", "--", "--clear"}, []string{"pr", "list", "--search", "--"}, true, false, false},
		{"boundary disables consumption", []string{"pr", "list", "--", "--clear"}, []string{"pr", "list", "--", "--clear"}, false, false, false},
		{"missing command value", []string{"--clear", "pr", "list", "--search"}, []string{"pr", "list", "--search"}, true, false, false},
		{"invalid boolean", []string{"--clear=wrong"}, []string{}, false, false, true},
		{"unknown command", []string{"--clear", "unknown"}, []string{"unknown"}, true, false, false},
		{"monitor", []string{"m", "--clear"}, []string{"m"}, true, false, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := append([]string(nil), test.args...)
			got, options, err := extractTerminalOptions(test.args)
			if !reflect.DeepEqual(got, test.want) || options.clear != test.clear || options.json != test.json || (err != nil) != test.wantErr {
				t.Fatalf("got %q %+v %v; want %q clear=%v json=%v error=%v", got, options, err, test.want, test.clear, test.json, test.wantErr)
			}
			if !reflect.DeepEqual(original, test.args) {
				t.Fatal("modified caller arguments")
			}
		})
	}
}

func TestRunClearBeforeAllOutput(t *testing.T) {
	forceTerminal(t, true)
	for _, args := range [][]string{
		{"--clear", "--help"}, {"help", "--clear"}, {"--clear"},
		{"--clear", "pr", "help", "--clear"}, {"--clear", "unknown"},
		{"--clear", "status", "--merged=bad"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var output bytes.Buffer
			_, _ = run(args, &output, &output)
			got := output.String()
			if !strings.HasPrefix(got, terminalClearSequence+"gh-x ") || strings.Count(got, terminalClearSequence) != 1 {
				t.Fatalf("clear must precede banner and output exactly once, got %q", got)
			}
		})
	}
}

func TestRunClearDisabledOutput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		tty  bool
	}{
		{"absent", []string{"--help"}, true},
		{"false", []string{"--clear=false", "--help"}, true},
		{"nonterminal", []string{"--clear", "--help"}, false},
		{"JSON", []string{"pr", "me", "--clear", "--json", "--help"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			forceTerminal(t, test.tty)
			var output bytes.Buffer
			_, err := run(test.args, &output, &output)
			if err != nil || strings.Contains(output.String(), terminalClearSequence) {
				t.Fatalf("clear should be disabled: output=%q error=%v", output.String(), err)
			}
		})
	}
}

func TestRunClearWriteError(t *testing.T) {
	forceTerminal(t, true)
	var stderr bytes.Buffer
	_, err := run([]string{"--clear", "help"}, clearErrorWriter{}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "clear terminal: fixture write failed") || stderr.Len() != 0 {
		t.Fatalf("clear failure must stop dispatch: error=%v stderr=%q", err, stderr.String())
	}
}

func TestRunClearInvalidValue(t *testing.T) {
	forceTerminal(t, true)
	var output bytes.Buffer
	_, err := run([]string{"--clear=wrong", "--help"}, &output, &output)
	if err == nil || !strings.Contains(err.Error(), "invalid value for --clear") || output.Len() != 0 {
		t.Fatalf("invalid global value: error=%v output=%q", err, output.String())
	}
}

func TestRunClearMonitorHelp(t *testing.T) {
	forceTerminal(t, true)
	var output bytes.Buffer
	_, err := run([]string{"m", "--clear", "--help"}, &output, &output)
	if err != nil || !strings.HasPrefix(output.String(), terminalClearSequence+"Usage:") {
		t.Fatalf("monitor help must clear before output without starting a TUI: error=%v output=%q", err, output.String())
	}
}

func TestTerminalOutputRejectsCapturedWriters(t *testing.T) {
	if terminalOutputFunc(&bytes.Buffer{}) {
		t.Fatal("buffer must not be classified as a terminal")
	}
	file, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close fixture output: %v", err)
		}
	})
	if terminalOutputFunc(file) {
		t.Fatal("regular file must not be classified as a terminal")
	}
}

func TestConsumesFlagValue(t *testing.T) {
	flags := flag.NewFlagSet("fixture", flag.ContinueOnError)
	flags.Bool("toggle", false, "")
	flags.String("value", "", "")
	for _, arg := range []string{"--unknown", "--toggle"} {
		if consumesFlagValue(flags, arg) {
			t.Fatalf("%q should not consume a following value", arg)
		}
	}
}

func forceTerminal(t *testing.T, enabled bool) {
	t.Helper()
	original := terminalOutputFunc
	terminalOutputFunc = func(io.Writer) bool { return enabled }
	t.Cleanup(func() { terminalOutputFunc = original })
}

type clearErrorWriter struct{}

func (clearErrorWriter) Write([]byte) (int, error) {
	return 0, errors.New("fixture write failed")
}
