# External WASM Guard Quick Start Guide

This guide explains how to create, build, and host WASM guards in a separate repository from the MCP Gateway.

## Overview

WASM guards can be developed and maintained in separate repositories, then loaded by the gateway at runtime. This allows:
- Independent versioning and development
- Team-specific guard implementations
- Secure distribution via GitHub Releases or Packages

## GitHub Storage Options for WASM Modules

GitHub provides several secure ways to host WASM modules:

### 1. GitHub Releases (Recommended)
**Best for**: Versioned guard releases
- Attach `.wasm` files as release assets
- Access via stable URLs: `https://github.com/owner/repo/releases/download/v1.0.0/guard.wasm`
- Supports checksums for verification
- Public or private repositories

### 2. GitHub Packages (Container Registry)
**Best for**: OCI-compatible workflows
- Package WASM as OCI artifacts
- Access via `ghcr.io/owner/guard:tag`
- Requires OCI tooling to extract WASM
- More complex but consistent with container workflows

### 3. Git LFS (Large File Storage)
**Best for**: Development/testing
- Store WASM in repository with Git LFS
- Clone repository to access guards
- Less suitable for production distribution

**Recommendation**: Use **GitHub Releases** for production guard distribution. It's simple, secure, and provides stable URLs.

## Quick Start: Creating a Separate Guard Repository

### Step 1: Fork or Create Guard Repository

```bash
# Option A: Fork the sample guard
gh repo fork githubnext/gh-aw-mcpg --clone
cd gh-aw-mcpg/examples/guards/sample-guard

# Option B: Create from scratch
mkdir my-difc-guard && cd my-difc-guard
git init
```

### Step 2: Set Up Guard Project

If starting from scratch, create the minimal structure:

```bash
# Create guard source
cat > main.go << 'EOF'
package main

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

//go:wasmimport env call_backend
func callBackend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32

//export label_resource
func labelResource(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	// Read input
	input := readBytes(inputPtr, inputLen)
	var req map[string]interface{}
	json.Unmarshal(input, &req)
	
	// Extract owner/repo for repo-scoped tags
	toolArgs, _ := req["tool_args"].(map[string]interface{})
	owner, _ := toolArgs["owner"].(string)
	repo, _ := toolArgs["repo"].(string)
	
	// Create response with empty labels (public, no endorsement)
	// Per DIFC spec: empty secrecy = public, empty integrity = no endorsement
	output := map[string]interface{}{
		"resource": map[string]interface{}{
			"description": fmt.Sprintf("resource:%s", req["tool_name"]),
			"secrecy":     []string{},  // empty = public
			"integrity":   []string{},  // empty = no endorsement
		},
		"operation": "read",
	}
	
	// Example: add repo-scoped contributor tag for write operations
	if req["tool_name"] == "create_issue" && owner != "" && repo != "" {
		output["resource"].(map[string]interface{})["integrity"] = []string{
			"contributor:" + owner + "/" + repo,
		}
		output["operation"] = "write"
	}
	
	// Write output
	outputJSON, _ := json.Marshal(output)
	copy(readBytes(outputPtr, uint32(len(outputJSON))), outputJSON)
	return int32(len(outputJSON))
}

//export label_response
func labelResponse(inputPtr, inputLen, outputPtr, outputSize uint32) int32 {
	return 0 // No fine-grained labeling
}

func readBytes(ptr, length uint32) []byte {
	return unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), length)
}

func main() {}
EOF

# Create Makefile
cat > Makefile << 'EOF'
.PHONY: build clean

GO123 := $(HOME)/go/bin/go1.23.4

build:
	@echo "Building WASM guard with TinyGo + Go 1.23.4..."
	@if [ -x "$(GO123)" ]; then \
		export GOROOT=$$($(GO123) env GOROOT) && \
		tinygo build -o guard.wasm -target=wasi main.go && \
		echo "✓ Built guard.wasm"; \
	else \
		echo "Error: Go 1.23.4 required."; \
		echo "Install: go install golang.org/dl/go1.23.4@latest && ~/go/bin/go1.23.4 download"; \
		exit 1; \
	fi

clean:
	rm -f guard.wasm
EOF

# Create README
cat > README.md << 'EOF'
# My DIFC Guard

Custom DIFC guard for MCP Gateway.

## Build

Requires:
- Go 1.23.4: `go install golang.org/dl/go1.23.4@latest && ~/go/bin/go1.23.4 download`
- TinyGo 0.34+: https://tinygo.org/getting-started/install/

Build: `make build`
EOF
```

