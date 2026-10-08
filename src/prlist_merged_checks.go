package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const mergedWorkflowFields = `checkSuite { workflowRun { workflow { name } } }`

const mergedCheckFields = `
  __typename
  ... on CheckRun {
    name status conclusion startedAt completedAt
    ` + mergedWorkflowFields + `
  }
  ... on StatusContext { context state }
`

type mergedPageInfo struct {
	HasNextPage bool   `json:"hasNextPage"`
	EndCursor   string `json:"endCursor"`
}

type mergedCheckConnection struct {
	Nodes    []mergedCheckNode `json:"nodes"`
	PageInfo mergedPageInfo    `json:"pageInfo"`
}

type mergedStatusCheckRollup struct {
	Contexts *mergedCheckConnection `json:"contexts"`
}

type mergedQueryClient struct {
	host         string
	omitWorkflow bool
}

// Older Enterprise schemas lack CheckSuite.workflowRun. Retry only that
// explicit schema error and remember the omission for this retrieval.
func (client *mergedQueryClient) query(query string) ([]byte, error) {
	if client.omitWorkflow {
		query = strings.ReplaceAll(query, mergedWorkflowFields, "")
	}
	data, err := fetchGraphQL(client.host, query)
	if err == nil || !strings.Contains(err.Error(), "Field 'workflowRun' doesn't exist on type 'CheckSuite'") {
		return data, err
	}
	client.omitWorkflow = true
	return fetchGraphQL(client.host, strings.ReplaceAll(query, mergedWorkflowFields, ""))
}

func completeMergedChecks(client *mergedQueryClient, node *mergedPRNode) error {
	if len(node.Commits.Nodes) == 0 {
		return nil
	}
	commit := node.Commits.Nodes[0].Commit
	if commit.StatusCheckRollup == nil {
		return nil
	}
	contexts := commit.StatusCheckRollup.Contexts
	if contexts == nil {
		return fmt.Errorf("merged check data unavailable")
	}
	seen := make(map[string]bool)
	for contexts.PageInfo.HasNextPage {
		next := contexts.PageInfo.EndCursor
		if commit.ID == "" || next == "" || seen[next] {
			return fmt.Errorf("merged check pagination did not advance")
		}
		seen[next] = true
		page, err := fetchMergedCheckPage(client, commit.ID, next)
		if err != nil {
			return err
		}
		if len(page.Nodes) == 0 {
			return fmt.Errorf("merged check pagination returned an empty continuation")
		}
		contexts.Nodes = append(contexts.Nodes, page.Nodes...)
		contexts.PageInfo = page.PageInfo
	}
	return nil
}

func fetchMergedCheckPage(client *mergedQueryClient, commitID, cursor string) (*mergedCheckConnection, error) {
	query := fmt.Sprintf(`query { node(id: %q) { ... on Commit { statusCheckRollup {
  contexts(first: 100, after: %q) {
    nodes { %s }
    pageInfo { hasNextPage endCursor }
  }
} } } }`, commitID, cursor, mergedCheckFields)
	data, err := client.query(query)
	if err != nil {
		return nil, err
	}
	return parseMergedCheckPage(data)
}

func parseMergedCheckPage(data []byte) (*mergedCheckConnection, error) {
	var response struct {
		Data struct {
			Node *struct {
				StatusCheckRollup *mergedStatusCheckRollup `json:"statusCheckRollup"`
			} `json:"node"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode merged checks: %w", err)
	}
	if len(response.Errors) > 0 {
		return nil, fmt.Errorf("merged checks: %s", response.Errors[0].Message)
	}
	if response.Data.Node == nil || response.Data.Node.StatusCheckRollup == nil || response.Data.Node.StatusCheckRollup.Contexts == nil {
		return nil, fmt.Errorf("merged check data unavailable")
	}
	return response.Data.Node.StatusCheckRollup.Contexts, nil
}
