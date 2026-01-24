//go:build tinygo.wasm || wasm

// Package guardsdk provides utilities for building WASM guards for MCP Gateway.
//
// This SDK simplifies guard development by handling:
//   - Memory management for WASM host/guest communication
//   - JSON marshaling/unmarshaling
//   - Backend tool calls via host functions
//   - Standard request/response types
//
// Example usage:
//
//	package main
//
//	import "github.com/githubnext/gh-aw-mcpg/examples/guards/guardsdk"
//
//	func init() {
//	    guardsdk.RegisterLabelResource(myLabelResource)
//	    guardsdk.RegisterLabelResponse(myLabelResponse)
//	}
//
//	func myLabelResource(req *guardsdk.LabelResourceRequest) (*guardsdk.LabelResourceResponse, error) {
//	    // Your labeling logic here
//	    return &guardsdk.LabelResourceResponse{
//	        Resource:  guardsdk.NewPublicResource("my-resource"),
//	        Operation: guardsdk.OperationRead,
//	    }, nil
//	}
//
//	func myLabelResponse(req *guardsdk.LabelResponseRequest) (*guardsdk.LabelResponseResponse, error) {
//	    return nil, nil // No fine-grained labeling
//	}
//
//	func main() {}
package guardsdk

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// Operation types for resource access
type Operation string

const (
	OperationRead      Operation = "read"
	OperationWrite     Operation = "write"
	OperationReadWrite Operation = "read-write"
)

// LabelResourceRequest contains the input for labeling a resource
type LabelResourceRequest struct {
	ToolName     string                 `json:"tool_name"`
	ToolArgs     map[string]interface{} `json:"tool_args"`
	Capabilities interface{}            `json:"capabilities,omitempty"`
}

// LabelResourceResponse contains the output from labeling a resource
type LabelResourceResponse struct {
	Resource  ResourceLabels `json:"resource"`
	Operation Operation      `json:"operation"`
}

// LabelResponseRequest contains the input for labeling a response
type LabelResponseRequest struct {
	ToolName     string      `json:"tool_name"`
	ToolResult   interface{} `json:"tool_result"`
	Capabilities interface{} `json:"capabilities,omitempty"`
}

// LabelResponseResponse contains the output from labeling a response
type LabelResponseResponse struct {
	Items []LabeledItem `json:"items,omitempty"`
}

// ResourceLabels contains security labels for a resource
type ResourceLabels struct {
	Description string   `json:"description"`
	Secrecy     []string `json:"secrecy"`
	Integrity   []string `json:"integrity"`
}

// LabeledItem represents a single item with its labels
type LabeledItem struct {
	Data   interface{}    `json:"data"`
	Labels ResourceLabels `json:"labels"`
}

// --- Helper constructors for common label patterns ---

// NewPublicResource creates a ResourceLabels for a public, untrusted resource
func NewPublicResource(description string) ResourceLabels {
	return ResourceLabels{
		Description: description,
		Secrecy:     []string{"public"},
		Integrity:   []string{"untrusted"},
	}
}

// NewPrivateResource creates a ResourceLabels for a private resource
func NewPrivateResource(description string, integrityLevel string) ResourceLabels {
	return ResourceLabels{
		Description: description,
		Secrecy:     []string{"repo_private"},
		Integrity:   []string{integrityLevel},
	}
}

// NewResource creates a ResourceLabels with custom secrecy and integrity
func NewResource(description string, secrecy, integrity []string) ResourceLabels {
	return ResourceLabels{
		Description: description,
		Secrecy:     secrecy,
		Integrity:   integrity,
	}
}

// --- Tool argument helpers ---

// GetString extracts a string from tool arguments
func (r *LabelResourceRequest) GetString(key string) (string, bool) {
	val, ok := r.ToolArgs[key].(string)
	return val, ok
}

// GetInt extracts an integer from tool arguments (JSON numbers are float64)
func (r *LabelResourceRequest) GetInt(key string) (int, bool) {
	if val, ok := r.ToolArgs[key].(float64); ok {
		return int(val), true
	}
	return 0, false
}

