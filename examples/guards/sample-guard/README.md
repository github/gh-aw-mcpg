# Sample DIFC Guard for WASM

This is a sample DIFC guard written in Go that compiles to WebAssembly (WASM).

> **Tip**: For simpler guard development, use the [Guard SDK](../guardsdk/README.md) which handles memory management and JSON marshaling for you. See [guardsdk/example](../guardsdk/example/) for a simplified version of this guard.

## Requirements and Limitations

### TinyGo + Go 1.23 Requirement

**TinyGo is required** for proper WASM function exports. Standard Go's `wasip1` target does not support the `//export` directive needed for guard functions.

**Version Compatibility**:
- **Gateway**: Go 1.25 (current project version)
- **Guards**: Go 1.23 (for TinyGo compatibility)
- **TinyGo**: 0.34+ (supports Go 1.19-1.23)

**Key insight**: WASM is version-independent! A guard compiled with Go 1.23 works perfectly with a gateway compiled with Go 1.25. The gateway and guard communicate only through:
- JSON data in linear memory
- Function calls via exported symbols

There is no Go version coupling between the gateway and guards.

### Setup

**For Gateway Development** (Go 1.25):
```bash
# Already installed - use for gateway
go version  # Should show go1.25
```

**For Guard Development** (Go 1.23 + TinyGo):

#### macOS

```bash
# Install Go 1.23.4 alongside your main Go version
go install golang.org/dl/go1.23.4@latest
~/go/bin/go1.23.4 download

# Verify installation
~/go/bin/go1.23.4 version  # Should show go1.23.4

# Install TinyGo via Homebrew
brew tap tinygo-org/tools
brew install tinygo

# Verify TinyGo
tinygo version
```

#### Linux (Debian/Ubuntu)

```bash
# Install Go 1.23.4 alongside your main Go version
go install golang.org/dl/go1.23.4@latest
~/go/bin/go1.23.4 download

# Install TinyGo
# See: https://tinygo.org/getting-started/install/
curl -sSfL -O https://github.com/tinygo-org/tinygo/releases/download/v0.34.0/tinygo_0.34.0_amd64.deb
sudo dpkg -i tinygo_0.34.0_amd64.deb
```

#### Other Platforms

