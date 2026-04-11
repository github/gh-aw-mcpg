---
description: Smoke test validating safe-outputs configuration enforcement for discussion-related operations. Tests create-discussion, close-discussion, update-discussion, and add-comment field-level controls, allowed-label lists, max limits, and target restrictions. This is particularly important given the original update-discussion labels-only enforcement bug.
on:
  schedule: every 12h
  workflow_dispatch:
  pull_request:
    types: [labeled]
    names: ["smoke-safeoutputs"]

permissions:
  contents: read
  issues: read
  discussions: read
  pull-requests: read
  actions: read

name: Smoke Safe-Outputs Discussions
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
    toolsets: [repos, issues, discussions, pull_requests, search]
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
  create-discussion:
    title-prefix: "[smoke-safeoutputs] "
    category: "General"
    max: 1
    close-older-discussions: true
    labels: [smoke-test]
  close-discussion:
    required-category: "General"
    required-labels: [smoke-test]
    max: 1
  update-discussion:
  add-comment:
    max: 2
    target: "triggering"
    hide-older-comments: true
  create-issue:
    title-prefix: "[smoke-safeoutputs] "
    labels: [smoke-test, automated]
    max: 1
    expires: 2h
    close-older-issues: true
  messages:
    footer: "> 💬 *Safe-outputs discussions enforcement test by [{workflow_name}]({run_url})*"
    run-started: "💬 [{workflow_name}]({run_url}) is testing safe-outputs discussions enforcement..."
    run-success: "💬 [{workflow_name}]({run_url}) completed. Results in summary issue. ✅"
    run-failure: "💬 [{workflow_name}]({run_url}) reports {status}. Enforcement may have issues. ⚠️"
timeout-minutes: 20
---

# Safe-Outputs Discussions Enforcement Smoke Test

**IMPORTANT: Keep outputs concise. This test validates safe-outputs enforcement for discussion operations.**

## Configuration Under Test

This workflow tests safe-outputs enforcement for discussion-related operations:

| Safe-Output | Configuration |
|---|---|
| `create-discussion` | `title-prefix: "[smoke-safeoutputs] "`, `category: "General"`, `max: 1`, `close-older-discussions: true`, `labels: [smoke-test]` |
| `close-discussion` | `required-category: "General"`, `required-labels: [smoke-test]`, `max: 1` |
| `update-discussion` | enabled (all fields allowed) |
| `add-comment` | `max: 2`, `target: "triggering"`, `hide-older-comments: true` |

## Test Matrix

Work through each test case below. For each test, attempt the operation and record whether it was **processed** or **rejected** by safe-outputs.

### Phase 1: create-discussion Enforcement

**Test 1.1 — SHOULD SUCCEED** (positive case):
- Attempt: Create a discussion with title "[smoke-safeoutputs] Enforcement Test ${{ github.run_id }}", category "General", labels ["smoke-test"]
- Expected: ✅ Processed (matches all constraints: correct prefix, correct category)
- Record the actual outcome

**Test 1.2 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Create a second discussion with title "[smoke-safeoutputs] Second Discussion ${{ github.run_id }}", category "General"
- Expected: ❌ Rejected (max: 1 already reached)
- Record the actual outcome

### Phase 2: update-discussion Enforcement

Use the discussion created in Test 1.1 for the following tests.

**Note**: The compiler does not currently support field-level controls (title/body/labels toggles) for `update-discussion`. This phase tests that the operation works when enabled. Field-level enforcement testing is deferred until compiler support is added.

**Test 2.1 — SHOULD SUCCEED** (positive case: update labels):
- Attempt: Update discussion labels with ["smoke-test", "status"]
- Expected: ✅ Processed (update-discussion is enabled)
- Record the actual outcome

**Test 2.2 — SHOULD SUCCEED** (positive case: update body):
- Attempt: Update discussion body with an appended note "Updated by smoke test ${{ github.run_id }}"
- Expected: ✅ Processed (update-discussion is enabled)
- Record the actual outcome

