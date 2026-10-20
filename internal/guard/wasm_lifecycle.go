package guard

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

var logWasm = logger.New("guard:wasm")

var globalCompilationCacheMu sync.Mutex

// WASMGuardsDirEnvVar is the environment variable name for the root directory
// containing per-server WASM guards (<root>/<serverID>/*.wasm).
const WASMGuardsDirEnvVar = "MCP_GATEWAY_WASM_GUARDS_DIR"

// ContainerGuardWasmPath is the baked-in guard path in the container image.
const ContainerGuardWasmPath = "/guards/github/00-github-guard.wasm"

// globalCompilationCache is a process-level compilation cache shared across all
// WasmGuard instances. wazero's cache is goroutine-safe and eliminates redundant
// JIT compilation when multiple guards load the same WASM binary.
var globalCompilationCache = wazero.NewCompilationCache()

// GetWASMGuardsRootDir returns the trimmed value of MCP_GATEWAY_WASM_GUARDS_DIR.
func GetWASMGuardsRootDir() string {
	return strings.TrimSpace(os.Getenv(WASMGuardsDirEnvVar))
}

// FindServerWASMGuardFile discovers the first .wasm file for a server under
// $MCP_GATEWAY_WASM_GUARDS_DIR/<serverID>.
func FindServerWASMGuardFile(serverID string) (string, bool, error) {
	guardsRootDir := GetWASMGuardsRootDir()
	if guardsRootDir == "" {
		logWasm.Printf("Skipping WASM guard discovery: %s is not set", WASMGuardsDirEnvVar)
		return "", false, nil
	}

	serverGuardDir := filepath.Join(guardsRootDir, serverID)
	logWasm.Printf("Searching for WASM guard file: serverID=%s, dir=%s", serverID, serverGuardDir)
	entries, err := os.ReadDir(serverGuardDir)
	if err != nil {
		if os.IsNotExist(err) {
			logWasm.Printf("No WASM guard directory found for serverID=%s", serverID)
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read server guard directory %q: %w", serverGuardDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(entry.Name()), ".wasm") {
			wasmPath := filepath.Join(serverGuardDir, entry.Name())
			logWasm.Printf("Found WASM guard file: serverID=%s, path=%s", serverID, wasmPath)
			return wasmPath, true, nil
		}
	}

	logWasm.Printf("No WASM guard file found in directory: serverID=%s, dir=%s", serverID, serverGuardDir)
	return "", false, nil
}

// FindGuardFile discovers the WASM guard file for a server. It checks in order:
//  1. The baked-in container guard at ContainerGuardWasmPath (only when serverID == "github").
//  2. The first .wasm file under $MCP_GATEWAY_WASM_GUARDS_DIR/<serverID>.
//
// Returns an empty string and found=false when no guard can be discovered.
func FindGuardFile(serverID string) (string, bool, error) {
	return findGuardFile(serverID, os.Stat)
}

func findGuardFile(serverID string, statFn func(string) (os.FileInfo, error)) (string, bool, error) {
	if serverID == "github" {
		// Preserve explicit opt-out behavior used by container runners:
		// when MCP_GATEWAY_WASM_GUARDS_DIR is set but blank, skip baked-in discovery.
		if guardDir, ok := os.LookupEnv(WASMGuardsDirEnvVar); ok && guardDir == "" {
			logWasm.Printf("Skipping baked-in guard discovery: %s is explicitly set but blank", WASMGuardsDirEnvVar)
			return "", false, nil
		}

		logWasm.Printf("Checking for baked-in container guard at %s", ContainerGuardWasmPath)
		if _, err := statFn(ContainerGuardWasmPath); err == nil {
			logWasm.Printf("Found baked-in container guard: %s", ContainerGuardWasmPath)
			return ContainerGuardWasmPath, true, nil
		}
	}

	return FindServerWASMGuardFile(serverID)
}

