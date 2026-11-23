package guard

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

// trapLabelAgentWasm exports "label_agent" and "memory". Calling label_agent
// executes an "unreachable" instruction, causing a genuine wazero trap. Used
// to exercise the isWasmTrap branch inside callWasmFunction (guard.failed and
// guard.failedErr get set on a real trap, not just a synthetic error).
//
// Compiled from:
//
//	(module
//	  (func (export "label_agent") (param i32 i32 i32 i32) (result i32) unreachable)
//	  (memory (export "memory") 1))
var trapLabelAgentWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x09, 0x01, 0x60, 0x04, 0x7f, 0x7f, 0x7f,
	0x7f, 0x01, 0x7f, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x18, 0x02, 0x0b,
	0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x00, 0x00, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x0a, 0x05, 0x01, 0x03, 0x00, 0x00, 0x0b,
}

// stuckAtMaxHintWasm exports "label_agent" and "memory". label_agent always
// writes a 16MB (maxOutputSize) hint into the output buffer and returns -2
// (buffer too small), regardless of the requested outputSize. Since the hint
// never exceeds maxOutputSize but also never satisfies the guard, callWasmFunction
// retries maxRetries (3) times, always receiving the same 16MB hint, and
// exhausts its retry budget without exceeding the max — exercising the final
// "failed after N attempts" return in callWasmFunction.
//
// Compiled from:
//
//	(module
//	  (func (export "label_agent") (param i32 i32 i32 i32) (result i32)
//	    (i32.store (local.get 2) (i32.const 16777216))
//	    (i32.const -2))
//	  (memory (export "memory") 1))
var stuckAtMaxHintWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x09, 0x01, 0x60, 0x04, 0x7f, 0x7f, 0x7f,
	0x7f, 0x01, 0x7f, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x18, 0x02, 0x0b,
	0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x00, 0x00, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x0a, 0x10, 0x01, 0x0e, 0x00, 0x20, 0x02, 0x41, 0x80, 0x80,
	0x80, 0x08, 0x36, 0x02, 0x00, 0x41, 0x7e, 0x0b,
}

// allocSucceedsForSmallSizeOnlyWasm exports alloc, dealloc, label_agent, and
// memory. alloc(size) returns a valid pointer (256) when size < 1000 (as with
// small input buffers) but returns 0 (allocation failure) once size reaches
// the multi-megabyte range requested for the output buffer. This is used to
// exercise the "failed to allocate WASM output buffer" branch in
// tryCallWasmFunction, which was previously unreachable because the shared
// allocGuardWasm fixture always succeeds.
//
// Compiled from:
//
//	(module
//	  (func (export "alloc") (param i32) (result i32)
//	    (if (result i32) (i32.lt_u (local.get 0) (i32.const 1000))
//	      (then (i32.const 256))
//	      (else (i32.const 0))))
//	  (func (export "dealloc") (param i32 i32))
//	  (func (export "label_agent") (param i32 i32 i32 i32) (result i32) i32.const 0)
//	  (memory (export "memory") 1))
var allocSucceedsForSmallSizeOnlyWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x13, 0x03, 0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x60, 0x02, 0x7f, 0x7f, 0x00, 0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f, 0x03, 0x04, 0x03,
	0x00, 0x01, 0x02, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x2a, 0x04, 0x05, 0x61, 0x6c, 0x6c, 0x6f,
	0x63, 0x00, 0x00, 0x07, 0x64, 0x65, 0x61, 0x6c, 0x6c, 0x6f, 0x63, 0x00, 0x01, 0x0b, 0x6c, 0x61,
	0x62, 0x65, 0x6c, 0x5f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x00, 0x02, 0x06, 0x6d, 0x65, 0x6d, 0x6f,
	0x72, 0x79, 0x02, 0x00, 0x0a, 0x1b, 0x03, 0x11, 0x00, 0x20, 0x00, 0x41, 0xe8, 0x07, 0x49, 0x04,
	0x7f, 0x41, 0x80, 0x02, 0x05, 0x41, 0x00, 0x0b, 0x0b, 0x02, 0x00, 0x0b, 0x04, 0x00, 0x41, 0x00,
	0x0b,
}

