# Implementation Summary

## Overview

The GitHub Guard is a Rust-based WASM module that implements DIFC (Decentralized Information Flow Control) for GitHub MCP servers. It labels all GitHub API responses with secrecy and integrity tags.

## Architecture

### WASM Module

The guard is compiled to WebAssembly (WASI target) and loaded by the MCP Gateway:

```
rust-guard/
├── src/
│   ├── lib.rs         # Main entry point, WASM exports
│   ├── labels/        # DIFC label generation modules
│   │   ├── mod.rs
│   │   ├── tool_rules.rs
│   │   ├── response_items.rs
│   │   └── backend.rs
│   ├── tools.rs       # Tool classification
│   └── permissions.rs # Permission helpers
├── Cargo.toml
└── build.sh
```

**Output**: `github-guard-rust.wasm` (~170KB)

### Exported Functions

#### `label_resource`
Called before each tool invocation to determine access labels.

**Input**: `{"tool_name": "...", "tool_args": {...}}`
**Output**: `{"resource": {...}, "operation": "read|write|read-write"}`

#### `label_response`
Called after each tool invocation to label response data.

**Input**: `{"tool_name": "...", "tool_args": {...}, "tool_result": ...}`
**Output**: `{"items": [{"data": ..., "labels": {...}}, ...]}`

### Memory Management

The WASM module exports `alloc` and `dealloc` for the host to manage memory:

```rust
#[no_mangle]
pub extern "C" fn alloc(size: u32) -> u32 { ... }

#[no_mangle]
pub extern "C" fn dealloc(ptr: u32, size: u32) { ... }
```

## Labeling Implementation

### Secrecy Labels

| Condition | Secrecy Tag |
|-----------|-------------|
| Public repository | `[]` (empty) |
| Private repository | `["private:owner/repo"]` |
| Secret scanning alerts | `["private:owner/repo"]` (always, even for public repos) |
| Job logs | Repository visibility: `[]` for public repos, `["private:owner/repo"]` for private repos |
| Sensitive/credential files | `["private:owner/repo"]` (always, even for public repos) |
| Artifact downloads | `["private:owner/repo"]` (always, even for public repos) |
| User notifications | `["private:user"]` |

### Integrity Labels

| Object Type | Condition | Integrity |
|-------------|-----------|-----------|
| Repository metadata | Always | `["unapproved:X", "approved:X"]` |
| Job-log content | No content-level endorsement, including private repos | Baseline `none:X` |
| Merged PRs | `merged = true` | `["unapproved:X", "approved:X", "merged:X"]` |
| Open PRs | `merged = false` | `["unapproved:X"]` (or elevated by policy evidence) |
| Default branch commits | `sha` is main/master | `["unapproved:X", "approved:X", "merged:X"]` |
| Feature branch commits | Other branches | `["unapproved:X"]` (or elevated by policy evidence) |
| Issues (verified author) | Author has merged PRs | `["unapproved:X"]` |
| Issues (unknown author) | No merged PRs | `[]` |
| Bot content | Detected bot | `["unapproved:X", "approved:X"]` |

## Backend Calls

The guard can call the MCP server to verify contributor status:

```rust
pub fn count_merged_prs(username: &str, owner: &str, repo: &str) -> Option<u32> {
    let query = format!("author:{} repo:{}/{} is:merged", username, owner, repo);
    // Call search_pull_requests via backend
}
```

## Tool Classification

Tools are classified as read, write, or read-write operations:

### Write Operations (require integrity)
- `create_*`: create_issue, create_pull_request, etc.
- `add_*`: add_issue_comment, add_pull_request_review_comment
- `run_*`: run_workflow
- `push_files`

### Delete Operations (require merged-level integrity)
- `delete_file`
- `delete_project_item`
- `delete_workflow_run_logs`

### Merge Operations (require merged-level integrity)
- `merge_pull_request`

### Read Operations (no integrity required)
- `list_*`, `get_*`, `search_*`

## Response Labeling

Fine-grained per-item labeling for collection responses:

### search_repositories
- Labels each repo with secrecy based on `private` field
- Adds approved-level integrity (with unapproved floor) to all repos (metadata is endorsed)

### list_pull_requests
- Merged PRs: merged-level integrity
- Open/closed PRs: unapproved-level integrity (or elevated by policy evidence)
- Bot PRs: approved-level integrity (with unapproved floor)

### list_issues
- Bot authors: approved-level integrity (with unapproved floor)
- Owner authors: approved-level integrity (with unapproved floor)
- Verified contributors: unapproved-level integrity
- Unknown authors: no integrity (untrusted)

### list_commits
- Default branch: merged-level integrity
- Feature branches: unapproved-level integrity (or elevated by policy evidence)

## Build Process

```bash
# Build script handles toolchain setup
cd rust-guard && ./build.sh

# Or via Makefile
make build
```

The build script:
1. Selects rustup toolchain with WASI support
2. Builds with release optimizations
3. Copies WASM to project root

## Testing

```bash
# Unit tests
make test

# Integration tests (requires Docker)
make test-integration

# Copilot CLI tests
make test-copilot
```

## Dependencies

- **serde**: JSON serialization
- **serde_json**: JSON parsing

No runtime dependencies - the WASM module is self-contained.
