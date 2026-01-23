# Sample DIFC Guard for WASM

This is a sample DIFC guard written in Go that can be compiled to WebAssembly (WASM).

## Overview

WASM guards run in a sandboxed environment and cannot make direct network calls or access the filesystem. They interact with the MCP Gateway through a controlled interface:

- **Host functions**: The guard can call `call_backend` to make read-only requests to the backend MCP server
- **Exported functions**: The guard exports `label_resource` and `label_response` functions that the gateway calls

## Building

To compile this guard to WASM:

```bash
make build
```

This will create `guard.wasm` in the current directory.

## Interface

### Exported Functions

#### `label_resource`
Called before accessing a resource to determine its DIFC labels and operation type.

**Input** (JSON):
```json
{
  "tool_name": "create_issue",
  "tool_args": {"owner": "org", "repo": "repo", "title": "Bug"},
  "capabilities": {...}
}
```

**Output** (JSON):
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

#### `label_response`
Called after a successful backend call to label response data for fine-grained filtering.

**Input** (JSON):
```json
{
  "tool_name": "list_issues",
  "tool_result": [...],
  "capabilities": {...}
}
```

**Output** (JSON):
```json
{
  "items": [
    {
      "data": {...},
      "labels": {
        "description": "issue:1",
        "secrecy": ["public"],
        "integrity": ["maintainer"]
      }
    }
  ]
}
```

### Host Functions

#### `call_backend`
Allows the guard to make read-only calls to the backend MCP server to gather metadata.

**Signature**:
```go
func callBackend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32
```

Returns the length of the result JSON, or a negative number on error.

## Example Configuration

```toml
[servers.github]
container = "ghcr.io/github/github-mcp-server"
guard = "github"

[guards.github]
type = "wasm"
path = "/path/to/guard.wasm"
```

## Implementation Notes

- The guard must export `malloc` and `free` for memory management
- All data is passed as JSON via linear memory
- The guard runs in a sandboxed environment with no direct I/O access
- Backend calls are mediated by the gateway and are read-only
