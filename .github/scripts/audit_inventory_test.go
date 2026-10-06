package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

func TestDashboardQualityGateCannotBeDisabled(t *testing.T) {
	contents, err := os.ReadFile("../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for name := range dashboardQualityCommands["quality"] {
		for _, disguise := range []string{"comment", "disabled", "allowed failure"} {
			t.Run(name+"/"+disguise, func(t *testing.T) {
				var ci workflow
				if err := yaml.Unmarshal(contents, &ci); err != nil {
					t.Fatal(err)
				}
				if !dashboardQualityReady(ci) {
					t.Fatal("current JavaScript gate is incomplete")
				}
				job := ci.Jobs["quality"]
				for i := range job.Steps {
					if job.Steps[i].Name != name {
						continue
					}
					switch disguise {
					case "comment":
						job.Steps[i].Run = "# " + job.Steps[i].Run
					case "disabled":
						job.Steps[i].If = "false"
					case "allowed failure":
						job.Steps[i].ContinueOnError = true
					}
				}
				ci.Jobs["quality"] = job
				if dashboardQualityReady(ci) {
					t.Fatal("disabled JavaScript gate was accepted")
				}
			})
		}
	}
}

func TestDashboardInfrastructureContract(t *testing.T) {
	contents, err := os.ReadFile("../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	nodeCases := []struct {
		name   string
		mutate func(*workflowStep)
	}{
		{"mutable tag", func(step *workflowStep) { step.Uses = "actions/setup-node@v6" }},
		{"conditional setup", func(step *workflowStep) { step.If = "false" }},
		{"allowed setup failure", func(step *workflowStep) { step.ContinueOnError = true }},
	}
	for _, jobName := range []string{"build-and-test", "quality", "performance"} {
		for _, test := range nodeCases {
			t.Run(jobName+"/"+test.name, func(t *testing.T) {
				var ci workflow
				if err := yaml.Unmarshal(contents, &ci); err != nil {
					t.Fatal(err)
				}
				job := ci.Jobs[jobName]
				for index := range job.Steps {
					if strings.HasPrefix(job.Steps[index].Uses, "actions/setup-node@") {
						test.mutate(&job.Steps[index])
					}
				}
				ci.Jobs[jobName] = job
				if dashboardQualityReady(ci) {
					t.Fatal("unlocked Node setup was accepted")
				}
			})
		}
	}
	uploadCases := []struct {
		name   string
		mutate func(*workflowStep)
	}{
		{"allowed upload failure", func(step *workflowStep) { step.ContinueOnError = true }},
		{"conditional upload", func(step *workflowStep) { step.If = "success()" }},
		{"wrong artifact name", func(step *workflowStep) { step.With["name"] = "wrong" }},
		{"wrong artifact path", func(step *workflowStep) { step.With["path"] = "wrong" }},
		{"wrong retention", func(step *workflowStep) { step.With["retention-days"] = "1" }},
		{"missing evidence must fail", func(step *workflowStep) { step.With["if-no-files-found"] = "warn" }},
		{"mutable uploader", func(step *workflowStep) { step.Uses = "actions/upload-artifact@v7" }},
	}
	for _, test := range uploadCases {
		t.Run(test.name, func(t *testing.T) {
			var ci workflow
			if err := yaml.Unmarshal(contents, &ci); err != nil {
				t.Fatal(err)
			}
			job := ci.Jobs["quality"]
			mutateWorkflowStep(&job, "Upload dashboard quality evidence", test.mutate)
			ci.Jobs["quality"] = job
			if dashboardQualityReady(ci) {
				t.Fatal("weakened evidence upload was accepted")
			}
		})
	}
}

