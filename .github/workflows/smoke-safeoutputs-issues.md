---
description: Smoke test validating safe-outputs configuration enforcement for issue-related operations. Tests create-issue (max limits, title-prefix, labels), close-issue (required-labels, required-title-prefix), update-issue (field-level controls), link-sub-issue (prefix restrictions), and assign-milestone (allowlists).
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

name: Smoke Safe-Outputs Issues
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
  create-issue:
    title-prefix: "[smoke-safeoutputs] "
    labels: [smoke-test, automated]
    max: 3
    expires: 2h
    close-older-issues: true
  close-issue:
    required-labels: [smoke-test]
    required-title-prefix: "[smoke-safeoutputs]"
    max: 1
  update-issue:
    body:
    max: 1
  link-sub-issue:
    parent-title-prefix: "[smoke-safeoutputs]"
    sub-title-prefix: "[smoke-safeoutputs]"
    max: 1
  assign-milestone:
    allowed: [v1.0]
    max: 1
  messages:
    footer: "> 🐛 *Safe-outputs issues enforcement test by [{workflow_name}]({run_url})*"
    run-started: "🐛 [{workflow_name}]({run_url}) is testing safe-outputs issues enforcement..."
    run-success: "🐛 [{workflow_name}]({run_url}) completed. Results in summary issue. ✅"
    run-failure: "🐛 [{workflow_name}]({run_url}) reports {status}. Enforcement may have issues. ⚠️"
timeout-minutes: 20
---

# Safe-Outputs Issues Enforcement Smoke Test

**IMPORTANT: Keep outputs concise. This test validates safe-outputs enforcement for issue operations.**

## Configuration Under Test

| Safe-Output | Configuration |
|---|---|
| `create-issue` | `title-prefix: "[smoke-safeoutputs] "`, `labels: [smoke-test, automated]`, `max: 3`, `expires: 2h`, `close-older-issues: true` |
| `close-issue` | `required-labels: [smoke-test]`, `required-title-prefix: "[smoke-safeoutputs]"`, `max: 1` |
| `update-issue` | `body:` (enabled), `max: 1` |
| `link-sub-issue` | `parent-title-prefix: "[smoke-safeoutputs]"`, `sub-title-prefix: "[smoke-safeoutputs]"`, `max: 1` |
| `assign-milestone` | `allowed: [v1.0]`, `max: 1` |

**Note**: `create-issue` has `max: 3` to allow creating test issues plus the summary results issue (which also uses `create-issue`).

## Test Matrix

Work through each test case. For each test, attempt the operation and record whether it was **processed** or **rejected**.

### Phase 1: create-issue Enforcement

**Test 1.1 — SHOULD SUCCEED** (positive case: create valid issue):
- Attempt: Create an issue with title "[smoke-safeoutputs] Parent Issue ${{ github.run_id }}", body "Test parent issue for smoke-safeoutputs enforcement test", temporary_id "aw_parent1"
- Expected: ✅ Processed (matches prefix constraint, gets required labels automatically)
- Record the actual outcome

**Test 1.2 — SHOULD SUCCEED** (positive case: create valid sub-issue):
- Attempt: Create an issue with title "[smoke-safeoutputs] Sub Issue ${{ github.run_id }}", body "Test sub-issue for smoke-safeoutputs enforcement test", temporary_id "aw_sub1"
- Expected: ✅ Processed (matches prefix constraint, gets required labels automatically)
- Record the actual outcome

**Test 1.3 — SHOULD FAIL** (negative case: create issue without required title prefix):
- Attempt: Create an issue with title "No prefix issue — should be rejected ${{ github.run_id }}"
- Expected: ❌ Rejected (title does not start with "[smoke-safeoutputs] " prefix)
- Record the actual outcome

**Test 1.4 — SHOULD FAIL** (negative case: max exceeded):
- Note: max is 3 and 2 issues + 1 summary issue = 3. Attempt one more creation beyond that limit.
- Attempt: Create one additional issue titled "[smoke-safeoutputs] Overflow Issue ${{ github.run_id }}" after the results issue creation
- Expected: ❌ Rejected (max: 3 exceeded)
- Record the actual outcome (test this last, after creating the results issue)

### Phase 2: update-issue Enforcement

Use the parent issue from Test 1.1 for the following tests.

**Test 2.1 — SHOULD SUCCEED** (positive case: update body):
- Attempt: Update the body of the parent issue to append "Updated by smoke test ${{ github.run_id }}"
- Expected: ✅ Processed (body: is enabled in update-issue config)
- Record the actual outcome

**Test 2.2 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: A second update-issue operation (max: 1 already consumed by Test 2.1)
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 3: link-sub-issue Enforcement

**Test 3.1 — SHOULD SUCCEED** (positive case: link matching issues):
- Attempt: Link sub-issue (aw_sub1) as a sub-issue of parent (aw_parent1)
- Expected: ✅ Processed (both parent and sub-issue match prefix "[smoke-safeoutputs]")
- Record the actual outcome

