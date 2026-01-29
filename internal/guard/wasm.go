package guard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/githubnext/gh-aw-mcpg/internal/difc"
	"github.com/githubnext/gh-aw-mcpg/internal/logger"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

var logWasm = logger.New("guard:wasm")

// WasmGuardOptions configures optional settings for WASM guard creation
type WasmGuardOptions struct {
	// Stdout is the writer for WASM stdout output. Defaults to os.Stdout if nil.
	Stdout io.Writer
	// Stderr is the writer for WASM stderr output. Defaults to os.Stderr if nil.
	Stderr io.Writer
}

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
	return NewWasmGuardWithOptions(ctx, name, wasmBytes, backend, nil)
}

// NewWasmGuardWithOptions creates a new WASM guard from WASM binary bytes with custom options
// Options can be nil to use defaults (stdout/stderr go to os.Stdout/os.Stderr)
func NewWasmGuardWithOptions(ctx context.Context, name string, wasmBytes []byte, backend BackendCaller, opts *WasmGuardOptions) (*WasmGuard, error) {
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

	// Configure module options with stdout/stderr
	moduleConfig := wazero.NewModuleConfig().WithName("guard").WithStartFunctions()
	if opts != nil {
		if opts.Stdout != nil {
			moduleConfig = moduleConfig.WithStdout(opts.Stdout)
		}
		if opts.Stderr != nil {
			moduleConfig = moduleConfig.WithStderr(opts.Stderr)
		}
	}

	// Compile and instantiate the WASM module
	module, err := runtime.InstantiateWithConfig(ctx, wasmBytes, moduleConfig)
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
		// call_backend: allows guards to call MCP tools on the backend
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
		// host_log: allows guards to send log messages to the gateway
		NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(g.hostLog), []api.ValueType{
			api.ValueTypeI32, // log level (0=debug, 1=info, 2=warn, 3=error)
			api.ValueTypeI32, // ptr to message
			api.ValueTypeI32, // message length
		}, []api.ValueType{}).
		Export("host_log").
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

// Log level constants for hostLog
const (
	logLevelDebug = 0
	logLevelInfo  = 1
	logLevelWarn  = 2
	logLevelError = 3
)

// hostLog is called by the WASM module to send log messages to the gateway
func (g *WasmGuard) hostLog(ctx context.Context, m api.Module, stack []uint64) {
	level := uint32(stack[0])
	msgPtr := uint32(stack[1])
	msgLen := uint32(stack[2])

	// Read message from WASM memory
	msgBytes, ok := m.Memory().Read(msgPtr, msgLen)
	if !ok {
		logWasm.Printf("hostLog: failed to read message from WASM memory")
		return
	}
	msg := string(msgBytes)

	// Log at the appropriate level
	prefix := fmt.Sprintf("[guard:%s] ", g.name)
	switch level {
	case logLevelDebug:
		logWasm.Printf("%sDEBUG: %s", prefix, msg)
	case logLevelInfo:
		logWasm.Printf("%sINFO: %s", prefix, msg)
	case logLevelWarn:
		logWasm.Printf("%sWARN: %s", prefix, msg)
	case logLevelError:
		logWasm.Printf("%sERROR: %s", prefix, msg)
	default:
		logWasm.Printf("%s%s", prefix, msg)
	}
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

	// Parse result - check for new path-based format first
	var responseMap map[string]interface{}
	if err := json.Unmarshal(resultJSON, &responseMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal WASM response: %w", err)
	}

	// Check for path-based labeling format (preferred, more efficient)
	if _, hasLabeledPaths := responseMap["labeled_paths"]; hasLabeledPaths {
		return parsePathLabeledResponse(resultJSON, result)
	}

	// Legacy format: check if it's a collection with "items"
	if items, ok := responseMap["items"].([]interface{}); ok && len(items) > 0 {
		return parseCollectionLabeledData(items)
	}

	// No fine-grained labeling
	return nil, nil
}

