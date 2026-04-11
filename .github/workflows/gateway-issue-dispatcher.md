---
name: Gateway Issue Dispatcher
description: Audits github/gh-aw issues labeled 'gateway' and creates tracking issues in github/gh-aw-mcpg with proposed solutions
on:
  schedule: every 6h
  workflow_dispatch:

permissions:
  contents: read
  issues: read

tools:
  github:
    toolsets: [repos, issues]
    github-token: ${{ secrets.CROSS_REPO_PAT }}
    allowed-repos: ["github/gh-aw", "github/gh-aw-mcpg"]
    min-integrity: none
  bash: true

safe-outputs:
  threat-detection:
    enabled: false
  github-token: ${{ secrets.CROSS_REPO_PAT }}
  max-bot-mentions: 1
  create-issue:
    max: 10
    target-repo: "github/gh-aw-mcpg"
    labels: [gateway, from-gh-aw]
  add-comment:
    max: 10
    target: "*"
    target-repo: "github/gh-aw"
    discussions: false
    pull-requests: false

timeout-minutes: 20
---

# Gateway Issue Dispatcher

**Goal**: Audit all open issues in `github/gh-aw` labeled `gateway`, create a
corresponding tracking issue in `github/gh-aw-mcpg` that describes the problem
and proposes a solution, then link the two issues via a comment.

## Procedure

### Step 1: Gather Gateway Issues

List all **open** issues in `github/gh-aw` with the label `gateway`:

```bash
gh issue list --repo github/gh-aw --label gateway --state open --json number,title,body,url,labels,createdAt --limit 100
```

If there are no open gateway issues, report "No open gateway issues found" and
stop.

### Step 2: Filter Already-Processed Issues

For each gateway issue, check its comments for an existing link to a
`github/gh-aw-mcpg` issue. An issue is already processed if any comment
contains a URL matching `github.com/github/gh-aw-mcpg/issues/`.

```bash
# For each issue number, check comments
gh issue view <NUMBER> --repo github/gh-aw --json comments --jq '.comments[].body' \
  | grep -q 'github.com/github/gh-aw-mcpg/issues/' && echo "SKIP" || echo "PROCESS"
```

**Skip** any issue that already has a linked `gh-aw-mcpg` issue. Only proceed
with unprocessed issues.

If all issues are already processed, report "All gateway issues already have
tracking issues" and stop.

### Step 3: Analyze and Create Tracking Issues

For each unprocessed gateway issue:

1. **Read the issue thoroughly** — understand the problem described, any error
   messages, configuration details, and expected vs actual behavior.

2. **Research the codebase** — use the GitHub MCP tools to search the
   `github/gh-aw-mcpg` codebase for relevant files, functions, and existing
   related issues. Look at the project structure:
   - `internal/server/` — HTTP server (routed/unified modes)
   - `internal/config/` — Config parsing and validation
   - `internal/guard/` — Security guards (AllowOnly, WriteSink)
   - `internal/mcp/` — MCP protocol types
   - `internal/proxy/` — Proxy mode implementation
   - `internal/launcher/` — Backend process management
   - `guards/github-guard/` — Rust WASM guard

3. **Create the tracking issue** in `github/gh-aw-mcpg` with:
   - A clear, descriptive title (prefix: `[gateway]`)
   - **Problem section**: Summarize the issue from gh-aw with a link back to the
     source issue
   - **Analysis section**: Identify which files/components are likely involved
   - **Proposed Solution section**: Describe a concrete implementation approach
     with specific files to change
   - **Source reference**: Link to the original `github/gh-aw` issue

   Use this structure for the issue body:

   ```markdown
   ### Problem

   [Clear description of the problem from the gh-aw issue]

   Source: <gh-aw issue URL>

   ### Analysis

   [Which components/files in gh-aw-mcpg are involved and why]

   ### Proposed Solution

   [Concrete steps to fix the problem, with specific files and changes]

   ### Testing

   [How to verify the fix works]
   ```

### Step 4: Link the Issues

After creating each tracking issue in `gh-aw-mcpg`, leave a comment on the
original `github/gh-aw` issue with a link:

> Tracking issue created in gh-aw-mcpg: <link to new issue>

This ensures the issue is marked as processed for future runs.

## Important Notes

- Only create issues for problems that are **actionable in the gh-aw-mcpg
  codebase**. If a gateway issue is purely about gh-aw configuration or
  documentation, note it but do not create a tracking issue.
- Be specific in proposed solutions — reference actual file paths, function
  names, and line numbers when possible.
- Do not duplicate existing open issues in gh-aw-mcpg. Before creating, search
  for similar open issues.
- Process issues in chronological order (oldest first).