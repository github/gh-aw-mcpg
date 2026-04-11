---
description: Smoke test validating safe-outputs configuration enforcement for pull request review operations. Tests create-pull-request-review-comment (max limits, target restrictions), submit-pull-request-review (max limits, footer control), reply-to-pull-request-review-comment (max limits), and resolve-pull-request-review-thread (max limits).
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

name: Smoke Safe-Outputs Reviews
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
  create-pull-request-review-comment:
    max: 2
    target: "triggering"
  submit-pull-request-review:
    max: 1
    footer: "if-body"
  reply-to-pull-request-review-comment:
    max: 2
  resolve-pull-request-review-thread:
    max: 2
  create-issue:
    title-prefix: "[smoke-safeoutputs] "
    labels: [smoke-test, automated]
    max: 1
    expires: 2h
    close-older-issues: true
  messages:
    footer: "> 🔍 *Safe-outputs reviews enforcement test by [{workflow_name}]({run_url})*"
    run-started: "🔍 [{workflow_name}]({run_url}) is testing safe-outputs reviews enforcement..."
    run-success: "🔍 [{workflow_name}]({run_url}) completed. Results in summary issue. ✅"
    run-failure: "🔍 [{workflow_name}]({run_url}) reports {status}. Enforcement may have issues. ⚠️"
timeout-minutes: 20
---

# Safe-Outputs Reviews Enforcement Smoke Test

**IMPORTANT: Keep outputs concise. This test validates safe-outputs enforcement for pull request review operations.**

## Configuration Under Test

| Safe-Output | Configuration |
|---|---|
| `create-pull-request-review-comment` | `max: 2`, `target: "triggering"` |
| `submit-pull-request-review` | `max: 1`, `footer: "if-body"` |
| `reply-to-pull-request-review-comment` | `max: 2` |
| `resolve-pull-request-review-thread` | `max: 2` |

## Context

Most review operations require a triggering pull request. If this workflow is triggered by a **pull_request** event, use the triggering PR. If triggered by **schedule** or **workflow_dispatch**:
- Phases 1–4 should be marked "SKIPPED - no triggering PR" unless there is a recent open PR you can use for `submit-pull-request-review` and related tests.
- Phase 5 (summary issue creation) always runs.

## Test Matrix

### Phase 1: create-pull-request-review-comment Enforcement

These tests require the triggering PR to have changed files (diff context). Use the triggering PR if available.

**Test 1.1 — SHOULD SUCCEED** (positive case: create first review comment):
- Context: Only run if triggered by pull_request
- Attempt: Create a review comment on the triggering PR on the first changed file (omit PR number to auto-target), referencing line 1 of the first changed file
- Expected: ✅ Processed (within max: 2, targeting triggering PR)
- Record the actual outcome

**Test 1.2 — SHOULD SUCCEED** (positive case: create second review comment):
- Context: Only run if triggered by pull_request
- Attempt: Create a second review comment on the triggering PR (same or different file/line)
- Expected: ✅ Processed (still within max: 2)
- Record the actual outcome

**Test 1.3 — SHOULD FAIL** (negative case: max exceeded):
- Context: Only run if triggered by pull_request
- Attempt: Create a third review comment on the triggering PR
- Expected: ❌ Rejected (max: 2 exceeded)
- Record the actual outcome

**Test 1.4 — SHOULD FAIL** (negative case: non-triggering PR):
- Context: Only run if triggered by pull_request and another open PR exists
- Attempt: Create a review comment on a specific non-triggering PR number
- Expected: ❌ Rejected (target: "triggering" only allows the triggering PR)
- Record the actual outcome (mark "SKIPPED - no second PR available" if none found)

### Phase 2: submit-pull-request-review Enforcement

**Test 2.1 — SHOULD SUCCEED** (positive case: submit review with body):
- Context: Only run if triggered by pull_request
- Attempt: Submit a pull request review on the triggering PR with a non-empty body "Smoke test review comment — testing safe-outputs enforcement for submit-pull-request-review" and event type "COMMENT"
- Expected: ✅ Processed (within max: 1, footer: "if-body" should append footer since body is non-empty)
- Record the actual outcome