func newCompilationCache(dir string) (wazero.CompilationCache, error) {
	if dir == "" {
		logWasm.Print("Creating in-memory compilation cache")
		return wazero.NewCompilationCache(), nil
	}
	logWasm.Printf("Creating disk-backed compilation cache: dir=%s", dir)
	return wazero.NewCompilationCacheWithDir(dir)
}

// newCompilationCacheFn is a package-level indirection over newCompilationCache
// so that tests can inject a cache implementation whose Close() fails, in
// order to exercise ConfigureGlobalCompilationCache's error-joining branch
// (both the previous and replacement caches failing to close). Production
// code must not reassign this; it always points at newCompilationCache.
var newCompilationCacheFn = newCompilationCache

// ConfigureGlobalCompilationCache replaces the process-level compilation cache.
// This should be called during process startup before any guards are created.
func ConfigureGlobalCompilationCache(ctx context.Context, dir string) error {
	logWasm.Printf("Configuring global compilation cache: dir=%q", dir)
	cache, err := newCompilationCacheFn(dir)
	if err != nil {
		return err
	}

	globalCompilationCacheMu.Lock()
	oldCache := globalCompilationCache
	if oldCache == nil {
		globalCompilationCache = cache
		globalCompilationCacheMu.Unlock()
		logWasm.Print("Global compilation cache set (no previous cache)")
		return nil
	}

	if err := oldCache.Close(ctx); err != nil {
		globalCompilationCacheMu.Unlock()

		closeReplacementErr := cache.Close(ctx)
		if closeReplacementErr != nil {
			return errors.Join(
				fmt.Errorf("failed to close previous compilation cache: %w", err),
				fmt.Errorf("failed to close replacement compilation cache: %w", closeReplacementErr),
			)
		}
		return fmt.Errorf("failed to close previous compilation cache: %w", err)
	}
	globalCompilationCache = cache
	globalCompilationCacheMu.Unlock()

	logWasm.Print("Global compilation cache replaced successfully")
	return nil
}

// getGlobalCompilationCache returns the process-level compilation cache under
// the lock so it cannot be read while ConfigureGlobalCompilationCache or
// CloseGlobalCompilationCache is swapping it out.
func getGlobalCompilationCache() wazero.CompilationCache {
	globalCompilationCacheMu.Lock()
	defer globalCompilationCacheMu.Unlock()
	return globalCompilationCache
}

// CloseGlobalCompilationCache releases JIT resources held by the shared
// compilation cache. It should be called during graceful shutdown, after all
// WasmGuard runtimes have been closed (i.e., after Registry.Close()).
// Calling it while guards are still active leads to undefined behavior.
// Repeated calls are no-ops once the global cache has been cleared.
// It is not safe to call concurrently.
func CloseGlobalCompilationCache(ctx context.Context) error {
	logWasm.Print("Closing global compilation cache")
	globalCompilationCacheMu.Lock()
	cache := globalCompilationCache
	globalCompilationCache = nil
	globalCompilationCacheMu.Unlock()
	if cache == nil {
		logWasm.Print("Global compilation cache is nil, nothing to close")
		return nil
	}
	if err := cache.Close(ctx); err != nil {
		return err
	}
	logWasm.Print("Global compilation cache closed")
	return nil
}

// guardMemoryLimitPages caps guest memory at 128 MiB. Budget 8x the input
// limit for JSON parsing and guest allocations, plus 4x the output limit for
// serialization, buffer growth, and allocator headroom. The input/output
// buffers alone underestimate the Rust guard's peak memory requirements.
const guardMemoryLimitPages = (8*wasmMaxInputSize + 4*wasmMaxOutputSize) / (64 * 1024)

