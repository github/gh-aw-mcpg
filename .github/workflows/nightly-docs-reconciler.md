---
name: Nightly Documentation Reconciler
description: Nightly workflow that tests and reconciles implementation against documentation to ensure README and quickstarts reflect current main branch
on:
  schedule: daily
permissions:
  contents: read
  issues: read
  pull-requests: read
network:
  allowed:
    - defaults
    - containers
tools:
  github:
    toolsets: [default]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved
  bash: true
safe-outputs:
  threat-detection:
    enabled: false
  create-issue:
    title-prefix: "📚 "
    labels: [documentation, maintenance, automated]
    max: 1
    expires: 3d
  missing-tool:
    create-issue: true
timeout-minutes: 45
---

<!-- Edit the file linked below to modify the agent without recompilation. Feel free to move the entire markdown body to that file. -->
@./agentics/nightly-docs-reconciler.md