See the [TinyGo installation guide](https://tinygo.org/getting-started/install/) for additional platforms.

### Building

To compile this guard to WASM using TinyGo with Go 1.23:

```bash
# Set GOROOT to use Go 1.23.4
export GOROOT=$(~/go/bin/go1.23.4 env GOROOT)
tinygo build -o guard.wasm -target=wasi main.go
```

Or use the Makefile (tries Go 1.23 automatically):
```bash
make build
```

## Overview

WASM guards run **inside the gateway process** in a sandboxed wazero runtime. They cannot make direct network calls or access the filesystem.

### Guard Execution Model

```
┌───────────────────────────────────────────────────────────────┐
│ Gateway Process                                               │
│                                                               │
│  ┌─────────────────────────────────┐                          │
│  │ WasmGuard (Go)                  │                          │
│  │  ┌───────────────────────────┐  │                          │
│  │  │ guard.wasm                │  │                          │
│  │  │ (sandboxed in wazero)     │  │                          │
│  │  │                           │  │                          │
│  │  │ - label_resource()        │  │                          │
│  │  │ - label_response()        │  │                          │
│  │  │ - call_backend() ─────────┼──┼───► BackendCaller        │
│  │  └───────────────────────────┘  │           │              │
│  └─────────────────────────────────┘           │              │
│                                                ▼              │
│                                           MCP Backend         │
└───────────────────────────────────────────────────────────────┘
```

Guards:
- Run in-process (not separate CLI)
- Execute in sandboxed wazero runtime
- Cannot make direct network/file I/O
- Call backend via controlled host function

## Interface

### Exported Functions (from WASM to Gateway)

#### `label_resource(inputPtr, inputLen, outputPtr, outputSize uint32) int32`
Labels a resource before access.

**Input** (JSON at inputPtr):
```json
{
  "tool_name": "create_issue",
  "tool_args": {"owner": "org", "repo": "repo", "title": "Bug"}
}
```

**Output** (JSON at outputPtr):
```json
{
  "resource": {
    "description": "resource:create_issue",
    "secrecy": ["public"],
    "integrity": ["contributor"]
  },
  "operation": "write"
}
```

**Returns**: Length of output JSON (>0), 0 for empty, or negative for error

#### `label_response(inputPtr, inputLen, outputPtr, outputSize uint32) int32`
Labels response data for fine-grained filtering.

**Input** (JSON at inputPtr):
```json
{
  "tool_name": "list_issues",
  "tool_result": { "items": [...] }
}
```

**Output** (JSON at outputPtr) - **Path-Based Format (Preferred)**:

The path-based format uses JSON Pointer (RFC 6901) paths to label elements in the response without copying data. This is more efficient for large responses.

```json
{
  "items_path": "/items",
  "labeled_paths": [
    {
      "path": "/items/0",
      "labels": {
        "description": "Issue #1 in public repo",
        "secrecy": ["public"],
        "integrity": ["untrusted"]
      }
    },
    {
      "path": "/items/1",
      "labels": {
        "description": "Issue #2 in private repo",
        "secrecy": ["repo:corp/internal-tools"],
        "integrity": ["github_verified"]
      }
    }
  ],
  "default_labels": {
    "description": "Unlabeled item",
    "secrecy": ["public"],
    "integrity": ["untrusted"]
  }
}
```

**Path-Based Response Schema**:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `items_path` | string | No | JSON Pointer to the array containing items (e.g., `"/items"`, `""` for root array) |
| `labeled_paths` | array | Yes | Array of path-label pairs |
| `labeled_paths[].path` | string | Yes | JSON Pointer (RFC 6901) to the element (e.g., `"/items/0"`) |
| `labeled_paths[].labels` | object | Yes | DIFC labels for this element |
| `labeled_paths[].labels.description` | string | No | Human-readable description |
| `labeled_paths[].labels.secrecy` | string[] | Yes | Secrecy tags (e.g., `["public"]`, `["repo:owner/name"]`) |
| `labeled_paths[].labels.integrity` | string[] | Yes | Integrity tags (e.g., `["untrusted"]`, `["github_verified"]`) |
| `default_labels` | object | No | Labels for items not explicitly matched by a path |

**Output** (JSON at outputPtr) - **Legacy Format (Deprecated)**:

The legacy format copies data for each item. Use path-based format for better performance.

```json
{
  "items": [
    {"data": {...}, "labels": {"secrecy": ["public"]}}
  ]
}
```

**Returns**: Length of output JSON, 0 for no labeling, or negative for error

### Host Functions (Imported from Gateway)

The gateway provides host functions that WASM guards can import to interact with the outside world. These are the only way for sandboxed guards to communicate with external systems.

#### `call_backend`

Makes read-only calls to backend MCP servers for gathering metadata needed for labeling decisions.

```go
//go:wasmimport env call_backend
func callBackend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32
```

**Parameters**:
- `toolNamePtr`, `toolNameLen`: Pointer and length of the tool name string in WASM memory
- `argsPtr`, `argsLen`: Pointer and length of JSON-encoded arguments
- `resultPtr`, `resultSize`: Pointer and size of buffer for the result

**Returns**: 
- Positive: Length of result JSON written to `resultPtr`
- `0xFFFFFFFF` (max uint32): Error occurred

**Example Usage**:
```go
func callBackendHelper(toolName string, args interface{}) ([]byte, error) {
    argsJSON, _ := json.Marshal(args)
    toolNameBytes := []byte(toolName)
    resultBuf := make([]byte, 1024*1024) // 1MB buffer
    
    resultLen := callBackend(
        uint32(uintptr(unsafe.Pointer(&toolNameBytes[0]))),
        uint32(len(toolNameBytes)),
        uint32(uintptr(unsafe.Pointer(&argsJSON[0]))),
        uint32(len(argsJSON)),
        uint32(uintptr(unsafe.Pointer(&resultBuf[0]))),
        uint32(len(resultBuf)),
    )
    
    if resultLen == 0xFFFFFFFF {
        return nil, fmt.Errorf("backend call failed")
    }
    return resultBuf[:resultLen], nil
}

// Usage
repoInfo, err := callBackendHelper("search_repositories", map[string]interface{}{
    "query": "repo:owner/name",
})
```

**Limitations**:
- Read-only: Guards can query but cannot modify backend state
- 1MB result buffer limit
- Synchronous: Blocks guard execution until complete

#### `host_log`

Sends log messages from the guard back to the gateway for debugging and monitoring.

```go
//go:wasmimport env host_log
func hostLog(level, msgPtr, msgLen uint32)
```

**Parameters**:
- `level`: Log level (0=debug, 1=info, 2=warn, 3=error)
- `msgPtr`, `msgLen`: Pointer and length of the message string in WASM memory

**Log Levels**:
| Value | Level | Description |
|-------|-------|-------------|
| 0 | Debug | Verbose debugging information |
| 1 | Info | Informational messages |
| 2 | Warn | Warning messages |
| 3 | Error | Error messages |

**Example Usage**:
```go
const (
    LogLevelDebug = 0
    LogLevelInfo  = 1
    LogLevelWarn  = 2
    LogLevelError = 3
)

func logInfo(msg string) {
    b := []byte(msg)
    hostLog(LogLevelInfo, uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b)))
}

func logDebug(msg string) {
    b := []byte(msg)
    hostLog(LogLevelDebug, uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b)))
}

// Usage in label_resource
func labelResource(...) int32 {
    logInfo("Processing tool: create_issue")
    // ... labeling logic
    logDebug("Resource labeled successfully")
}
```

**Viewing Guard Logs**:

Guard logs appear in gateway debug output. Enable with:

```bash
# Enable all guard logs
DEBUG=guard:* ./awmg --config config.toml

# Enable logs for specific guard
DEBUG=guard:github ./awmg --config config.toml
```

Log messages are prefixed with the guard name:
```
[guard:github] INFO: Processing tool: create_issue
[guard:github] DEBUG: Resource labeled successfully
```

> **Tip**: For simpler logging, use the [Guard SDK](../guardsdk/README.md) which provides `sdk.LogInfo()`, `sdk.LogDebug()`, `sdk.LogWarn()`, and `sdk.LogError()` helper functions.

## Example Configuration

```toml
[servers.github]
container = "ghcr.io/github/github-mcp-server"
guard = "github"

[guards.github]
type = "wasm"
path = "./examples/guards/sample-guard/guard.wasm"
```

## Implementation Notes

- **In-process execution**: Guard runs inside gateway, not as separate process
- **Sandboxed**: wazero runtime prevents direct I/O and network access
- **TinyGo required**: Standard Go doesn't support `//export` for WASM
- **JSON-based**: All data exchange uses JSON (TinyGo-compatible)
- **Simple types**: No complex Go types across WASM boundary
- **Read-only backend**: Guards can only read from backend, not write

## TinyGo Limitations

TinyGo has some standard library limitations:
- ✓ encoding/json - Works
- ✓ fmt - Works
- ✓ Basic stdlib - Works
- ✗ Reflection - Limited
- ✗ Some stdlib packages - Not available

The guard interface is designed to work within these constraints using simple JSON data exchange.
