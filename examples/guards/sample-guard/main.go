package main

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// This is a sample DIFC guard that can be compiled to WASM
// It demonstrates the guard interface and how to interact with the backend

//go:wasmimport env call_backend
func callBackend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32

// Input structures
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

// Output structures
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

// Memory allocation functions (required)
//
//export malloc
func malloc(size uint32) uint32 {
	buf := make([]byte, size)
	ptr := &buf[0]
	return uint32(uintptr(unsafe.Pointer(ptr)))
}

//export free
func free(ptr uint32) {
	// Go's GC will handle this
}

// Guard functions

//export label_resource
func labelResource(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	// Read input JSON
	input := readBytes(inputPtr, inputLen)
	var req LabelResourceInput
	if err := json.Unmarshal(input, &req); err != nil {
		return -1
	}

	// Determine labels based on tool name
	output := LabelResourceOutput{
		Resource: ResourceLabels{
			Description: fmt.Sprintf("resource:%s", req.ToolName),
			Secrecy:     []string{"public"},    // Default to public
			Integrity:   []string{"untrusted"}, // Default to untrusted
		},
		Operation: "read", // Default to read
	}

	// Example: Label different operations differently
	switch req.ToolName {
	case "create_issue", "update_issue", "create_pull_request":
		output.Operation = "write"
		output.Resource.Integrity = []string{"contributor"}

	case "merge_pull_request":
		output.Operation = "read-write"
		output.Resource.Integrity = []string{"maintainer"}

	case "list_issues", "get_issue", "list_pull_requests":
		output.Operation = "read"
		// Could call backend here to check repository visibility
		// For demo, just use public
		output.Resource.Secrecy = []string{"public"}
	}

	// Marshal output
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return -1
	}

	// Write output
	if uint32(len(outputJSON)) > outputSize {
		return -1 // Output too large
	}

	writeBytes(outputPtr, outputJSON)
	return int32(len(outputJSON))
}

//export label_response
func labelResponse(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	// Read input JSON
	input := readBytes(inputPtr, inputLen)
	var req LabelResponseInput
	if err := json.Unmarshal(input, &req); err != nil {
		return -1
	}

	// For this sample, we don't do fine-grained labeling
	// Return empty result to indicate no fine-grained labeling
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

// CallBackend is a helper to call the backend from within the guard
func CallBackend(toolName string, args interface{}) (interface{}, error) {
	// Marshal args
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}

	// Allocate result buffer (1MB)
	resultBuf := make([]byte, 1024*1024)

	// Call backend
	toolNameBytes := []byte(toolName)
	
	toolNamePtr := (*byte)(nil)
	if len(toolNameBytes) > 0 {
		toolNamePtr = &toolNameBytes[0]
	}
	
	argsPtr := (*byte)(nil)
	if len(argsJSON) > 0 {
		argsPtr = &argsJSON[0]
	}
	
	resultLen := callBackend(
		uint32(uintptr(unsafe.Pointer(toolNamePtr))),
		uint32(len(toolNameBytes)),
		uint32(uintptr(unsafe.Pointer(argsPtr))),
		uint32(len(argsJSON)),
		uint32(uintptr(unsafe.Pointer(&resultBuf[0]))),
		uint32(len(resultBuf)),
	)

	if resultLen < 0 {
		return nil, fmt.Errorf("backend call failed")
	}

	// Parse result
	var result interface{}
	if err := json.Unmarshal(resultBuf[:resultLen], &result); err != nil {
		return nil, err
	}

	return result, nil
}

func main() {
	// Required for WASM, but not called
}
