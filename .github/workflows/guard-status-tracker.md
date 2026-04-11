---
name: Guard Status Tracker
description: Daily update of the Guards and Integrity tracking issue (#1711) based on current codebase state
on:
  schedule:
    - cron: "0 8 * * 1-5"  # Weekdays at 8 AM UTC
  workflow_dispatch:

permissions:
  contents: read
  issues: read
  pull-requests: read

engine: copilot

network:
  allowed:
    - defaults
    - github
    - containers

imports:
  - shared/reporting.md

safe-outputs:
  threat-detection:
    enabled: false
  add-comment:
    max: 1
  noop:

tools:
  cache-memory: true
  github:
    toolsets: [default, pull_requests]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved
  bash:
    - "*"

timeout-minutes: 30
strict: true
---

# 🛡️ Guard Status Tracker

You maintain the **Guards and Integrity tracking issue** ([#1711](https://github.com/github/gh-aw-mcpg/issues/1711)) with an up-to-date snapshot of the codebase. Collaborators rely on this issue to understand the current state of DIFC guards and integrity enforcement.

## Context

- **Repository**: ${{ github.repository }}
- **Tracking Issue**: #1711
- **Run ID**: ${{ github.run_id }}

## Your Mission

Each run you will:

1. Load your previous status from cache
2. Scan the codebase for current guard/integrity state
3. Scan recent PRs for guard-related changes
4. Compare with previous state to identify what changed
5. Post a concise status update comment on issue #1711

## Step 1: Load Previous State from Cache

Use cache-memory to check:
- `last_status_date`: Date of last status update
- `last_summary`: Brief summary from last run (for change detection)
- `known_pr_numbers`: List of PRs already reported

## Step 2: Scan the Codebase

Analyze the current state of the guard implementation by reading key files.

### 2.1 Inventory Source Files

```bash
echo "=== DIFC Engine ===" && find internal/difc -name '*.go' ! -name '*_test.go' | sort | xargs wc -l
echo "=== Guard Framework ===" && find internal/guard -name '*.go' ! -name '*_test.go' | sort | xargs wc -l
echo "=== Guard Config ===" && wc -l internal/config/guard_policy.go
echo "=== Rust Guard ===" && find guards/github-guard/rust-guard/src -name '*.rs' | sort | xargs wc -l
echo "=== Test Files ===" && find internal/difc internal/guard -name '*_test.go' | xargs wc -l
```

### 2.2 Check Guard Policy Configuration

```bash
grep -n 'func Validate' internal/config/guard_policy.go | head -20
```

### 2.3 Check WASM Guard Status

```bash
ls -la guards/github-guard/rust-guard/target/wasm32-wasip1/release/*.wasm 2>/dev/null || echo "No WASM binary found"
ls -la internal/guard/*.wasm 2>/dev/null || echo "No embedded WASM found"
```

### 2.4 Check CLI Flags

```bash
grep -n 'guards\|guard-policy\|enable-guards\|guards-mode' internal/cmd/root.go | head -20
```

### 2.5 Read Key Implementation Files

Read these files to understand the current architecture:
- `internal/difc/evaluator.go` — DIFC flow evaluation logic
- `internal/guard/wasm.go` — WASM guard runtime
- `internal/config/guard_policy.go` — AllowOnly policy model
- `guards/github-guard/rust-guard/src/labels/mod.rs` — Rust label_agent/label_resource/label_response

## Step 3: Scan Recent PRs

Use GitHub tools to find guard-related PRs from the last 14 days:

Search for PRs mentioning guards, DIFC, integrity, secrecy, allow-only, or write-sink:
- Use `list_pull_requests` to get recent merged and open PRs
- Filter for those touching guard-related files or mentioning guard keywords
- Note: Check titles and changed files — look for paths matching `internal/difc/`, `internal/guard/`, `guards/`, `guard_policy`

## Step 4: Compare with Previous State

Compare current findings with `last_summary` from cache:
- Count changes in source lines of code
- Identify new/merged PRs since last report
- Note any new files, removed files, or significant refactorings
- Check if any Next Steps from the tracking issue have been completed

## Step 5: Post Status Update

Add a comment to issue #1711 using `add-comment` with `item_number: 1711`.

### Status Comment Format

```markdown
## 🛡️ Guard Status Update — <DATE>

### Codebase Snapshot

| Component | Files | Lines | Tests |
|-----------|-------|-------|-------|
| DIFC Engine (`internal/difc/`) | <count> | <count> | <count> |
| Guard Framework (`internal/guard/`) | <count> | <count> | <count> |
| Guard Config (`guard_policy.go`) | 1 | <count> | — |
| Rust GitHub Guard (`rust-guard/src/`) | <count> | <count> | — |
| **Total** | **<count>** | **<count>** | **<count>** |

### Recent Changes (since <last_date>)

<List merged PRs and notable changes. If no changes, say "No guard-related changes since last update.">

### Current Architecture
- **Enforcement modes**: strict, filter, propagate
- **Guard types**: WasmGuard (GitHub), WriteSinkGuard, NoopGuard
- **Policy model**: AllowOnly with repos scope + min-integrity
- **Integrity hierarchy**: `none < unapproved < approved < merged`
- **WASM binary**: <size> (wasm32-wasip1)

### Open Items
<Check the "Next Steps" section of issue #1711 and report which are done vs still pending>

---
*Auto-generated by Guard Status Tracker • [Run ${{ github.run_id }}](${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }})*
```

### Comment Rules

- **Be concise**: The comment should fit on one screen. Use the table for numbers, bullet points for changes.
- **Only report real changes**: If nothing changed since the last update, still post a brief "No changes" status with updated line counts.
- **Link PRs**: Reference PRs by number (e.g., #1234) so they're clickable.
- **Use the exact `item_number: 1711`** parameter when calling `add-comment`.

## Step 6: Update Cache

Save to cache-memory:
- `last_status_date`: Today's date (ISO 8601)
- `last_summary`: Brief one-liner of what was found (e.g., "14,300 LOC across 4 components, 2 PRs merged since last update")
- `known_pr_numbers`: Updated list of all reported PR numbers

## Guidelines

- **Accuracy over speculation**: Only report what you can verify from the code.
- **Focus on deltas**: Collaborators care most about what changed since the last update.
- **Keep it scannable**: Tables and bullet points, not paragraphs.
- **Respect the tracking issue**: Don't duplicate the full issue body — just provide a current snapshot and delta.