### Step 3: Build Guard

```bash
# Install Go 1.23.4 (if not already installed)
go install golang.org/dl/go1.23.4@latest
~/go/bin/go1.23.4 download

# Verify Go 1.23.4 is installed
~/go/bin/go1.23.4 version  # Should show go1.23.4

# Install TinyGo (if not already installed)
# macOS: brew tap tinygo-org/tools && brew install tinygo
# Linux: See https://tinygo.org/getting-started/install/

# Build the guard
make build
# Creates: guard.wasm
```

### Step 4: Verify Guard

```bash
# Check the WASM file
file guard.wasm
# Should show: guard.wasm: WebAssembly (wasm) binary module version 0x1 (MVP)

# Check size (should be reasonable, typically < 5MB)
ls -lh guard.wasm
```

### Step 5: Create GitHub Repository and Release

```bash
# Initialize git (if not already done)
git init
git add .
git commit -m "Initial guard implementation"

# Create GitHub repository
gh repo create my-org/my-difc-guard --private --source=. --push

# Create a release with the WASM file
git tag v1.0.0
git push origin v1.0.0
gh release create v1.0.0 guard.wasm \
  --title "v1.0.0" \
  --notes "Initial release of DIFC guard"
```

### Step 6: Configure Gateway to Use External Guard

Update your gateway configuration to reference the guard:

**Option A: Local file** (for development):
```toml
[servers.github]
container = "ghcr.io/github/github-mcp-server"
guard = "myguard"

[guards.myguard]
type = "wasm"
path = "/path/to/local/guard.wasm"
```

**Option B: GitHub Release URL** (for production):
```toml
[servers.github]
container = "ghcr.io/github/github-mcp-server"
guard = "myguard"

[guards.myguard]
type = "wasm"
url = "https://github.com/my-org/my-difc-guard/releases/download/v1.0.0/guard.wasm"
sha256 = "abc123..."  # Required for URL-based loading
cache_dir = "/var/cache/mcp-guards"  # Optional, defaults to system temp
```

**JSON Configuration** (for stdin):
```json
{
  "guards": {
    "myguard": {
      "type": "wasm",
      "url": "https://github.com/my-org/my-difc-guard/releases/download/v1.0.0/guard.wasm",
      "sha256": "abc123...",
      "cacheDir": "/var/cache/mcp-guards"
    }
  }
}
```

**Private Repository Access**: Set the `GITHUB_TOKEN` environment variable to download guards from private GitHub repositories.

## Security Best Practices

### 1. Verify WASM Integrity

Always verify downloaded WASM modules:

```bash
# Generate checksum when building
sha256sum guard.wasm > guard.wasm.sha256

# Include checksum in release notes
gh release create v1.0.0 guard.wasm guard.wasm.sha256 \
  --title "v1.0.0" \
  --notes "SHA256: $(cat guard.wasm.sha256)"

# Verify before loading (in deployment scripts)
echo "expected_sha256  guard.wasm" | sha256sum -c -
```

## Host Functions Available to Guards

WASM guards can import host functions from the `env` module to interact with the gateway:

### call_backend

Allows guards to make read-only calls to backend MCP servers for gathering metadata:

```go
//go:wasmimport env call_backend
func callBackend(toolNamePtr, toolNameLen, argsPtr, argsLen, resultPtr, resultSize uint32) int32
```

### host_log

Allows guards to send log messages back to the gateway host for debugging and monitoring:

```go
//go:wasmimport env host_log
func hostLog(level, msgPtr, msgLen uint32)

// Log levels: 0=debug, 1=info, 2=warn, 3=error
```

**Using the guardsdk for logging** (recommended):

```go
import sdk "github.com/githubnext/gh-aw-mcpg/examples/guards/guardsdk"

func labelResource(req *sdk.LabelResourceRequest) (*sdk.LabelResourceResponse, error) {
    // Log at different levels
    sdk.LogDebug("Processing tool: " + req.ToolName)
    sdk.LogInfo("Starting resource labeling")
    sdk.LogWarn("Fallback to default labels")
    sdk.LogError("Critical error occurred")
    
    // Formatted logging
    sdk.Logf(sdk.LogLevelInfo, "Processing %s with %d args", req.ToolName, len(req.ToolArgs))
    
    // ... rest of labeling logic
}
```

**Without guardsdk** (direct host function use):

```go
import "unsafe"

//go:wasmimport env host_log
func hostLog(level, msgPtr, msgLen uint32)

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
```

Log messages from guards appear in gateway debug output (with `DEBUG=guard:*` environment variable) and are prefixed with the guard name for easy identification.

### 2. Use Private Repositories

For sensitive guard logic:
```bash
# Create private repository
gh repo create my-org/my-difc-guard --private --source=. --push

# Private releases require authentication
# Set GITHUB_TOKEN in gateway environment
export GITHUB_TOKEN="ghp_..."
```

### 3. Sign Releases

Use GPG to sign releases:
```bash
# Sign the WASM file
gpg --detach-sign --armor guard.wasm

# Include signature in release
gh release create v1.0.0 guard.wasm guard.wasm.asc \
  --title "v1.0.0 (signed)" \
  --notes "GPG signed release"
```

### 4. Audit Guard Code

Before using external guards:
- Review source code
- Verify build reproducibility
- Test in isolated environment
- Monitor guard behavior

## Development Workflow

### Iterative Development

```bash
# 1. Make changes to guard logic
vi main.go

# 2. Build and test locally
make build
# Test with local gateway configuration

# 3. Commit and create new release
git add main.go
git commit -m "Update guard logic"
git push
git tag v1.0.1
git push origin v1.0.1
gh release create v1.0.1 guard.wasm --title "v1.0.1"

# 4. Update gateway configuration to new version
# Change url to: .../releases/download/v1.0.1/guard.wasm
```

### Testing Guards

```bash
# Test guard locally before releasing
cd /path/to/gateway
cat > test-config.toml << EOF
[servers.testserver]
container = "test-mcp-server"
guard = "testguard"

[guards.testguard]
type = "wasm"
path = "/path/to/your/guard.wasm"
EOF

# Run gateway with test config
./awmg --config test-config.toml
```

## Example: Complete Guard Repository

See the sample guard in the main repository:
```bash
# View the complete example
git clone https://github.com/githubnext/gh-aw-mcpg
cd gh-aw-mcpg/examples/guards/sample-guard
cat main.go  # Review guard implementation
cat Makefile # Review build process
make build   # Build the guard
```

## Troubleshooting

### Build fails with "requires go version 1.19 through 1.23"
**Solution**: Install Go 1.23.4 specifically for guard compilation:
```bash
go install golang.org/dl/go1.23.4@latest
~/go/bin/go1.23.4 download
```

### TinyGo not found
**Solution**: Install TinyGo from https://tinygo.org/getting-started/install/

### Guard doesn't export functions
**Problem**: Compiled with standard Go instead of TinyGo
**Solution**: Ensure TinyGo is in PATH and Makefile uses it

### "failed to read WASM file"
**Solution**: Check file path in configuration is absolute or relative to gateway working directory

## Resources

- TinyGo documentation: https://tinygo.org/docs/
- WASI specification: https://wasi.dev/
- WebAssembly documentation: https://webassembly.org/
- GitHub Releases API: https://docs.github.com/en/rest/releases
