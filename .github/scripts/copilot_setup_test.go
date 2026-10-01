package main

import (
	"os"
	"regexp"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCopilotSetupActionAndCLIVersionsMatch(t *testing.T) {
	data, err := os.ReadFile("../workflows/copilot-setup-steps.yml")
	if err != nil {
		t.Fatal(err)
	}
	// The action's verified release tag is recorded beside its immutable SHA.
	pin := regexp.MustCompile(`(?m)^\s+uses: (github/gh-aw-actions/setup-cli@[a-f0-9]{40}) # (v[0-9]+\.[0-9]+\.[0-9]+)\s*$`).FindSubmatch(data)
	if len(pin) != 3 {
		t.Fatal("setup-cli must record an immutable action SHA and its release tag")
	}
	var workflow struct {
		Jobs map[string]struct {
			Steps []struct {
				Uses string `yaml:"uses"`
				With struct {
					Version string `yaml:"version"`
				} `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	for _, step := range workflow.Jobs["copilot-setup-steps"].Steps {
		if step.Uses == string(pin[1]) {
			if step.With.Version != string(pin[2]) {
				t.Fatalf("setup-cli action tag %s differs from installed CLI %s; update both pins together", pin[2], step.With.Version)
			}
			return
		}
	}
	t.Fatal("copilot-setup-steps job must install the pinned gh-aw CLI")
}
