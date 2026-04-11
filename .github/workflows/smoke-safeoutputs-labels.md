---
description: Smoke test validating safe-outputs configuration enforcement for label operations. Tests add-labels (allowed list, max limits, target restrictions) and remove-labels (allowed list, max limits, target restrictions).
on:
  schedule: every 12h
  workflow_dispatch:
  pull_request:
    types: [labeled]
    names: ["smoke-safeoutputs"]

permissions:
  contents: read
  issues: read
  pull-requests: read
  actions: read

name: Smoke Safe-Outputs Labels
engine:
  id: copilot
strict: false
imports:
  - shared/reporting.md
  - shared/github-mcp-app.md
network:
  allowed:
    - defaults
    - github
    - github.com
tools:
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
sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg"
safe-outputs:
  threat-detection:
    enabled: false
  add-labels:
    allowed: [smoke-test, verified]
    max: 2
    target: "triggering"
  remove-labels:
    allowed: [smoke-test]
    max: 1
    target: "triggering"
  create-issue:
    title-prefix: "[smoke-safeoutputs] "
    labels: [smoke-test, automated]
    max: 1
    expires: 2h
    close-older-issues: true
  messages:
    footer: "> 🏷️ *Safe-outputs labels enforcement test by [{workflow_name}]({run_url})*"
    run-started: "🏷️ [{workflow_name}]({run_url}) is testing safe-outputs labels enforcement..."
    run-success: "🏷️ [{workflow_name}]({run_url}) completed. Results in summary issue. ✅"
    run-failure: "🏷️ [{workflow_name}]({run_url}) reports {status}. Enforcement may have issues. ⚠️"
timeout-minutes: 15
---

# Safe-Outputs Labels Enforcement Smoke Test

**IMPORTANT: Keep outputs concise. This test validates safe-outputs enforcement for label operations.**

## Configuration Under Test

| Safe-Output | Configuration |
|---|---|
| `add-labels` | `allowed: [smoke-test, verified]`, `max: 2`, `target: "triggering"` |
| `remove-labels` | `allowed: [smoke-test]`, `max: 1`, `target: "triggering"` |

## Context

If this workflow is triggered by a **pull_request** event, use the triggering PR as the target for label operations. If triggered by **schedule** or **workflow_dispatch**, all `add-labels` and `remove-labels` tests that require a triggering item should be marked as "SKIPPED - no triggering item".

## Test Matrix

### Phase 1: add-labels Enforcement

**Test 1.1 — SHOULD SUCCEED** (positive case: add allowed label to triggering item):
- Context: Only run if triggered by pull_request
- Attempt: Add label "smoke-test" to the triggering PR/issue (omit item_number to auto-target)
- Expected: ✅ Processed (label "smoke-test" is in allowed list, within max: 2)
- Record the actual outcome

**Test 1.2 — SHOULD SUCCEED** (positive case: add second allowed label):
- Context: Only run if triggered by pull_request
- Attempt: Add label "verified" to the triggering PR/issue
- Expected: ✅ Processed (label "verified" is in allowed list, still within max: 2)
- Record the actual outcome

**Test 1.3 — SHOULD FAIL** (negative case: add non-allowed label):
- Context: Only run if triggered by pull_request
- Attempt: Add label "enhancement" to the triggering PR/issue (not in allowed list)
- Expected: ❌ Rejected (label "enhancement" is not in allowed: [smoke-test, verified])
- Record the actual outcome

**Test 1.4 — SHOULD FAIL** (negative case: max exceeded):
- Context: Only run if triggered by pull_request
- Attempt: Add a third label to the triggering item (max: 2 already consumed by Tests 1.1 and 1.2)
- Expected: ❌ Rejected (max: 2 exceeded)
- Record the actual outcome

**Test 1.5 — SHOULD FAIL** (negative case: non-triggering target):
- Context: Only run if triggered by pull_request
- Attempt: Add label "smoke-test" to a specific issue number (e.g., issue #1, or any other non-triggering item) using an explicit item_number
- Expected: ❌ Rejected (target: "triggering" only allows the triggering item)
- Record the actual outcome

### Phase 2: remove-labels Enforcement

**Test 2.1 — SHOULD SUCCEED** (positive case: remove allowed label from triggering item):
- Context: Only run if triggered by pull_request and "smoke-test" label was added in Phase 1
- Attempt: Remove label "smoke-test" from the triggering PR/issue
- Expected: ✅ Processed (label "smoke-test" is in allowed: [smoke-test], within max: 1)
- Record the actual outcome

**Test 2.2 — SHOULD FAIL** (negative case: remove non-allowed label):
- Context: Only run if triggered by pull_request
- Attempt: Remove label "verified" from the triggering item (not in allowed: [smoke-test])
- Expected: ❌ Rejected (label "verified" is not in allowed: [smoke-test])
- Record the actual outcome

**Test 2.3 — SHOULD FAIL** (negative case: max exceeded):
- Context: Only run if triggered by pull_request
- Attempt: Remove a second label (max: 1 already consumed by Test 2.1)
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

**Test 2.4 — SHOULD FAIL** (negative case: non-triggering target):
- Context: Only run if triggered by pull_request
- Attempt: Remove a label from a specific item number (not the triggering item)
- Expected: ❌ Rejected (target: "triggering" only allows the triggering item)
- Record the actual outcome

## Output

**Create an issue** with the full test results:
- Title: "Smoke Safe-Outputs Labels: ${{ github.run_id }}"
- Body:

```
## Safe-Outputs Labels Enforcement Test Results

**Run**: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
**Trigger**: ${{ github.event_name }}
**Configuration**: add-labels (allowed:[smoke-test,verified], max:2, target:triggering), remove-labels (allowed:[smoke-test], max:1, target:triggering)

### Phase 1: add-labels
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 1.1 | Add "smoke-test" (allowed, triggering) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 1.2 | Add "verified" (allowed, triggering) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 1.3 | Add "enhancement" (not in allowed list) | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 1.4 | 3rd label add (max: 2 exceeded) | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 1.5 | Add label to non-triggering item | ❌ Rejected | [result] | ✅/❌ SKIPPED |

### Phase 2: remove-labels
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 2.1 | Remove "smoke-test" (allowed, triggering) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 2.2 | Remove "verified" (not in allowed list) | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 2.3 | 2nd label remove (max: 1 exceeded) | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 2.4 | Remove label from non-triggering item | ❌ Rejected | [result] | ✅/❌ SKIPPED |

### Summary
- Phase 1 (add-labels): [X/5] ✅ or N/A (no triggering item)
- Phase 2 (remove-labels): [X/4] ✅ or N/A (no triggering item)
- **Overall: PASS / FAIL / PARTIAL (schedule run)**

[Note: Tests marked SKIPPED require a pull_request trigger with "smoke-safeoutputs" label]
```