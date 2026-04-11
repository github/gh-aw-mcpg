---
name: Nightly Workflow Upgrader
description: Nightly workflow that upgrades all workflows to the latest agentic workflow release (agent files, codemods, and compilations)
on:
  schedule: daily
  workflow_dispatch:
permissions:
  contents: read
  issues: read
  pull-requests: read
network:
  allowed:
    - defaults
tools:
  github:
    toolsets: [default]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved
  bash: ["*"]
safe-outputs:
  threat-detection:
    enabled: false
  create-pull-request:
    title-prefix: "🤖 "
    labels: [agentic-workflows, automation, maintenance]
    draft: true
    expires: 7d
  missing-tool:
    create-issue: true
timeout-minutes: 25
---

<!-- Edit the file linked below to modify the agent without recompilation. Feel free to move the entire markdown body to that file. -->
@./agentics/nightly-workflow-compiler.md