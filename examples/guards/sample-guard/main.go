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
		return -1
	}

	// Default labels
	output := LabelResourceOutput{
		Resource: ResourceLabels{
			Description: fmt.Sprintf("resource:%s", req.ToolName),
			Secrecy:     []string{"public"},
			Integrity:   []string{"untrusted"},
		},
		Operation: "read",
	}

	// Determine labels based on tool name
	switch req.ToolName {
	case "create_issue", "update_issue", "create_pull_request":
		output.Operation = "write"
		output.Resource.Integrity = []string{"contributor"}

	case "merge_pull_request":
		output.Operation = "read-write"
		output.Resource.Integrity = []string{"maintainer"}

	case "list_issues", "get_issue", "list_pull_requests":
		output.Operation = "read"

		// Call backend to check repository visibility
		// This demonstrates calling the backend from within the WASM guard
		if owner, ok := req.ToolArgs["owner"].(string); ok {
			if repo, ok := req.ToolArgs["repo"].(string); ok {
				// Call the backend via host function
				repoInfo, err := callBackendHelper("search_repositories", map[string]interface{}{
					"query": fmt.Sprintf("repo:%s/%s", owner, repo),
				})

				if err == nil {
					// Check if repository is private
					if repoData, ok := repoInfo.(map[string]interface{}); ok {
						if items, ok := repoData["items"].([]interface{}); ok && len(items) > 0 {
							if firstItem, ok := items[0].(map[string]interface{}); ok {
								if private, ok := firstItem["private"].(bool); ok && private {
									// Repository is private
									output.Resource.Secrecy = []string{"repo_private"}
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

// label_response is called by the gateway to label response data
//
//export label_response
func labelResponse(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	// Read input JSON from WASM memory
	input := readBytes(inputPtr, inputLen)
	var req LabelResponseInput
	if err := json.Unmarshal(input, &req); err != nil {
		return -1
	}

	// For this sample, we don't do fine-grained labeling
	// Return 0 to indicate no fine-grained labeling
	return 0
}

// Helper functions

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
