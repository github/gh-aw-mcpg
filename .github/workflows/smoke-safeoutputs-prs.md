---
description: Smoke test validating safe-outputs configuration enforcement for pull request operations. Tests create-pull-request (max limits, title-prefix, draft mode), close-pull-request (required-labels, required-title-prefix), update-pull-request (field-level controls), push-to-pull-request-branch (target restrictions), mark-pull-request-as-ready-for-review (required-labels), and add-reviewer (allowlist).
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

name: Smoke Safe-Outputs PRs
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
    - "git"
    - "mkdir"
sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg"
safe-outputs:
  threat-detection:
    enabled: false
  create-pull-request:
    title-prefix: "[smoke-safeoutputs] "
    labels: [smoke-test]
    draft: true
    max: 1
  close-pull-request:
    required-labels: [smoke-test]
    required-title-prefix: "[smoke-safeoutputs]"
    max: 1
  update-pull-request:
    title: true
    body: false
    max: 1
  push-to-pull-request-branch:
    target: "triggering"
    title-prefix: "[smoke-safeoutputs]"
  mark-pull-request-as-ready-for-review:
    required-labels: [smoke-test]
    max: 1
  add-reviewer:
    reviewers: [copilot]
    max: 1
  create-issue:
    title-prefix: "[smoke-safeoutputs] "
    labels: [smoke-test, automated]
    max: 1
    expires: 2h
    close-older-issues: true
  messages:
    footer: "> 🔀 *Safe-outputs PRs enforcement test by [{workflow_name}]({run_url})*"
    run-started: "🔀 [{workflow_name}]({run_url}) is testing safe-outputs PRs enforcement..."
    run-success: "🔀 [{workflow_name}]({run_url}) completed. Results in summary issue. ✅"
    run-failure: "🔀 [{workflow_name}]({run_url}) reports {status}. Enforcement may have issues. ⚠️"
timeout-minutes: 20
---

# Safe-Outputs Pull Requests Enforcement Smoke Test

**IMPORTANT: Keep outputs concise. This test validates safe-outputs enforcement for pull request operations.**

## Configuration Under Test

| Safe-Output | Configuration |
|---|---|
| `create-pull-request` | `title-prefix: "[smoke-safeoutputs] "`, `labels: [smoke-test]`, `draft: true`, `max: 1` |
| `close-pull-request` | `required-labels: [smoke-test]`, `required-title-prefix: "[smoke-safeoutputs]"`, `max: 1` |
| `update-pull-request` | `title: true`, `body: false`, `max: 1` |
| `push-to-pull-request-branch` | `target: "triggering"`, `title-prefix: "[smoke-safeoutputs]"` |
| `mark-pull-request-as-ready-for-review` | `required-labels: [smoke-test]`, `max: 1` |
| `add-reviewer` | `reviewers: [copilot]`, `max: 1` |

## Context

- **If triggered by pull_request**: The triggering PR is available for `push-to-pull-request-branch`, `mark-pull-request-as-ready-for-review`, `add-reviewer`, and `update-pull-request` tests.
- **If triggered by schedule or workflow_dispatch**: Tests requiring a triggering PR should be marked "SKIPPED - no triggering item". The `create-pull-request` test creates a new PR which can be used for some subsequent tests.

## Test Matrix

### Phase 1: create-pull-request Enforcement

**Test 1.1 — SHOULD SUCCEED** (positive case: create valid draft PR):
- Prerequisite: Use bash to create a test branch and add a test file:
  ```bash
  git checkout -b smoke-safeoutputs-test-${{ github.run_id }}
  mkdir -p /tmp/smoke-pr-test
  echo "Smoke test file for run ${{ github.run_id }}" > /tmp/smoke-pr-test/smoke-test.txt
  ```
- Attempt: Create a pull request with title "[smoke-safeoutputs] Test PR ${{ github.run_id }}", targeting the changes in the test branch
- Expected: ✅ Processed (matches prefix, creates as draft with smoke-test label)
- Record the actual outcome and the new PR number if created

