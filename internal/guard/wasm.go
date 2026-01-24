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

// WasmGuard implements Guard interface by executing a WASM module in-process
// The WASM module runs sandboxed within the gateway using wazero runtime
// Guards cannot make direct network calls - they receive a BackendCaller interface via host functions
type WasmGuard struct {
	name    string
	runtime wazero.Runtime
	module  api.Module

	// Backend caller provided to the guard via host functions
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

	return NewWasmGuardFromBytes(ctx, name, wasmBytes, backend)
}

// NewWasmGuardFromBytes creates a new WASM guard from WASM binary bytes
// This is useful when loading guards from URLs or other sources
func NewWasmGuardFromBytes(ctx context.Context, name string, wasmBytes []byte, backend BackendCaller) (*WasmGuard, error) {
	logWasm.Printf("Creating WASM guard from bytes: name=%s, size=%d", name, len(wasmBytes))

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
		wazero.NewModuleConfig().WithName("guard").WithStartFunctions())
	if err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate WASM module: %w", err)
	}

	guard.module = module

	// Verify required functions are exported
	labelResourceFn := module.ExportedFunction("label_resource")
	labelResponseFn := module.ExportedFunction("label_response")

	if labelResourceFn == nil || labelResponseFn == nil {
		runtime.Close(ctx)

		// Check if this was compiled with standard Go (only _start is exported)
		if module.ExportedFunction("_start") != nil && labelResourceFn == nil {
			return nil, fmt.Errorf("WASM module does not export guard functions. " +
				"This usually means the guard was compiled with standard Go instead of TinyGo. " +
				"TinyGo is required for proper function exports. " +
				"Note: TinyGo 0.34 supports Go 1.19-1.23 (not yet compatible with Go 1.25). " +
				"See examples/guards/sample-guard/README.md for details")
		}

		return nil, fmt.Errorf("WASM module must export label_resource and label_response functions")
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

	// Helper to set error return value
	setError := func() {
		stack[0] = uint64(^uint32(0)) // Max uint32 represents error
	}

	// Read tool name from WASM memory
	toolNameBytes, ok := m.Memory().Read(toolNamePtr, toolNameLen)
	if !ok {
		setError()
		return
	}
	toolName := string(toolNameBytes)

	// Read args JSON from WASM memory
	argsBytes, ok := m.Memory().Read(argsPtr, argsLen)
	if !ok {
		setError()
		return
	}

	// Parse args
	var args interface{}
	if len(argsBytes) > 0 {
		if err := json.Unmarshal(argsBytes, &args); err != nil {
			logWasm.Printf("Failed to unmarshal backend call args: %v", err)
			setError()
			return
		}
	}

	logWasm.Printf("WASM guard calling backend: tool=%s", toolName)

	// Call backend
	result, err := g.backend.CallTool(ctx, toolName, args)
	if err != nil {
		logWasm.Printf("Backend call failed: %v", err)
		setError()
		return
	}

	// Marshal result to JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		logWasm.Printf("Failed to marshal backend result: %v", err)
		setError()
		return
	}

	// Check if result fits in buffer
	if uint32(len(resultJSON)) > resultSize {
		logWasm.Printf("Result too large: %d > %d", len(resultJSON), resultSize)
		setError()
		return
	}

	// Write result to WASM memory
	if !m.Memory().Write(resultPtr, resultJSON) {
		logWasm.Printf("Failed to write result to WASM memory")
		setError()
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
	var response map[string]interface{}
	if err := json.Unmarshal(resultJSON, &response); err != nil {
		return nil, difc.OperationWrite, fmt.Errorf("failed to unmarshal WASM response: %w", err)
	}

	return parseResourceResponse(response)
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

	// Parse result
	var responseMap map[string]interface{}
	if err := json.Unmarshal(resultJSON, &responseMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal WASM response: %w", err)
	}

	// Check if it's a collection
	if items, ok := responseMap["items"].([]interface{}); ok && len(items) > 0 {
		return parseCollectionLabeledData(items)
	}

	// No fine-grained labeling
	return nil, nil
}

