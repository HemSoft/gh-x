package main

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestWindowsLauncherGateReady(t *testing.T) {
	cases := []struct {
		name   string
		want   bool
		mutate func(*workflowJob, *workflowJob)
	}{
		{name: "actual workflow qualifies", want: true},
		{name: "missing launcher step", mutate: func(job, _ *workflowJob) {
			job.Steps = slices.DeleteFunc(job.Steps, func(step workflowStep) bool { return step.Name == "Test Windows standalone launcher" })
		}},
		{name: "wrong test command", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows standalone launcher", func(step *workflowStep) { step.Run = "node --test src/dashboard-hub/*.test.mjs" })
		}},
		{name: "commented test command", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows standalone launcher", func(step *workflowStep) { step.Run = "# " + step.Run })
		}},
		{name: "wrong shell", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows standalone launcher", func(step *workflowStep) { step.Shell = "bash" })
		}},
		{name: "conditional launcher step", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows standalone launcher", func(step *workflowStep) { step.If = "false" })
		}},
		{name: "ignored launcher failure", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows standalone launcher", func(step *workflowStep) { step.ContinueOnError = true })
		}},
		{name: "missing Node setup", mutate: func(job, _ *workflowJob) {
			job.Steps = slices.DeleteFunc(job.Steps, func(step workflowStep) bool {
				return step.Uses == "actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38"
			})
		}},
		{name: "mutable Node action", mutate: func(job, _ *workflowJob) {
			mutateLauncherNodeSetup(job, func(step *workflowStep) { step.Uses = "actions/setup-node@v6" })
		}},
		{name: "wrong Node version source", mutate: func(job, _ *workflowJob) {
			mutateLauncherNodeSetup(job, func(step *workflowStep) { step.With["node-version-file"] = "go.mod" })
		}},
		{name: "conditional Node setup", mutate: func(job, _ *workflowJob) {
			mutateLauncherNodeSetup(job, func(step *workflowStep) { step.If = "false" })
		}},
		{name: "ignored Node setup failure", mutate: func(job, _ *workflowJob) {
			mutateLauncherNodeSetup(job, func(step *workflowStep) { step.ContinueOnError = true })
		}},
		{name: "Node setup after launcher", mutate: func(job, _ *workflowJob) {
			reorderLauncherSetup(job, "actions/setup-node", "Test Windows standalone launcher")
		}},
		{name: "checkout after Node setup", mutate: func(job, _ *workflowJob) {
			reorderLauncherSetup(job, "actions/checkout", "actions/setup-node")
		}},
		{name: "missing checkout", mutate: func(job, _ *workflowJob) {
			job.Steps = slices.DeleteFunc(job.Steps, func(step workflowStep) bool { return strings.HasPrefix(step.Uses, "actions/checkout@") })
		}},
		{name: "conditional checkout", mutate: func(job, _ *workflowJob) {
			job.Steps[0].If = "false"
		}},
		{name: "ignored checkout failure", mutate: func(job, _ *workflowJob) {
			job.Steps[0].ContinueOnError = true
		}},
		{name: "Linux cannot qualify lifecycle", mutate: func(job, _ *workflowJob) { job.RunsOn = "ubuntu-latest" }},
		{name: "aggregate dependency missing", mutate: func(_, gate *workflowJob) {
			gate.Needs = slices.DeleteFunc(gate.Needs, func(name string) bool { return name == "windows-installer" })
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := os.ReadFile("../workflows/ci.yml")
			if err != nil {
				t.Fatal(err)
			}
			var ci workflow
			if err := yaml.Unmarshal(data, &ci); err != nil {
				t.Fatal(err)
			}
			job, gate := ci.Jobs["windows-installer"], ci.Jobs["gate"]
			if c.mutate != nil {
				c.mutate(&job, &gate)
			}
			if got := windowsLauncherGateReady(job, gate); got != c.want {
				t.Fatalf("windowsLauncherGateReady() = %v, want %v", got, c.want)
			}
		})
	}
}

func mutateLauncherNodeSetup(job *workflowJob, mutate func(*workflowStep)) {
	for index := range job.Steps {
		if job.Steps[index].Uses == "actions/setup-node@249970729cb0ef3589644e2896645e5dc5ba9c38" {
			mutate(&job.Steps[index])
			return
		}
	}
	panic("missing Windows launcher Node setup")
}

func reorderLauncherSetup(job *workflowJob, action, after string) {
	from := slices.IndexFunc(job.Steps, func(step workflowStep) bool { return strings.HasPrefix(step.Uses, action+"@") })
	to := slices.IndexFunc(job.Steps, func(step workflowStep) bool { return step.Name == after || strings.HasPrefix(step.Uses, after+"@") })
	if from < 0 || to < 0 || from >= to {
		panic("invalid setup ordering fixture")
	}
	setup := job.Steps[from]
	copy(job.Steps[from:to], job.Steps[from+1:to+1])
	job.Steps[to] = setup
}
