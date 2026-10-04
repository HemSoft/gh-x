package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWindowsInstallerGateReady(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*workflowJob, *workflowJob)
	}{
		{name: "actual workflow qualifies"},
		{name: "Linux cannot qualify Windows 5.1", mutate: func(job, _ *workflowJob) { job.RunsOn = "ubuntu-latest" }},
		{name: "conditional job is not required", mutate: func(job, _ *workflowJob) { job.If = "false" }},
		{name: "PowerShell 7 is not 5.1", mutate: func(job, _ *workflowJob) { job.Steps[2].Run = "./tests/installer/test-dashboard-hub.ps1" }},
		{name: "wrong shell", mutate: func(job, _ *workflowJob) { job.Steps[2].Shell = "bash" }},
		{name: "ignored failures", mutate: func(job, _ *workflowJob) { job.Steps[2].ContinueOnError = true }},
		{name: "conditional step", mutate: func(job, _ *workflowJob) { job.Steps[2].If = "false" }},
		{name: "missing dependency", mutate: func(_, gate *workflowJob) {
			gate.Needs = slices.DeleteFunc(gate.Needs, func(name string) bool { return name == "windows-installer" })
		}},
		{name: "missing failure guard", mutate: func(_, gate *workflowJob) {
			gate.Steps[0].Run = strings.ReplaceAll(gate.Steps[0].Run, `"${{ needs.windows-installer.result }}" != "success"`, `"${{ needs.windows-installer.result }}" == "success"`)
		}},
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
			if got, want := windowsInstallerGateReady(job, gate), test.mutate == nil; got != want {
				t.Fatalf("windowsInstallerGateReady() = %v, want %v", got, want)
			}
		})
	}
}
