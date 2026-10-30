# GitHub Guard Implementation

This document provides a complete overview of the GitHub Guard implementation for the MCP Gateway.

## What Has Been Implemented

### ✅ Core Guard Implementation (rust-guard/src/)

A complete DIFC (Decentralized Information Flow Control) guard that:

1. **Classifies Operations**: Categorizes GitHub MCP tools as read, write, or read-write
2. **Assigns Labels**: Applies integrity and secrecy labels based on:
   - Operation type
   - Repository visibility
   - Author contribution history
   - Resource sensitivity
3. **Enforces DIFC**: Implements the security hierarchy:
    - Integrity: `merged > approved > unapproved > none`
   - Secrecy: `secret > private > public`

**Key Features:**
- Fine-grained per-item labeling
- Contributor verification via backend calls
- Bot account detection
- Sensitive content detection

### ✅ Module Structure

```
rust-guard/src/
├── lib.rs         # Main entry point, WASM exports, memory management
├── labels/        # DIFC label generation and response labeling
│   ├── mod.rs
│   ├── tool_rules.rs
│   ├── response_items.rs
│   └── backend.rs
├── tools.rs       # Tool classification (read/write/merge/delete)
└── permissions.rs # Permission level helpers and utilities
```

### ✅ WASM Exports

The guard exports these functions for the MCP Gateway:

| Function | Purpose |
|----------|---------|
| `label_resource` | Label a resource before access |
| `label_response` | Label response data (fine-grained) |
| `alloc` | Allocate memory for host |
| `dealloc` | Free allocated memory |

### ✅ Host Imports

The guard imports these functions from the host:

| Function | Purpose |
|----------|---------|
| `call_backend` | Call the MCP server |
| `host_log` | Log messages to gateway |

## Implementation Details

### DIFC Label Assignment

The guard implements a principled labeling scheme:

**Secrecy Labels:**
- Public repos: `[]` (empty)
- Private repos: `["private:owner/repo"]`
- Job logs: inherit repository visibility (`[]` for public repos, `["private:owner/repo"]` for private repos)
- Sensitive resources (secret scanning alerts, workflow files, artifacts): `["private:owner/repo"]` (always, even for public repos)
- User data: `["private:user"]`

**Integrity Labels:**
- Merged level: `["unapproved:X", "approved:X", "merged:X"]`
- Writer level: `["unapproved:X", "approved:X"]`
- Reader level: `["unapproved:X"]`
- Untrusted: `[]` (empty)

Job-log content receives explicit baseline `none` integrity, including in the singleton
response fallback. Plain-text or search-shaped log output is not approved server metadata.

### Response Labeling

Fine-grained labeling of collection responses:

```rust
pub fn label_response_items(
    tool_name: &str,
    tool_args: &Value,
    response: &Value,
) -> Vec<LabeledItem> {
    // Parse response and label each item
}
```

Supported tools:
- `search_repositories`: Labels by private/public
- `list_pull_requests`: Labels by merged state
- `list_issues`: Labels by author trust status
- `list_commits`: Labels by branch
- `list_releases`: Writer-level integrity (with unapproved floor)
- `list_gists`: Reader-level integrity
- `list_notifications`: Private secrecy

### Contributor Verification

The guard verifies contributor status via backend:

```rust
pub fn is_verified_contributor(username: &str, owner: &str, repo: &str) -> bool {
    count_merged_prs(username, owner, repo).unwrap_or(0) > 0
}
```

Uses `search_pull_requests` with query: `author:X repo:Y is:merged`


## Project Structure

```
github-guard/
├── rust-guard/
│   ├── src/
│   │   ├── lib.rs         # WASM exports
│   │   ├── labels/        # Label generation modules
│   │   ├── tools.rs       # Tool classification
│   │   └── permissions.rs # Permission helpers
│   ├── Cargo.toml
│   └── build.sh
├── docs/
│   ├── README.md          # Main documentation
│   ├── LABELING.md        # Labeling specification
│   ├── QUICKSTART.md      # Quick start guide
│   ├── TESTING.md         # Testing guide
│   └── ...
├── scripts/
│   ├── run_copilot_test.sh
│   └── run_integration_tests.sh
├── Makefile
├── config.example.json
├── LICENSE
└── README.md
```

## Build Infrastructure

### build.sh

Automated build script:

1. Selects rustup toolchain with WASI support
2. Builds with release optimizations
3. Copies WASM to project root

```bash
./build.sh           # Release build
./build.sh debug     # Debug build
```

### Makefile

Build and test automation:

```bash
make build           # Build WASM
make test            # Run tests
make test-copilot    # Run with Copilot
make clean           # Clean artifacts
```

## Dependencies

**Cargo.toml:**
```toml
[dependencies]
serde = { version = "1.0", features = ["derive"] }
serde_json = "1.0"
```

No runtime dependencies - pure WASM.

## Testing

### Unit Tests

```bash
cd rust-guard && cargo test
```

Tests cover:
- Permission level parsing
- Contributor/maintainer detection
- Bot account identification

### Integration Tests

```bash
make test-integration
```

Requires Docker containers:
- `ghcr.io/lpcox/github-guard:latest`
- `ghcr.io/github/github-mcp-server:latest`

### Copilot Tests

```bash
make test-copilot
```

End-to-end testing with GitHub Copilot CLI.

## Documentation

| File | Purpose |
|------|---------|
| [README.md](../README.md) | Project overview |
| [docs/OVERVIEW.md](OVERVIEW.md) | Detailed documentation |
| [docs/LABELING.md](LABELING.md) | Labeling specification |
| [docs/QUICKSTART.md](QUICKSTART.md) | Quick start guide |
| [docs/TESTING.md](TESTING.md) | Testing guide |

## License

MIT License - see [LICENSE](../LICENSE)
