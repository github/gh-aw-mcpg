package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/githubnext/gh-aw-mcpg/internal/difc"
	"github.com/githubnext/gh-aw-mcpg/internal/logger"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

var logWasm = logger.New("guard:wasm")

// WasmGuard implements Guard interface by executing a WASM module
// The WASM module is sandboxed and cannot make direct network calls
// It receives a BackendCaller interface to make controlled backend requests
type WasmGuard struct {
	name    string
	runtime wazero.Runtime
	module  api.Module
	malloc  api.Function
	free    api.Function

	// Backend caller for metadata requests
	backend BackendCaller
	ctx     context.Context
}

// NewWasmGuard creates a new WASM guard from a WASM binary file
func NewWasmGuard(ctx context.Context, name string, wasmPath string, backend BackendCaller) (*WasmGuard, error) {
	logWasm.Printf("Creating WASM guard: name=%s, path=%s", name, wasmPath)

	// Read WASM binary
	wasmBytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read WASM file: %w", err)
	}

	// Create WASM runtime
	runtime := wazero.NewRuntime(ctx)

	// Instantiate WASI
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate WASI: %w", err)
	}

	guard := &WasmGuard{
		name:    name,
		runtime: runtime,
		backend: backend,
		ctx:     ctx,
	}

	// Create host functions for the guard to call
	if err := guard.instantiateHostFunctions(ctx); err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate host functions: %w", err)
	}

	// Compile and instantiate the WASM module
	module, err := runtime.InstantiateWithConfig(ctx, wasmBytes,
		wazero.NewModuleConfig().WithName("guard"))
	if err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate WASM module: %w", err)
	}

	guard.module = module

	// Get malloc and free functions for memory management
	guard.malloc = module.ExportedFunction("malloc")
	guard.free = module.ExportedFunction("free")

	if guard.malloc == nil || guard.free == nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("WASM module must export malloc and free functions")
	}

	logWasm.Printf("WASM guard created successfully: name=%s", name)
	return guard, nil
}

// instantiateHostFunctions creates the host functions that the WASM module can call
func (g *WasmGuard) instantiateHostFunctions(ctx context.Context) error {
	// Create a host module with functions the guard can call
	_, err := g.runtime.NewHostModuleBuilder("env").
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(g.hostCallBackend), []api.ValueType{
			api.ValueTypeI32, // ptr to tool name
			api.ValueTypeI32, // tool name length
			api.ValueTypeI32, // ptr to args JSON
			api.ValueTypeI32, // args length
			api.ValueTypeI32, // ptr to result buffer
			api.ValueTypeI32, // result buffer size
		}, []api.ValueType{api.ValueTypeI32}). // returns result length or negative error
		Export("call_backend").
		Instantiate(ctx)

	return err
}

// hostCallBackend is called by the WASM module to make backend MCP calls
func (g *WasmGuard) hostCallBackend(ctx context.Context, m api.Module, stack []uint64) {
	toolNamePtr := uint32(stack[0])
	toolNameLen := uint32(stack[1])
	argsPtr := uint32(stack[2])
	argsLen := uint32(stack[3])
	resultPtr := uint32(stack[4])
	resultSize := uint32(stack[5])

	// Read tool name from WASM memory
	toolNameBytes, ok := m.Memory().Read(toolNamePtr, toolNameLen)
	if !ok {
		stack[0] = uint64(^uint32(0)) // error - max uint32 value
		return
	}
	toolName := string(toolNameBytes)

	// Read args JSON from WASM memory
	argsBytes, ok := m.Memory().Read(argsPtr, argsLen)
	if !ok {
		stack[0] = uint64(^uint32(0)) // error
		return
	}

	// Parse args
	var args interface{}
	if len(argsBytes) > 0 {
		if err := json.Unmarshal(argsBytes, &args); err != nil {
			logWasm.Printf("Failed to unmarshal backend call args: %v", err)
			stack[0] = uint64(^uint32(0)) // error
			return
		}
	}

	logWasm.Printf("WASM guard calling backend: tool=%s", toolName)

	// Call backend
	result, err := g.backend.CallTool(ctx, toolName, args)
	if err != nil {
		logWasm.Printf("Backend call failed: %v", err)
		stack[0] = uint64(^uint32(0)) // error
		return
	}

	// Marshal result to JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		logWasm.Printf("Failed to marshal backend result: %v", err)
		stack[0] = uint64(^uint32(0)) // error
		return
	}

	// Write result to WASM memory
	if uint32(len(resultJSON)) > resultSize {
		logWasm.Printf("Result too large: %d > %d", len(resultJSON), resultSize)
		stack[0] = uint64(^uint32(0)) // error
		return
	}

	if !m.Memory().Write(resultPtr, resultJSON) {
		stack[0] = uint64(^uint32(0)) // error
		return
	}

	// Return result length
	stack[0] = uint64(uint32(len(resultJSON)))
}