// newGuardRuntimeConfig builds the wazero runtime configuration used for guard
// runtimes. It applies the memory cap, context-cancellation cleanup, and
// compilation cache selection (explicit opt-out, injected cache, or shared
// global cache).
//
// DWARF debug info is disabled unless guard debug logging is enabled: guard
// binaries are untrusted third-party artifacts and the gateway never
// symbolicates guest stack traces, so skipping DWARF parsing saves compile
// time and memory without any functional loss.
//
// wazero.NewRuntimeConfig() is used (rather than NewRuntimeConfigCompiler())
// so the runtime auto-selects the Compiler (AOT) engine on supported
// architectures (amd64/arm64) and transparently falls back to the
// Interpreter engine elsewhere (e.g. riscv64), matching wazero's own
// recommended usage and keeping WASM guard support portable across all
// Go-supported architectures instead of failing to instantiate on
// unsupported ones.
func newGuardRuntimeConfig(opts *WasmGuardOptions) wazero.RuntimeConfig {
	runtimeConfig := wazero.NewRuntimeConfig().
		WithCloseOnContextDone(true).
		WithMemoryLimitPages(guardMemoryLimitPages).
		WithDebugInfoEnabled(logWasm.Enabled())

	switch {
	case opts != nil && opts.DisableCompilationCache:
		// Caller explicitly disabled caching
	case opts != nil && opts.CompilationCache != nil:
		runtimeConfig = runtimeConfig.WithCompilationCache(opts.CompilationCache)
	default:
		runtimeConfig = runtimeConfig.WithCompilationCache(getGlobalCompilationCache())
	}

	return runtimeConfig
}

// newGuardModuleConfig builds the wazero module configuration used to
// instantiate guard modules. It keeps the stdin/stdout isolation guarantees
// consistent across every instantiation site.
func newGuardModuleConfig(name string, stdout, stderr io.Writer) wazero.ModuleConfig {
	guardName := name
	if guardName == "" {
		guardName = "guard"
	}
	return wazero.NewModuleConfig().
		WithName(guardName).
		// WithStartFunctions with no args suppresses automatic _start execution
		// so guard loading cannot block on stdin or perform unexpected I/O.
		WithStartFunctions().
		WithStdin(strings.NewReader("")). // Isolate stdin
		WithStdout(stdout).               // Keep WASM stdout off gateway stdout (MCP stream)
		WithStderr(stderr)
}

// WasmGuardOptions configures optional settings for WASM guard creation
type WasmGuardOptions struct {
	// Stdout is the writer for WASM stdout output. Defaults to os.Stderr if nil.
	Stdout io.Writer
	// Stderr is the writer for WASM stderr output. Defaults to os.Stderr if nil.
	Stderr io.Writer
	// CompilationCache overrides the process-level compilation cache.
	// If nil and DisableCompilationCache is false, the shared globalCompilationCache is used.
	CompilationCache wazero.CompilationCache
	// DisableCompilationCache, when true, prevents any compilation cache from being used
	// even if a global or per-instance cache is available. This can be useful to avoid
	// unbounded memory growth when many distinct WASM binaries are loaded over the
	// lifetime of a long-lived process.
	DisableCompilationCache bool
}

