package main

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// This is a sample DIFC guard that runs as a WASM module inside the gateway
// It uses exported functions and host function imports for sandbox security

// callBackend is imported from the host (gateway) environment
// It allows the guard to make read-only calls to the backend MCP server
//
//go:wasmimport env call_backend
func callBackend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32

// hostLog is imported from the host (gateway) environment
// It allows the guard to send log messages back to the gateway
// Log levels: 0=debug, 1=info, 2=warn, 3=error
//
//go:wasmimport env host_log
func hostLog(level, msgPtr, msgLen uint32)

// Log level constants
const (
	LogLevelDebug = 0
	LogLevelInfo  = 1
	LogLevelWarn  = 2
	LogLevelError = 3
)

// logDebug sends a debug level log message to the gateway
func logDebug(msg string) {
	b := []byte(msg)
	hostLog(LogLevelDebug, uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b)))
}

// logInfo sends an info level log message to the gateway
func logInfo(msg string) {
	b := []byte(msg)
	hostLog(LogLevelInfo, uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b)))
}

// logWarn sends a warning level log message to the gateway
func logWarn(msg string) {
	b := []byte(msg)
	hostLog(LogLevelWarn, uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b)))
}

// logError sends an error level log message to the gateway
func logError(msg string) {
	b := []byte(msg)
	hostLog(LogLevelError, uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b)))
}

// Request structures
type LabelResourceInput struct {
	ToolName     string                 `json:"tool_name"`
	ToolArgs     map[string]interface{} `json:"tool_args"`
	Capabilities interface{}            `json:"capabilities,omitempty"`
}

type LabelResponseInput struct {
	ToolName     string      `json:"tool_name"`
	ToolResult   interface{} `json:"tool_result"`
	Capabilities interface{} `json:"capabilities,omitempty"`
}

// Response structures
type LabelResourceOutput struct {
	Resource  ResourceLabels `json:"resource"`
	Operation string         `json:"operation"`
}

type ResourceLabels struct {
	Description string   `json:"description"`
	Secrecy     []string `json:"secrecy"`
	Integrity   []string `json:"integrity"`
}

type LabelResponseOutput struct {
	Items []LabeledItem `json:"items,omitempty"`
}

type LabeledItem struct {
	Data   interface{}    `json:"data"`
	Labels ResourceLabels `json:"labels"`
}