**Test 2.2 — SHOULD SUCCEED** (positive case: submit review without body — footer should be suppressed):
- Context: This is a config behavior test for `footer: "if-body"`. If max allows a second review (it doesn't here), a review with empty body should have no footer. Note this as "CONFIGURATION VERIFIED" if Test 2.1 succeeded — it confirms footer: "if-body" is configured.
- Skip if max: 1 is already consumed.

**Test 2.3 — SHOULD FAIL** (negative case: max exceeded):
- Context: Only run if triggered by pull_request
- Attempt: Submit a second review (max: 1 already consumed by Test 2.1)
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 3: reply-to-pull-request-review-comment Enforcement

These tests require an existing review comment on the triggering PR to reply to.

**Test 3.1 — SHOULD SUCCEED** (positive case: first reply):
- Context: Only run if triggered by pull_request and review comments were created in Phase 1
- Attempt: Reply to the first review comment created in Test 1.1
- Expected: ✅ Processed (within max: 2)
- Record the actual outcome

**Test 3.2 — SHOULD SUCCEED** (positive case: second reply):
- Context: Only run if triggered by pull_request
- Attempt: Reply to the second review comment from Test 1.2 (or the same one)
- Expected: ✅ Processed (still within max: 2)
- Record the actual outcome

**Test 3.3 — SHOULD FAIL** (negative case: max exceeded):
- Context: Only run if triggered by pull_request
- Attempt: A third reply to a review comment
- Expected: ❌ Rejected (max: 2 exceeded)
- Record the actual outcome

### Phase 4: resolve-pull-request-review-thread Enforcement

These tests require unresolved review threads on the triggering PR.

**Test 4.1 — SHOULD SUCCEED** (positive case: resolve first thread):
- Context: Only run if triggered by pull_request and there are unresolved review threads (from Phase 1)
- Attempt: Resolve the review thread created by the first review comment from Phase 1
- Expected: ✅ Processed (within max: 2)
- Record the actual outcome (mark "SKIPPED - no review threads available" if none found)

**Test 4.2 — SHOULD SUCCEED** (positive case: resolve second thread):
- Context: Only run if triggered by pull_request and a second unresolved thread exists
- Attempt: Resolve the second review thread
- Expected: ✅ Processed (still within max: 2)
- Record the actual outcome

**Test 4.3 — SHOULD FAIL** (negative case: max exceeded):
- Context: Only run if triggered by pull_request and a third thread exists
- Attempt: Resolve a third review thread (max: 2 already consumed)
- Expected: ❌ Rejected (max: 2 exceeded)
- Record the actual outcome

## Output

**Create an issue** with the full test results:
- Title: "Smoke Safe-Outputs Reviews: ${{ github.run_id }}"
- Body:

```
## Safe-Outputs Reviews Enforcement Test Results

**Run**: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
**Trigger**: ${{ github.event_name }}
**Configuration**: create-pr-review-comment (max:2, target:triggering), submit-pr-review (max:1, footer:if-body), reply-to-review-comment (max:2), resolve-review-thread (max:2)

### Phase 1: create-pull-request-review-comment (max:2, target:triggering)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 1.1 | Create 1st review comment (triggering PR) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 1.2 | Create 2nd review comment (triggering PR) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 1.3 | Create 3rd review comment (max exceeded) | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 1.4 | Create comment on non-triggering PR | ❌ Rejected | [result] | ✅/❌ SKIPPED |

### Phase 2: submit-pull-request-review (max:1, footer:if-body)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 2.1 | Submit review with body (footer added) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 2.2 | footer: "if-body" behavior verified | CONFIG NOTED | [result] | ✅/❌ SKIPPED |
| 2.3 | Submit 2nd review (max exceeded) | ❌ Rejected | [result] | ✅/❌ SKIPPED |

### Phase 3: reply-to-pull-request-review-comment (max:2)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 3.1 | Reply to 1st review comment | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 3.2 | Reply to 2nd review comment | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 3.3 | 3rd reply (max exceeded) | ❌ Rejected | [result] | ✅/❌ SKIPPED |

### Phase 4: resolve-pull-request-review-thread (max:2)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 4.1 | Resolve 1st review thread | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 4.2 | Resolve 2nd review thread | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 4.3 | Resolve 3rd thread (max exceeded) | ❌ Rejected | [result] | ✅/❌ SKIPPED |

### Summary
- Phase 1 (review-comment): [X/4] ✅ or SKIPPED
- Phase 2 (submit-review): [X/3] ✅ or SKIPPED
- Phase 3 (reply-to-comment): [X/3] ✅ or SKIPPED
- Phase 4 (resolve-thread): [X/3] ✅ or SKIPPED
- **Overall: PASS / FAIL / PARTIAL (schedule run)**

[Note: Most tests require pull_request trigger with "smoke-safeoutputs" label to fully execute]
```