**Test 1.2 — SHOULD FAIL** (negative case: create PR without required title prefix):
- Attempt: Create a pull request with title "No prefix PR — should be rejected ${{ github.run_id }}"
- Expected: ❌ Rejected (title does not start with "[smoke-safeoutputs] " prefix)
- Record the actual outcome

**Test 1.3 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Create a second PR with title "[smoke-safeoutputs] Second PR ${{ github.run_id }}"
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 2: update-pull-request Enforcement

Use the PR created in Test 1.1 (or the triggering PR if triggered by pull_request).

**Test 2.1 — SHOULD SUCCEED** (positive case: update title):
- Attempt: Update the PR title to "[smoke-safeoutputs] Test PR (updated) ${{ github.run_id }}"
- Expected: ✅ Processed (title: true is configured)
- Record the actual outcome

**Test 2.2 — SHOULD FAIL** (negative case: update body — body: false):
- Attempt: Update the PR body with "This body update should be rejected by safe-outputs"
- Expected: ❌ Rejected (body: false in update-pull-request config)
- Record the actual outcome

**Test 2.3 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: A second update-pull-request operation
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 3: push-to-pull-request-branch Enforcement

**Test 3.1 — SHOULD SUCCEED** (positive case: push to triggering PR with matching prefix):
- Context: Only run if triggered by pull_request AND the triggering PR title starts with "[smoke-safeoutputs]"
- Attempt: Push a small change to the triggering PR branch (omit PR number to auto-target)
- Expected: ✅ Processed (target is triggering, prefix matches)
- Record the actual outcome

**Test 3.2 — SHOULD FAIL** (negative case: push to non-triggering PR):
- Context: Only run if triggered by pull_request and there's another open PR available
- Attempt: Push to a different PR by specifying an explicit PR number that is NOT the triggering PR
- Expected: ❌ Rejected (target: "triggering" only allows the triggering PR)
- Record the actual outcome (mark "SKIPPED - no second PR available" if no other PR found)

**Test 3.3 — SHOULD FAIL** (negative case: push to PR without matching title prefix):
- Context: Only run if a PR without "[smoke-safeoutputs]" prefix exists
- Attempt: Push to a PR whose title does not start with "[smoke-safeoutputs]"
- Expected: ❌ Rejected (title-prefix: "[smoke-safeoutputs]" not matched)
- Record the actual outcome (mark "SKIPPED - no suitable PR" if none found)

### Phase 4: mark-pull-request-as-ready-for-review Enforcement

**Test 4.1 — SHOULD SUCCEED** (positive case: mark PR with required label as ready):
- Context: Use the PR created in Test 1.1 which has the "smoke-test" label (or the triggering PR if it has "smoke-test")
- Attempt: Mark the test PR as ready for review
- Expected: ✅ Processed (has required-labels: [smoke-test], within max: 1)
- Record the actual outcome

**Test 4.2 — SHOULD FAIL** (negative case: mark PR without required label):
- Context: Find a PR that does NOT have "smoke-test" label
- Attempt: Mark that PR as ready for review
- Expected: ❌ Rejected (required-labels: [smoke-test] not satisfied)
- Record the actual outcome (mark "SKIPPED - no suitable PR" if none found)

**Test 4.3 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Mark a second PR as ready for review
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 5: add-reviewer Enforcement

**Test 5.1 — SHOULD SUCCEED** (positive case: add allowed reviewer):
- Context: Use the triggering PR or the PR created in Test 1.1
- Attempt: Add reviewer "copilot" to the PR
- Expected: ✅ Processed (reviewer "copilot" is in allowed reviewers list)
- Record the actual outcome

**Test 5.2 — SHOULD FAIL** (negative case: add non-allowed reviewer):
- Context: Use the same PR
- Attempt: Add a reviewer other than "copilot" (e.g., any GitHub user that is not "copilot")
- Expected: ❌ Rejected (only "copilot" is in allowed reviewers list)
- Record the actual outcome