// callWasmFunction calls an exported function in the WASM module
func (g *WasmGuard) callWasmFunction(funcName string, inputJSON []byte) ([]byte, error) {
	fn := g.module.ExportedFunction(funcName)
	if fn == nil {
		return nil, fmt.Errorf("function %s not exported from WASM module", funcName)
	}

	mem := g.module.Memory()
	if mem == nil {
		return nil, fmt.Errorf("WASM module has no memory")
	}

	// Allocate memory regions
	// We use the end of memory for our buffers to avoid conflicts
	memSize := mem.Size()
	minSize := uint32(4 * 1024 * 1024) // 4MB minimum

	if memSize < minSize {
		// Try to grow memory
		pages := (minSize - memSize + 65535) / 65536 // Round up to pages
		_, success := mem.Grow(pages)
		if !success {
			return nil, fmt.Errorf("failed to grow WASM memory from %d to %d bytes", memSize, minSize)
		}
		memSize = mem.Size()
	}

	// Use last 2MB for buffers
	outputPtr := memSize - 2*1024*1024
	outputSize := uint32(1024 * 1024)
	inputPtr := memSize - 1*1024*1024

	if uint32(len(inputJSON)) > 1024*1024 {
		return nil, fmt.Errorf("input too large: %d bytes", len(inputJSON))
	}

	// Write input to WASM memory
	if !mem.Write(inputPtr, inputJSON) {
		return nil, fmt.Errorf("failed to write input to WASM memory")
	}

	// Call the WASM function
	results, err := fn.Call(g.ctx,
		uint64(inputPtr),
		uint64(len(inputJSON)),
		uint64(outputPtr),
		uint64(outputSize))
	if err != nil {
		return nil, fmt.Errorf("WASM function call failed: %w", err)
	}

	// Check result (negative = error)
	resultLen := int32(results[0])
	if resultLen < 0 {
		return nil, fmt.Errorf("WASM function returned error code: %d", resultLen)
	}

	if resultLen == 0 {
		// Empty result
		return []byte{}, nil
	}

	// Read output from WASM memory
	outputJSON, ok := mem.Read(outputPtr, uint32(resultLen))
	if !ok {
		return nil, fmt.Errorf("failed to read output from WASM memory (len=%d)", resultLen)
	}

	return outputJSON, nil
}

// parseResourceResponse converts guard response to LabeledResource
func parseResourceResponse(response map[string]interface{}) (*difc.LabeledResource, difc.OperationType, error) {
	resourceData, ok := response["resource"].(map[string]interface{})
	if !ok {
		return nil, difc.OperationWrite, fmt.Errorf("invalid resource format in guard response")
	}

	resource := &difc.LabeledResource{}

	if desc, ok := resourceData["description"].(string); ok {
		resource.Description = desc
	}

	// Parse secrecy tags
	if secrecy, ok := resourceData["secrecy"].([]interface{}); ok {
		tags := make([]difc.Tag, 0, len(secrecy))
		for _, t := range secrecy {
			if tagStr, ok := t.(string); ok {
				tags = append(tags, difc.Tag(tagStr))
			}
		}
		resource.Secrecy = *difc.NewSecrecyLabelWithTags(tags)
	} else {
		resource.Secrecy = *difc.NewSecrecyLabel()
	}

	// Parse integrity tags
	if integrity, ok := resourceData["integrity"].([]interface{}); ok {
		tags := make([]difc.Tag, 0, len(integrity))
		for _, t := range integrity {
			if tagStr, ok := t.(string); ok {
				tags = append(tags, difc.Tag(tagStr))
			}
		}
		resource.Integrity = *difc.NewIntegrityLabelWithTags(tags)
	} else {
		resource.Integrity = *difc.NewIntegrityLabel()
	}

	// Parse operation type
	operation := difc.OperationWrite // default to most restrictive
	if opStr, ok := response["operation"].(string); ok {
		switch opStr {
		case "read":
			operation = difc.OperationRead
		case "write":
			operation = difc.OperationWrite
		case "read-write":
			operation = difc.OperationReadWrite
		}
	}

	return resource, operation, nil
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

// Close releases WASM runtime resources
func (g *WasmGuard) Close(ctx context.Context) error {
	if g.module != nil {
		if err := g.module.Close(ctx); err != nil {
			logWasm.Printf("Error closing module: %v", err)
		}
	}
	if g.runtime != nil {
		return g.runtime.Close(ctx)
	}
	return nil
}