// WasmGuard implements Guard interface by executing a WASM module in-process
// The WASM module runs sandboxed within the gateway using wazero runtime
// Guards cannot make direct network calls - they receive a BackendCaller interface via host functions
//
// Thread Safety: WASM modules are single-threaded, so all calls to a guard instance
// are serialized using a mutex. Concurrent requests will queue and execute one at a time.
type WasmGuard struct {
	name    string
	runtime wazero.Runtime
	module  api.Module
	wasm    []byte
	options *WasmGuardOptions

	// Backend caller provided to the guard via host functions
	backend BackendCaller

	// mu serializes all calls to the WASM module.
	// WASM modules are single-threaded and cannot handle concurrent calls.
	// All exported methods (LabelAgent, LabelResource, LabelResponse) hold mu
	// for their entire duration, including any nested calls to callWasmFunction.
	mu sync.Mutex

	// failed and failedErr are set when the WASM module encounters a trap
	// (e.g. unreachable instruction from a Rust panic). Once failed, all
	// subsequent calls return an error immediately because the module's
	// internal state may be corrupted.
	// Both fields are accessed only while mu is held.
	failed    bool
	failedErr error

	// warnedDirectMemoryPath ensures we emit the allocator-fallback warning at
	// most once per guard instance. It is accessed only while mu is held.
	warnedDirectMemoryPath bool

	// backendCallCount tracks the number of call_backend invocations for the
	// current guard function execution. It is reset at the start of each
	// callWasmGuardFunction call and accessed only while mu is held.
	backendCallCount int
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
// Options can be nil to use defaults (stdout/stderr go to os.Stderr/os.Stderr)
func NewWasmGuardWithOptions(ctx context.Context, name string, wasmBytes []byte, backend BackendCaller, opts *WasmGuardOptions) (*WasmGuard, error) {
	logWasm.Printf("Creating WASM guard from bytes: name=%s, size=%d", name, len(wasmBytes))

	runtime := wazero.NewRuntimeWithConfig(ctx, newGuardRuntimeConfig(opts))

	// Instantiate WASI
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate WASI: %w", err)
	}

	guard := &WasmGuard{
		name:    name,
		runtime: runtime,
		backend: backend,
		wasm:    append([]byte(nil), wasmBytes...),
		options: cloneWasmGuardOptions(opts),
	}

	// Create host functions for the guard to call
	if err := guard.instantiateHostFunctions(ctx); err != nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate host functions: %w", err)
	}

	// Configure module options with stdout/stderr and stdin isolation.
	stdoutWriter := io.Writer(os.Stderr)
	stderrWriter := io.Writer(os.Stderr)
	if opts != nil {
		if opts.Stdout != nil {
			stdoutWriter = opts.Stdout
		}
		if opts.Stderr != nil {
			stderrWriter = opts.Stderr
		}
	}

	// WithStdin prevents WASM from accidentally reading gateway's MCP protocol stdin
	moduleConfig := newGuardModuleConfig(name, stdoutWriter, stderrWriter)

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
	labelAgentFn := module.ExportedFunction("label_agent")

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

	if labelAgentFn == nil {
		runtime.Close(ctx)
		return nil, fmt.Errorf("WASM module must export label_agent")
	}

	expectedParamTypes := []api.ValueType{
		api.ValueTypeI32,
		api.ValueTypeI32,
		api.ValueTypeI32,
		api.ValueTypeI32,
	}
	expectedResultTypes := []api.ValueType{api.ValueTypeI32}
	validateFunctionSignature := func(functionName string, fn api.Function) error {
		def := fn.Definition()
		paramTypes := def.ParamTypes()
		resultTypes := def.ResultTypes()
		if !slices.Equal(paramTypes, expectedParamTypes) || !slices.Equal(resultTypes, expectedResultTypes) {
			return fmt.Errorf(
				"WASM module function %s must have signature (i32,i32,i32,i32)->i32, got %v->%v",
				functionName,
				paramTypes,
				resultTypes,
			)
		}
		return nil
	}

	if err := validateFunctionSignature("label_resource", labelResourceFn); err != nil {
		runtime.Close(ctx)
		return nil, err
	}
	if err := validateFunctionSignature("label_response", labelResponseFn); err != nil {
		runtime.Close(ctx)
		return nil, err
	}
	if err := validateFunctionSignature("label_agent", labelAgentFn); err != nil {
		runtime.Close(ctx)
		return nil, err
	}

	logWasm.Printf("WASM guard created successfully: name=%s", name)
	return guard, nil
}

func cloneWasmGuardOptions(opts *WasmGuardOptions) *WasmGuardOptions {
	if opts == nil {
		return nil
	}
	copy := *opts
	return &copy
}