func TestHiddenHelperQualityScopes(t *testing.T) {
	contents, err := os.ReadFile("../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ job, name, scope string }{
		{"build-and-test", "Build", "./.github/scripts/..."},
		{"build-and-test", "Vet", "./.github/scripts/..."},
		{"build-and-test", "Test Go packages (race detection enabled)", "./.github/scripts/..."},
		{"lint", "Staticcheck", "./.github/scripts/..."},
		{"lint", "Gocritic (anti-pattern detection)", "./.github/scripts/..."},
		{"lint", "Errcheck (unchecked errors)", "./.github/scripts/..."},
		{"lint", "Dead code detection", "./.github/scripts/..."},
		{"lint", "Vulnerability scan", "./.github/scripts/..."},
		{"quality", "Enforce: cyclomatic complexity ≤ 10", ".github/scripts"},
		{"quality", "Enforce: cognitive complexity ≤ 15", ".github/scripts"},
		{"quality", "Enforce: CRAP score < 30", "Assert-CrapThreshold"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var ci workflow
			if err := yaml.Unmarshal(contents, &ci); err != nil {
				t.Fatal(err)
			}
			if !helperQualityScopesReady(ci) {
				t.Fatal("current CI omits helper gates")
			}
			job := ci.Jobs[test.job]
			for i := range job.Steps {
				if job.Steps[i].Name == test.name {
					job.Steps[i].Run = strings.ReplaceAll(job.Steps[i].Run, test.scope, "")
				}
			}
			ci.Jobs[test.job] = job
			if helperQualityScopesReady(ci) {
				t.Fatal("removing helper scope did not fail validator")
			}
		})
	}
}

func TestHelperScopeCannotComeFromCommentsOrUnrelatedCommands(t *testing.T) {
	contents, err := os.ReadFile("../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for jobName, steps := range helperQualityCommands {
		for name := range steps {
			for _, disguise := range []string{"comment", "echo", "disabled", "missing step"} {
				t.Run(name+"/"+disguise, func(t *testing.T) {
					var ci workflow
					if err := yaml.Unmarshal(contents, &ci); err != nil {
						t.Fatal(err)
					}
					job := ci.Jobs[jobName]
					for i := range job.Steps {
						if job.Steps[i].Name != name {
							continue
						}
						scope := "./.github/scripts/..."
						if jobName == "quality" {
							scope = ".github/scripts"
						}
						job.Steps[i].Run = strings.ReplaceAll(job.Steps[i].Run, scope, "")
						switch disguise {
						case "comment":
							job.Steps[i].Run += "\n# " + scope
						case "echo":
							job.Steps[i].Run += "\necho " + scope
						case "disabled":
							job.Steps[i].Run = "if false; then\n" + steps[name] + "\nfi"
						case "missing step":
							job.Steps[i].Name = "renamed step"
						}
					}
					ci.Jobs[jobName] = job
					if helperQualityScopesReady(ci) {
						t.Fatal("disguised or missing helper scope was accepted")
					}
				})
			}
		}
	}
}

func TestDashboardDiagnosticsRetainFailure(t *testing.T) {
	data, err := os.ReadFile("../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var ci workflow
	if err := yaml.Unmarshal(data, &ci); err != nil {
		t.Fatal(err)
	}
	for name, log := range map[string]string{"Install dashboard quality tools": "install.log", "Test dashboard quality gate": "fixtures.log"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			bin := filepath.Join(directory, "bin")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bin, "node"), []byte("#!/bin/sh\necho original-quality-failure >&2\nexit 23\n"), 0700); err != nil {
				t.Fatal(err)
			}
			step := namedStep(ci.Jobs["quality"], name)
			if step.Shell != "bash" {
				t.Fatalf("shell %q loses pipeline failure semantics", step.Shell)
			}
			command := exec.Command("bash", "--noprofile", "--norc", "-e", "-o", "pipefail", "-c", step.Run)
			command.Env = append(os.Environ(), "RUNNER_TEMP="+directory, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := command.CombinedOutput()
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 23 {
				t.Fatalf("original exit 23 lost: %v, %s", err, output)
			}
			retained, err := os.ReadFile(filepath.Join(directory, "dashboard-quality", log))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(retained), "original-quality-failure") {
				t.Fatalf("original stderr not retained: %s", retained)
			}
		})
	}
}
