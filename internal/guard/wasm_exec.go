package guard

import (
	"context"
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/tetratelabs/wazero/api"
)

const (
	wasmInitialOutputSize = 4 * 1024 * 1024
	wasmMaxOutputSize     = 16 * 1024 * 1024
	wasmMaxInputSize      = 8 * 1024 * 1024
)

// callWasmFunction calls an exported function in the WASM module.
// Precondition: g.mu must be held by the caller. All public methods
// (LabelAgent, LabelResource, LabelResponse) hold g.mu for their entire
// duration, satisfying this requirement.
func (g *WasmGuard) callWasmFunction(ctx context.Context, funcName string, inputJSON []byte) ([]byte, error) {
	logWasm.Printf("callWasmFunction: guard=%s, func=%s, inputSize=%d", g.name, funcName, len(inputJSON))

	// If the module has already trapped, refuse further calls immediately.
	// A WASM trap may corrupt the module's internal state (e.g. the global
	// policy context stored by label_agent), so all subsequent calls are
	// unsafe until the guard is reloaded.
	if g.failed {
		logWasm.Printf("callWasmFunction: guard=%s is unavailable after a previous trap", g.name)
		return nil, fmt.Errorf("WASM guard '%s' is unavailable after a previous trap: %w", g.name, g.failedErr)
	}

	fn := g.module.ExportedFunction(funcName)
	if fn == nil {
		return nil, fmt.Errorf("function %s not exported from WASM module", funcName)
	}

	mem := g.module.Memory()
	if _, ok := wasmMemorySize(mem); !ok {
		return nil, fmt.Errorf("WASM module has no memory")
	}

	if len(inputJSON) > wasmMaxInputSize {
		return nil, fmt.Errorf("input too large: %d bytes (max %d)", len(inputJSON), wasmMaxInputSize)
	}

	// Adaptive output buffer strategy:
	//
	// WASM guards communicate buffer-too-small via a return code convention:
	//   -2  → buffer too small; first 4 bytes of the output buffer MAY contain the
	//          required size as a little-endian uint32. If present and > 0, we use
	//          that size for the next attempt; otherwise we double the buffer.
	//   < 0 → other error (returned as-is to the caller).
	//   >= 0 → success; value is the number of bytes written to the output buffer.
	//
	// We retry up to maxRetries times, growing from 4MB toward the 16MB ceiling.
	// A WASM trap (e.g. "wasm error: unreachable" from a Rust panic) permanently
	// marks the guard as failed because the module's internal state may be corrupt.
	outputSize := uint32(wasmInitialOutputSize)
	const maxRetries = 3

	for attempt := 0; attempt < maxRetries; attempt++ {
		result, requiredSize, err := g.tryCallWasmFunction(ctx, fn, mem, inputJSON, outputSize)
		if err != nil {
			if isWasmTrap(err) {
				// A WASM trap (e.g. unreachable from a Rust panic) leaves the
				// module in an undefined state. Log it prominently and mark the
				// guard as permanently failed so callers get a clear error.
				logger.LogError("backend", "WASM guard trap: guard=%s, func=%s, error=%v", g.name, funcName, err)
				g.failed = true
				g.failedErr = err
			}
			return nil, err
		}

		// If we got a result, return it
		if result != nil {
			logWasm.Printf("callWasmFunction: guard=%s, func=%s succeeded on attempt %d", g.name, funcName, attempt+1)
			return result, nil
		}

		// Buffer was too small, check if we can grow
		if requiredSize == 0 {
			// Guard didn't tell us the required size, double the buffer
			requiredSize = outputSize * 2
		}

		if requiredSize > wasmMaxOutputSize {
			return nil, fmt.Errorf("guard requires buffer of %d bytes which exceeds maximum of %d bytes", requiredSize, wasmMaxOutputSize)
		}

		logWasm.Printf("Buffer too small (%d bytes), retrying with %d bytes", outputSize, requiredSize)
		outputSize = requiredSize
	}

	return nil, fmt.Errorf("failed after %d attempts, buffer size %d still insufficient", maxRetries, outputSize)
}