**Test 3.2 — SHOULD FAIL** (negative case: link with non-matching prefix):
- Attempt: Find any open issue that does NOT have the "[smoke-safeoutputs]" prefix and try to link it
- Expected: ❌ Rejected (parent-title-prefix or sub-title-prefix not matched)
- Record the actual outcome (mark "SKIPPED - no suitable target" if no suitable issue found)

**Test 3.3 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Link a second pair of issues (max: 1 already consumed by Test 3.1)
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 4: close-issue Enforcement

**Test 4.1 — SHOULD SUCCEED** (positive case: close matching issue):
- Attempt: Close the sub-issue from Test 1.2 (it has "smoke-test" label and title starts with "[smoke-safeoutputs]")
- Expected: ✅ Processed (matches required-labels and required-title-prefix)
- Record the actual outcome

**Test 4.2 — SHOULD FAIL** (negative case: close issue without required label):
- Attempt: Close any open issue that does NOT have the "smoke-test" label
- Expected: ❌ Rejected (required-labels: [smoke-test] not satisfied)
- Record the actual outcome (mark "SKIPPED - no suitable target" if none found)

**Test 4.3 — SHOULD FAIL** (negative case: close issue without required title prefix):
- Attempt: Close any open issue that does NOT have "[smoke-safeoutputs]" prefix
- Expected: ❌ Rejected (required-title-prefix: "[smoke-safeoutputs]" not satisfied)
- Record the actual outcome (mark "SKIPPED - no suitable target" if none found)

**Test 4.4 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: Close a second issue (max: 1 already consumed by Test 4.1)
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

### Phase 5: assign-milestone Enforcement

**Test 5.1 — SHOULD SUCCEED** (positive case: assign allowed milestone):
- Attempt: Assign milestone "v1.0" to the parent issue from Test 1.1 (if "v1.0" milestone exists in the repository)
- Expected: ✅ Processed (milestone "v1.0" is in allowed list)
- Record the actual outcome (mark "SKIPPED - milestone not found" if v1.0 milestone doesn't exist)

**Test 5.2 — SHOULD FAIL** (negative case: assign non-allowed milestone):
- Attempt: Assign a milestone that is NOT "v1.0" (e.g., "v2.0" or any other milestone if one exists)
- Expected: ❌ Rejected (only "v1.0" is in allowed list)
- Record the actual outcome (mark "SKIPPED - no alternative milestone" if no other milestone exists)

**Test 5.3 — SHOULD FAIL** (negative case: max exceeded):
- Attempt: A second assign-milestone operation (max: 1 already consumed by Test 5.1)
- Expected: ❌ Rejected (max: 1 exceeded)
- Record the actual outcome

## Output

**Create an issue** with the full test results using the following format:
- Title: "Smoke Safe-Outputs Issues: ${{ github.run_id }}"
- Body:

```
## Safe-Outputs Issues Enforcement Test Results

**Run**: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
**Trigger**: ${{ github.event_name }}
**Configuration**: create-issue (max:3, prefix), close-issue (required-labels, required-prefix, max:1), update-issue (body enabled, max:1), link-sub-issue (prefix restrictions), assign-milestone (allowed:[v1.0])

### Phase 1: create-issue
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 1.1 | Create parent issue (valid prefix) | ✅ Processed | [result] | ✅/❌ |
| 1.2 | Create sub-issue (valid prefix) | ✅ Processed | [result] | ✅/❌ |
| 1.3 | Create issue without prefix | ❌ Rejected | [result] | ✅/❌ |
| 1.4 | Create 4th issue (max exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 2: update-issue (body enabled, max:1)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 2.1 | Update body (allowed) | ✅ Processed | [result] | ✅/❌ |
| 2.2 | 2nd update (max: 1 exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 3: link-sub-issue (prefix restrictions)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 3.1 | Link matching issues (valid prefix) | ✅ Processed | [result] | ✅/❌ |
| 3.2 | Link non-matching prefix issue | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 3.3 | 2nd link (max: 1 exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 4: close-issue (required-labels, required-prefix)
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 4.1 | Close matching issue (valid label+prefix) | ✅ Processed | [result] | ✅/❌ |
| 4.2 | Close issue without required label | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 4.3 | Close issue without required prefix | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 4.4 | 2nd close (max: 1 exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Phase 5: assign-milestone (allowed:[v1.0])
| Test | Operation | Expected | Actual | Status |
|------|-----------|----------|--------|--------|
| 5.1 | Assign milestone "v1.0" (allowed) | ✅ Processed | [result] | ✅/❌ SKIPPED |
| 5.2 | Assign non-allowed milestone | ❌ Rejected | [result] | ✅/❌ SKIPPED |
| 5.3 | 2nd milestone assignment (max exceeded) | ❌ Rejected | [result] | ✅/❌ |

### Summary
- Phase 1 (create-issue): [X/4] ✅
- Phase 2 (update-issue): [X/2] ✅
- Phase 3 (link-sub-issue): [X/3] ✅
- Phase 4 (close-issue): [X/4] ✅
- Phase 5 (assign-milestone): [X/3] ✅
- **Overall: PASS / FAIL**
```