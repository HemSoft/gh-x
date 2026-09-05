package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
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
	RequiredStatusChecks           []requiredStatusCheck `json:"required_status_checks"`
	Strict                         *bool                 `json:"strict_required_status_checks_policy"`
	EnforceOnCreate                *bool                 `json:"do_not_enforce_on_create"`
	RequiredApprovingReviewCount   *int                  `json:"required_approving_review_count"`
	DismissStaleReviewsOnPush      *bool                 `json:"dismiss_stale_reviews_on_push"`
	RequireCodeOwnerReview         *bool                 `json:"require_code_owner_review"`
	RequireLastPushApproval        *bool                 `json:"require_last_push_approval"`
	RequiredReviewThreadResolution *bool                 `json:"required_review_thread_resolution"`
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
	ci, ciContent := loadWorkflowWithContent(".github/workflows/ci.yml")
	autoRelease := loadWorkflow(".github/workflows/auto-release.yml")

	if err := validateRuleset(configuredRuleset); err != nil {
		fail(err.Error())
	}

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
	requirePinnedAction(ciContent, codeQLInit, "github/codeql-action/init", "v4")
	require(reflect.DeepEqual(codeQLInit.With, map[string]string{
		"languages":  "${{ matrix.language }}",
		"build-mode": "${{ matrix.build-mode }}",
	}), "CodeQL init must receive the language and build mode matrix values")
	codeQLAnalyze := namedStep(security, "Analyze with CodeQL")
	requirePinnedAction(ciContent, codeQLAnalyze, "github/codeql-action/analyze", "v4")
	require(actionSHA(codeQLInit.Uses) == actionSHA(codeQLAnalyze.Uses), "CodeQL init and analyze must use the same action revision")

	dependencyReview := ci.Jobs["dependency-review"]
	require(dependencyReview.Name == "Dependency Review", "CI must publish the Dependency Review check")
	require(reflect.DeepEqual(dependencyReview.Permissions, map[string]string{"contents": "read"}), "dependency review must use read-only contents permission")
	reviewStep := namedStep(dependencyReview, "Review dependency changes")
	require(reviewStep.If == "github.event_name == 'pull_request'", "dependency review must run only for pull requests")
	requirePinnedAction(ciContent, reviewStep, "actions/dependency-review-action", "v5")
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
		releaseJob.If == "github.event.workflow_run.conclusion == 'success' && (github.event.workflow_run.event == 'push' || github.event.workflow_run.event == 'workflow_dispatch')",
		"auto-release must require successful push or trusted workflow-dispatch CI",
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
	require(!existingNotes.ContinueOnError, "release lookup failures must not be suppressed")
	require(existingNotes.Env["RELEASE_TAG"] == "${{ steps.check.outputs.release_tag }}", "existing release notes must use the tagged head")
	require(strings.Contains(existingNotes.Run, `gh api --include --silent "repos/${GITHUB_REPOSITORY}/releases/tags/${RELEASE_TAG}"`), "existing release lookup must expose its HTTP status")
	require(strings.Contains(existingNotes.Run, `grep -Eq '^HTTP/[^ ]+ 404 '`), "only a confirmed missing release may skip reconciliation")
	require(strings.Contains(existingNotes.Run, `gh release view "$RELEASE_TAG" --json body --jq .body > release-notes.md`), "existing release notes must load the authoritative GitHub Release body")
	require(strings.Contains(existingNotes.Run, `echo "found=true" >> "$GITHUB_OUTPUT"`), "existing release lookup must publish a found result")
	require(strings.Contains(existingNotes.Run, `echo "found=false" >> "$GITHUB_OUTPUT"`), "a confirmed missing release must publish a not-found result")
	require(strings.Contains(existingNotes.Run, `exit 1`), "non-404 release lookup failures must fail reconciliation")

	changelog := namedStep(releaseJob, "Update changelog")
	require(changelog.If == "steps.check.outputs.skip == 'false' || steps.existing_release.outputs.found == 'true'", "changelog update must run for new and confirmed existing releases")
	require(changelog.Env["RELEASE_TAG"] == "${{ steps.version.outputs.tag || steps.check.outputs.release_tag }}", "changelog update must receive the new or resumed release tag")
	require(strings.Contains(changelog.Run, "git switch --detach origin/main"), "changelog reconciliation must start from current main")
	require(strings.Contains(changelog.Run, "go run ./.github/scripts/release-plan changelog"), "release workflow must use the tested changelog updater")

	mergeChangelog := namedStep(releaseJob, "Merge changelog update through CI")
	require(mergeChangelog.If == "steps.check.outputs.skip == 'false' || steps.existing_release.outputs.found == 'true'", "changelog pull request must run for new and confirmed existing releases")
	require(mergeChangelog.Env["RELEASE_TAG"] == "${{ steps.version.outputs.tag || steps.check.outputs.release_tag }}", "changelog pull request must receive the new or resumed release tag")
	require(mergeChangelog.Env["RELEASE_SHA"] == "${{ github.event.workflow_run.head_sha }}", "changelog reconciliation must retain the released commit SHA")
	require(strings.Contains(mergeChangelog.Run, "gh workflow run ci.yml --ref \"$branch\""), "changelog pull request must run CI on its exact branch")
	require(strings.Contains(mergeChangelog.Run, `branch="chore/changelog-${RELEASE_TAG#v}"`), "release retries must reuse a deterministic changelog branch")
	require(strings.Contains(mergeChangelog.Run, `gh pr list --base main --head "$branch" --state open`), "release retries must reuse an existing open changelog pull request")
	require(strings.Contains(mergeChangelog.Run, `--jq '.[0].url // empty'`), "a missing changelog pull request must produce an empty lookup")
	require(strings.Contains(mergeChangelog.Run, `git push --force-with-lease=`), "release retries must safely refresh the existing changelog branch")
	require(strings.Contains(mergeChangelog.Run, "gh run watch \"$run_id\" --exit-status"), "changelog pull request must wait for successful CI")
	require(strings.Contains(mergeChangelog.Run, "gh pr merge \"$pr_url\" --squash --delete-branch"), "changelog update must merge through a pull request")
	require(strings.Contains(mergeChangelog.Run, "gh workflow run ci.yml --ref main"), "the bot merge must dispatch main CI for any intervening product changes")
	noDiffStart := strings.Index(mergeChangelog.Run, "if git diff --quiet -- CHANGELOG.md; then")
	require(noDiffStart >= 0, "release retries must recognize an already-current changelog")
	noDiffEnd := strings.Index(mergeChangelog.Run[noDiffStart:], "\nfi\n\nbranch=")
	require(noDiffEnd >= 0, "already-current changelog handling must terminate its conditional")
	noDiffBranch := mergeChangelog.Run[noDiffStart : noDiffStart+noDiffEnd]
	require(strings.Contains(noDiffBranch, `if [[ "$(git rev-parse HEAD)" != "$RELEASE_SHA" ]]; then`), "already-current changelog retries must dispatch only when main advanced past the released commit")
	noDiffDispatch := strings.Index(noDiffBranch, "gh workflow run ci.yml --ref main")
	noDiffExit := strings.Index(noDiffBranch, "exit 0")
	require(noDiffDispatch >= 0 && noDiffExit >= 0 && noDiffDispatch < noDiffExit, "already-current changelog retries must dispatch main CI before returning")

	fmt.Fprintln(os.Stdout, "main ruleset and release workflows are consistent")
}