// tryCallWasmFunction attempts to call the WASM function with the given buffer size.
// Returns (result, 0, nil) on success.
// Returns (nil, requiredSize, nil) if buffer was too small.
// Returns (nil, 0, error) on actual error.
func (g *WasmGuard) tryCallWasmFunction(ctx context.Context, fn api.Function, mem api.Memory, inputJSON []byte, outputSize uint32) ([]byte, uint32, error) {
	inputSize := uint32(len(inputJSON))
	functionName := "<unknown>"
	if def := fn.Definition(); def != nil && def.Name() != "" {
		functionName = def.Name()
	}
	logWasm.Printf("tryCallWasmFunction: guard=%s, func=%s, inputSize=%d, outputSize=%d", g.name, functionName, inputSize, outputSize)

	// Preferred path: use guard allocator only when both allocator exports are
	// available, to avoid overlapping host-managed buffers with guard heap
	// allocations and to ensure allocated memory can be freed.
	allocFn := g.module.ExportedFunction("alloc")
	deallocFn := g.module.ExportedFunction("dealloc")
	if allocFn != nil && deallocFn != nil {
		logWasm.Printf("Using guard allocator path: guard=%s", g.name)
		// Use a non-cancelable context for cleanup to avoid leaking WASM heap
		// allocations if the request context is canceled or times out.
		cleanupCtx := context.WithoutCancel(ctx)

		inputPtr, err := g.wasmAlloc(ctx, allocFn, inputSize)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to allocate WASM input buffer: %w", err)
		}
		defer g.wasmDealloc(cleanupCtx, deallocFn, inputPtr, inputSize)

		outputPtr, err := g.wasmAlloc(ctx, allocFn, outputSize)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to allocate WASM output buffer: %w", err)
		}
		defer g.wasmDealloc(cleanupCtx, deallocFn, outputPtr, outputSize)

		if !mem.Write(inputPtr, inputJSON) {
			return nil, 0, fmt.Errorf("failed to write input to WASM memory")
		}

		return decodeWasmCallResult(ctx, fn, mem, inputPtr, inputSize, outputPtr, outputSize)
	}

	if !g.warnedDirectMemoryPath {
		logger.LogWarn("guard", "WASM guard '%s' is using the direct memory fallback for %s without alloc/dealloc exports; export alloc/dealloc to avoid linear-memory overlap risks", g.name, functionName)
		g.warnedDirectMemoryPath = true
	}
	logWasm.Printf("Using direct memory path: guard=%s, inputSize=%d, outputSize=%d", g.name, inputSize, outputSize)

	// Ensure memory is large enough for our buffers
	// Layout: [...guard memory...][input buffer][output buffer]
	// Both the runtime's guardMemoryLimitPages cap and any smaller maximum
	// declared by the module apply to memory growth.
	requiredMemory := inputSize + outputSize + uint32(64*1024) // Extra 64KB for safety margin

	memSize, ok := wasmMemorySize(mem)
	if !ok {
		return nil, 0, fmt.Errorf("WASM module has no memory")
	}
	if memSize < requiredMemory {
		pages := (requiredMemory - memSize + 65535) / 65536 // Round up to pages
		logWasm.Printf("Growing WASM memory: guard=%s, current=%d, required=%d, pages=%d", g.name, memSize, requiredMemory, pages)
		_, success := mem.Grow(pages)
		if !success {
			return nil, 0, fmt.Errorf("failed to grow WASM memory from %d to %d bytes", memSize, requiredMemory)
		}
		memSize, ok = wasmMemorySize(mem)
		if !ok {
			return nil, 0, fmt.Errorf("WASM module has no memory")
		}
	}

	// Place buffers at end of memory
	outputPtr := memSize - outputSize
	inputPtr := outputPtr - inputSize

	// Write input to WASM memory
	if !mem.Write(inputPtr, inputJSON) {
		return nil, 0, fmt.Errorf("failed to write input to WASM memory")
	}

	// Call the WASM function
	return decodeWasmCallResult(ctx, fn, mem, inputPtr, inputSize, outputPtr, outputSize)
}

