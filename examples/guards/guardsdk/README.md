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

Security classification for a resource.

```go
type ResourceLabels struct {
    Description string   // Human-readable description
    Secrecy     []string // Secrecy tags (e.g., "public", "repo_private")
    Integrity   []string // Integrity tags (e.g., "untrusted", "contributor")
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
        isPrivate := checkItemPrivate(item)
        
        labels[i] = sdk.PathLabel{
            Path: fmt.Sprintf("/items/%d", i),
            Labels: sdk.ResourceLabels{
                Description: fmt.Sprintf("Item %d", i),
                Secrecy:     getSecrecyTags(isPrivate),
                Integrity:   []string{"untrusted"},
            },
        }
    }
    
    return sdk.NewPathLabelResponseResponse("/items", labels...).
        WithDefaultLabels(sdk.NewPublicResource("default")), nil
}
```

### Label Constructors

```go
// Create a public, untrusted resource
sdk.NewPublicResource("issue:owner/repo#123")

// Create a private resource with specified integrity
sdk.NewPrivateResource("issue:owner/repo#123", "contributor")

// Create a resource with custom labels
sdk.NewResource("issue:owner/repo#123", 
    []string{"repo_private", "sensitive"},
    []string{"maintainer"})
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