// Name returns the identifier for this guard
func (g *WasmGuard) Name() string {
	return g.name
}

// LabelResource calls the WASM module's label_resource function
func (g *WasmGuard) LabelResource(ctx context.Context, toolName string, args interface{}, backend BackendCaller, caps *difc.Capabilities) (*difc.LabeledResource, difc.OperationType, error) {
	logWasm.Printf("LabelResource called: toolName=%s", toolName)

	// Update backend caller for this request
	g.backend = backend

	// Prepare input
	input := map[string]interface{}{
		"tool_name": toolName,
		"tool_args": args,
	}
	if caps != nil {
		input["capabilities"] = caps
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, difc.OperationWrite, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Call WASM function
	resultJSON, err := g.callWasmFunction("label_resource", inputJSON)
	if err != nil {
		return nil, difc.OperationWrite, err
	}

	// Parse result
	var response struct {
		Resource struct {
			Description string   `json:"description"`
			Secrecy     []string `json:"secrecy"`
			Integrity   []string `json:"integrity"`
		} `json:"resource"`
		Operation string `json:"operation"`
	}

	if err := json.Unmarshal(resultJSON, &response); err != nil {
		return nil, difc.OperationWrite, fmt.Errorf("failed to unmarshal WASM response: %w", err)
	}

	// Convert to LabeledResource
	resource := &difc.LabeledResource{
		Description: response.Resource.Description,
	}

	// Convert secrecy tags
	secrecyTags := make([]difc.Tag, len(response.Resource.Secrecy))
	for i, tag := range response.Resource.Secrecy {
		secrecyTags[i] = difc.Tag(tag)
	}
	resource.Secrecy = *difc.NewSecrecyLabelWithTags(secrecyTags)

	// Convert integrity tags
	integrityTags := make([]difc.Tag, len(response.Resource.Integrity))
	for i, tag := range response.Resource.Integrity {
		integrityTags[i] = difc.Tag(tag)
	}
	resource.Integrity = *difc.NewIntegrityLabelWithTags(integrityTags)

	// Parse operation type
	operation := difc.OperationWrite // default to most restrictive
	switch response.Operation {
	case "read":
		operation = difc.OperationRead
	case "write":
		operation = difc.OperationWrite
	case "read-write":
		operation = difc.OperationReadWrite
	}

	logWasm.Printf("LabelResource complete: operation=%s, description=%s", operation, resource.Description)
	return resource, operation, nil
}

// LabelResponse calls the WASM module's label_response function
func (g *WasmGuard) LabelResponse(ctx context.Context, toolName string, result interface{}, backend BackendCaller, caps *difc.Capabilities) (difc.LabeledData, error) {
	logWasm.Printf("LabelResponse called: toolName=%s", toolName)

	// Update backend caller for this request
	g.backend = backend

	// Prepare input
	input := map[string]interface{}{
		"tool_name":   toolName,
		"tool_result": result,
	}
	if caps != nil {
		input["capabilities"] = caps
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal input: %w", err)
	}

	// Call WASM function
	resultJSON, err := g.callWasmFunction("label_response", inputJSON)
	if err != nil {
		return nil, err
	}

	// If empty result, return nil (no fine-grained labeling)
	if len(resultJSON) == 0 {
		return nil, nil
	}

	// Parse result to see if it's a collection
	var responseMap map[string]interface{}
	if err := json.Unmarshal(resultJSON, &responseMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal WASM response: %w", err)
	}

	// Check if it's a collection
	if items, ok := responseMap["items"].([]interface{}); ok {
		return parseCollectionLabeledData(items)
	}

	// No fine-grained labeling
	return nil, nil
}

// callWasmFunction calls a function in the WASM module with JSON input/output
func (g *WasmGuard) callWasmFunction(funcName string, inputJSON []byte) ([]byte, error) {
	// Get the exported function
	fn := g.module.ExportedFunction(funcName)
	if fn == nil {
		return nil, fmt.Errorf("function %s not exported from WASM module", funcName)
	}

	// Allocate memory for input
	inputSize := uint32(len(inputJSON))
	results, err := g.malloc.Call(g.ctx, uint64(inputSize))
	if err != nil {
		return nil, fmt.Errorf("failed to allocate input memory: %w", err)
	}
	inputPtr := uint32(results[0])
	defer g.free.Call(g.ctx, uint64(inputPtr))

	// Write input to WASM memory
	if !g.module.Memory().Write(inputPtr, inputJSON) {
		return nil, fmt.Errorf("failed to write input to WASM memory")
	}

	// Allocate memory for output (max 1MB)
	outputSize := uint32(1024 * 1024)
	results, err = g.malloc.Call(g.ctx, uint64(outputSize))
	if err != nil {
		return nil, fmt.Errorf("failed to allocate output memory: %w", err)
	}
	outputPtr := uint32(results[0])
	defer g.free.Call(g.ctx, uint64(outputPtr))

	// Call the WASM function
	results, err = fn.Call(g.ctx, uint64(inputPtr), uint64(inputSize), uint64(outputPtr), uint64(outputSize))
	if err != nil {
		return nil, fmt.Errorf("WASM function call failed: %w", err)
	}

	// Check result (negative = error)
	resultLen := int32(results[0])
	if resultLen < 0 {
		return nil, fmt.Errorf("WASM function returned error: %d", resultLen)
	}

	// Read output from WASM memory
	outputJSON, ok := g.module.Memory().Read(outputPtr, uint32(resultLen))
	if !ok {
		return nil, fmt.Errorf("failed to read output from WASM memory")
	}

	return outputJSON, nil
}

// Close releases WASM runtime resources
func (g *WasmGuard) Close(ctx context.Context) error {
	if g.runtime != nil {
		return g.runtime.Close(ctx)
	}
	return nil
}

// parseCollectionLabeledData converts an array of items to CollectionLabeledData
func parseCollectionLabeledData(items []interface{}) (*difc.CollectionLabeledData, error) {
	collection := &difc.CollectionLabeledData{
		Items: make([]difc.LabeledItem, 0, len(items)),
	}

	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		labeledItem := difc.LabeledItem{
			Data: itemMap["data"],
		}

		// Parse labels
		if labelsData, ok := itemMap["labels"].(map[string]interface{}); ok {
			labels := &difc.LabeledResource{}

			if desc, ok := labelsData["description"].(string); ok {
				labels.Description = desc
			}

			// Parse secrecy tags
			if secrecy, ok := labelsData["secrecy"].([]interface{}); ok {
				tags := make([]difc.Tag, 0, len(secrecy))
				for _, t := range secrecy {
					if tagStr, ok := t.(string); ok {
						tags = append(tags, difc.Tag(tagStr))
					}
				}
				labels.Secrecy = *difc.NewSecrecyLabelWithTags(tags)
			} else {
				labels.Secrecy = *difc.NewSecrecyLabel()
			}

			// Parse integrity tags
			if integrity, ok := labelsData["integrity"].([]interface{}); ok {
				tags := make([]difc.Tag, 0, len(integrity))
				for _, t := range integrity {
					if tagStr, ok := t.(string); ok {
						tags = append(tags, difc.Tag(tagStr))
					}
				}
				labels.Integrity = *difc.NewIntegrityLabelWithTags(tags)
			} else {
				labels.Integrity = *difc.NewIntegrityLabel()
			}

			labeledItem.Labels = labels
		}

		collection.Items = append(collection.Items, labeledItem)
	}

	return collection, nil
}
