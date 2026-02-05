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
  discussions: read
  actions: read
  
name: Smoke Copilot
engine:
  id: copilot
strict: true
imports:
  - shared/mcp-pagination.md
  - shared/gh.md
  - shared/mcp/tavily.md
  - shared/reporting.md
  - shared/github-queries-safe-input.md
  - shared/go-make.md
  - shared/github-mcp-app.md
network:
  allowed:
    - defaults
    - github
    - playwright
tools:
  cache-memory: true
  github:
    toolsets: [repos, pull_requests]
  playwright:
    allowed_domains:
      - github.com
  edit:
  bash:
    - "*"
  serena:
    languages:
      go: {}
runtimes:
  go:
    version: "1.25"
mcp-servers:
  agentic_workflows:
    type: stdio
    container: "alpine:latest"
    entrypoint: "/opt/gh-aw/gh-aw"
    entrypointArgs: ["mcp-server"]
    mounts: 
      - "/opt/gh-aw:/opt/gh-aw:ro"
      - "/usr/bin/gh:/usr/bin/gh:ro"
      - "${{ github.workspace }}:${{ github.workspace }}:rw"
      - "/tmp/gh-aw:/tmp/gh-aw:rw"
    env:
      GITHUB_TOKEN: "${{ secrets.GITHUB_TOKEN }}"
      PATH: "/opt/gh-aw:/usr/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/sbin:/bin"
sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg"
safe-outputs:
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
jobs:
  build-gh-aw:
    runs-on: ubuntu-latest
    steps:
      - name: Setup Go
        uses: actions/setup-go@4dc6199c7b1a012772edbd06daecab0f50c9053c # v6.1.0
        with:
          go-version: '1.25'
      - name: Clone and build gh-aw from source
        run: |
          # Clone gh-aw repository
          cd /tmp
          rm -rf gh-aw
          git clone https://github.com/github/gh-aw.git
          echo "✓ Cloned gh-aw repository"
          
          # Build gh-aw from source
          cd /tmp/gh-aw
          make build
          echo "✓ Built gh-aw binary"
          
          # Upload as artifact
          mkdir -p /tmp/gh-aw-artifact
          cp /tmp/gh-aw/gh-aw /tmp/gh-aw-artifact/gh-aw
          chmod +x /tmp/gh-aw-artifact/gh-aw
          /tmp/gh-aw-artifact/gh-aw --version
          echo "✓ Built gh-aw binary"
      - name: Upload gh-aw binary
        uses: actions/upload-artifact@v4
        with:
          name: gh-aw-binary
          path: /tmp/gh-aw-artifact/gh-aw
---

# Smoke Test: Copilot Engine Validation.

**IMPORTANT: Keep all outputs extremely short and concise. Use single-line responses where possible. No verbose explanations.**

## Test Requirements

1. **GitHub MCP Testing**: Review the last 2 merged pull requests in ${{ github.repository }}
2. **Safe Inputs GH CLI Testing**: Use the `safeinputs-gh` tool to query 2 pull requests from ${{ github.repository }} (use args: "pr list --repo ${{ github.repository }} --limit 2 --json number,title,author")
3. **Serena MCP Testing**: 
   - Use the Serena MCP server tool `activate_project` to initialize the workspace at `${{ github.workspace }}` and verify it succeeds (do NOT use bash to run go commands - use Serena's MCP tools or the safeinputs-go/safeinputs-make tools from the go-make shared workflow)
   - After initialization, use the `find_symbol` tool to search for symbols (find which tool to call) and verify that at least 3 symbols are found in the results
4. **Make Build Testing**: Use the `safeinputs-make` tool to build the project (use args: "build") and verify it succeeds
5. **Playwright Testing**: Use the playwright tools to navigate to https://github.com and verify the page title contains "GitHub" (do NOT try to install playwright - use the provided MCP tools)
6. **Tavily Web Search Testing**: Use the Tavily MCP server to perform a web search for "GitHub Agentic Workflows" and verify that results are returned with at least one item
7. **File Writing Testing**: Create a test file `/tmp/gh-aw/agent/smoke-test-copilot-${{ github.run_id }}.txt` with content "Smoke test passed for Copilot at $(date)" (create the directory if it doesn't exist)
8. **Bash Tool Testing**: Execute bash commands to verify file creation was successful (use `cat` to read the file back)
9. **Discussion Interaction Testing**: 
   - Use the `github-discussion-query` safe-input tool with params: `limit=1, jq=".[0]"` to get the latest discussion from ${{ github.repository }}
   - Extract the discussion number from the result (e.g., if the result is `{"number": 123, "title": "...", ...}`, extract 123)
   - Use the `add_comment` tool with `discussion_number: <extracted_number>` to add a fun, news-reporter style comment stating that the smoke test agent was here
10. **Agentic Workflows MCP Testing**: 
   - Use the `agentic-workflows` MCP tool with the `status` method to query the status of the "smoke-copilot" workflow in ${{ github.repository }}
   - Extract key information: total runs, recent success/failure status, last run time
   - Write a summary of the smoke-copilot workflow status to `/tmp/gh-aw/agent/smoke-copilot-status-${{ github.run_id }}.txt`
   - Use bash to display the file contents

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

3. Use the `add_comment` tool with `item_number` set to the discussion number you extracted in step 9 to add a **fun news-reporter style comment** to that discussion - be playful and use reporter language like "📰 BREAKING NEWS!"

If all tests pass, use the `add_labels` tool to add the label `smoke-copilot` to the pull request (omit the `item_number` parameter to auto-target the triggering PR if this workflow was triggered by a pull_request event).