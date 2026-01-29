// Example guard using the guardsdk package
//
// This demonstrates how to build a DIFC guard using the SDK,
// which handles all the low-level WASM memory management and JSON marshaling.
//
// Build with:
//
//	export GOROOT=$(~/go/bin/go1.23.4 env GOROOT)
//	tinygo build -o guard.wasm -target=wasi main.go
package main

import (
	"fmt"

	sdk "github.com/githubnext/gh-aw-mcpg/examples/guards/guardsdk"
)

func init() {
	sdk.RegisterLabelResource(labelResource)
	sdk.RegisterLabelResponse(labelResponse)
}

func labelResource(req *sdk.LabelResourceRequest) (*sdk.LabelResourceResponse, error) {
	// Log the incoming request using host logging
	sdk.LogDebug(fmt.Sprintf("labelResource called for tool: %s", req.ToolName))

	// Extract owner/repo for repo-scoped tags
	owner, repo, hasRepo := req.GetOwnerRepo()
	repoID := ""
	if hasRepo {
		repoID = owner + "/" + repo
	}

	// Default response - empty labels (public, no endorsement)
	resp := &sdk.LabelResourceResponse{
		Resource:  sdk.NewPublicResource(fmt.Sprintf("resource:%s", req.ToolName)),
		Operation: sdk.OperationRead,
	}

	switch req.ToolName {
	// Write operations - contributor level
	case "create_issue", "update_issue", "create_pull_request":
		resp.Operation = sdk.OperationWrite
		if repoID != "" {
			resp.Resource.Integrity = sdk.ContributorIntegrity(repoID)
		}

	// Read-write operations - maintainer level (expands to contributor + maintainer)
	case "merge_pull_request":
		resp.Operation = sdk.OperationReadWrite
		if repoID != "" {
			resp.Resource.Integrity = sdk.MaintainerIntegrity(repoID)
		}

	// Read operations with repository visibility check
	case "list_issues", "list_pull_requests":
		labelByRepoVisibility(req, resp)

	case "get_issue":
		labelByRepoVisibility(req, resp)
		labelByIssueDetails(req, resp)
	}

	return resp, nil
}

func labelResponse(req *sdk.LabelResponseRequest) (*sdk.LabelResponseResponse, error) {
	// No fine-grained response labeling in this example
	return nil, nil
}

// labelByRepoVisibility checks if the repository is private
func labelByRepoVisibility(req *sdk.LabelResourceRequest, resp *sdk.LabelResourceResponse) {
	owner, repo, ok := req.GetOwnerRepo()
	if !ok {
		return
	}
	repoID := owner + "/" + repo

	// Call backend to check repository visibility
	result, err := sdk.CallBackend("search_repositories", map[string]interface{}{
		"query": fmt.Sprintf("repo:%s", repoID),
	})
	if err != nil {
		return
	}

	// Check if private - use repo-scoped tag
	if repoData, ok := result.(map[string]interface{}); ok {
		if items, ok := repoData["items"].([]interface{}); ok && len(items) > 0 {
			if firstItem, ok := items[0].(map[string]interface{}); ok {
				if private, ok := firstItem["private"].(bool); ok && private {
					resp.Resource.Secrecy = []string{"private:" + repoID}
				}
			}
		}
	}
}

// labelByIssueDetails adds labels based on issue-specific information
func labelByIssueDetails(req *sdk.LabelResourceRequest, resp *sdk.LabelResourceResponse) {
	owner, repo, ok := req.GetOwnerRepo()
	if !ok {
		return
	}

	issueNum, ok := req.GetInt("issue_number")
	if !ok {
		return
	}

	// Get issue details from backend
	result, err := sdk.CallBackend("get_issue", map[string]interface{}{
		"owner":        owner,
		"repo":         repo,
		"issue_number": issueNum,
	})
	if err != nil {
		return
	}

	issueData, ok := result.(map[string]interface{})
	if !ok {
		return
	}

	// Update description with author
	if user, ok := issueData["user"].(map[string]interface{}); ok {
		if login, ok := user["login"].(string); ok {
			resp.Resource.Description = fmt.Sprintf("issue:%s/%s#%d by %s", owner, repo, issueNum, login)
		}
	}

	// Check for sensitive labels - use "secret" for highest secrecy
	if labels, ok := issueData["labels"].([]interface{}); ok {
		for _, label := range labels {
			if labelData, ok := label.(map[string]interface{}); ok {
				if name, ok := labelData["name"].(string); ok {
					if name == "security" || name == "confidential" {
						resp.Resource.Secrecy = append(resp.Resource.Secrecy, "secret")
						break
					}
				}
			}
		}
	}
}

func main() {}
