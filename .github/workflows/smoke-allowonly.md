---
description: Daily smoke test validating GitHub AllowOnly guard policy enforcement through the MCP Gateway
on:
  schedule: daily
  workflow_dispatch:
  pull_request:
    types: [labeled]
    names: ["smoke"]
  reaction: "eyes"
permissions:
  contents: read
  issues: read
  pull-requests: read
  actions: read

name: Smoke AllowOnly
engine:
  id: copilot
strict: false
imports:
  - shared/mcp-pagination.md
  - shared/reporting.md
  - shared/github-mcp-app.md
network:
  allowed:
    - defaults
    - github
    - github.com
tools:
  agentic-workflows:
  cache-memory: true
  github:
    toolsets: [repos, issues, pull_requests, search]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved
  edit:
  bash:
    - "cat"
    - "echo"
    - "date"
    - "mkdir"
sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg"
safe-outputs:
    threat-detection:
        enabled: false
    add-comment:
      hide-older-comments: true
      max: 2
    create-issue:
      expires: 2h
      group: true
      close-older-issues: true
    add-labels:
      allowed: [smoke-allowonly]
    messages:
      footer: "> 🛡️ *AllowOnly guard smoke test by [{workflow_name}]({run_url})*"
      run-started: "🛡️ [{workflow_name}]({run_url}) is testing AllowOnly guard enforcement..."
      run-success: "🛡️ [{workflow_name}]({run_url}) completed. Guard enforcement validated. ✅"
      run-failure: "🛡️ [{workflow_name}]({run_url}) reports {status}. Guard enforcement may have issues. ⚠️"
timeout-minutes: 15
---

# AllowOnly Guard Policy Smoke Test

**IMPORTANT: Keep all outputs extremely short and concise. Use single-line responses where possible.**

## Policy Under Test

This workflow validates that the AllowOnly guard policy correctly enforces repository-scoped access:
- **Allowed repos**: `github/gh-aw*` (wildcard match)
- **Min integrity**: `approved` (OWNER, MEMBER, COLLABORATOR author_association only)

## Test Plan

Execute read-only GitHub MCP calls against allowed and disallowed repositories to verify guard enforcement.

### Part 1: In-Scope Repository Access (Expected: ALLOWED)

These calls target `github/gh-aw-mcpg` which matches the `github/gh-aw*` pattern:

1. **list_issues**: Call `list_issues` with owner=github, repo=gh-aw-mcpg, perPage=3
2. **list_pull_requests**: Call `list_pull_requests` with owner=github, repo=gh-aw-mcpg, perPage=3
3. **list_commits**: Call `list_commits` with owner=github, repo=gh-aw-mcpg, perPage=3
4. **get_file_contents**: Call `get_file_contents` with owner=github, repo=gh-aw-mcpg, path="README.md"
5. **list_branches**: Call `list_branches` with owner=github, repo=gh-aw-mcpg, perPage=5
6. **search_code**: Call `search_code` with query="repo:github/gh-aw-mcpg README", perPage=3

Also test the other matching repo `github/gh-aw`:

7. **list_issues (gh-aw)**: Call `list_issues` with owner=github, repo=gh-aw, perPage=3
8. **get_file_contents (gh-aw)**: Call `get_file_contents` with owner=github, repo=gh-aw, path="README.md"

### Part 2: Out-of-Scope Repository Access (Expected: BLOCKED)

These calls target `octocat/Hello-World` which does NOT match `github/gh-aw*`:

1. **list_issues**: Call `list_issues` with owner=octocat, repo=Hello-World, perPage=3
2. **list_pull_requests**: Call `list_pull_requests` with owner=octocat, repo=Hello-World, perPage=3
3. **list_commits**: Call `list_commits` with owner=octocat, repo=Hello-World, perPage=3
4. **get_file_contents**: Call `get_file_contents` with owner=octocat, repo=Hello-World, path="README"
5. **search_code**: Call `search_code` with query="repo:octocat/Hello-World README", perPage=3

### Part 3: Global API Access (Expected: BLOCKED)

These calls are not scoped to any specific allowed repo:

1. **search_repositories**: Call `search_repositories` with query="github-guard", perPage=3
2. **search_users**: Call `search_users` with query="octocat", perPage=3

### Part 4: Integrity Filtering Validation (Expected: FILTERED)

For `github/gh-aw-mcpg`, if any issues or PRs exist from non-approved authors (CONTRIBUTOR, FIRST_TIME_CONTRIBUTOR, FIRST_TIMER, NONE), they should be filtered out. Only items from OWNER, MEMBER, or COLLABORATOR should appear.

1. **list_issues (full page)**: Call `list_issues` with owner=github, repo=gh-aw-mcpg, perPage=20
   - Note any filtering behavior or author_association values visible
2. **list_pull_requests (full page)**: Call `list_pull_requests` with owner=github, repo=gh-aw-mcpg, perPage=20
   - Note any filtering behavior or author_association values visible

## Validation Criteria

For each call, record:
- **Tool name**
- **Result**: data returned, empty, or error
- **Expected**: ALLOWED, BLOCKED, or FILTERED
- **Status**: ✅ PASS or ❌ FAIL

**PASS criteria**:
- In-scope repos return data (non-empty)
- Out-of-scope repos return empty results or are blocked
- Global APIs return empty results or are blocked
- Integrity filtering removes content from non-approved authors (if any exist)

## Output

Write a test results file to `/tmp/gh-aw/agent/smoke-allowonly-${{ github.run_id }}.txt` with a brief summary.

**Create an issue** with the test results:
- Title: "Smoke AllowOnly: ${{ github.run_id }}"
- Body must include:

```
## AllowOnly Guard Smoke Test Results

**Policy**: repos=["github/gh-aw*"], min-integrity=approved
**Run**: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}

### In-Scope Access (github/gh-aw*)
| Tool | Target | Result | Status |
|------|--------|--------|--------|
| list_issues | gh-aw-mcpg | [result] | ✅/❌ |
| list_pull_requests | gh-aw-mcpg | [result] | ✅/❌ |
| list_commits | gh-aw-mcpg | [result] | ✅/❌ |
| get_file_contents | gh-aw-mcpg | [result] | ✅/❌ |
| list_branches | gh-aw-mcpg | [result] | ✅/❌ |
| search_code | gh-aw-mcpg | [result] | ✅/❌ |
| list_issues | gh-aw | [result] | ✅/❌ |
| get_file_contents | gh-aw | [result] | ✅/❌ |

### Out-of-Scope Access (octocat/Hello-World)
| Tool | Result | Status |
|------|--------|--------|
| list_issues | [result] | ✅/❌ |
| list_pull_requests | [result] | ✅/❌ |
| list_commits | [result] | ✅/❌ |
| get_file_contents | [result] | ✅/❌ |
| search_code | [result] | ✅/❌ |

### Global APIs
| Tool | Result | Status |
|------|--------|--------|
| search_repositories | [result] | ✅/❌ |
| search_users | [result] | ✅/❌ |

### Integrity Filtering
| Observation | Status |
|-------------|--------|
| [describe filtering behavior] | ✅/❌ |

### Summary
- In-Scope Access: [X/8] ✅
- Out-of-Scope Blocked: [X/5] ✅
- Global APIs Blocked: [X/2] ✅
- Integrity Filtering: ✅/❌
- **Overall: PASS/FAIL**
```

**Only if triggered by pull_request**: Add a brief comment to the PR with ✅/❌ status per category.

If all tests pass, use the `add_labels` tool to add the label `smoke-allowonly` to the triggering PR.