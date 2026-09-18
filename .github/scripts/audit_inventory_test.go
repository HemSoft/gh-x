package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type auditInventory struct {
	Gates []struct {
		ID string   `json:"id"`
		CI []string `json:"ci"`
	} `json:"gates"`
	Hosted         []auditStepClassification `json:"hosted"`
	Infrastructure []auditStepClassification `json:"infrastructure"`
}

type auditStepClassification struct {
	Step   string `json:"step"`
	Reason string `json:"reason"`
}

type auditWorkflow struct {
	Jobs map[string]struct {
		Steps []struct {
			Name string `yaml:"name"`
			Run  string `yaml:"run"`
			Uses string `yaml:"uses"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestLocalAuditInventoryMatchesCI(t *testing.T) {
	inventoryData, err := os.ReadFile("../local-quality-gates.json")
	if err != nil {
		t.Fatal(err)
	}
	workflowData, err := os.ReadFile("../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var inventory auditInventory
	if err := json.Unmarshal(inventoryData, &inventory); err != nil {
		t.Fatal(err)
	}
	var workflow auditWorkflow
	if err := yaml.Unmarshal(workflowData, &workflow); err != nil {
		t.Fatal(err)
	}
	classified := make(map[string]bool)
	add := func(step string) {
		t.Helper()
		if classified[step] {
			t.Fatalf("CI step classified twice: %s", step)
		}
		classified[step] = true
	}
	for _, gate := range inventory.Gates {
		for _, step := range gate.CI {
			add(step)
		}
	}
	for _, group := range [][]auditStepClassification{inventory.Hosted, inventory.Infrastructure} {
		for _, entry := range group {
			if strings.TrimSpace(entry.Reason) == "" {
				t.Fatalf("missing non-local reason for %s", entry.Step)
			}
			add(entry.Step)
		}
	}
	for job, definition := range workflow.Jobs {
		for _, step := range definition.Steps {
			if auditSetupAction(step.Uses) {
				continue
			}
			key := job + "/" + step.Name
			if !classified[key] {
				t.Fatalf("CI step missing from local audit inventory: %s", key)
			}
			delete(classified, key)
		}
	}
	if len(classified) != 0 {
		t.Fatalf("local audit inventory references removed CI steps: %v", classified)
	}
}

func auditSetupAction(uses string) bool {
	for _, prefix := range []string{
		"actions/checkout@", "actions/setup-go@", "actions/setup-node@",
		"actions/upload-artifact@", "actions/download-artifact@",
	} {
		if strings.HasPrefix(uses, prefix) {
			return true
		}
	}
	return false
}
