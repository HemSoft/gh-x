package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func mutateWorkflowStep(job *workflowJob, name string, mutate func(*workflowStep)) {
	for index := range job.Steps {
		if job.Steps[index].Name == name {
			mutate(&job.Steps[index])
			return
		}
	}
	panic("missing workflow step: " + name)
}

func TestWindowsInstallerGateReady(t *testing.T) {
	tests := []struct {
		name   string
		want   bool
		mutate func(*workflowJob, *workflowJob)
	}{
		{name: "actual workflow qualifies", want: true},
		{name: "unrelated setup preserves contract", want: true, mutate: func(job, gate *workflowJob) {
			job.Steps = append([]workflowStep{{Name: "Future setup"}}, job.Steps...)
			gate.Steps = append([]workflowStep{{Name: "Future setup"}}, gate.Steps...)
		}},
		{name: "Linux cannot qualify Windows 5.1", mutate: func(job, _ *workflowJob) { job.RunsOn = "ubuntu-latest" }},
		{name: "conditional job is not required", mutate: func(job, _ *workflowJob) { job.If = "false" }},
		{name: "PowerShell 7 is not 5.1", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows PowerShell installer", func(step *workflowStep) { step.Run = "./tests/installer/test-dashboard-hub.ps1" })
		}},
		{name: "wrong shell", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows PowerShell installer", func(step *workflowStep) { step.Shell = "bash" })
		}},
		{name: "ignored failures", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows PowerShell installer", func(step *workflowStep) { step.ContinueOnError = true })
		}},
		{name: "conditional step", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows PowerShell installer", func(step *workflowStep) { step.If = "false" })
		}},
		{name: "missing dependency", mutate: func(_, gate *workflowJob) {
			gate.Needs = slices.DeleteFunc(gate.Needs, func(name string) bool { return name == "windows-installer" })
		}},
		{name: "missing failure guard", mutate: func(_, gate *workflowJob) {
			mutateWorkflowStep(gate, "Evaluate all gates", func(step *workflowStep) {
				step.Run = strings.ReplaceAll(step.Run, `"${{ needs.windows-installer.result }}" != "success"`, `"${{ needs.windows-installer.result }}" == "success"`)
			})
		}},
		{name: "skipped evaluator", mutate: func(_, gate *workflowJob) {
			mutateWorkflowStep(gate, "Evaluate all gates", func(step *workflowStep) { step.If = "false" })
		}},
		{name: "ignored evaluator failure", mutate: func(_, gate *workflowJob) {
			mutateWorkflowStep(gate, "Evaluate all gates", func(step *workflowStep) { step.ContinueOnError = true })
		}},
		{name: "conditional gate", mutate: func(_, gate *workflowJob) { gate.If = "false" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data, err := os.ReadFile("../workflows/ci.yml")
			if err != nil {
				t.Fatal(err)
			}
			var ci workflow
			if err := yaml.Unmarshal(data, &ci); err != nil {
				t.Fatal(err)
			}
			job, gate := ci.Jobs["windows-installer"], ci.Jobs["gate"]
			if test.mutate != nil {
				test.mutate(&job, &gate)
			}
			if got := windowsInstallerGateReady(job, gate); got != test.want {
				t.Fatalf("windowsInstallerGateReady() = %v, want %v", got, test.want)
			}
		})
	}
}
