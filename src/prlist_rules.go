package main

import (
	"encoding/json"
	"fmt"
	"net/url"
)

// extractReportedContexts collects the context/check names from statusCheckRollup items.
func extractReportedContexts(items []checkItem) map[string]bool {
	contexts := make(map[string]bool)
	for _, item := range items {
		switch item.Typename {
		case "CheckRun":
			if item.Name != "" {
				contexts[item.Name] = true
			}
		case "StatusContext":
			if item.Context != "" {
				contexts[item.Context] = true
			}
		}
	}
	return contexts
}

// fetchRequiredCheckContexts returns the set of required status check context
// names for a branch, derived from repository rulesets. Best-effort: returns
// nil on error so callers fall back to per-item normalization only.
func fetchRequiredCheckContexts(owner, name, branch string) (map[string]bool, bool) {
	endpoint := fmt.Sprintf("repos/%s/%s/rules/branches/%s", owner, name, url.PathEscape(branch))
	stdout, _, err := ghExecFunc("api", endpoint)
	if err != nil {
		return nil, false
	}
	return parseRequiredCheckRulesResult(stdout.Bytes())
}

func parseRequiredCheckRulesResult(data []byte) (map[string]bool, bool) {
	var rawRules []json.RawMessage
	if err := json.Unmarshal(data, &rawRules); err != nil {
		return nil, false
	}
	if rawRules == nil {
		return nil, false
	}
	for _, rawRule := range rawRules {
		var rule map[string]json.RawMessage
		if err := json.Unmarshal(rawRule, &rule); err != nil || rule == nil {
			return nil, false
		}
	}
	if _, err := decodeRequiredCheckRules(data); err != nil {
		return nil, false
	}
	return parseRequiredCheckRules(data), true
}

func parseRequiredCheckRules(data []byte) map[string]bool {
	contexts, _ := decodeRequiredCheckRules(data)
	return contexts
}

func decodeRequiredCheckRules(data []byte) (map[string]bool, error) {
	var rules []struct {
		Type       string `json:"type"`
		Parameters struct {
			RequiredStatusChecks []struct {
				Context string `json:"context"`
			} `json:"required_status_checks"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		return nil, err
	}
	contexts := make(map[string]bool)
	for _, rule := range rules {
		if rule.Type == "required_status_checks" {
			for _, check := range rule.Parameters.RequiredStatusChecks {
				if check.Context != "" {
					contexts[check.Context] = true
				}
			}
		}
	}
	if len(contexts) == 0 {
		return nil, nil
	}
	return contexts, nil
}
