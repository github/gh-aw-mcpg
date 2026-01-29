# Guard SDK

A support library for building WASM guards for MCP Gateway.

## Overview

The `guardsdk` package simplifies guard development by handling:

- **Memory management** - WASM host/guest communication
- **JSON marshaling** - Request/response serialization
- **Backend calls** - Simplified interface to call backend MCP tools
- **Standard types** - Common request/response structures
- **Helper functions** - Argument extraction and label constructors

## Installation

The Guard SDK is currently available on the `lpcox/github-difc` branch.

**In your guard's go.mod:**

```
module my-guard

go 1.23.4

require github.com/githubnext/gh-aw-mcpg v0.0.0

replace github.com/githubnext/gh-aw-mcpg => github.com/githubnext/gh-aw-mcpg lpcox/github-difc
```

**Import in your code:**

```go
import sdk "github.com/githubnext/gh-aw-mcpg/examples/guards/guardsdk"
```

## Quick Start

```go
package main

import (
    "fmt"
    sdk "github.com/githubnext/gh-aw-mcpg/examples/guards/guardsdk"
)

func init() {
    sdk.RegisterLabelResource(labelResource)
    sdk.RegisterLabelResponse(labelResponse)
}

func labelResource(req *sdk.LabelResourceRequest) (*sdk.LabelResourceResponse, error) {
    return &sdk.LabelResourceResponse{
        Resource:  sdk.NewPublicResource(fmt.Sprintf("resource:%s", req.ToolName)),
        Operation: sdk.OperationRead,
    }, nil
}

func labelResponse(req *sdk.LabelResponseRequest) (*sdk.LabelResponseResponse, error) {
    return nil, nil // No fine-grained labeling
}

func main() {}
```

## Building

Requires Go 1.23.4 and TinyGo 0.34+:

```bash
# Install Go 1.23.4
go install golang.org/dl/go1.23.4@latest
~/go/bin/go1.23.4 download

# Build with TinyGo
export GOROOT=$(~/go/bin/go1.23.4 env GOROOT)
tinygo build -o guard.wasm -target=wasi main.go
```

## Host Functions

WASM guards run in a sandboxed wazero runtime inside the gateway process. They cannot make direct network calls or access the filesystem. Instead, the gateway provides **host functions** that guards can import to interact with the outside world.

The Guard SDK wraps these host functions for convenient use. If you're not using the SDK, you can import them directly.

### call_backend

Allows guards to make **read-only** calls to backend MCP servers for gathering metadata needed for labeling decisions.

**SDK Usage (recommended):**

```go
import sdk "github.com/githubnext/gh-aw-mcpg/examples/guards/guardsdk"

// Generic call - returns interface{}
result, err := sdk.CallBackend("get_issue", map[string]interface{}{
    "owner":        "octocat",
    "repo":         "hello-world",
    "issue_number": 42,
})

// Typed call - unmarshals to specific type
type Issue struct {
    Number int    `json:"number"`
    Title  string `json:"title"`
}
issue, err := sdk.CallBackendTyped[Issue]("get_issue", args)
```

**Direct import (without SDK):**

```go
//go:wasmimport env call_backend
func callBackend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32
```

**Parameters:**
- `toolNamePtr`, `toolNameLen`: Pointer and length of the tool name string
- `argsPtr`, `argsLen`: Pointer and length of JSON-encoded arguments
- `resultPtr`, `resultSize`: Pointer and size of buffer for result

**Returns:** Result length on success, or `0xFFFFFFFF` (max uint32) on error.

**Limitations:**
- Read-only: Guards can query backend state but cannot modify it
- 1MB result buffer limit
- Calls are synchronous and block the guard execution

### host_log

Allows guards to send log messages back to the gateway for debugging and monitoring.

**SDK Usage (recommended):**

```go
import sdk "github.com/githubnext/gh-aw-mcpg/examples/guards/guardsdk"

// Log at different levels
sdk.LogDebug("Processing tool: " + toolName)
sdk.LogInfo("Starting resource labeling")
sdk.LogWarn("Fallback to default labels")
sdk.LogError("Critical error occurred")

// Formatted logging
sdk.Logf(sdk.LogLevelInfo, "Processing %s with %d args", toolName, len(args))
```

**Log Levels:**

| Constant | Value | Description |
|----------|-------|-------------|
| `sdk.LogLevelDebug` | 0 | Debug messages (verbose) |
| `sdk.LogLevelInfo` | 1 | Informational messages |
| `sdk.LogLevelWarn` | 2 | Warning messages |
| `sdk.LogLevelError` | 3 | Error messages |

**Direct import (without SDK):**

```go
//go:wasmimport env host_log
func hostLog(level, msgPtr, msgLen uint32)
```