// NewSessionGuard instantiates a WASM module with independent linear memory and
// failure state while reusing the configured compilation cache.
func (g *WasmGuard) NewSessionGuard(ctx context.Context) (Guard, error) {
	logWasm.Printf("Creating new session guard instance: guard=%s, wasmSize=%d", g.name, len(g.wasm))
	return NewWasmGuardWithOptions(ctx, g.name, g.wasm, nil, g.options)
}

// instantiateHostFunctions creates the host functions that the WASM module can call
func (g *WasmGuard) instantiateHostFunctions(ctx context.Context) error {
	logWasm.Printf("Instantiating WASM host functions: guard=%s", g.name)
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

	if err != nil {
		return err
	}
	logWasm.Printf("WASM host functions instantiated: guard=%s (call_backend, host_log)", g.name)
	return nil
}

// hostCallBackend is called by the WASM module to make backend MCP calls
func (g *WasmGuard) hostCallBackend(ctx context.Context, m api.Module, stack []uint64) {
	toolNamePtr := uint32(stack[0])
	toolNameLen := uint32(stack[1])
	argsPtr := uint32(stack[2])
	argsLen := uint32(stack[3])
	resultPtr := uint32(stack[4])
	resultSize := uint32(stack[5])

	// Helper to set return value
	setResult := func(code int32) {
		stack[0] = uint64(uint32(code))
	}

	// Helper to set generic error return value
	setError := func() {
		setResult(-1)
	}

	// Enforce per-invocation call limit to prevent runaway backend exhaustion.
	g.backendCallCount++
	if g.backendCallCount > maxBackendCallsPerInvocation {
		logWasm.Printf("Backend call limit exceeded: guard=%s, count=%d, max=%d", g.name, g.backendCallCount, maxBackendCallsPerInvocation)
		setError()
		return
	}

	// Read tool name from WASM memory
	toolNameBytes, ok := m.Memory().Read(toolNamePtr, toolNameLen)
	if !ok {
		logWasm.Printf("hostCallBackend: failed to read tool name from WASM memory: guard=%s, ptr=%d, len=%d", g.name, toolNamePtr, toolNameLen)
		setError()
		return
	}
	toolName := string(toolNameBytes)

	// Read args JSON from WASM memory
	argsBytes, ok := m.Memory().Read(argsPtr, argsLen)
	if !ok {
		logWasm.Printf("hostCallBackend: failed to read args from WASM memory: guard=%s, tool=%s, ptr=%d, len=%d", g.name, toolName, argsPtr, argsLen)
		setError()
		return
	}

	// Parse args
	var args any
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
		// Signal buffer-too-small with required size protocol:
		// return -2 and (if possible) write required size (u32 LE) to resultPtr.
		if resultSize >= 4 {
			var required [4]byte
			binary.LittleEndian.PutUint32(required[:], uint32(len(resultJSON)))
			if !m.Memory().Write(resultPtr, required[:]) {
				logWasm.Printf("Failed to write required size to WASM memory")
			}
		}
		setResult(-2)
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

// maxBackendCallsPerInvocation limits the number of times a WASM guard may call
// call_backend within a single label_agent/label_resource/label_response execution.
// Prevents runaway backend exhaustion from buggy or malicious guards.
const maxBackendCallsPerInvocation = 50

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
		logger.LogWarn("guard", "[%s] %s", g.name, msg)
	case logLevelError:
		logWasm.Printf("%sERROR: %s", prefix, msg)
		logger.LogError("guard", "[%s] %s", g.name, msg)
	default:
		logWasm.Printf("%s%s", prefix, msg)
	}
}

// Name returns the identifier for this guard
func (g *WasmGuard) Name() string {
	return g.name
}

// IsHealthy reports whether the guard is still usable after previous WASM calls.
func (g *WasmGuard) IsHealthy() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return !g.failed
}

