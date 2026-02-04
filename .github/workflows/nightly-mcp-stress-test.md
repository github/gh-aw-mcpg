---
name: Nightly MCP Server Stress Test
description: Comprehensive stress test that loads 14 well-known MCP servers, tests their tools, and reports results with automated issue creation for failures
on:
  schedule: daily
  workflow_dispatch:

permissions:
  contents: read
  actions: read
  issues: read
  pull-requests: read

roles: [admin, maintainer, write]

network:
  allowed:
    - defaults
    - go
    - containers

tools:
  github:
    toolsets: [default]
  bash: ["*"]

mcp-servers:
  github:
    type: stdio
    container: "ghcr.io/github/github-mcp-server:v0.30.2"
    env:
      GITHUB_PERSONAL_ACCESS_TOKEN: "${{ secrets.GITHUB_TOKEN }}"
  filesystem:
    type: stdio
    container: "mcp/filesystem"
    mounts:
      - "/tmp/mcp-test-fs:/workspace:rw"
  memory:
    type: stdio
    container: "mcp/memory"
  sqlite:
    type: stdio
    container: "mcp/sqlite"
  brave-search:
    type: stdio
    container: "mcp/brave-search"
  fetch:
    type: stdio
    container: "mcp/fetch"
  puppeteer:
    type: stdio
    container: "mcp/puppeteer"
  slack:
    type: stdio
    container: "mcp/slack"
  gdrive:
    type: stdio
    container: "mcp/gdrive"
  google-maps:
    type: stdio
    container: "mcp/google-maps"
  everart:
    type: stdio
    container: "mcp/everart"
  sentry:
    type: stdio
    container: "mcp/sentry"
  git:
    type: stdio
    container: "mcp/git"
  time:
    type: stdio
    container: "mcp/time"

sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg:v0.0.94"

safe-outputs:
  create-issue:
    title-prefix: "[mcp-stress-test] "
    labels: [mcp-gateway, stress-test, automation]
    assignees: []
    max: 25
  missing-tool:
    create-issue: true

post-steps:
  - name: Upload Test Results
    if: always()
    uses: actions/upload-artifact@v4
    with:
      name: mcp-stress-test-results
      path: |
        /tmp/mcp-stress-results/
        /tmp/mcp-stress-test/logs/
      retention-days: 30

timeout-minutes: 30
strict: true
---

<!-- Edit the file linked below to modify the agent without recompilation. Feel free to move the entire markdown body to that file. -->
@./agentics/nightly-mcp-stress-test.md
