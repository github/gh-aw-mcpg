# Echo Guard

A simple debugging guard that prints all request data to stdout.

## Purpose

Use this guard to understand what data is passed to guards during labeling decisions. It logs:

- **Tool Name** - The MCP tool being called
- **Tool Args** - All arguments passed to the tool (JSON formatted)
- **Capabilities** - Agent capabilities if present
- **Tool Result** - The result from the backend (in `label_response`)

## Building

```bash
# Install Go 1.23.4 (if not already installed)
go install golang.org/dl/go1.23.4@latest
~/go/bin/go1.23.4 download

# Build
export GOROOT=$(~/go/bin/go1.23.4 env GOROOT)
tinygo build -o guard.wasm -target=wasi main.go
```

## Configuration

```toml
[servers.github]
container = "ghcr.io/github/github-mcp-server"
guard = "echo"

[guards.echo]
type = "wasm"
path = "./examples/guards/echo-guard/guard.wasm"
```

## Example Output

When a `get_issue` tool is called:

```
=== label_resource called ===
Tool Name: get_issue
Tool Args:
  {
    "owner": "octocat",
    "repo": "hello-world",
    "issue_number": 42
  }
=============================
```

When the response is received:

```
=== label_response called ===
Tool Name: get_issue
Tool Result:
  {
    "number": 42,
    "title": "Found a bug",
    "user": {
      "login": "octocat"
    },
    "labels": [
      {"name": "bug"}
    ]
  }
=============================
```

## Behavior

The echo guard always returns:
- **Resource**: Public, untrusted with description `echo:<tool_name>`
- **Operation**: Read
- **Response labeling**: None (passes through unchanged)

This makes it safe to use for debugging without affecting access control.
