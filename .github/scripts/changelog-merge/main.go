package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var branchPattern = regexp.MustCompile(`^chore/changelog-(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type config struct{ repo, branch, head string }
type command func(...string) ([]byte, error)
type pullRequest struct {
	Number       int
	State        string
	Draft        bool
	ChangedFiles int `json:"changed_files"`
	User         struct{ Login, Type string }
	Head         struct {
		Ref, SHA string
		Repo     struct {
			FullName string `json:"full_name"`
		}
	}
	Base struct {
		Ref  string
		Repo struct {
			FullName string `json:"full_name"`
		}
	}
	Merged bool
}
type changedFile struct{ Filename, Status string }

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), executionTimeout(os.Args[1:]))
	defer cancel()
	cfg := config{os.Getenv("GITHUB_REPOSITORY"), os.Getenv("CHANGELOG_BRANCH"), os.Getenv("EXPECTED_HEAD")}
	if err := run(ctx, cfg, os.Args[1:], ghCommand(ctx)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func ghCommand(ctx context.Context) command {
	return func(args ...string) ([]byte, error) {
		callCtx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		output, err := exec.CommandContext(callCtx, "gh", args...).CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("gh %s: %w: %s", strings.Join(args, " "), err, output)
		}
		return output, nil
	}
}

func run(ctx context.Context, cfg config, args []string, gh command) error {
	if len(args) != 1 || (args[0] != "review" && args[0] != "enable") {
		return errors.New("usage: changelog-merge <review|enable>")
	}
	if !repoPattern.MatchString(cfg.repo) || !branchPattern.MatchString(cfg.branch) || !shaPattern.MatchString(cfg.head) {
		return errors.New("invalid repository, changelog branch, or expected head")
	}
	var matches []struct{ Number int }
	if err := readJSON(gh, &matches, "pr", "list", "--repo", cfg.repo, "--head", cfg.branch, "--state", "open", "--json", "number"); err != nil {
		return err
	}
	if len(matches) != 1 {
		return errors.New("expected exactly one open changelog pull request")
	}
	number := strconv.Itoa(matches[0].Number)
	if err := inspectEligibility(gh, cfg, number); err != nil {
		return err
	}
	if args[0] == "review" {
		return waitForReview(ctx, gh, cfg, number, false)
	}
	if err := waitForReview(ctx, gh, cfg, number, true); err != nil {
		return err
	}
	if err := waitForReviewGate(ctx, gh, cfg); err != nil {
		return err
	}
	if err := inspectEligibility(gh, cfg, number); err != nil {
		return err
	}
	if _, err := gh("pr", "merge", number, "--repo", cfg.repo, "--auto", "--squash", "--match-head-commit", cfg.head); err != nil {
		return err
	}
	return waitForMerge(ctx, gh, cfg, number)
}

func readJSON(gh command, target any, args ...string) error {
	output, err := gh(args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(output, target); err != nil {
		return fmt.Errorf("decode GitHub response: %w", err)
	}
	return nil
}

func inspectEligibility(gh command, cfg config, number string) error {
	var pr pullRequest
	if err := readJSON(gh, &pr, "api", "repos/"+cfg.repo+"/pulls/"+number); err != nil {
		return err
	}
	var files []changedFile
	if err := readJSON(gh, &files, "api", "repos/"+cfg.repo+"/pulls/"+number+"/files?per_page=100"); err != nil {
		return err
	}
	return eligible(cfg, pr, files)
}

func eligible(cfg config, pr pullRequest, files []changedFile) error {
	if pr.State != "open" || pr.Draft || pr.Merged {
		return errors.New("changelog PR must be open and non-draft")
	}
	if pr.User.Login != "github-actions[bot]" || pr.User.Type != "Bot" {
		return errors.New("changelog PR must be authored by github-actions[bot]")
	}
	if pr.Head.Repo.FullName != cfg.repo || pr.Base.Repo.FullName != cfg.repo || pr.Base.Ref != "main" {
		return errors.New("changelog PR must be same-repository and target main")
	}
	if pr.Head.Ref != cfg.branch || pr.Head.SHA != cfg.head {
		return errors.New("changelog PR branch or head changed")
	}
	if pr.ChangedFiles != 1 || len(files) != 1 || files[0].Filename != "CHANGELOG.md" || files[0].Status != "modified" {
		return errors.New("changelog PR may only modify CHANGELOG.md")
	}
	return nil
}

func pause(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitForMerge(ctx context.Context, gh command, cfg config, number string) error {
	for {
		var pr pullRequest
		if err := readJSON(gh, &pr, "api", "repos/"+cfg.repo+"/pulls/"+number); err != nil {
			return err
		}
		if pr.Head.SHA != cfg.head {
			return errors.New("head changed while waiting for auto-merge; new head must pass its own Quality Gate")
		}
		if pr.Merged {
			return nil
		}
		if pr.State != "open" {
			return errors.New("changelog PR closed without merging")
		}
		if err := pause(ctx); err != nil {
			return fmt.Errorf("PR #%s has not merged; native auto-merge remains subject to its required gates: %w", number, err)
		}
	}
}

func waitForReview(ctx context.Context, gh command, cfg config, number string, allowRequests bool) error {
	sent := map[string]bool{}
	for {
		ready, err := pollReview(gh, cfg, number, sent, allowRequests)
		if err != nil {
			return err
		}
		if ready {
			return inspectEligibility(gh, cfg, number)
		}
		if err := pause(ctx); err != nil {
			return fmt.Errorf("PR #%s lacks a clean current-head AI review or resolved conversations: %w", number, err)
		}
	}
}

func pollReview(gh command, cfg config, number string, sent map[string]bool, allowRequests bool) (bool, error) {
	if err := inspectEligibility(gh, cfg, number); err != nil {
		return false, err
	}
	state, err := fetchReviewState(gh, cfg, number)
	if err != nil {
		return false, err
	}
	ready, requested, err := reviewReady(state, cfg.head)
	if err != nil {
		return false, err
	}
	cubicReady, cubicRequested, err := inspectCubic(gh, cfg, state)
	if err != nil {
		return false, err
	}
	if !allowRequests {
		return ready && cubicReady, nil
	}
	if !requested {
		if err := requestReview(gh, cfg, number, "codex", sent); err != nil {
			return false, err
		}
	}
	if !cubicRequested {
		if err := requestReview(gh, cfg, number, "cubic", sent); err != nil {
			return false, err
		}
	}
	return ready && cubicReady, nil
}

func requestReview(gh command, cfg config, number, reviewer string, sent map[string]bool) error {
	if sent[reviewer] {
		return nil
	}
	trigger := "@codex review"
	if reviewer == "cubic" {
		trigger = "@cubic-dev-ai review this PR"
	}
	body := trigger + "\n\n" + requestMarker(reviewer, cfg.head)
	if _, err := gh("pr", "comment", number, "--repo", cfg.repo, "--body", body); err != nil {
		return err
	}
	sent[reviewer] = true
	return nil
}

// A legacy CI workflow without this job must not be auto-merged by this helper.
func waitForReviewGate(ctx context.Context, gh command, cfg config) error {
	for {
		var response struct {
			CheckRuns []checkRun `json:"check_runs"`
		}
		if err := readJSON(gh, &response, "api", "repos/"+cfg.repo+"/commits/"+cfg.head+"/check-runs?check_name=Changelog%20AI%20Review&filter=latest"); err != nil {
			return err
		}
		passed, err := passingReviewGate(response.CheckRuns, cfg.head)
		if err != nil {
			return err
		}
		if passed {
			return nil
		}
		if err := pause(ctx); err != nil {
			return fmt.Errorf("no passing current-head Changelog AI Review job; keep PR open: %w", err)
		}
	}
}

func passingReviewGate(checks []checkRun, head string) (bool, error) {
	for _, check := range checks {
		if check.App.Slug != "github-actions" || check.Name != "Changelog AI Review" {
			continue
		}
		if check.HeadSHA != head {
			return false, errors.New("changelog review gate head mismatch")
		}
		if check.Status != "completed" {
			return false, nil
		}
		if check.Conclusion != "success" {
			return false, errors.New("changelog AI review gate failed")
		}
		return true, nil
	}
	return false, nil
}

// Merge polling includes queued CI, reviewer setup, and the actual merge.
func executionTimeout(args []string) time.Duration {
	if len(args) == 1 && args[0] == "enable" {
		return 90 * time.Minute
	}
	return 30 * time.Minute
}