**Parameters:**
- `level`: Log level (0=debug, 1=info, 2=warn, 3=error)
- `msgPtr`, `msgLen`: Pointer and length of the message string

**Viewing Guard Logs:**

Guard log messages appear in gateway debug output. Enable with:

```bash
# Enable all guard logs
DEBUG=guard:* ./awmg --config config.toml

# Enable specific guard logs
DEBUG=guard:myguard ./awmg --config config.toml
```

Log messages are prefixed with the guard name for easy identification:
```
[guard:myguard] INFO: Processing create_issue for owner/repo
```

## API Reference

### Types

#### LabelResourceRequest

Input for labeling a resource before access.

```go
type LabelResourceRequest struct {
    ToolName     string                 // Name of the tool being called
    ToolArgs     map[string]interface{} // Arguments passed to the tool
    Capabilities interface{}            // Agent capabilities (optional)
}
```

**Helper methods:**

```go
// Extract typed values from ToolArgs
req.GetString("owner")              // (string, bool)
req.GetInt("issue_number")          // (int, bool)
req.GetFloat("amount")              // (float64, bool)
req.GetBool("draft")                // (bool, bool)
req.GetStringSlice("labels")        // ([]string, bool)
req.GetOwnerRepo()                  // (owner, repo string, ok bool)
```

#### LabelResourceResponse

Output from labeling a resource.

```go
type LabelResourceResponse struct {
    Resource  ResourceLabels // Security labels for the resource
    Operation Operation      // "read", "write", or "read-write"
}
```

#### ResourceLabels

Security classification for a resource. Per DIFC conventions:
- Empty secrecy `[]` means public (no sensitivity restrictions)
- Empty integrity `[]` means no endorsement
- Tags must be repo-scoped: `contributor:<owner/repo>`, `maintainer:<owner/repo>`, `private:<owner/repo>`

```go
type ResourceLabels struct {
    Description string   // Human-readable description
    Secrecy     []string // Secrecy tags (e.g., [], ["private:owner/repo"], ["secret"])
    Integrity   []string // Integrity tags (e.g., [], ["contributor:owner/repo"])
}
```

#### LabelResponseResponse (Path-Based Labeling)

Output from labeling a response. The **path-based format is preferred** as it doesn't copy data.

```go
type LabelResponseResponse struct {
    // Path-based format (preferred): paths and labels without data copying
    LabeledPaths  []PathLabel     `json:"labeled_paths,omitempty"`
    DefaultLabels *ResourceLabels `json:"default_labels,omitempty"`
    ItemsPath     string          `json:"items_path,omitempty"`
    
    // Legacy format: items with copied data (deprecated)
    Items []LabeledItem `json:"items,omitempty"`
}

type PathLabel struct {
    Path   string         `json:"path"`   // JSON Pointer (RFC 6901), e.g., "/items/0"
    Labels ResourceLabels `json:"labels"` // DIFC labels for this element
}
```

**JSON Schema for Path-Based Response:**

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "items_path": {
      "type": "string",
      "description": "JSON Pointer to the array containing items (e.g., '/items', '' for root)"
    },
    "labeled_paths": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["path", "labels"],
        "properties": {
          "path": {
            "type": "string",
            "description": "JSON Pointer (RFC 6901) to the element"
          },
          "labels": {
            "type": "object",
            "required": ["secrecy", "integrity"],
            "properties": {
              "description": { "type": "string" },
              "secrecy": { "type": "array", "items": { "type": "string" } },
              "integrity": { "type": "array", "items": { "type": "string" } }
            }
          }
        }
      }
    },
    "default_labels": {
      "type": "object",
      "properties": {
        "description": { "type": "string" },
        "secrecy": { "type": "array", "items": { "type": "string" } },
        "integrity": { "type": "array", "items": { "type": "string" } }
      }
    }
  }
}
```

**Example - Path-Based Response:**

```go
func labelResponse(req *sdk.LabelResponseRequest) (*sdk.LabelResponseResponse, error) {
    // Check if response has an "items" array
    resultMap, ok := req.ToolResult.(map[string]interface{})
    if !ok {
        return nil, nil // No labeling for non-object responses
    }
    
    items, ok := resultMap["items"].([]interface{})
    if !ok || len(items) == 0 {
        return nil, nil
    }
    
    // Label each item using paths (no data copying)
    labels := make([]sdk.PathLabel, len(items))
    for i, item := range items {
        // Determine labels based on item content
        repoID, isPrivate := getRepoInfo(item)
        
        labels[i] = sdk.PathLabel{
            Path: fmt.Sprintf("/items/%d", i),
            Labels: sdk.NewRepoResource(
                fmt.Sprintf("Item %d", i),
                repoID,
                isPrivate,
                []string{}, // empty = no endorsement
            ),
        }
    }
    
    return sdk.NewPathLabelResponseResponse("/items", labels...).
        WithDefaultLabels(sdk.NewPublicResource("default")), nil
}
```

### Label Constructors

```go
// Create a public resource with no endorsement (empty labels)
sdk.NewPublicResource("issue:owner/repo#123")
// → Secrecy: [], Integrity: []

