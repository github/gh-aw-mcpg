---
description: Smoke test workflow that validates Copilot engine functionality by reviewing recent PRs twice daily
on: 
  schedule: every 12h
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
  
name: Smoke Copilot
engine:
  id: copilot
strict: false
imports:
  - shared/mcp-pagination.md
  - shared/reporting.md
  - shared/go-make.md
  - shared/github-mcp-app.md
network:
  allowed:
    - defaults
    - github
    - playwright
    - github.com
tools:
  cache-memory: true
  github:
    mode: gh-proxy
    toolsets: [repos, pull_requests]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved
  playwright:
  edit:
  bash:
    - "*"
runtimes:
  go:
    version: "1.25"
steps:
  - name: Set up Go
    uses: actions/setup-go@4dc6199c7b1a012772edbd06daecab0f50c9053c # v6
    with:
      go-version-file: go.mod
      cache: true
sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg"
    version: "latest"
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
      allowed: [smoke-copilot]
    messages:
      footer: "> 📰 *BREAKING: Report filed by [{workflow_name}]({run_url})*"
      run-started: "📰 BREAKING: [{workflow_name}]({run_url}) is now investigating this {event_type}. Sources say the story is developing..."
      run-success: "📰 VERDICT: [{workflow_name}]({run_url}) has concluded. All systems operational. This is a developing story. 🎤"
      run-failure: "📰 DEVELOPING STORY: [{workflow_name}]({run_url}) reports {status}. Our correspondents are investigating the incident..."
timeout-minutes: 15
---

# Smoke Test: Copilot Engine Validation.

**IMPORTANT: Keep all outputs extremely short and concise. Use single-line responses where possible. No verbose explanations.**

## Test Requirements

1. **GitHub MCP Testing**: Review the last 2 merged pull requests in ${{ github.repository }}
2. **Make Build Testing**: Use the `safeinputs-make` tool to build the project (use args: "build") and verify it succeeds
3. **Playwright Testing**: Use the playwright tools to navigate to https://github.com and verify the page title contains "GitHub" (do NOT try to install playwright - use the provided MCP tools)
4. **File Writing Testing**: Create a test file `/tmp/gh-aw/agent/smoke-test-copilot-${{ github.run_id }}.txt` with content "Smoke test passed for Copilot at $(date)" (create the directory if it doesn't exist)
5. **Bash Tool Testing**: Execute bash commands to verify file creation was successful (use `cat` to read the file back)
6. **Artifact Readability Testing**: Run `find /tmp/gh-aw/mcp-logs/ -not -readable 2>/dev/null` and verify the output is empty. If any files or directories are not readable, this test MUST FAIL — report the unreadable paths and mark the overall status as FAIL. This catches permission issues (like restrictive wazero-cache directories) that would silently break artifact uploads.

## Output

1. **Create an issue** with a summary of the smoke test run:
   - Title: "Smoke Test: Copilot - ${{ github.run_id }}"
   - Body should include:
     - Test results (✅ or ❌ for each test)
     - Overall status: PASS or FAIL
     - Run URL: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
     - Timestamp

2. **Only if this workflow was triggered by a pull_request event**: Use the `add_comment` tool to add a **very brief** comment (max 5-10 lines) to the triggering pull request (omit the `item_number` parameter to auto-target the triggering PR) with:
   - PR titles only (no descriptions)
   - ✅ or ❌ for each test result
   - Overall status: PASS or FAIL

If all tests pass, use the `add_labels` tool to add the label `smoke-copilot` to the pull request (omit the `item_number` parameter to auto-target the triggering PR if this workflow was triggered by a pull_request event).