// decodeWasmCallResult calls fn with the given buffer pointers and decodes the
// result using the MCP Gateway WASM buffer protocol.
//
// Return values:
//   - (data, 0, nil)             — success; data contains the output bytes.
//   - (nil, requiredSize, nil)   — output buffer too small; caller should retry
//     with at least requiredSize bytes.
//   - (nil, 0, err)              — unrecoverable error.
func decodeWasmCallResult(ctx context.Context, fn api.Function, mem api.Memory, inputPtr, inputSize, outputPtr, outputSize uint32) ([]byte, uint32, error) {
	results, err := fn.Call(ctx,
		uint64(inputPtr),
		uint64(inputSize),
		uint64(outputPtr),
		uint64(outputSize))
	if err != nil {
		return nil, 0, fmt.Errorf("WASM function call failed: %w", err)
	}
	if len(results) == 0 {
		return nil, 0, fmt.Errorf("WASM function returned no results")
	}

	resultLen := int32(results[0])

	// -2 means the output buffer was too small; the guard may have written the
	// required size as a uint32 into the first four bytes of the output buffer.
	if resultLen == -2 {
		if requiredSize, ok := mem.ReadUint32Le(outputPtr); ok && requiredSize > 0 {
			return nil, requiredSize, nil
		}
		return nil, 0, nil
	}

	if resultLen < 0 {
		return nil, 0, fmt.Errorf("WASM function returned error code: %d", resultLen)
	}

	if resultLen == 0 {
		return []byte{}, 0, nil
	}

	outputJSON, ok := mem.Read(outputPtr, uint32(resultLen))
	if !ok {
		return nil, 0, fmt.Errorf("failed to read output from WASM memory (len=%d)", resultLen)
	}

	// Copy out of WASM linear memory before deferred dealloc runs or the next call
	// aliases the same region.
	return append([]byte(nil), outputJSON...), 0, nil
}

// wasmMemorySize returns mem.Size() and reports whether the memory interface is
// usable. A typed-nil/invalid memory implementation can panic on Size(); those
// cases are treated as "no memory" by returning ok=false.
func wasmMemorySize(mem api.Memory) (size uint32, ok bool) {
	if mem == nil {
		return 0, false
	}
	defer func() {
		if recover() != nil {
			size = 0
			ok = false
		}
	}()
	return mem.Size(), true
}

// wasmAlloc allocates a buffer in WASM linear memory using the guard's exported alloc function.
func (g *WasmGuard) wasmAlloc(ctx context.Context, allocFn api.Function, size uint32) (uint32, error) {
	results, err := allocFn.Call(ctx, uint64(size))
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("alloc returned no result")
	}
	ptr := uint32(results[0])
	if ptr == 0 {
		return 0, fmt.Errorf("alloc returned null pointer")
	}
	logWasm.Printf("wasmAlloc: guard=%s, size=%d, ptr=%d", g.name, size, ptr)
	return ptr, nil
}

// wasmDealloc frees a WASM linear-memory allocation via the guard's exported dealloc function.
func (g *WasmGuard) wasmDealloc(ctx context.Context, deallocFn api.Function, ptr, size uint32) {
	if deallocFn == nil || ptr == 0 || size == 0 {
		return
	}
	if _, err := deallocFn.Call(ctx, uint64(ptr), uint64(size)); err != nil {
		logWasm.Printf("WASM dealloc failed: ptr=%d size=%d err=%v", ptr, size, err)
	}
}