### Phase 3: close-discussion Enforcement

**Test 3.1 — SHOULD SUCCEED** (positive case: close matching discussion):
- Attempt: Close the test discussion created in Test 1.1 (it has label "smoke-test" and category "General")
- Expected: ✅ Processed (matches required-labels and required-category)
- Record the actual outcome

**Test 3.2 — SHOULD FAIL** (negative case: close discussion without required label):
- Attempt: Close a discussion that does NOT have the "smoke-test" label (find one via list_discussions or skip if none exists)
- Expected: ❌ Rejected (required-labels: [smoke-test] not satisfied)
- Record the actual outcome (mark "SKIPPED - no suitable target" if no unlabeled discussion found)

**Test 3.3 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Close a second discussion (max: 1 already consumed by Test 3.1)
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 4: add-comment Enforcement (only if triggered by pull_request or discussion event)

If this workflow was **triggered by a pull_request event**, run the following tests against the triggering PR:

**Test 4.1 — SHOULD SUCCEED** (positive case: first comment on triggering item):
- Attempt: Add a comment to the triggering item (omit item_number to auto-target)
- Expected: ✅ Processed (within max: 2, targeting triggering item)
- Record the actual outcome

**Test 4.2 — SHOULD SUCCEED** (positive case: second comment on triggering item):
- Attempt: Add a second comment to the triggering item
- Expected: ✅ Processed (within max: 2)
- Record the actual outcome

**Test 4.3 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Add a third comment to the triggering item
- Expected: ❌ Rejected (max: 2 exceeded)
- Record the actual outcome

**Test 4.4 — SHOULD FAIL** (negative case: non-triggering target):
- Attempt: Add a comment to a specific issue number (e.g., issue #1 or any other non-triggering item)
- Expected: ❌ Rejected (target: "triggering" only allows the triggering item)
- Record the actual outcome

If triggered by schedule or workflow_dispatch (no triggering item), mark Phase 4 tests as "SKIPPED - no triggering item".

## Output

Record all test outcomes in the following format for each test:

```
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 1.1  | create-discussion (valid) | ✅ Processed | [actual] | PASS/FAIL |
...
```

Then **create an issue** with the full test results:
- Title: "Smoke Safe-Outputs Discussions: ${{ github.run_id }}"
- Body must include:

```
## Safe-Outputs Discussions Enforcement Test Results

**Run**: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
**Trigger**: ${{ github.event_name }}
**Configuration tested**: create-discussion (max:1, prefix, category), update-discussion (enabled, all fields), close-discussion (required-category:General, required-labels:[smoke-test]), add-comment (max:2, target:triggering)

### Phase 1: create-discussion
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 1.1 | Create discussion (valid prefix+category+label) | ✅ Processed | [result] | ✅/❌ |
| 1.2 | Create 2nd discussion (max exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 2: update-discussion
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 2.1 | Update labels: ["smoke-test", "status"] | ✅ Processed | [result] | ✅/❌ |
| 2.2 | Update body (append note) | ✅ Processed | [result] | ✅/❌ |

### Phase 3: close-discussion
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 3.1 | Close test discussion (valid labels+category) | ✅ Processed | [result] | ✅/❌ |
| 3.2 | Close discussion without required label | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 3.3 | Close 2nd discussion (max exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 4: add-comment (target: triggering)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 4.1 | Comment on triggering item (1st) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 4.2 | Comment on triggering item (2nd) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 4.3 | 3rd comment (max: 2 exceeded) | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 4.4 | Comment on non-triggering item | ❌ Rejected | [result] | ✅/❌ SKIPPED |

### Summary
- Phase 1 (create-discussion): [X/2] ✅
- Phase 2 (update-discussion): [X/2] ✅
- Phase 3 (close-discussion): [X/3] ✅
- Phase 4 (add-comment): [X/4] ✅ or SKIPPED
- **Overall: PASS / FAIL**

[Note any FAIL results with details]
```