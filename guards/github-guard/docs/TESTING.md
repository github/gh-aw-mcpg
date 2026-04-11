# GitHub Guard Testing Guide

This document describes the testing strategy and provides instructions for running and writing tests for the GitHub Guard.

## Test Suite Overview

The GitHub Guard includes tests covering:

- ✅ **Permission Level Parsing**: Verify permission string parsing
- ✅ **Permission Level Checks**: Test writer/maintainer detection
- ✅ **Bot Account Detection**: Test bot username identification
- ✅ **WASM Build Verification**: Ensure WASM compiles correctly

## Running Tests with Makefile

The project includes a Makefile for easy test execution:

```bash
# Run default pipeline (build, unit, wasm, integration, integrity)
make test

# Run default pipeline + Copilot test
make test-all

# Run unit tests only
make test-unit

# Verify WASM build
make test-wasm

# Run integration tests (requires Docker + .env)
make test-integration

# Run corpus-driven WASM integrity harness tests
make test-integrity-harness

# Refresh integrity corpus from real open-source data
make capture-integrity-corpus

# Run with Copilot CLI (default: yolo mode)
make test-copilot

# Run with Copilot CLI in different security modes
make test-copilot-yolo           # No protection (development)
make test-copilot-all            # AllowOnly repos=all
make test-copilot-public-only    # Public-safe filtering behavior
make test-copilot-owner-only     # Owner-scoped policy behavior
make test-copilot-repo-only      # Repo-scoped policy behavior
make test-copilot-prefix-only    # Repo-prefix policy behavior
make test-copilot-multi-only     # Multi-entry scope behavior
make test-copilot-lockdown       # GitHub MCP lockdown mode

# Show all available targets
make help
```

### Test Targets Quick Reference

| Target | Description |
|---|---|
| `make test` | Default pipeline: `build + test-unit + test-wasm + test-integration + test-integrity-harness` |
| `make test-all` | Default pipeline plus `test-copilot` |
| `make test-unit` | Rust unit tests (`cargo test --lib`) |
| `make test-wasm` | WASM build verification |
| `make test-integration` | Integration tests using gateway + MCP containers |
| `make test-integrity-harness` | Corpus-driven WASM integrity harness |
| `make capture-integrity-corpus` | Refresh integrity corpus fixture from live OSS repos |
| `make test-copilot` | Copilot test runner (default yolo mode) |
| `make test-copilot-yolo` | No guard / no DIFC |
| `make test-copilot-all` | AllowOnly `repos=all` policy mode |
| `make test-copilot-public-only` | Public-only AllowOnly mode |
| `make test-copilot-owner-only` | Owner-scoped AllowOnly mode |
| `make test-copilot-repo-only` | Repo-scoped AllowOnly mode |
| `make test-copilot-prefix-only` | Repo-prefix AllowOnly mode |
| `make test-copilot-multi-only` | Multi-entry AllowOnly scope mode |
| `make test-copilot-lockdown` | Lockdown mode (`--lockdown-mode`) |

## Security Modes

The runner supports four documented modes for Copilot testing:

## BYOK and Offline Mode

The runner supports BYOK (Bring Your Own Key) to route Copilot's LLM calls
through a custom provider (local model, Azure OpenAI, Anthropic, etc.) instead
of GitHub's model API. When a provider URL is configured, `COPILOT_OFFLINE=true`
is set automatically so Copilot does not phone home to GitHub's model backend.

The GitHub MCP server still uses `GITHUB_TOKEN` for API access, so that token
remains required even in offline mode.

### Configuring BYOK in `.env`

Add your provider settings to `.env` alongside `GITHUB_TOKEN`:

```bash
# Required for GitHub MCP server
GITHUB_TOKEN=ghp_your_token_here

# BYOK provider (activates offline mode automatically)
COPILOT_PROVIDER_BASE_URL=http://localhost:11434   # e.g. Ollama
COPILOT_MODEL=llama3.2

# Optional: provider type and API key
# COPILOT_PROVIDER_TYPE=openai   # openai | azure | anthropic
# COPILOT_PROVIDER_API_KEY=sk-...
```

Variables already exported in your shell take precedence over `.env` values.

### Supported providers

| Provider | `COPILOT_PROVIDER_BASE_URL` | `COPILOT_PROVIDER_TYPE` |
|---|---|---|
| Ollama (local) | `http://localhost:11434` | `openai` (default) |
| vLLM (local)   | `http://localhost:8000`  | `openai` |
| Azure OpenAI   | `https://<resource>.openai.azure.com/openai/deployments/<deployment>` | `azure` |
| Anthropic      | `https://api.anthropic.com` | `anthropic` |
| Any OpenAI-compatible | custom URL | `openai` |

When `COPILOT_PROVIDER_BASE_URL` is set the `gh auth login` prerequisite is
skipped, since GitHub authentication is not required for the LLM side.


1. **YOLO Mode** (`make test-copilot-yolo`)
   - No guard, no DIFC enforcement
   - Use for development and debugging