type rulesetValidation struct {
	valid   bool
	message string
}

func validateRuleset(configuredRuleset ruleset) error {
	requiredStatusRules := rulesOfType(configuredRuleset, "required_status_checks")
	pullRequestRules := rulesOfType(configuredRuleset, "pull_request")
	if err := firstRulesetError(
		rulesetValidation{configuredRuleset.Name == "main-quality-gate", "unexpected ruleset name"},
		rulesetValidation{configuredRuleset.Target == "branch", "ruleset must target branches"},
		rulesetValidation{configuredRuleset.Enforcement == "active", "ruleset must be active"},
		rulesetValidation{len(configuredRuleset.BypassActors) == 0, "ruleset must not have bypass actors"},
		rulesetValidation{equal(configuredRuleset.Conditions.RefName.Include, "refs/heads/main"), "ruleset must target only main"},
		rulesetValidation{len(configuredRuleset.Conditions.RefName.Exclude) == 0, "ruleset must not exclude refs"},
		rulesetValidation{len(requiredStatusRules) == 1, "ruleset must define one required-status-check rule"},
		rulesetValidation{len(pullRequestRules) == 1, "ruleset must define one pull-request rule"},
		rulesetValidation{len(configuredRuleset.Rules) == 2, "ruleset must define only status-check and pull-request rules"},
	); err != nil {
		return err
	}
	if err := validateRequiredStatusRule(requiredStatusRules[0]); err != nil {
		return err
	}
	return validatePullRequestRule(pullRequestRules[0])
}