// labelReturnsHugeLenWasm exports "label_agent" and "memory" with a single
// 1-page (64KB) memory. label_agent always returns 999999, a positive
// resultLen far larger than the available memory, which fails the bounds
// check inside mem.Read (used to exercise the "failed to read output from
// WASM memory" branch of decodeWasmCallResult).
//
// Compiled from:
//
//	(module
//	  (func (export "label_agent") (param i32 i32 i32 i32) (result i32) i32.const 999999)
//	  (memory (export "memory") 1))
var labelReturnsHugeLenWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, 0x01, 0x09, 0x01, 0x60, 0x04, 0x7f, 0x7f, 0x7f,
	0x7f, 0x01, 0x7f, 0x03, 0x02, 0x01, 0x00, 0x05, 0x03, 0x01, 0x00, 0x01, 0x07, 0x18, 0x02, 0x0b,
	0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x00, 0x00, 0x06, 0x6d, 0x65,
	0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00, 0x0a, 0x08, 0x01, 0x06, 0x00, 0x41, 0xbf, 0x84, 0x3d, 0x0b,
}

// noMemoryGuardWasm exports label_agent but no linear memory.
var noMemoryGuardWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x09, 0x01, 0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x07, 0x0f, 0x01, 0x0b, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x00, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0b,
}

// noGrowGuardWasm limits its one-page memory to one page, so a buffer larger
// than the initial page exercises the direct-memory growth failure path.
var noGrowGuardWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x09, 0x01, 0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
	0x03, 0x02, 0x01, 0x00,
	0x05, 0x04, 0x01, 0x01, 0x01, 0x01,
	0x07, 0x18, 0x02, 0x0b, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x00, 0x00,
	0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00,
	0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, 0x00, 0x0b,
}

// allocOutOfBoundsGuardWasm returns an address at the end of its one-page
// memory, making even a small input fail the allocator-path write.
var allocOutOfBoundsGuardWasm = []byte{
	0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
	0x01, 0x13, 0x03, 0x60, 0x01, 0x7f, 0x01, 0x7f,
	0x60, 0x02, 0x7f, 0x7f, 0x00,
	0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
	0x03, 0x04, 0x03, 0x00, 0x01, 0x02,
	0x05, 0x03, 0x01, 0x00, 0x01,
	0x07, 0x2a, 0x04, 0x06, 0x6d, 0x65, 0x6d, 0x6f, 0x72, 0x79, 0x02, 0x00,
	0x05, 0x61, 0x6c, 0x6c, 0x6f, 0x63, 0x00, 0x00,
	0x07, 0x64, 0x65, 0x61, 0x6c, 0x6c, 0x6f, 0x63, 0x00, 0x01,
	0x0b, 0x6c, 0x61, 0x62, 0x65, 0x6c, 0x5f, 0x61, 0x67, 0x65, 0x6e, 0x74, 0x00, 0x02,
	0x0a, 0x10, 0x03, 0x06, 0x00, 0x41, 0xff, 0xff, 0x03, 0x0b,
	0x02, 0x00, 0x0b,
	0x04, 0x00, 0x41, 0x00, 0x0b,
}

func captureWasmLogs(t *testing.T, f func()) string {
	t.Helper()
	t.Setenv("DEBUG", "*")
	t.Setenv("DEBUG_COLORS", "0")

	previous := logWasm
	logWasm = logger.New("guard:wasm")
	require.True(t, logWasm.Enabled(), "the capture harness must actually enable debug logging")
	t.Cleanup(func() { logWasm = previous })

	original := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	var (
		wg  sync.WaitGroup
		buf bytes.Buffer
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&buf, r)
	}()

	func() {
		defer func() {
			os.Stderr = original
			_ = w.Close()
		}()
		f()
	}()
	wg.Wait()
	_ = r.Close()
	return buf.String()
}

