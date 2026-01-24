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
  "tool_result": [...]
}
```

**Output** (JSON at outputPtr):
```json
{
  "items": [
    {"data": {...}, "labels": {"secrecy": ["public"]}}
  ]
}
```

**Returns**: Length of output JSON, 0 for no labeling, or negative for error

### Host Functions (from WASM to Gateway)

#### `call_backend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32`
Makes read-only calls to backend MCP server.

**Parameters**:
- Tool name and args as JSON in WASM memory
- Result buffer for backend response

**Returns**: Length of result JSON, or negative on error

**Example**:
```go
// Inside WASM guard
repoInfo, err := callBackendHelper("search_repositories", map[string]interface{}{
    "query": "repo:owner/name",
})
```

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
