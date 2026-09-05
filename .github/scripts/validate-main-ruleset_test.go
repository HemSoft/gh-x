package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestValidateRuleset(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*testing.T, *ruleset)
		wantErr string
	}{
		{name: "versioned ruleset is valid"},
		{
			name: "status check rule is missing",
			mutate: func(_ *testing.T, config *ruleset) {
				config.Rules = rulesOfType(*config, "pull_request")
			},
			wantErr: "must define one required-status-check rule",
		},
		{
			name: "pull request rule is missing",
			mutate: func(_ *testing.T, config *ruleset) {
				config.Rules = rulesOfType(*config, "required_status_checks")
			},
			wantErr: "must define one pull-request rule",
		},
		{
			name: "thread resolution is omitted",
			mutate: func(t *testing.T, config *ruleset) {
				parametersFor(t, config, "pull_request").RequiredReviewThreadResolution = nil
			},
			wantErr: "require resolved review threads",
		},
		{
			name: "thread resolution is disabled",
			mutate: func(t *testing.T, config *ruleset) {
				parametersFor(t, config, "pull_request").RequiredReviewThreadResolution = boolPointer(false)
			},
			wantErr: "require resolved review threads",
		},
		{
			name: "approval count is omitted",
			mutate: func(t *testing.T, config *ruleset) {
				parametersFor(t, config, "pull_request").RequiredApprovingReviewCount = nil
			},
			wantErr: "exactly zero approving reviews",
		},
		{
			name: "approval count changes",
			mutate: func(t *testing.T, config *ruleset) {
				parametersFor(t, config, "pull_request").RequiredApprovingReviewCount = intPointer(1)
			},
			wantErr: "exactly zero approving reviews",
		},
		{
			name: "stale review dismissal is enabled",
			mutate: func(t *testing.T, config *ruleset) {
				parametersFor(t, config, "pull_request").DismissStaleReviewsOnPush = boolPointer(true)
			},
			wantErr: "stale-review dismissal must remain disabled",
		},
		{
			name: "code owner review is enabled",
			mutate: func(t *testing.T, config *ruleset) {
				parametersFor(t, config, "pull_request").RequireCodeOwnerReview = boolPointer(true)
			},
			wantErr: "code-owner review must remain disabled",
		},
		{
			name: "last push approval is enabled",
			mutate: func(t *testing.T, config *ruleset) {
				parametersFor(t, config, "pull_request").RequireLastPushApproval = boolPointer(true)
			},
			wantErr: "last-push approval must remain disabled",
		},
		{
			name: "quality gate changes",
			mutate: func(t *testing.T, config *ruleset) {
				parametersFor(t, config, "required_status_checks").RequiredStatusChecks[0].Context = "Other Gate"
			},
			wantErr: "require Quality Gate from GitHub Actions",
		},
		{
			name: "bypass actor is added",
			mutate: func(_ *testing.T, config *ruleset) {
				config.BypassActors = []json.RawMessage{json.RawMessage(`{"actor_type":"RepositoryRole"}`)}
			},
			wantErr: "must not have bypass actors",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := loadTestRuleset(t)
			if test.mutate != nil {
				test.mutate(t, &config)
			}

			err := validateRuleset(config)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("validateRuleset() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateRuleset() error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func loadTestRuleset(t *testing.T) ruleset {
	t.Helper()
	data, err := os.ReadFile("../rulesets/main.json")
	if err != nil {
		t.Fatalf("read ruleset: %v", err)
	}
	var config ruleset
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse ruleset: %v", err)
	}
	return config
}

func parametersFor(t *testing.T, config *ruleset, ruleType string) *rulesetParameters {
	t.Helper()
	for i := range config.Rules {
		if config.Rules[i].Type == ruleType {
			return &config.Rules[i].Parameters
		}
	}
	t.Fatalf("missing %s rule", ruleType)
	return nil
}

func boolPointer(value bool) *bool {
	return &value
}

func intPointer(value int) *int {
	return &value
}