// label_resource is called by the gateway to label a resource before access
//
//export label_resource
func labelResource(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	// Read input JSON from WASM memory
	input := readBytes(inputPtr, inputLen)
	var req LabelResourceInput
	if err := json.Unmarshal(input, &req); err != nil {
		logError("failed to unmarshal label_resource input")
		return -1
	}

	logDebug(fmt.Sprintf("labeling resource for tool: %s", req.ToolName))

	// Extract owner/repo for repo-scoped tags
	owner, _ := req.ToolArgs["owner"].(string)
	repo, _ := req.ToolArgs["repo"].(string)
	repoID := ""
	if owner != "" && repo != "" {
		repoID = owner + "/" + repo
	}

	// Default labels - empty secrecy (public) and empty integrity (no endorsement)
	output := LabelResourceOutput{
		Resource: ResourceLabels{
			Description: fmt.Sprintf("resource:%s", req.ToolName),
			Secrecy:     []string{},
			Integrity:   []string{},
		},
		Operation: "read",
	}

	// Determine labels based on tool name
	switch req.ToolName {
	case "create_issue", "update_issue", "create_pull_request":
		output.Operation = "write"
		// Contributor level: only contributor tag
		if repoID != "" {
			output.Resource.Integrity = []string{"contributor:" + repoID}
		}

	case "merge_pull_request":
		output.Operation = "read-write"
		// Maintainer level: contributor + maintainer (hierarchical expansion)
		if repoID != "" {
			output.Resource.Integrity = []string{"contributor:" + repoID, "maintainer:" + repoID}
		}

	case "list_issues", "list_pull_requests":
		output.Operation = "read"
		// Label based on repository visibility
		labelByRepoVisibility(&output, req.ToolArgs)

	case "get_issue":
		output.Operation = "read"
		// Label based on repository visibility first
		labelByRepoVisibility(&output, req.ToolArgs)

		// Use tool arguments to get issue-specific information
		// ToolArgs contains: owner, repo, issue_number
		if owner, ok := req.ToolArgs["owner"].(string); ok {
			if repo, ok := req.ToolArgs["repo"].(string); ok {
				if issueNum, ok := req.ToolArgs["issue_number"].(float64); ok {
					// Call backend to get issue details for labeling
					issueInfo, err := callBackendHelper("get_issue", map[string]interface{}{
						"owner":        owner,
						"repo":         repo,
						"issue_number": int(issueNum),
					})

					if err == nil {
						if issueData, ok := issueInfo.(map[string]interface{}); ok {
							// Label based on issue author
							if user, ok := issueData["user"].(map[string]interface{}); ok {
								if login, ok := user["login"].(string); ok {
									output.Resource.Description = fmt.Sprintf("issue:%s/%s#%d by %s", owner, repo, int(issueNum), login)
								}
							}

							// Check for sensitive labels
							if labels, ok := issueData["labels"].([]interface{}); ok {
								for _, label := range labels {
									if labelData, ok := label.(map[string]interface{}); ok {
										if name, ok := labelData["name"].(string); ok {
											if name == "security" || name == "confidential" {
												// Use repo-scoped private tag plus sensitivity indicator
												output.Resource.Secrecy = []string{"private:" + owner + "/" + repo, "secret"}
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	// Marshal output
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return -1
	}

	// Check output size
	if uint32(len(outputJSON)) > outputSize {
		return -1 // Output too large
	}

	// Write output to WASM memory
	writeBytes(outputPtr, outputJSON)
	return int32(len(outputJSON))
}

// label_response is called by the gateway to label response data.
// Uses the path-based labeling format which is more efficient as it doesn't
// require copying response data - just returns paths and labels.
//
//export label_response
func labelResponse(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	// Read input JSON from WASM memory
	input := readBytes(inputPtr, inputLen)
	var req LabelResponseInput
	if err := json.Unmarshal(input, &req); err != nil {
		return -1
	}

	// Check if this is a collection response that needs fine-grained labeling
	response := labelResponseItems(req.ToolName, req.ToolResult)
	if response == nil {
		// No fine-grained labeling needed
		return 0
	}

	// Marshal response
	outputJSON, err := json.Marshal(response)
	if err != nil {
		return -1
	}

	// Check buffer size
	if uint32(len(outputJSON)) > outputSize {
		// Return -2 to indicate buffer too small, write required size
		sizeBytes := []byte{
			byte(len(outputJSON)),
			byte(len(outputJSON) >> 8),
			byte(len(outputJSON) >> 16),
			byte(len(outputJSON) >> 24),
		}
		writeBytes(outputPtr, sizeBytes)
		return -2
	}

	writeBytes(outputPtr, outputJSON)
	return int32(len(outputJSON))
}

// PathLabelResponse is the path-based labeling format
type PathLabelResponse struct {
	LabeledPaths  []PathLabel     `json:"labeled_paths"`
	DefaultLabels *ResourceLabels `json:"default_labels,omitempty"`
	ItemsPath     string          `json:"items_path,omitempty"`
}

// PathLabel associates a JSON Pointer path with labels
type PathLabel struct {
	Path   string         `json:"path"`
	Labels ResourceLabels `json:"labels"`
}

// labelResponseItems checks if this is a collection and labels each item by path
func labelResponseItems(toolName string, result interface{}) *PathLabelResponse {
	// Check common collection patterns
	resultMap, ok := result.(map[string]interface{})
	if !ok {
		// Not a map - might be direct array or single item
		if arr, ok := result.([]interface{}); ok {
			return labelArrayItems(toolName, arr, "")
		}
		return nil
	}

	// Check for "items" array (common in GitHub search results)
	if items, ok := resultMap["items"].([]interface{}); ok && len(items) > 0 {
		return labelArrayItems(toolName, items, "/items")
	}

	// Check for direct array results (e.g., list_issues)
	// No collection found
	return nil
}

// labelArrayItems labels each item in an array using path-based format
func labelArrayItems(toolName string, items []interface{}, itemsPath string) *PathLabelResponse {
	if len(items) == 0 {
		return nil
	}

	labels := make([]PathLabel, 0, len(items))

	for i, item := range items {
		path := fmt.Sprintf("%s/%d", itemsPath, i)
		itemLabels := labelSingleItem(toolName, item)
		labels = append(labels, PathLabel{
			Path:   path,
			Labels: itemLabels,
		})
	}

	return &PathLabelResponse{
		ItemsPath:    itemsPath,
		LabeledPaths: labels,
		DefaultLabels: &ResourceLabels{
			Description: "Unlabeled item",
			Secrecy:     []string{}, // empty = public
			Integrity:   []string{}, // empty = no endorsement
		},
	}
}

// labelSingleItem determines labels for a single item based on its content
func labelSingleItem(toolName string, item interface{}) ResourceLabels {
	itemMap, ok := item.(map[string]interface{})
	if !ok {
		return ResourceLabels{
			Description: "Unknown item",
			Secrecy:     []string{}, // empty = public
			Integrity:   []string{}, // empty = no endorsement
		}
	}

	// Extract repo info for scoped tags
	repoID := ""
	if repo, ok := itemMap["repository"].(map[string]interface{}); ok {
		if fullName, ok := repo["full_name"].(string); ok {
			repoID = fullName
		}
	} else if fullName, ok := itemMap["full_name"].(string); ok {
		repoID = fullName
	}

	// Check for repository visibility
	// Items with private repos get repo-scoped private tag
	if repo, ok := itemMap["repository"].(map[string]interface{}); ok {
		if private, ok := repo["private"].(bool); ok && private {
			secrecy := []string{}
			if repoID != "" {
				secrecy = []string{"private:" + repoID}
			}
			return ResourceLabels{
				Description: describeItem(toolName, itemMap),
				Secrecy:     secrecy,
				Integrity:   []string{}, // empty = no endorsement
			}
		}
	}

	// Check for direct "private" field (e.g., in repo objects)
	if private, ok := itemMap["private"].(bool); ok && private {
		secrecy := []string{}
		if repoID != "" {
			secrecy = []string{"private:" + repoID}
		}
		return ResourceLabels{
			Description: describeItem(toolName, itemMap),
			Secrecy:     secrecy,
			Integrity:   []string{}, // empty = no endorsement
		}
	}

	// Default: public repository (empty secrecy and integrity)
	return ResourceLabels{
		Description: describeItem(toolName, itemMap),
		Secrecy:     []string{},
		Integrity:   []string{},
	}
}

// describeItem generates a human-readable description for an item
func describeItem(toolName string, item map[string]interface{}) string {
	// Try common identifier fields
	if number, ok := item["number"].(float64); ok {
		if title, ok := item["title"].(string); ok {
			return fmt.Sprintf("Issue/PR #%d: %s", int(number), truncateString(title, 50))
		}
		return fmt.Sprintf("Issue/PR #%d", int(number))
	}

	if fullName, ok := item["full_name"].(string); ok {
		return fmt.Sprintf("Repository: %s", fullName)
	}

	if login, ok := item["login"].(string); ok {
		return fmt.Sprintf("User: %s", login)
	}

	return fmt.Sprintf("Item from %s", toolName)
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// Helper functions

// labelByRepoVisibility checks repository visibility and updates secrecy labels
func labelByRepoVisibility(output *LabelResourceOutput, toolArgs map[string]interface{}) {
	owner, _ := toolArgs["owner"].(string)
	repo, _ := toolArgs["repo"].(string)
	if owner == "" || repo == "" {
		return
	}
	repoID := owner + "/" + repo

	// Call the backend via host function to check visibility
	repoInfo, err := callBackendHelper("search_repositories", map[string]interface{}{
		"query": fmt.Sprintf("repo:%s", repoID),
	})

	if err == nil {
		// Check if repository is private
		if repoData, ok := repoInfo.(map[string]interface{}); ok {
			if items, ok := repoData["items"].([]interface{}); ok && len(items) > 0 {
				if firstItem, ok := items[0].(map[string]interface{}); ok {
					if private, ok := firstItem["private"].(bool); ok && private {
						output.Resource.Secrecy = []string{"private:" + repoID}
					}
				}
			}
		}
	}
}

func readBytes(ptr, length uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
}

func writeBytes(ptr uint32, data []byte) {
	dest := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(data))
	copy(dest, data)
}

// callBackendHelper wraps the call_backend host function with a nicer interface
func callBackendHelper(toolName string, args interface{}) (interface{}, error) {
	// Marshal args to JSON
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal args: %w", err)
	}

	// Allocate buffers
	toolNameBytes := []byte(toolName)
	resultBuf := make([]byte, 1024*1024) // 1MB result buffer

	// Get pointers
	var toolNamePtr, argsJSONPtr *byte
	if len(toolNameBytes) > 0 {
		toolNamePtr = &toolNameBytes[0]
	}
	if len(argsJSON) > 0 {
		argsJSONPtr = &argsJSON[0]
	}

	// Call the host function
	resultLen := callBackend(
		uint32(uintptr(unsafe.Pointer(toolNamePtr))),
		uint32(len(toolNameBytes)),
		uint32(uintptr(unsafe.Pointer(argsJSONPtr))),
		uint32(len(argsJSON)),
		uint32(uintptr(unsafe.Pointer(&resultBuf[0]))),
		uint32(len(resultBuf)),
	)

	if resultLen < 0 {
		return nil, fmt.Errorf("backend call failed with error code: %d", resultLen)
	}

	// Parse result
	var result interface{}
	if err := json.Unmarshal(resultBuf[:resultLen], &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal backend result: %w", err)
	}

	return result, nil
}

func main() {
	// Required for WASM compilation, but not called when used as a library
}