// GetFloat extracts a float from tool arguments
func (r *LabelResourceRequest) GetFloat(key string) (float64, bool) {
	val, ok := r.ToolArgs[key].(float64)
	return val, ok
}

// GetBool extracts a boolean from tool arguments
func (r *LabelResourceRequest) GetBool(key string) (bool, bool) {
	val, ok := r.ToolArgs[key].(bool)
	return val, ok
}

// GetStringSlice extracts a string slice from tool arguments
func (r *LabelResourceRequest) GetStringSlice(key string) ([]string, bool) {
	arr, ok := r.ToolArgs[key].([]interface{})
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result, true
}

// GetOwnerRepo extracts owner and repo from tool arguments (common pattern)
func (r *LabelResourceRequest) GetOwnerRepo() (owner, repo string, ok bool) {
	owner, ownerOk := r.GetString("owner")
	repo, repoOk := r.GetString("repo")
	return owner, repo, ownerOk && repoOk
}

// --- Backend calling ---

// callBackend is imported from the host (gateway) environment
//
//go:wasmimport env call_backend
func callBackend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32

// CallBackend calls a tool on the backend MCP server
// This is a read-only call for gathering metadata to inform labeling decisions
func CallBackend(toolName string, args interface{}) (interface{}, error) {
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

// CallBackendTyped calls a backend tool and unmarshals the result into the provided type
func CallBackendTyped[T any](toolName string, args interface{}) (*T, error) {
	result, err := CallBackend(toolName, args)
	if err != nil {
		return nil, err
	}

	// Re-marshal and unmarshal to get proper typing
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to re-marshal result: %w", err)
	}

	var typed T
	if err := json.Unmarshal(data, &typed); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to type: %w", err)
	}

	return &typed, nil
}

// --- Handler registration ---

// LabelResourceFunc is the function signature for labeling resources
type LabelResourceFunc func(*LabelResourceRequest) (*LabelResourceResponse, error)

// LabelResponseFunc is the function signature for labeling responses
type LabelResponseFunc func(*LabelResponseRequest) (*LabelResponseResponse, error)

var (
	labelResourceHandler LabelResourceFunc
	labelResponseHandler LabelResponseFunc
)

// RegisterLabelResource registers the handler for label_resource calls
func RegisterLabelResource(handler LabelResourceFunc) {
	labelResourceHandler = handler
}

// RegisterLabelResponse registers the handler for label_response calls
func RegisterLabelResponse(handler LabelResponseFunc) {
	labelResponseHandler = handler
}

// --- WASM exports (called by the gateway) ---

// label_resource is the WASM export called by the gateway
//
//export label_resource
func labelResource(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	if labelResourceHandler == nil {
		return -1 // No handler registered
	}

	// Read input
	input := readBytes(inputPtr, inputLen)
	var req LabelResourceRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return -1
	}

	// Call handler
	resp, err := labelResourceHandler(&req)
	if err != nil {
		return -1
	}

	// Marshal output
	outputJSON, err := json.Marshal(resp)
	if err != nil {
		return -1
	}

	if uint32(len(outputJSON)) > outputSize {
		return -1
	}

	writeBytes(outputPtr, outputJSON)
	return int32(len(outputJSON))
}

// label_response is the WASM export called by the gateway
//
//export label_response
func labelResponse(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	if labelResponseHandler == nil {
		return 0 // No handler = no fine-grained labeling
	}

	// Read input
	input := readBytes(inputPtr, inputLen)
	var req LabelResponseRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return -1
	}

	// Call handler
	resp, err := labelResponseHandler(&req)
	if err != nil {
		return -1
	}

	// If nil response, no fine-grained labeling
	if resp == nil || len(resp.Items) == 0 {
		return 0
	}

	// Marshal output
	outputJSON, err := json.Marshal(resp)
	if err != nil {
		return -1
	}

	if uint32(len(outputJSON)) > outputSize {
		return -1
	}

	writeBytes(outputPtr, outputJSON)
	return int32(len(outputJSON))
}

// --- Memory helpers ---

func readBytes(ptr, length uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
}

func writeBytes(ptr uint32, data []byte) {
	dest := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len(data))
	copy(dest, data)
}
