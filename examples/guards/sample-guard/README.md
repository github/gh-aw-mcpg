# Sample DIFC Guard for WASM

This is a sample DIFC guard written in Go that compiles to WebAssembly (WASM).

## Requirements and Limitations

### TinyGo Requirement

**TinyGo is required** for proper WASM function exports. Standard Go's `wasip1` target does not support the `//export` directive needed for guard functions.

**Current Limitation**: TinyGo 0.34 supports Go 1.19-1.23, but this project uses Go 1.25. 

**Workarounds**:
1. Wait for TinyGo to support Go 1.25 (check https://github.com/tinygo-org/tinygo/releases)
2. Use a separate Go 1.23 installation for guard compilation only
3. The framework is implemented and ready - guard compilation is the only blocker

### Building

```bash
make build
```

The Makefile will:
1. Try to build with TinyGo (required for working guards)
2. Fall back to standard Go if TinyGo fails (produces non-functional WASM for testing structure only)

## Overview

WASM guards run **inside the gateway process** in a sandboxed wazero runtime. They cannot make direct network calls or access the filesystem.

### Guard Execution Model

```
┌─────────────────────────────────────┐
│ Gateway Process                      │
│  ┌────────────────────────────────┐ │
│  │ WasmGuard (Go)                 │ │
│  │  ┌──────────────────────────┐  │ │
│  │  │ guard.wasm               │  │ │
│  │  │ (sandboxed in wazero)    │  │ │
│  │  │                          │  │ │
│  │  │ - label_resource()       │  │ │
│  │  │ - label_response()       │  │ │
│  │  │ - call_backend() ───────┐│  │ │
│  │  └──────────────────────────┘│  │ │
│  │             │                 │  │ │
│  │             └─────────────────┼──┼─┼─► BackendCaller
│  └────────────────────────────────┘ │ │       │
│                                      │ │       ▼
│                                      │ │   MCP Backend
└──────────────────────────────────────┘ └───────────
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