// parsePathLabeledResponse parses the new path-based labeling format
// This is more efficient as guards don't need to copy data, just return paths and labels
func parsePathLabeledResponse(responseJSON []byte, originalData interface{}) (difc.LabeledData, error) {
	pathLabels, err := difc.ParsePathLabels(responseJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to parse path labels: %w", err)
	}

	pld, err := difc.NewPathLabeledData(originalData, pathLabels)
	if err != nil {
		return nil, fmt.Errorf("failed to apply path labels: %w", err)
	}

	// Convert to CollectionLabeledData for compatibility with existing filtering
	return pld.ToCollectionLabeledData(), nil
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

	// Start with 4MB output buffer, can grow up to 16MB if needed
	initialOutputSize := uint32(4 * 1024 * 1024) // 4MB initial
	maxOutputSize := uint32(16 * 1024 * 1024)    // 16MB maximum
	maxInputSize := uint32(8 * 1024 * 1024)      // 8MB max input

	if uint32(len(inputJSON)) > maxInputSize {
		return nil, fmt.Errorf("input too large: %d bytes (max %d)", len(inputJSON), maxInputSize)
	}

	// Try with initial buffer size, retry with larger buffer if needed
	outputSize := initialOutputSize
	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		result, requiredSize, err := g.tryCallWasmFunction(fn, mem, inputJSON, outputSize)
		if err != nil {
			return nil, err
		}

		// If we got a result, return it
		if result != nil {
			return result, nil
		}

		// Buffer was too small, check if we can grow
		if requiredSize == 0 {
			// Guard didn't tell us the required size, double the buffer
			requiredSize = outputSize * 2
		}

		if requiredSize > maxOutputSize {
			return nil, fmt.Errorf("guard requires buffer of %d bytes which exceeds maximum of %d bytes", requiredSize, maxOutputSize)
		}

		logWasm.Printf("Buffer too small (%d bytes), retrying with %d bytes", outputSize, requiredSize)
		outputSize = requiredSize
	}

	return nil, fmt.Errorf("failed after %d attempts, buffer size %d still insufficient", maxRetries, outputSize)
}

// tryCallWasmFunction attempts to call the WASM function with the given buffer size
// Returns (result, 0, nil) on success
// Returns (nil, requiredSize, nil) if buffer was too small
// Returns (nil, 0, error) on actual error
func (g *WasmGuard) tryCallWasmFunction(fn api.Function, mem api.Memory, inputJSON []byte, outputSize uint32) ([]byte, uint32, error) {
	// Ensure memory is large enough for our buffers
	// Layout: [...guard memory...][input buffer][output buffer]
	inputSize := uint32(len(inputJSON))
	requiredMemory := inputSize + outputSize + uint32(64*1024) // Extra 64KB for safety margin

	memSize := mem.Size()
	if memSize < requiredMemory {
		pages := (requiredMemory - memSize + 65535) / 65536 // Round up to pages
		_, success := mem.Grow(pages)
		if !success {
			return nil, 0, fmt.Errorf("failed to grow WASM memory from %d to %d bytes", memSize, requiredMemory)
		}
		memSize = mem.Size()
	}

	// Place buffers at end of memory
	outputPtr := memSize - outputSize
	inputPtr := outputPtr - inputSize

	// Write input to WASM memory
	if !mem.Write(inputPtr, inputJSON) {
		return nil, 0, fmt.Errorf("failed to write input to WASM memory")
	}

	// Call the WASM function
	results, err := fn.Call(g.ctx,
		uint64(inputPtr),
		uint64(inputSize),
		uint64(outputPtr),
		uint64(outputSize))
	if err != nil {
		return nil, 0, fmt.Errorf("WASM function call failed: %w", err)
	}

	// Check result
	resultLen := int32(results[0])

	// Error code -2 means "buffer too small"
	// The guard can optionally return the required size in the output buffer as a uint32
	if resultLen == -2 {
		// Try to read the required size from the output buffer (first 4 bytes as uint32)
		if sizeBytes, ok := mem.Read(outputPtr, 4); ok && len(sizeBytes) == 4 {
			requiredSize := uint32(sizeBytes[0]) | uint32(sizeBytes[1])<<8 | uint32(sizeBytes[2])<<16 | uint32(sizeBytes[3])<<24
			if requiredSize > 0 {
				return nil, requiredSize, nil
			}
		}
		// Guard didn't specify size, return 0 to trigger doubling
		return nil, 0, nil
	}

	// Other negative values are errors
	if resultLen < 0 {
		return nil, 0, fmt.Errorf("WASM function returned error code: %d", resultLen)
	}

	if resultLen == 0 {
		return []byte{}, 0, nil
	}

	// Read output from WASM memory
	outputJSON, ok := mem.Read(outputPtr, uint32(resultLen))
	if !ok {
		return nil, 0, fmt.Errorf("failed to read output from WASM memory (len=%d)", resultLen)
	}

	return outputJSON, 0, nil
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