func rulesOfType(configuredRuleset ruleset, ruleType string) []rulesetRule {
	matching := make([]rulesetRule, 0, 1)
	for _, rule := range configuredRuleset.Rules {
		if rule.Type == ruleType {
			matching = append(matching, rule)
		}
	}
	return matching
}

func validateRequiredStatusRule(rule rulesetRule) error {
	parameters := rule.Parameters
	return firstRulesetError(
		rulesetValidation{
			reflect.DeepEqual(parameters.RequiredStatusChecks, []requiredStatusCheck{{Context: "Quality Gate", IntegrationID: 15368}}),
			"ruleset must require Quality Gate from GitHub Actions",
		},
		rulesetValidation{parameters.Strict != nil && !*parameters.Strict, "strict mode must remain disabled"},
		rulesetValidation{parameters.EnforceOnCreate != nil && !*parameters.EnforceOnCreate, "checks must apply to new refs"},
	)
}

func validatePullRequestRule(rule rulesetRule) error {
	parameters := rule.Parameters
	return firstRulesetError(
		rulesetValidation{parameters.RequiredApprovingReviewCount != nil && *parameters.RequiredApprovingReviewCount == 0, "pull requests must require exactly zero approving reviews"},
		rulesetValidation{parameters.DismissStaleReviewsOnPush != nil && !*parameters.DismissStaleReviewsOnPush, "stale-review dismissal must remain disabled"},
		rulesetValidation{parameters.RequireCodeOwnerReview != nil && !*parameters.RequireCodeOwnerReview, "code-owner review must remain disabled"},
		rulesetValidation{parameters.RequireLastPushApproval != nil && !*parameters.RequireLastPushApproval, "last-push approval must remain disabled"},
		rulesetValidation{parameters.RequiredReviewThreadResolution != nil && *parameters.RequiredReviewThreadResolution, "pull requests must require resolved review threads"},
	)
}

func firstRulesetError(validations ...rulesetValidation) error {
	for _, validation := range validations {
		if !validation.valid {
			return fmt.Errorf("%s", validation.message)
		}
	}
	return nil
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
	result, _ := loadWorkflowWithContent(path)
	return result
}

func loadWorkflowWithContent(path string) (workflow, string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fail(fmt.Sprintf("read %s: %v", path, err))
	}
	var result workflow
	if err := yaml.Unmarshal(data, &result); err != nil {
		fail(fmt.Sprintf("parse %s: %v", path, err))
	}
	return result, string(data)
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

var actionSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func isPinnedAction(uses, expectedAction string) bool {
	action, sha, found := strings.Cut(uses, "@")
	if !found {
		return false
	}
	return action == expectedAction && actionSHAPattern.MatchString(sha)
}

func actionSHA(uses string) string {
	_, sha, _ := strings.Cut(uses, "@")
	return sha
}

func requirePinnedAction(workflowContent string, step workflowStep, expectedAction string, expectedMajor string) {
	require(isPinnedAction(step.Uses, expectedAction), fmt.Sprintf("%s must use a pinned commit SHA", expectedAction))
	version, ok := actionVersionComment(workflowContent, expectedAction)
	require(ok, fmt.Sprintf("%s must include a release-version comment", expectedAction))
	require(strings.HasPrefix(version, expectedMajor+"."), fmt.Sprintf("%s must use supported major version %s (found %s)", expectedAction, expectedMajor, version))
}

func actionVersionComment(workflowContent, action string) (string, bool) {
	pattern := regexp.MustCompile(`(?m)^[ \t]*(?:-[ \t]+)?uses:[ \t]+` + regexp.QuoteMeta(action) + `@[0-9a-f]{40}[ \t]+#[ \t]+(v\d+(?:\.\d+)*)`)
	matches := pattern.FindStringSubmatch(workflowContent)
	if len(matches) < 2 {
		return "", false
	}
	return matches[1], true
}
