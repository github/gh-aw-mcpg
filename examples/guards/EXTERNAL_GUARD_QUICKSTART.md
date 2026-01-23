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
	
	// Create response
	output := map[string]interface{}{
		"resource": map[string]interface{}{
			"description": fmt.Sprintf("resource:%s", req["tool_name"]),
			"secrecy":     []string{"public"},
			"integrity":   []string{"untrusted"},
		},
		"operation": "read",
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

build:
	@echo "Building WASM guard with TinyGo + Go 1.23..."
	@for go_bin in go1.23 go1.23.9 go1.23.10; do \
		if command -v $$go_bin >/dev/null 2>&1; then \
			GOROOT=$$($$go_bin env GOROOT) tinygo build -o guard.wasm -target=wasi main.go && \
			echo "✓ Built with $$go_bin" && exit 0; \
		fi; \
	done; \
	echo "Error: Go 1.23 required. Install: go install golang.org/dl/go1.23.9@latest && go1.23.9 download"

clean:
	rm -f guard.wasm
EOF

# Create README
cat > README.md << 'EOF'
# My DIFC Guard

Custom DIFC guard for MCP Gateway.

## Build

Requires:
- Go 1.23: `go install golang.org/dl/go1.23.9@latest && go1.23.9 download`
- TinyGo 0.34+: https://tinygo.org

Build: `make build`
EOF
```

### Step 3: Build Guard

```bash
# Install Go 1.23 (if not already installed)
go install golang.org/dl/go1.23.9@latest
go1.23.9 download

# Install TinyGo (if not already installed)
# See: https://tinygo.org/getting-started/install/

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
sha256 = "abc123..."  # Optional but recommended for security
```

**Note**: The `url` field is not yet implemented in the current framework. See "Future Enhancement" section below.

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

## Future Enhancement: URL Loading

The framework currently supports local `path` but not remote `url` loading. To add URL support:

**Proposed configuration**:
```toml
[guards.myguard]
type = "wasm"
url = "https://github.com/my-org/my-difc-guard/releases/download/v1.0.0/guard.wasm"
sha256 = "expected_checksum"  # Required for URL loading
cache_dir = "/var/cache/mcp-guards"  # Optional cache location
```

**Implementation would include**:
1. HTTP client to download WASM from URL
2. SHA256 verification (required for security)
3. Local caching to avoid repeated downloads
4. Support for GitHub authentication (`GITHUB_TOKEN` env var)
5. Retry logic for network failures

**Workaround until implemented**:
```bash
# Download guard in deployment script
wget https://github.com/my-org/my-difc-guard/releases/download/v1.0.0/guard.wasm \
  -O /var/lib/mcp-guards/myguard.wasm

# Verify checksum
echo "expected_sha256  /var/lib/mcp-guards/myguard.wasm" | sha256sum -c -

# Reference local path in config
# path = "/var/lib/mcp-guards/myguard.wasm"
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
**Solution**: Install Go 1.23 specifically for guard compilation:
```bash
go install golang.org/dl/go1.23.9@latest
go1.23.9 download
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