**Test 5.3 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Add a second reviewer (max: 1 already consumed by Test 5.1)
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 6: close-pull-request Enforcement

**Test 6.1 — SHOULD SUCCEED** (positive case: close PR with required label and prefix):
- Attempt: Close the PR created in Test 1.1 (it has "smoke-test" label and title starts with "[smoke-safeoutputs]")
- Expected: ✅ Processed (matches required-labels and required-title-prefix)
- Record the actual outcome

**Test 6.2 — SHOULD FAIL** (negative case: close PR without required label):
- Attempt: Close a PR that does NOT have the "smoke-test" label
- Expected: ❌ Rejected (required-labels: [smoke-test] not satisfied)
- Record the actual outcome (mark "SKIPPED - no suitable PR" if none found)

**Test 6.3 — SHOULD FAIL** (negative case: close PR without required title prefix):
- Attempt: Close a PR whose title does NOT start with "[smoke-safeoutputs]"
- Expected: ❌ Rejected (required-title-prefix: "[smoke-safeoutputs]" not satisfied)
- Record the actual outcome (mark "SKIPPED - no suitable PR" if none found)

**Test 6.4 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Close a second PR
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

## Output

**Create an issue** with the full test results:
- Title: "Smoke Safe-Outputs PRs: ${{ github.run_id }}"
- Body:

```
## Safe-Outputs Pull Requests Enforcement Test Results

**Run**: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
**Trigger**: ${{ github.event_name }}
**Configuration**: create-pull-request (max:1, prefix, draft:true), close-pull-request (required-labels, required-prefix, max:1), update-pull-request (title:true, body:false, max:1), push-to-pr-branch (target:triggering, prefix), mark-ready (required-labels:[smoke-test], max:1), add-reviewer (reviewers:[copilot], max:1)

### Phase 1: create-pull-request
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 1.1 | Create draft PR (valid prefix) | ✅ Processed | [result] | ✅/❌ |
| 1.2 | Create PR without prefix | ❌ Rejected | [result] | ✅/❌ |
| 1.3 | Create 2nd PR (max exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 2: update-pull-request (title:true, body:false)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 2.1 | Update title (allowed) | ✅ Processed | [result] | ✅/❌ |
| 2.2 | Update body (body: false) | ❌ Rejected | [result] | ✅/❌ |
| 2.3 | 2nd update (max: 1 exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 3: push-to-pull-request-branch (target:triggering)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 3.1 | Push to triggering PR (matching prefix) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 3.2 | Push to non-triggering PR | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 3.3 | Push to PR without matching prefix | ❌ Rejected | [result] | ✅/❌ SKIPPED |

### Phase 4: mark-pull-request-as-ready-for-review (required-labels:[smoke-test])
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 4.1 | Mark PR with smoke-test label as ready | ✅ Processed | [result] | ✅/❌ |
| 4.2 | Mark PR without required label as ready | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 4.3 | 2nd mark-as-ready (max: 1 exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 5: add-reviewer (reviewers:[copilot])
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 5.1 | Add reviewer "copilot" (allowed) | ✅ Processed | [result] | ✅/❌ |
| 5.2 | Add non-allowed reviewer | ❌ Rejected | [result] | ✅/❌ |
| 5.3 | Add 2nd reviewer (max: 1 exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 6: close-pull-request (required-labels, required-prefix)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 6.1 | Close PR with required label+prefix | ✅ Processed | [result] | ✅/❌ |
| 6.2 | Close PR without required label | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 6.3 | Close PR without required prefix | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 6.4 | 2nd close (max: 1 exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Summary
- Phase 1 (create-pull-request): [X/3] ✅
- Phase 2 (update-pull-request): [X/3] ✅
- Phase 3 (push-to-pr-branch): [X/3] ✅ or SKIPPED
- Phase 4 (mark-ready): [X/3] ✅
- Phase 5 (add-reviewer): [X/3] ✅
- Phase 6 (close-pull-request): [X/4] ✅
- **Overall: PASS / FAIL**
```