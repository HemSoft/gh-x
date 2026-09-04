package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"

	"gopkg.in/yaml.v3"
)

type ruleset struct {
	Name         string            `json:"name"`
	Target       string            `json:"target"`
	Enforcement  string            `json:"enforcement"`
	BypassActors []json.RawMessage `json:"bypass_actors"`
	Conditions   struct {
		RefName struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"ref_name"`
	} `json:"conditions"`
	Rules []rulesetRule `json:"rules"`
}

type rulesetRule struct {
	Type       string            `json:"type"`
	Parameters rulesetParameters `json:"parameters"`
}

type rulesetParameters struct {
	RequiredStatusChecks []requiredStatusCheck `json:"required_status_checks"`
	Strict               *bool                 `json:"strict_required_status_checks_policy"`
	EnforceOnCreate      *bool                 `json:"do_not_enforce_on_create"`
}

type requiredStatusCheck struct {
	Context       string `json:"context"`
	IntegrationID int    `json:"integration_id"`
}

type workflow struct {
	On   workflowTriggers       `yaml:"on"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowTriggers struct {
	PullRequest workflowEvent    `yaml:"pull_request"`
	Push        workflowEvent    `yaml:"push"`
	WorkflowRun workflowRunEvent `yaml:"workflow_run"`
}

type workflowEvent struct {
	Branches []string `yaml:"branches"`
	Types    []string `yaml:"types"`
}

type workflowRunEvent struct {
	Workflows []string `yaml:"workflows"`
	Branches  []string `yaml:"branches"`
	Types     []string `yaml:"types"`
}

type workflowJob struct {
	Name  string         `yaml:"name"`
	If    string         `yaml:"if"`
	Uses  string         `yaml:"uses"`
	Steps []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Name string            `yaml:"name"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
}

func main() {
	var configuredRuleset ruleset
	loadJSON(".github/rulesets/main.json", &configuredRuleset)
	ci := loadWorkflow(".github/workflows/ci.yml")
	autoRelease := loadWorkflow(".github/workflows/auto-release.yml")

	require(configuredRuleset.Name == "main-quality-gate", "unexpected ruleset name")
	require(configuredRuleset.Target == "branch", "ruleset must target branches")
	require(configuredRuleset.Enforcement == "active", "ruleset must be active")
	require(len(configuredRuleset.BypassActors) == 0, "ruleset must not have bypass actors")
	require(equal(configuredRuleset.Conditions.RefName.Include, "refs/heads/main"), "ruleset must target only main")
	require(len(configuredRuleset.Conditions.RefName.Exclude) == 0, "ruleset must not exclude refs")

	requiredRules := make([]rulesetRule, 0, 1)
	for _, rule := range configuredRuleset.Rules {
		if rule.Type == "required_status_checks" {
			requiredRules = append(requiredRules, rule)
		}
	}
	require(len(requiredRules) == 1, "ruleset must define one required-status-check rule")
	parameters := requiredRules[0].Parameters
	require(
		reflect.DeepEqual(parameters.RequiredStatusChecks, []requiredStatusCheck{{Context: "Quality Gate", IntegrationID: 15368}}),
		"ruleset must require Quality Gate from GitHub Actions",
	)
	require(parameters.Strict != nil && !*parameters.Strict, "strict mode must remain disabled")
	require(parameters.EnforceOnCreate != nil && !*parameters.EnforceOnCreate, "checks must apply to new refs")

	require(equal(ci.On.PullRequest.Branches, "main"), "CI must target pull requests to main")
	require(equal(ci.On.PullRequest.Types, "opened", "synchronize", "reopened", "edited"), "CI pull-request events changed")
	require(equal(ci.On.Push.Branches, "main"), "CI must report status for the main branch badge")
	require(ci.Jobs["gate"].Name == "Quality Gate", "CI must publish the Quality Gate check")

	require(equal(autoRelease.On.WorkflowRun.Workflows, "CI Quality Gates"), "auto-release must follow CI Quality Gates")
	require(equal(autoRelease.On.WorkflowRun.Types, "completed"), "auto-release must follow completed CI runs")
	require(equal(autoRelease.On.WorkflowRun.Branches, "main"), "auto-release must follow main-branch CI runs")
	releaseJob, ok := autoRelease.Jobs["release"]
	require(ok, "auto-release must define the release job")
	require(
		releaseJob.If == "github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.event == 'push'",
		"auto-release must require a successful push-triggered CI run",
	)
	for _, job := range autoRelease.Jobs {
		require(job.Uses != "./.github/workflows/ci.yml", "auto-release must not duplicate the CI suite")
	}

	check := namedStep(releaseJob, "Check whether release is needed")
	require(check.Env["RELEASE_SHA"] == "${{ github.event.workflow_run.head_sha }}", "release check must receive the validated SHA through env")
	require(strings.TrimSpace(check.Run) == "go run ./.github/scripts/release-plan check", "release check must use the tested release-plan command")

	version := namedStep(releaseJob, "Determine next version")
	require(version.Env["LATEST_TAG"] == "${{ steps.check.outputs.latest }}", "version step must receive the latest tag through env")
	require(version.Env["VERSION_BASE_TAG"] == "${{ steps.check.outputs.version_base }}", "version step must reserve versions across all semantic tags")
	require(strings.TrimSpace(version.Run) == "go run ./.github/scripts/release-plan version", "version step must use the tested release-plan command")

	notes := namedStep(releaseJob, "Generate release notes")
	require(notes.Env["LATEST_TAG"] == "${{ steps.check.outputs.latest }}", "release notes must receive the latest tag through env")
	require(notes.Env["RELEASE_TAG"] == "${{ steps.version.outputs.tag }}", "release notes must receive the calculated release tag through env")
	require(strings.TrimSpace(notes.Run) == "go run ./.github/scripts/release-plan notes", "release notes must use the tested release-plan command")

	create := namedStep(releaseJob, "Create release")
	require(create.Env["RELEASE_SHA"] == "${{ github.event.workflow_run.head_sha }}", "release creation must receive the validated SHA through env")
	require(create.Env["RELEASE_TAG"] == "${{ steps.version.outputs.tag }}", "release creation must receive the calculated release tag through env")
	require(strings.TrimSpace(create.Run) == "go run ./.github/scripts/release-plan create", "release creation must use the tested release-plan command")

	fmt.Fprintln(os.Stdout, "main ruleset and release workflows are consistent")
}

func loadJSON(path string, target any) {
	data, err := os.ReadFile(path)
	if err != nil {
		fail(fmt.Sprintf("read %s: %v", path, err))
	}
	if err := json.Unmarshal(data, target); err != nil {
		fail(fmt.Sprintf("parse %s: %v", path, err))
	}
}

func loadWorkflow(path string) workflow {
	data, err := os.ReadFile(path)
	if err != nil {
		fail(fmt.Sprintf("read %s: %v", path, err))
	}
	var result workflow
	if err := yaml.Unmarshal(data, &result); err != nil {
		fail(fmt.Sprintf("parse %s: %v", path, err))
	}
	return result
}

func namedStep(job workflowJob, name string) workflowStep {
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	fail("missing workflow step: " + name)
	return workflowStep{}
}

func equal[T comparable](actual []T, expected ...T) bool {
	return reflect.DeepEqual(actual, expected)
}

func require(condition bool, message string) {
	if !condition {
		fail(message)
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