// Create a repo-scoped private resource with expanded integrity
sdk.NewPrivateResource("issue:owner/repo#123", "owner/repo", sdk.ContributorIntegrity("owner/repo"))
// → Secrecy: ["private:owner/repo"], Integrity: ["contributor:owner/repo"]

// Create a resource with custom secrecy and integrity
sdk.NewResource("issue:owner/repo#123", 
    []string{"private:owner/repo", "secret"},
    sdk.MaintainerIntegrity("owner/repo"))
// → Secrecy: ["private:owner/repo", "secret"], Integrity: ["contributor:owner/repo", "maintainer:owner/repo"]

// Create a repo-scoped resource (public or private based on visibility)
sdk.NewRepoResource("issue:owner/repo#123", "owner/repo", isPrivate, sdk.ContributorIntegrity("owner/repo"))
```

### Integrity Hierarchy Helpers

GitHub integrity tags are hierarchical. Guards must expand them to include all implied levels:

```go
// Contributor level (just contributor)
sdk.ContributorIntegrity("owner/repo")
// → ["contributor:owner/repo"]

// Maintainer level (implies contributor)
sdk.MaintainerIntegrity("owner/repo")
// → ["contributor:owner/repo", "maintainer:owner/repo"]

// Project level (implies maintainer and contributor)
sdk.ProjectIntegrity("owner/repo")
// → ["contributor:owner/repo", "maintainer:owner/repo", "project:owner/repo"]
```

### Operations

```go
sdk.OperationRead      // "read" - Read-only access
sdk.OperationWrite     // "write" - Write/modify access
sdk.OperationReadWrite // "read-write" - Both read and write
```

### Backend Calls

Guards can call backend MCP tools to gather metadata for labeling decisions:

```go
// Generic call - returns interface{}
result, err := sdk.CallBackend("get_issue", map[string]interface{}{
    "owner":        "octocat",
    "repo":         "hello-world",
    "issue_number": 42,
})

// Typed call - unmarshals to specific type
type Issue struct {
    Number int    `json:"number"`
    Title  string `json:"title"`
    User   struct {
        Login string `json:"login"`
    } `json:"user"`
}

issue, err := sdk.CallBackendTyped[Issue]("get_issue", args)
```

## Example: GitHub Guard

See [example/main.go](example/main.go) for a complete example that:

1. Labels write operations (create_issue, merge_pull_request)
2. Checks repository visibility for read operations
3. Inspects issue details for fine-grained labeling
4. Detects sensitive issue labels (security, confidential)

## Common Patterns

### Checking Repository Visibility

```go
func checkRepoPrivate(owner, repo string) bool {
    result, err := sdk.CallBackend("search_repositories", map[string]interface{}{
        "query": fmt.Sprintf("repo:%s/%s", owner, repo),
    })
    if err != nil {
        return false
    }
    
    if data, ok := result.(map[string]interface{}); ok {
        if items, ok := data["items"].([]interface{}); ok && len(items) > 0 {
            if first, ok := items[0].(map[string]interface{}); ok {
                if private, ok := first["private"].(bool); ok {
                    return private
                }
            }
        }
    }
    return false
}
```

### Write vs Read Operations

```go
func labelResource(req *sdk.LabelResourceRequest) (*sdk.LabelResourceResponse, error) {
    resp := &sdk.LabelResourceResponse{
        Resource:  sdk.NewPublicResource(req.ToolName),
        Operation: sdk.OperationRead,
    }

    // Categorize by tool name patterns
    switch {
    case strings.HasPrefix(req.ToolName, "create_"),
         strings.HasPrefix(req.ToolName, "update_"),
         strings.HasPrefix(req.ToolName, "delete_"):
        resp.Operation = sdk.OperationWrite
        resp.Resource.Integrity = []string{"contributor"}
        
    case strings.HasPrefix(req.ToolName, "merge_"):
        resp.Operation = sdk.OperationReadWrite
        resp.Resource.Integrity = []string{"maintainer"}
    }

    return resp, nil
}
```

## Limitations

- **TinyGo required** - Standard Go doesn't support WASM function exports
- **Read-only backend calls** - Guards can only read from backends, not write
- **1MB response limit** - Backend call results are limited to 1MB
