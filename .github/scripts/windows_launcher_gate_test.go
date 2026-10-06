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
				return strings.HasPrefix(step.Uses, "actions/setup-node@")
			})
		}},
		{name: "mutable Node action", mutate: func(job, _ *workflowJob) {
			mutateLauncherNodeSetup(job, func(step *workflowStep) { step.Uses = "actions/setup-node@v6" })
		}},
		{name: "wrong Node version source", mutate: func(job, _ *workflowJob) {
			mutateLauncherNodeSetup(job, func(step *workflowStep) { step.With["node-version-file"] = "go.mod" })
		}},
		{name: "later Node setup overrides version", mutate: func(job, _ *workflowJob) {
			insertBeforeLauncher(job, workflowStep{Uses: "actions/setup-node@" + strings.Repeat("a", 40), With: map[string]string{"node-version": "18"}})
		}},
		{name: "later mutable Node setup", mutate: func(job, _ *workflowJob) {
			insertBeforeLauncher(job, workflowStep{Uses: "actions/setup-node@v6", With: map[string]string{"node-version-file": ".node-version"}})
		}},
		{name: "unrelated step before checkout", want: true, mutate: func(job, _ *workflowJob) {
			job.Steps = append([]workflowStep{{Name: "Unrelated preparation", Run: "echo ready"}}, job.Steps...)
		}},
		{name: "conditional Node setup", mutate: func(job, _ *workflowJob) {
			mutateLauncherNodeSetup(job, func(step *workflowStep) { step.If = "false" })
		}},
		{name: "ignored Node setup failure", mutate: func(job, _ *workflowJob) {
			mutateLauncherNodeSetup(job, func(step *workflowStep) { step.ContinueOnError = true })
		}},
		{name: "PATH override between Node and launcher", mutate: func(job, _ *workflowJob) {
			insertBeforeLauncher(job, workflowStep{Name: "Replace Node path", Run: "'C:/unqualified-node' >> $env:GITHUB_PATH"})
		}},
		{name: "launcher test filtering via Node options", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows standalone launcher", func(step *workflowStep) { step.Env = map[string]string{"NODE_OPTIONS": "--test-only"} })
		}},
		{name: "launcher environment overrides PATH", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows standalone launcher", func(step *workflowStep) {
				step.Env = map[string]string{"NODE_OPTIONS": "", "PATH": "C:/unqualified-node"}
			})
		}},
		{name: "launcher inherits Node options", mutate: func(job, _ *workflowJob) {
			mutateWorkflowStep(job, "Test Windows standalone launcher", func(step *workflowStep) { step.Env = nil })
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
			mutateLauncherAction(job, "actions/checkout", func(step *workflowStep) { step.If = "false" })
		}},
		{name: "ignored checkout failure", mutate: func(job, _ *workflowJob) {
			mutateLauncherAction(job, "actions/checkout", func(step *workflowStep) { step.ContinueOnError = true })
		}},
		{name: "foreign checkout before Node cannot qualify a later repository checkout", mutate: func(job, _ *workflowJob) {
			var original workflowStep
			mutateLauncherAction(job, "actions/checkout", func(step *workflowStep) { original = *step })
			mutateLauncherAction(job, "actions/checkout", func(step *workflowStep) { step.With = map[string]string{"repository": "example/unrelated"} })
			job.Steps = append(job.Steps, original)
		}},
		{name: "later checkout replaces triggering revision", mutate: func(job, _ *workflowJob) {
			mutateLauncherAction(job, "actions/checkout", func(step *workflowStep) {
				copy := *step
				copy.With = map[string]string{"ref": "main"}
				job.Steps = append(job.Steps, copy)
			})
		}},
		{name: "checkout outside workspace root", mutate: func(job, _ *workflowJob) {
			mutateLauncherAction(job, "actions/checkout", func(step *workflowStep) { step.With = map[string]string{"path": "dependency"} })
		}},
		{name: "checkout of an old ref", mutate: func(job, _ *workflowJob) {
			mutateLauncherAction(job, "actions/checkout", func(step *workflowStep) { step.With = map[string]string{"ref": "main"} })
		}},
		{name: "explicit current repository and workspace root", want: true, mutate: func(job, _ *workflowJob) {
			mutateLauncherAction(job, "actions/checkout", func(step *workflowStep) {
				step.With = map[string]string{"repository": "${{ github.repository }}", "path": "."}
			})
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
	mutateLauncherAction(job, "actions/setup-node", mutate)
}

func mutateLauncherAction(job *workflowJob, action string, mutate func(*workflowStep)) {
	for index := range job.Steps {
		if strings.HasPrefix(job.Steps[index].Uses, action+"@") {
			mutate(&job.Steps[index])
			return
		}
	}
	panic("missing Windows launcher action: " + action)
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

func TestLauncherNodeMutationsFollowPinChanges(t *testing.T) {
	data, err := os.ReadFile("../workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var ci workflow
	if err := yaml.Unmarshal(data, &ci); err != nil {
		t.Fatal(err)
	}
	job, gate := ci.Jobs["windows-installer"], ci.Jobs["gate"]
	mutateLauncherNodeSetup(&job, func(step *workflowStep) { step.Uses = "actions/setup-node@" + strings.Repeat("a", 40) })
	if !windowsLauncherGateReady(job, gate) {
		t.Fatal("an immutable pin change must preserve the contract")
	}
	job.Steps = append([]workflowStep{{Name: "Unrelated preparation", Run: "echo ready"}}, job.Steps...)
	mutateLauncherAction(&job, "actions/checkout", func(step *workflowStep) { step.If = "false" })
	if windowsLauncherGateReady(job, gate) {
		t.Fatal("checkout mutations must follow unrelated prepended steps")
	}
	mutateLauncherAction(&job, "actions/checkout", func(step *workflowStep) { step.If = "" })
	mutateLauncherNodeSetup(&job, func(step *workflowStep) { step.ContinueOnError = true })
	if windowsLauncherGateReady(job, gate) {
		t.Fatal("mutations must still target Node after its pin changes")
	}
}

func insertBeforeLauncher(job *workflowJob, step workflowStep) {
	index := slices.IndexFunc(job.Steps, func(candidate workflowStep) bool { return candidate.Name == "Test Windows standalone launcher" })
	job.Steps = slices.Insert(job.Steps, index, step)
}
