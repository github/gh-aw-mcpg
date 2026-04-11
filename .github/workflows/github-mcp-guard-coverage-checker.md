---
name: GitHub Guard Coverage Checker (MCP + CLI)
description: Daily check that audits the GitHub guard implementation against both the official GitHub MCP server tool list and the GitHub CLI write-command surface, creating issues for any unclassified or unguarded operations.
on:
  schedule: daily
  workflow_dispatch:

permissions:
  contents: read
  issues: read

engine: copilot

network:
  allowed:
    - defaults
    - github

imports:
  - shared/reporting.md

safe-outputs:
  threat-detection:
    enabled: false
  create-issue:
    title-prefix: "[guard-coverage] "
    labels: [guard, automation, security]
    max: 1
    expires: 14d
  noop:

tools:
  cache-memory: true
  github:
    toolsets: [default]
    allowed-repos: ["github/gh-aw-mcpg", "github/github-mcp-server", "cli/cli"]
    min-integrity: unapproved
  bash:
    - "*"

timeout-minutes: 30
strict: true
---

# 🔍 GitHub MCP Guard Coverage Checker

You are an AI security auditor that verifies the GitHub guard implementation covers all tools exposed by the official [GitHub MCP server](https://github.com/github/github-mcp-server) **and** all write/mutating operations reachable via the [GitHub CLI](https://github.com/cli/cli). Your job is to find operations that are missing from the guard's classification logic and report them with actionable remediation steps.

## Context

- **Repository**: ${{ github.repository }}
- **Run ID**: ${{ github.run_id }}
- **Guard implementation**: `guards/github-guard/rust-guard/src/tools.rs` and `guards/github-guard/rust-guard/src/labels/tool_rules.rs`
- **Upstream sources**: `github/github-mcp-server` (MCP tools), `cli/cli` (GitHub CLI commands)

## Step 1: Load Previous State from Cache

Use cache-memory to check:
- `last_run_date`: ISO date of the last coverage check
- `known_gaps`: Array of tool/command names already reported as gaps (to avoid duplicate issues)
- `last_all_gaps`: Array of ALL gap tool/command names found in the previous run (to detect regressions — gaps that were fixed then reappeared)
- `last_upstream_tools_hash`: A short hash or count of the MCP tool list from the last run (to detect when new tools are added)
- `last_cli_commands_hash`: A short hash or count of the CLI write-command list from the last run (to detect when new CLI commands are added)

If cache is empty, start fresh.

## Step 2: Fetch Tools from the Official GitHub MCP Server

Use the GitHub MCP `get_file_contents` tool to read the upstream tool definitions.

### 2.1 Read the README for the full tool list

```
Use github get_file_contents with owner=github, repo=github-mcp-server, path=README.md, ref=main
```

Parse the README to extract every tool name listed in the toolsets section. Tools are documented as markdown tables or lists under headings like "Tools", "Toolsets", or similar. Extract all tool names — both read-only tools and write tools — and store them as the canonical upstream tool list.

### 2.2 Discover toolset source files (if README is insufficient)

If the README doesn't have a complete or clearly structured tool list, fall back to reading source code to find all tool names:

```
Use github get_file_contents with owner=github, repo=github-mcp-server, path=pkg/toolsets/toolsets.go, ref=main
```

Or look for tool registration files:

```
Use github get_file_contents with owner=github, repo=github-mcp-server, path=pkg/github/tools.go, ref=main
```

If those paths don't exist, use `search_code` to locate tool registration patterns directly:

```
Use github search_code with query=AddTool repo:github/github-mcp-server
```

This finds all files that contain `AddTool(` calls, which is the standard Go MCP SDK pattern for registering a named tool (e.g. `s.AddTool(mcp.NewTool("tool_name", ...)`). Read the matched files to extract all registered tool names.

### 2.3 Build the canonical tool list

Produce a complete, deduplicated list of tool names from the upstream GitHub MCP server. This is your **MCP reference set**. Record the total count for cache comparison.

## Step 3: Fetch Write Operations from the GitHub CLI

The GitHub CLI (`cli/cli`) exposes a rich set of GitHub API operations through its commands. Any write or mutating operation reachable via the CLI that could also be invoked through the MCP gateway should be represented in the guard's classification.

### 3.1 Discover CLI command categories

Read the CLI source structure to understand what command groups exist:

```
Use github get_file_contents with owner=cli, repo=cli, path=pkg/cmd, ref=trunk
```

This lists the top-level command directories (e.g., `pr/`, `issue/`, `repo/`, `gist/`, `release/`, `workflow/`, `label/`, `project/`, `org/`, `secret/`, etc.).

### 3.2 Read write-command implementations

For each command group that has write/mutating sub-commands, read the corresponding Go source files to extract the HTTP method (POST, PATCH, PUT, DELETE) used. Focus on these high-value categories:

- **`pkg/cmd/pr/`**: `create`, `merge`, `close`, `reopen`, `edit`, `review`, `comment`, `ready`, `convert`, `lock`, `unlock`
- **`pkg/cmd/issue/`**: `create`, `close`, `reopen`, `edit`, `comment`, `lock`, `unlock`, `pin`, `unpin`, `transfer`, `delete`
- **`pkg/cmd/repo/`**: `create`, `fork`, `delete`, `edit`, `archive`, `rename`, `transfer`, `set-default`
- **`pkg/cmd/release/`**: `create`, `edit`, `delete`, `upload`
- **`pkg/cmd/gist/`**: `create`, `edit`, `delete`
- **`pkg/cmd/workflow/`**: `run`, `enable`, `disable`
- **`pkg/cmd/label/`**: `create`, `edit`, `delete`, `clone`
- **`pkg/cmd/project/`**: `create`, `edit`, `delete`, `link`, `unlink`, `item-add`, `item-edit`, `item-delete`, `item-archive`
- **`pkg/cmd/secret/`**: `set`, `delete`
- **`pkg/cmd/variable/`**: `set`, `delete`
- **`pkg/cmd/org/`**: any write sub-commands

Use `get_file_contents` to read individual command files if needed. For example:

```
Use github get_file_contents with owner=cli, repo=cli, path=pkg/cmd/pr/merge/merge.go, ref=trunk
```

### 3.3 Extract GitHub API endpoints used by CLI write commands

For each write command file you read, note:
- The REST API endpoint (e.g., `POST /repos/{owner}/{repo}/issues`)
- The corresponding MCP tool name that covers the same operation (if any)
- Whether the operation has a counterpart in the GitHub MCP server's tool set

The goal is to build a **CLI write-operations list**: a mapping of `{cli_command} → {rest_endpoint} → {mcp_tool_or_none}`.

### 3.4 Identify CLI operations without MCP/guard coverage

A CLI write operation has a **guard coverage gap** if:
1. It uses a mutating HTTP method (POST, PATCH, PUT, DELETE) against the GitHub API, AND
2. There is no equivalent MCP tool name in the guard's `WRITE_OPERATIONS` or `READ_WRITE_OPERATIONS`, AND
3. The operation is not covered by a prefix pattern (`merge_*`, `delete_*`, `update_*`, `create_*`)

**Important**: You are looking for *semantic equivalences*, not exact name matches. For example, `gh issue comment --edit` maps to editing an issue comment, which might map to an `edit_issue_comment` MCP tool. Check whether the guard covers the underlying GitHub API operation, not just the CLI command name.

## Step 4: Read the Guard Implementation

Read the local guard files to understand what's currently covered:

### 4.1 Read tools.rs (explicit classifications)

```bash
cat guards/github-guard/rust-guard/src/tools.rs
```

This file contains:
- `WRITE_OPERATIONS`: explicit list of write tools
- `READ_WRITE_OPERATIONS`: explicit list of read-write tools
- Pattern functions: `is_merge_operation`, `is_delete_operation`, `is_update_operation`, `is_create_operation`

### 4.2 Read tool_rules.rs (per-tool DIFC labeling)

```bash
cat guards/github-guard/rust-guard/src/labels/tool_rules.rs
```

This file contains the `apply_tool_labels` function with a `match tool_name { ... }` block. Tools with explicit match arms have **specific DIFC labeling rules** (secrecy tags, integrity levels). Tools without explicit match arms fall through to default handling.

### 4.3 Build the guard coverage sets

From reading the code, produce:

1. **write_ops**: set of tool names in `WRITE_OPERATIONS`
2. **read_write_ops**: set of tool names in `READ_WRITE_OPERATIONS`
3. **pattern_covered**: tools from the upstream list that match any pattern (`merge_*`, `delete_*`, `update_*`, `create_*`)
4. **label_ruled**: set of tool names with explicit match arms in `apply_tool_labels`

## Step 5: Identify Coverage Gaps

### 5.1 MCP tool classification gaps (tools.rs)

A tool has a **classification gap** if it is in the upstream MCP tool list AND:
- It is NOT in `WRITE_OPERATIONS`
- It is NOT in `READ_WRITE_OPERATIONS`
- It does NOT match any prefix pattern (`merge_*`, `delete_*`, `update_*`, `create_*`)
- AND it appears to perform write or mutating operations based on its name or description (e.g., tools with verbs like "add", "set", "enable", "disable", "submit", "publish", "request", "approve", "reject", "resolve", "reopen", "close", "lock", "unlock", "pin", "unpin", "convert")

For read-only tools (get, list, search, read), missing classification is expected and not a gap.

### 5.2 MCP tool labeling gaps (tool_rules.rs)

A tool has a **labeling gap** if it is in the upstream MCP tool list AND has no explicit match arm in `apply_tool_labels`. This is lower severity than a classification gap, but still important for DIFC correctness — read tools that return repo-scoped data (issues, PRs, code, files) should have explicit secrecy/integrity rules.

### 5.3 GitHub CLI gaps

For each write operation discovered in Step 3.4, determine if the underlying GitHub API operation has guard coverage:

- If there is an equivalent MCP tool and it is already in `WRITE_OPERATIONS` / `READ_WRITE_OPERATIONS` (or covered by a pattern) → **covered, skip**.
- If there is an equivalent MCP tool but it is NOT in the guard lists and not covered by any pattern → **MCP classification gap** (already captured in 5.1).
- If there is **no equivalent MCP tool** for a CLI write command → flag as a **CLI-only gap**: the guard does not model this operation at all. Note the CLI command, the REST endpoint, and the GitHub API action it performs.

For CLI-only gaps, the fix is to add a new entry to `WRITE_OPERATIONS` (or `READ_WRITE_OPERATIONS`) using a descriptive MCP-style tool name (snake_case) that maps to the CLI operation — or to file an issue requesting that the GitHub MCP server add a corresponding tool.

### 5.4 Stale entries (bonus check)

Check if any entries in `WRITE_OPERATIONS` or `READ_WRITE_OPERATIONS` are **no longer in the upstream MCP tool list** and also have no equivalent in the CLI write-operations list. These are stale guard entries that should be removed.

### 5.5 Filter known gaps and detect regressions

Compare the full set of gaps discovered this run against `known_gaps` from cache:

- **New gaps**: gaps found this run that are NOT in `known_gaps` → these are reportable
- **Regressions**: gaps that were previously in `known_gaps`, were later fixed (absent in a prior run), but have now reappeared → also reportable. To detect regressions, compare against `last_all_gaps` (the complete gap list from the previous run, stored in cache). Any gap in `known_gaps` but NOT in `last_all_gaps` that reappears this run is a regression.
- **Persisting gaps**: gaps in both this run and `known_gaps` that were also in `last_all_gaps` → already reported, skip

Only report new gaps and regressions.

## Step 6: Determine Output

### If no new gaps found

Call the `noop` safe output with a message like:
> "GitHub guard coverage check complete — no new gaps found. MCP tools: [M] scanned. CLI write commands: [C] scanned. Guard write ops: [N]."

Then update cache:
- `last_run_date`: today's ISO date
- `last_upstream_tools_hash`: total count of upstream MCP tools (as string)
- `last_cli_commands_hash`: total count of CLI write commands scanned (as string)

### If new gaps are found

Proceed to Step 7 to create an issue.

## Step 7: Create a Gap Report Issue

Create a GitHub issue using the `create-issue` safe output.

**Title**: `Guard coverage gap: [N] operations from github-mcp-server / GitHub CLI not fully covered`

**Body**:

```markdown
## Summary

The GitHub guard does not fully cover **[N]** operation(s) from the [github-mcp-server](https://github.com/github/github-mcp-server) and/or [GitHub CLI](https://github.com/cli/cli). This may allow write operations to bypass DIFC classification or leave read operations without proper secrecy/integrity labeling.

- **MCP tools scanned**: [total count from github-mcp-server]
- **CLI write commands scanned**: [total count from cli/cli]
- **Guard-covered write tools (tools.rs)**: [count in WRITE_OPERATIONS + READ_WRITE_OPERATIONS]
- **Tools with explicit DIFC rules (tool_rules.rs)**: [count of match arms]
- **New gaps found this run**: [N]

---

## MCP Tool Classification Gaps (tools.rs)

These MCP tools perform write or mutating operations but are missing from `WRITE_OPERATIONS` or `READ_WRITE_OPERATIONS` in `guards/github-guard/rust-guard/src/tools.rs`:

| Tool Name | Operation Type | Suggested Classification | Notes |
|-----------|---------------|--------------------------|-------|
| `tool_name` | write / read-write | `WRITE_OPERATIONS` | Brief description of what this tool does |

### Suggested fix for tools.rs

```rust
// Add to WRITE_OPERATIONS or READ_WRITE_OPERATIONS as appropriate:
pub const WRITE_OPERATIONS: &[&str] = &[
    // ... existing entries ...
    "new_tool_name_here",  // brief description
];
```

---

## MCP Tool DIFC Labeling Gaps (tool_rules.rs)

These MCP tools exist in the upstream server but have no explicit match arm in `apply_tool_labels` in `guards/github-guard/rust-guard/src/labels/tool_rules.rs`. They fall through to default label handling, which may not correctly apply repo-scoped secrecy tags or appropriate integrity levels:

| Tool Name | Data Scope | Suggested Labels | Risk |
|-----------|-----------|-----------------|------|
| `tool_name` | repo-scoped | secrecy: S(repo), integrity: writer | Medium |

### Suggested fix for tool_rules.rs

Add a match arm to `apply_tool_labels` for each missing tool, following the pattern of similar existing tools (e.g., `get_issue` for issue-scoped tools, `get_pull_request` for PR-scoped tools).

---

## GitHub CLI-Only Gaps

These write operations are reachable via the GitHub CLI but have no corresponding MCP tool and no guard entry. The guard has no visibility into these operations if an agent invokes them via `gh` or direct API calls:

| CLI Command | REST Endpoint | GitHub API Action | Risk |
|-------------|--------------|------------------|------|
| `gh issue transfer` | `POST /repos/{owner}/{repo}/issues/{issue_number}/transfer` | Transfers issue to another repo | High |

### Suggested remediation

For each CLI-only gap, either:
1. Add a new entry to `WRITE_OPERATIONS` using a descriptive MCP-style name (e.g., `transfer_issue`) — this pre-emptively guards the operation when/if a matching MCP tool is added, OR
2. File a request to add the equivalent tool to the GitHub MCP server so it can be properly guarded

---

## Stale Guard Entries (bonus)

These tools are in `WRITE_OPERATIONS` or `READ_WRITE_OPERATIONS` but no longer appear in the upstream github-mcp-server or CLI write-operations list. Consider removing them to keep the guard clean:

- `stale_tool_name` — not found in upstream

---

## References

- [github-mcp-server tools](https://github.com/github/github-mcp-server/blob/main/README.md)
- [GitHub CLI commands](https://github.com/cli/cli/tree/trunk/pkg/cmd)
- [guard tools.rs](https://github.com/github/gh-aw-mcpg/blob/main/guards/github-guard/rust-guard/src/tools.rs)
- [guard tool_rules.rs](https://github.com/github/gh-aw-mcpg/blob/main/guards/github-guard/rust-guard/src/labels/tool_rules.rs)
- Run: [${{ github.run_id }}](${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }})
```

## Step 8: Update Cache

After creating the issue (or calling noop), update cache-memory:
- `last_run_date`: today's ISO date
- `known_gaps`: add the newly-reported tool/command names to the existing list
- `last_all_gaps`: the complete list of ALL gaps found this run (not just new ones — this enables regression detection next run)
- `last_upstream_tools_hash`: total count of upstream MCP tools (as string)
- `last_cli_commands_hash`: total count of CLI write commands scanned (as string)

Keep `known_gaps` bounded to the last 200 entries — remove the oldest if it exceeds this limit.

## Guidelines

- **Be precise**: only flag tools that are genuinely missing from the guard. Read-only tools (get, list, search) that fall through to default handling are not gaps unless they handle sensitive cross-repo data.
- **Provide actionable fixes**: every gap must include a concrete suggested fix with actual Rust code or a clear description of what match arm to add.
- **Avoid false positives**: if a tool name matches a prefix pattern (`create_*`, `merge_*`, `delete_*`, `update_*`), it is already covered by the pattern functions — do not report it as a classification gap.
- **Deduplicate**: always check the cache before reporting a gap. Only report tools not already in `known_gaps`.
- **Silence is success**: if the guard is fully up-to-date, call noop and exit cleanly — do not create an issue.