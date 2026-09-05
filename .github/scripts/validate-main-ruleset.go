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
	On          workflowTriggers       `yaml:"on"`
	Concurrency *workflowConcurrency   `yaml:"concurrency"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress"`
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
	Name        string              `yaml:"name"`
	If          string              `yaml:"if"`
	Uses        string              `yaml:"uses"`
	Needs       []string            `yaml:"needs"`
	Permissions map[string]string   `yaml:"permissions"`
	Strategy    workflowStrategy    `yaml:"strategy"`
	Concurrency workflowConcurrency `yaml:"concurrency"`
	Steps       []workflowStep      `yaml:"steps"`
}

type workflowStrategy struct {
	FailFast *bool `yaml:"fail-fast"`
	Matrix   struct {
		Include []map[string]string `yaml:"include"`
	} `yaml:"matrix"`
}

type workflowStep struct {
	Name            string            `yaml:"name"`
	ID              string            `yaml:"id"`
	Run             string            `yaml:"run"`
	Env             map[string]string `yaml:"env"`
	If              string            `yaml:"if"`
	Uses            string            `yaml:"uses"`
	With            map[string]string `yaml:"with"`
	ContinueOnError bool              `yaml:"continue-on-error"`
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

	security := ci.Jobs["security-analysis"]
	require(security.Name == "CodeQL Analysis (${{ matrix.language }})", "CI must publish per-language CodeQL checks")
	require(reflect.DeepEqual(security.Permissions, map[string]string{
		"contents":        "read",
		"security-events": "write",
	}), "CodeQL must use least-privilege permissions")
	require(security.Strategy.FailFast != nil && !*security.Strategy.FailFast, "CodeQL must explicitly disable matrix fail-fast")
	require(reflect.DeepEqual(security.Strategy.Matrix.Include, []map[string]string{
		{"language": "go", "build-mode": "autobuild"},
		{"language": "javascript-typescript", "build-mode": "none"},
	}), "CodeQL must analyze Go and JavaScript with supported build modes")
	codeQLInit := namedStep(security, "Initialize CodeQL")
	require(codeQLInit.Uses == "github/codeql-action/init@v4", "CodeQL init must use the supported v4 action")
	require(reflect.DeepEqual(codeQLInit.With, map[string]string{
		"languages":  "${{ matrix.language }}",
		"build-mode": "${{ matrix.build-mode }}",
	}), "CodeQL init must receive the language and build mode matrix values")
	require(namedStep(security, "Analyze with CodeQL").Uses == "github/codeql-action/analyze@v4", "CodeQL analysis must use the supported v4 action")

	dependencyReview := ci.Jobs["dependency-review"]
	require(dependencyReview.Name == "Dependency Review", "CI must publish the Dependency Review check")
	require(reflect.DeepEqual(dependencyReview.Permissions, map[string]string{"contents": "read"}), "dependency review must use read-only contents permission")
	reviewStep := namedStep(dependencyReview, "Review dependency changes")
	require(reviewStep.If == "github.event_name == 'pull_request'", "dependency review must run only for pull requests")
	require(reviewStep.Uses == "actions/dependency-review-action@v5", "dependency review must use the current v5 action")
	require(reviewStep.With["fail-on-severity"] == "high", "dependency review must block high and critical vulnerabilities")
	require(namedStep(dependencyReview, "Skip dependency review outside pull requests").If == "github.event_name != 'pull_request'", "non-PR runs must complete dependency review without a bypass")

	changelogCheck := namedStep(ci.Jobs["lint"], "Validate changelog release links")
	require(changelogCheck.Env["GH_TOKEN"] == "${{ github.token }}", "changelog validation must authenticate GitHub Release queries")
	require(strings.TrimSpace(changelogCheck.Run) == "go run ./.github/scripts/changelog-check", "CI must run the tested changelog validator")

	gate := ci.Jobs["gate"]
	require(gate.Name == "Quality Gate", "CI must publish the Quality Gate check")
	require(equal(gate.Needs, "build-and-test", "lint", "quality", "mutation", "security-analysis", "dependency-review"), "Quality Gate must depend on every build, quality, and security job")
	gateRun := namedStep(gate, "Evaluate all gates").Run
	require(strings.Contains(gateRun, `"${{ needs.security-analysis.result }}" != "success"`), "Quality Gate must reject failed CodeQL analysis")
	require(strings.Contains(gateRun, `"${{ needs.dependency-review.result }}" != "success"`), "Quality Gate must reject failed dependency review")
	require(strings.Contains(gateRun, "::error::One or more quality gates failed"), "Quality Gate must report a failed dependency")
	require(strings.Contains(gateRun, "exit 1"), "Quality Gate must fail when a dependency is unsuccessful")

	require(equal(autoRelease.On.WorkflowRun.Workflows, "CI Quality Gates"), "auto-release must follow CI Quality Gates")
	require(equal(autoRelease.On.WorkflowRun.Types, "completed"), "auto-release must follow completed CI runs")
	require(equal(autoRelease.On.WorkflowRun.Branches, "main"), "auto-release must follow main-branch CI runs")
	require(autoRelease.Concurrency == nil, "auto-release concurrency must not include skipped workflow runs")
	releaseJob, ok := autoRelease.Jobs["release"]
	require(ok, "auto-release must define the release job")
	require(
		releaseJob.If == "github.event.workflow_run.conclusion == 'success' && github.event.workflow_run.event == 'push'",
		"auto-release must require a successful push-triggered CI run",
	)
	require(releaseJob.Concurrency.Group == "auto-release", "eligible releases must share one concurrency group")
	require(!releaseJob.Concurrency.CancelInProgress, "an active release must not be cancelled")
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

	existingNotes := namedStep(releaseJob, "Load existing release notes")
	require(existingNotes.ID == "existing_release", "existing release notes step must expose its outcome")
	require(existingNotes.If == "steps.check.outputs.release_tag != ''", "tagged release runs must check for existing release notes")
	require(existingNotes.ContinueOnError, "a tag without a GitHub Release must remain a successful skip")
	require(existingNotes.Env["RELEASE_TAG"] == "${{ steps.check.outputs.release_tag }}", "existing release notes must use the tagged head")
	require(strings.TrimSpace(existingNotes.Run) == `gh release view "$RELEASE_TAG" --json body --jq .body > release-notes.md`, "existing release notes must load the authoritative GitHub Release body")

	changelog := namedStep(releaseJob, "Update changelog")
	require(changelog.If == "steps.check.outputs.skip == 'false' || steps.existing_release.outcome == 'success'", "changelog update must run for new and confirmed existing releases")
	require(changelog.Env["RELEASE_TAG"] == "${{ steps.version.outputs.tag || steps.check.outputs.release_tag }}", "changelog update must receive the new or resumed release tag")
	require(strings.Contains(changelog.Run, "git switch --detach origin/main"), "changelog reconciliation must start from current main")
	require(strings.Contains(changelog.Run, "go run ./.github/scripts/release-plan changelog"), "release workflow must use the tested changelog updater")

	mergeChangelog := namedStep(releaseJob, "Merge changelog update through CI")
	require(mergeChangelog.If == "steps.check.outputs.skip == 'false' || steps.existing_release.outcome == 'success'", "changelog pull request must run for new and confirmed existing releases")
	require(mergeChangelog.Env["RELEASE_TAG"] == "${{ steps.version.outputs.tag || steps.check.outputs.release_tag }}", "changelog pull request must receive the new or resumed release tag")
	require(strings.Contains(mergeChangelog.Run, "gh workflow run ci.yml --ref \"$branch\""), "changelog pull request must run CI on its exact branch")
	require(strings.Contains(mergeChangelog.Run, `branch="chore/changelog-${RELEASE_TAG#v}"`), "release retries must reuse a deterministic changelog branch")
	require(strings.Contains(mergeChangelog.Run, `gh pr list --base main --head "$branch" --state open`), "release retries must reuse an existing open changelog pull request")
	require(strings.Contains(mergeChangelog.Run, `git push --force-with-lease=`), "release retries must safely refresh the existing changelog branch")
	require(strings.Contains(mergeChangelog.Run, "gh run watch \"$run_id\" --exit-status"), "changelog pull request must wait for successful CI")
	require(strings.Contains(mergeChangelog.Run, "gh pr merge \"$pr_url\" --squash --delete-branch"), "changelog update must merge through a pull request")

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