2. **Public-Only Mode** (`make test-copilot-public-only`)
  - Runs DIFC in filter mode with `{"allow-only":{"repos":"public","min-integrity": "none"}}`
  - Uses `allow-only.min-integrity=none`
  - Intended to filter/block private data exposure
  - Use for public-safe testing

3. **Owner-Only Mode** (`make test-copilot-owner-only`)
  - Runs DIFC in filter mode
  - Uses `{"allow-only":{"repos":["<allow_owner>/*"],"min-integrity": "none"}}`
  - Use for owner-scoped validation

4. **Repo-Only Mode** (`make test-copilot-repo-only`)
  - Runs DIFC in filter mode
  - Uses `{"allow-only":{"repos":["<owner>/<repo>"],"min-integrity": "none"}}`
  - Use for repo-scoped validation against one repository

See [docs/OVERVIEW.md](./OVERVIEW.md#operating-modes) for detailed mode descriptions.

## Running Tests Directly

### Run Rust Tests Directly

```bash
cd rust-guard && cargo test
```

Expected output:
```
running 3 tests
test permissions::tests::test_bot_detection ... ok
test permissions::tests::test_permission_level_checks ... ok
test permissions::tests::test_permission_level_parsing ... ok

test result: ok. 3 passed; 0 failed; 0 ignored
```

### Run Tests with Verbose Output

```bash
cd rust-guard && cargo test -- --nocapture
```

### Run Specific Tests

```bash
# Test permission parsing
cd rust-guard && cargo test test_permission_level_parsing

# Test bot detection
cd rust-guard && cargo test test_bot_detection
```

## Log Files and Storage Locations

### Copilot runner logs (`make test-copilot-*`)

- Copilot CLI logs: `/tmp/copilot/logs/` (for example `process-*.log`)
- Gateway container output during run: `/tmp/copilot/gateway.log`
- Gateway log copied to repository root on runner cleanup: `./gateway.log`

### Integration runner logs (`make test-integration`)

- Gateway container output during run: `/tmp/gateway.log`
- Gateway log copied to repository root after run: `./gateway.log`

### In-container gateway internal logs

The gateway itself writes internal logs to `/tmp/gh-aw/mcp-logs` inside the container.
These are not persisted to the host unless that path is mounted from the host.

## Integration Tests

Integration tests run the guard with actual MCP Gateway and GitHub MCP server containers.

### Prerequisites

1. **Docker** must be running
2. **GitHub Personal Access Token (PAT)** with appropriate scopes
3. **GitHub Container Registry (ghcr.io) authentication**

### GitHub Token Setup

Create a GitHub Personal Access Token at https://github.com/settings/tokens with these scopes:
- `repo` - Repository access for MCP server operations
- `read:org` - Organization read access
- `read:user` - User profile read access
- `read:packages` - Required for pulling container images from ghcr.io

```bash
# Create .env file with your GitHub token (NEVER commit this!)
echo 'GITHUB_TOKEN=ghp_your_token_here' > .env

# The .env file is already in .gitignore
```

### GitHub Container Registry Authentication

```bash
# Using GitHub CLI (recommended)
gh auth token | docker login ghcr.io -u $(gh api user -q .login) --password-stdin

# Verify authentication
docker pull ghcr.io/lpcox/github-guard:latest
```

### Local Gateway Build Testing

When validating current gateway behavior, prefer a locally built gateway image over `ghcr.io`.
The local image reflects your latest code and avoids lag from published tags.

Build the local gateway image from `gh-aw-mcpg` branch `lpcox/github-difc`:

```bash
# Match CROSS_REPO_RELEASE.md source repository/branch
git clone https://github.com/github/gh-aw-mcpg.git
cd gh-aw-mcpg
git fetch origin lpcox/github-difc
git checkout lpcox/github-difc

# Build local image used by integration tests
docker build -t local/gh-aw-mcpg:latest .
```

Use `github/gh-aw-mcpg` with the same branch/tagging flow.

```bash
# Run integration tests against the local image
GATEWAY_IMAGE=local/gh-aw-mcpg:latest make test-integration
```

### Running Integration Tests

```bash
# Run integration tests pinned to your local gateway build
GATEWAY_IMAGE=local/gh-aw-mcpg:latest make test-integration
```

### What Integration Tests Cover

- Gateway startup with guard loaded
- MCP session initialization
- Tool listing through the gateway
- Read operations (`get_me`, `search_repositories`)

## Corpus-Driven Integrity Harness

The integrity harness executes the compiled WASM artifact and validates `label_agent`,
`label_resource`, and `label_response` against explicit ground truth.

- Harness runtime: Go + wazero (`src/integrity_harness_test.go`)
- Corpus fixture: `src/testdata/integrity/corpus_v1.json`
- Schema: `src/testdata/integrity/schema_v1.json`
- Backend behavior: replayed through mocked `env.call_backend` host import
- Operation matrix: auto-discovers operations from Rust source and exercises every implemented tool in `label_resource` across public/private repo visibility contexts while asserting operation class and integrity coverage.
- Strict visibility guard: fails when a discovered tool has no inferable visibility scenarios unless explicitly marked visibility-agnostic.

Run:

```bash
make test-integrity-harness
```

Refresh corpus from live OSS repos (`cli/cli`, `octocat/Hello-World`):

```bash
make capture-integrity-corpus
```

Notes:
- Requires `gh` and `jq` for corpus capture.
- Harness tests are deterministic and do not need live network access.

## Test Cases

### Important distinction: permission vs integrity floor

The tests in `rust-guard/src/permissions.rs` validate parsing and helper logic for repository
permission levels (`admin`, `maintain`, `write`, `triage`, `read`, etc.).

Integrity initialization for issue/PR content is driven by GitHub response field
`author_association` and is implemented in `rust-guard/src/labels/helpers.rs`
(`author_association_floor_from_str`).

These are related concepts but tested in different places.

### test_permission_level_parsing

Tests the permission level parsing from GitHub API strings.

**Test Cases:**
- `"admin"` → `PermissionLevel::Admin`
- `"maintain"` → `PermissionLevel::Maintain`
- `"write"` / `"push"` → `PermissionLevel::Write`
- `"read"` / `"pull"` → `PermissionLevel::Read`
- `"unknown"` → `PermissionLevel::None`

**Why these matter:**
- Ensures correct interpretation of GitHub permission strings
- Handles case-insensitive matching
- Maps legacy permission names (push/pull)

### test_permission_level_checks

Tests permission level methods.

**Test Cases:**
- `Admin.is_maintainer()` → `true`
- `Maintain.is_maintainer()` → `true`
- `Write.is_maintainer()` → `false`
- `Admin.is_contributor()` → `true`
- `Write.is_contributor()` → `true`
- `Read.is_contributor()` → `false`

**Why these matter:**
- Validates hierarchical permission model
- Ensures proper access control decisions

### author_association integrity initialization

For content labeling, the guard maps GitHub `author_association` values to an initial
integrity floor (case-insensitive) as follows:

- `OWNER` → approved
- `MEMBER` → approved
- `COLLABORATOR` → approved
- `CONTRIBUTOR` → unapproved
- `FIRST_TIME_CONTRIBUTOR` → unapproved
- `FIRST_TIMER` → none
- `NONE` → none
- missing/unknown value → none

In the implementation:
- approved floor uses `writer_integrity(scope)`
- unapproved floor uses `reader_integrity(scope)`
- none floor uses empty labels (`[]`)

After initialization, tool-specific logic can further elevate integrity (for example,
private repo defaults or merged/default-branch evidence).

### test_bot_detection

Tests bot account identification.

**Test Cases:**
- `"dependabot[bot]"` → detected as bot
- `"renovate"` → detected as bot
- `"github-actions"` → detected as bot
- `"octocat"` → not a bot
- `"user-bot-helper"` → detected as bot (ends with `-bot`)

**Why these matter:**
- Bots receive approved-level integrity (with unapproved floor)
- Prevents false negatives on bot detection
- Case-insensitive matching

## Writing New Tests

### Adding Tests in Rust

Tests are located in `rust-guard/src/` files, typically in a `#[cfg(test)]` module:

```rust
#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_new_feature() {
        let result = function_under_test("input");
        assert_eq!(result, "expected");
    }
}
```

### Testing Labeling Logic

When adding new labeling rules, add tests in the relevant file under `rust-guard/src/labels/`:

```rust
#[test]
fn test_new_labeling_rule() {
    let scope = "owner/repo";
    
    // Test approved-level integrity generation
    let labels = writer_integrity(scope);
    assert!(labels.contains(&"unapproved:owner/repo".to_string()));
    assert!(labels.contains(&"approved:owner/repo".to_string()));
}
```

## Test Limitations

### No Backend Calls in Unit Tests

Unit tests run without the MCP Gateway, so backend calls (like `count_merged_prs`) cannot be tested directly. To test backend integration:

- Use `make test-copilot` with a live gateway
- Run `make test-integration` with Docker
- Test manually in a real environment

### WASM Target

Unit tests run on the native target, not WASM. The WASM build is verified separately with `make test-wasm`.

## Continuous Integration

### GitHub Actions Example

Create `.github/workflows/test.yml`:

```yaml
name: Test

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Install Rust
        uses: actions-rs/toolchain@v1
        with:
          toolchain: stable
          target: wasm32-wasip1
          override: true
      
      - name: Run tests
        run: make test
      
      - name: Build WASM
        run: make build
```

## Troubleshooting

### Tests Fail to Compile

Ensure you have the correct Rust version:

```bash
rustc --version
# Should be 1.70+ for wasm32-wasip1 target
```

### WASM Build Fails

Add the WASI target:

```bash
rustup target add wasm32-wasip1
```

### Integration Tests Timeout

Check Docker is running and containers are accessible:

```bash
docker ps
docker pull ghcr.io/lpcox/github-guard:latest
```

## Coverage

To generate a coverage report (requires `cargo-tarpaulin`):

```bash
cargo install cargo-tarpaulin
cd rust-guard && cargo tarpaulin --out Html
open tarpaulin-report.html
```