// TestCallWasmFunction_ActualTrap exercises the real isWasmTrap branch (lines
// 64-71 of wasm_exec.go) by triggering a genuine wazero "unreachable" trap,
// rather than a synthetic error string. It verifies that the guard is
// permanently poisoned (g.failed=true, g.failedErr set) and that a subsequent
// call is rejected immediately without touching the (now unsafe) module.
func TestCallWasmFunction_ActualTrap(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, trapLabelAgentWasm, "actual-trap-test")
	defer cleanup()

	g.mu.Lock()
	_, err := g.callWasmFunction(context.Background(), "label_agent", []byte(`{}`))
	failed := g.failed
	failedErr := g.failedErr
	g.mu.Unlock()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "wasm error")
	assert.True(t, failed, "guard should be marked failed after a real trap")
	require.Error(t, failedErr)
	assert.Equal(t, err, failedErr)

	// A second call must be rejected immediately with the "unavailable after a
	// previous trap" message, proving the guard stays poisoned.
	g.mu.Lock()
	_, err2 := g.callWasmFunction(context.Background(), "label_agent", []byte(`{}`))
	g.mu.Unlock()
	require.Error(t, err2)
	assert.Contains(t, err2.Error(), "unavailable after a previous trap")
}

// TestCallWasmFunction_ExhaustsRetries exercises the final return statement of
// callWasmFunction (line 95), reached only when every retry attempt still
// reports a buffer-too-small condition without exceeding the maximum output
// size. stuckAtMaxHintWasm always hints exactly at the 16MB ceiling, so the
// loop runs all maxRetries attempts and falls through to the "failed after N
// attempts" error rather than the "exceeds maximum" error.
func TestCallWasmFunction_ExhaustsRetries(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, stuckAtMaxHintWasm, "exhausts-retries-test")
	defer cleanup()

	g.mu.Lock()
	result, err := g.callWasmFunction(context.Background(), "label_agent", []byte(`{}`))
	g.mu.Unlock()

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed after 3 attempts")
	assert.Contains(t, err.Error(), "buffer size 16777216 still insufficient")
}

// TestTryCallWasmFunction_FunctionNameFallback exercises the "<unknown>"
// fallback in tryCallWasmFunction (line 104-107): when fn.Definition() is nil
// or its Name() is empty, functionName should remain "<unknown>" instead of
// panicking or leaving it blank. wazero's own function definitions built from
// standard export sections normally report an empty Name() (the export name
// lives elsewhere), so this exercises the fallback branch under ordinary
// conditions without requiring a custom Function mock.
func TestTryCallWasmFunction_FunctionNameFallback(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, allocGuardWasm, "function-name-fallback-test")
	defer cleanup()

	fn := g.module.ExportedFunction("label_agent")
	require.NotNil(t, fn)
	def := fn.Definition()
	require.NotNil(t, def)
	// Confirm the precondition this test relies on: Definition().Name() is
	// empty for a plain export, forcing tryCallWasmFunction to keep using the
	// "<unknown>" fallback rather than a real function name.
	require.Empty(t, def.Name())

	mem := g.module.Memory()
	require.NotNil(t, mem)

	var (
		result       []byte
		requiredSize uint32
		err          error
	)
	logs := captureWasmLogs(t, func() {
		g.mu.Lock()
		result, requiredSize, err = g.tryCallWasmFunction(context.Background(), fn, mem, []byte(`{}`), 4096)
		g.mu.Unlock()
	})

	require.NoError(t, err)
	assert.Empty(t, result)
	assert.Zero(t, requiredSize)
	assert.Contains(t, logs, "tryCallWasmFunction: guard=function-name-fallback-test, func=<unknown>")
}