// isWasmTrap reports whether err represents a WASM execution trap that should
// permanently poison the guard. Normal process exits (exit code 0, e.g. TinyGo
// init) are NOT considered traps. A non-zero exit code is treated as a trap.
// As a fallback for wazero execution faults (e.g. Rust panic → unreachable),
// the function also matches on wazero's "wasm error:" message prefix
// (verified against wazero v1.12.0; re-verify on upgrades).
//
// Host-side context cancellation and deadline expiry are NOT traps: the runtime
// is configured with WithCloseOnContextDone(true), so those exit codes mean the
// host tore the module down, not that the guest misbehaved. Poisoning the guard
// in that case would disable it for every other caller of the shared module.
func isWasmTrap(err error) bool {
	if err == nil {
		return false
	}
	// A normal WASI process exit (exit code 0) is not a trap — don't poison the guard.
	var exitErr *sys.ExitError
	if errors.As(err, &exitErr) {
		switch exitErr.ExitCode() {
		case 0, sys.ExitCodeContextCanceled, sys.ExitCodeDeadlineExceeded:
			return false
		}
		return true
	}
	// Fallback for wazero execution traps (e.g. Rust panic → unreachable).
	return strings.Contains(err.Error(), "wasm error:")
}

// callWasmGuardFunction serialises WASM access, sets the backend reference, marshals
// inputData, logs its size, calls the named WASM export, and returns the raw result.
// All three public dispatch methods (LabelAgent, LabelResource, LabelResponse) share
// this preamble; keeping it in one place ensures locking and backend-update logic
// cannot drift between them.
func (g *WasmGuard) callWasmGuardFunction(ctx context.Context, funcName string, backend BackendCaller, inputData map[string]any) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	// Callers can wait a long time for the mutex, so their context may already
	// be canceled by the time the lock is acquired.
	execCtx, err := wasmExecContext(ctx)
	if err != nil {
		logWasm.Printf("callWasmGuardFunction: guard=%s, func=%s aborted before execution: %v", g.name, funcName, err)
		return nil, fmt.Errorf("guard call %s aborted before WASM execution: %w", funcName, err)
	}

	g.backend = backend
	g.backendCallCount = 0 // reset per-invocation backend call counter

	inputJSON, err := json.Marshal(inputData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %s input: %w", funcName, err)
	}
	logWasm.Printf("%s input JSON: %d bytes", funcName, len(inputJSON))

	return g.callWasmFunction(execCtx, funcName, inputJSON)
}

// wasmExecContext derives the context used to execute a WASM guard call.
//
// The wazero runtime is configured with WithCloseOnContextDone(true), so a
// canceled context closes the module that every caller of this guard shares.
// Guard calls are short and mutate module-global state, so they must not be
// interrupted by a caller that goes away:
//   - if ctx is already done (e.g. the client disconnected while the call was
//     queued behind the guard mutex) the call is refused before it can reach
//     the module;
//   - otherwise the call runs under a context that cannot be canceled by the
//     caller, while keeping its values (tracing, logging) intact.
func wasmExecContext(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		return context.Background(), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return context.WithoutCancel(ctx), nil
}

// Close releases WASM runtime resources
func (g *WasmGuard) Close(ctx context.Context) error {
	logWasm.Printf("Closing WASM guard: guard=%s", g.name)
	if ctx == nil {
		ctx = context.Background()
	}
	cleanupCtx := context.WithoutCancel(ctx)
	var moduleErr, runtimeErr error
	if g.module != nil {
		moduleErr = g.module.Close(cleanupCtx)
	}
	if g.runtime != nil {
		runtimeErr = g.runtime.Close(cleanupCtx)
	}
	err := errors.Join(moduleErr, runtimeErr)
	if err != nil {
		logWasm.Printf("WASM guard close completed with errors: guard=%s, error=%v", g.name, err)
	} else {
		logWasm.Printf("WASM guard closed successfully: guard=%s", g.name)
	}
	return err
}