// TestTryCallWasmFunction_AllocatorPath_OutputAllocationFails exercises the
// "failed to allocate WASM output buffer" branch (lines 127-130) of
// tryCallWasmFunction. allocSucceedsForSmallSizeOnlyWasm's alloc function
// succeeds for the small input buffer but fails (returns null pointer 0) once
// asked for the multi-megabyte output buffer, so wasmAlloc returns an error
// for the *second* call only.
func TestTryCallWasmFunction_AllocatorPath_OutputAllocationFails(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, allocSucceedsForSmallSizeOnlyWasm, "output-alloc-fail-test")
	defer cleanup()

	fn := g.module.ExportedFunction("label_agent")
	require.NotNil(t, fn)
	mem := g.module.Memory()
	require.NotNil(t, mem)

	g.mu.Lock()
	result, requiredSize, err := g.tryCallWasmFunction(context.Background(), fn, mem, []byte(`{}`), 4*1024*1024)
	g.mu.Unlock()

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Zero(t, requiredSize)
	assert.Contains(t, err.Error(), "failed to allocate WASM output buffer")
}

func TestTryCallWasmFunction_MissingMemory(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, noMemoryGuardWasm, "missing-memory-test")
	defer cleanup()

	fn := g.module.ExportedFunction("label_agent")
	require.NotNil(t, fn)

	result, requiredSize, err := g.tryCallWasmFunction(context.Background(), fn, nil, []byte(`{}`), 4096)

	assert.Nil(t, result)
	assert.Zero(t, requiredSize)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WASM module has no memory")
}

func TestTryCallWasmFunction_MemoryGrowthFails(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, noGrowGuardWasm, "memory-growth-failure-test")
	defer cleanup()

	fn := g.module.ExportedFunction("label_agent")
	mem := g.module.Memory()
	require.NotNil(t, fn)
	require.NotNil(t, mem)

	result, requiredSize, err := g.tryCallWasmFunction(context.Background(), fn, mem, []byte(`{}`), wasmInitialOutputSize)

	assert.Nil(t, result)
	assert.Zero(t, requiredSize)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to grow WASM memory")
}

func TestTryCallWasmFunction_AllocatorInputWriteFails(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, allocOutOfBoundsGuardWasm, "input-write-failure-test")
	defer cleanup()

	fn := g.module.ExportedFunction("label_agent")
	mem := g.module.Memory()
	require.NotNil(t, fn)
	require.NotNil(t, mem)

	result, requiredSize, err := g.tryCallWasmFunction(context.Background(), fn, mem, []byte(`{}`), 512)

	assert.Nil(t, result)
	assert.Zero(t, requiredSize)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write input to WASM memory")
}

func TestTryCallWasmFunction_DirectInputWriteFails(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, directMemoryFallbackGuardWasm, "direct-input-write-failure-test")
	defer cleanup()

	fn := g.module.ExportedFunction("label_response")
	mem := g.module.Memory()
	require.NotNil(t, fn)
	require.NotNil(t, mem)

	result, requiredSize, err := g.tryCallWasmFunction(context.Background(), fn, mem, []byte(`{}`), ^uint32(0))

	assert.Nil(t, result)
	assert.Zero(t, requiredSize)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to write input to WASM memory")
}

// TestDecodeWasmCallResult_ReadOutOfBounds exercises the "failed to read
// output from WASM memory" branch (lines 225-228) of decodeWasmCallResult.
// labelReturnsHugeLenWasm reports a positive resultLen (999999) that is far
// larger than the module's single-page (64KB) memory, so mem.Read fails its
// bounds check.
func TestDecodeWasmCallResult_ReadOutOfBounds(t *testing.T) {
	g, cleanup := setupTestWasmGuard(t, labelReturnsHugeLenWasm, "read-out-of-bounds-test")
	defer cleanup()

	fn := g.module.ExportedFunction("label_agent")
	require.NotNil(t, fn)
	mem := g.module.Memory()
	require.NotNil(t, mem)

	result, requiredSize, err := decodeWasmCallResult(context.Background(), fn, mem, 0, 0, 0, 100)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.Zero(t, requiredSize)
	assert.Contains(t, err.Error(), "failed to read output from WASM memory")
}
