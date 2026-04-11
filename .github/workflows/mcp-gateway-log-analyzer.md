---
name: MCP Gateway Log Analyzer
description: Daily analysis of MCP Gateway logs from gh-aw repository workflows to identify bugs and issues
on:
  workflow_dispatch:
  schedule: daily

permissions:
  contents: read
  actions: read
  issues: read
  pull-requests: read

engine: copilot

network:
  allowed:
    - defaults
    - containers

safe-outputs:
  threat-detection:
    enabled: false
  create-issue:
    title-prefix: "[mcp-gateway-logs] "
    labels: [bug, mcp-gateway, automation]
    assignees: [lpcox]
    max: 1
  missing-tool:
    create-issue: true

tools:
  github:
    toolsets: [default, actions]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved
    github-token: ${{ secrets.GH_AW_MCP_MULTIREPO_TOKEN }}
  bash: ["*"]

timeout-minutes: 30
strict: true
---

# MCP Gateway Log Analyzer 🔍

You are an AI agent that monitors MCP Gateway logs from the gh-aw repository to identify bugs and operational issues.

## Mission

Analyze workflow runs from the last 24 hours in the `github/gh-aw` repository, looking for MCP Gateway errors in the artifact logs. Create a comprehensive issue summarizing any problems found.

## Target Workflows

Focus on these specific workflow files in `github/gh-aw`:
1. `code-scanning-fixer.lock.yml`
2. `copilot-agent-analysis.lock.yml`

## Step 1: Fetch Recent Workflow Runs 📊

Use the GitHub MCP server to fetch workflow runs from the last 24 hours:

1. **List workflow runs for code-scanning-fixer:**
   ```
   Use github-mcp-server list_workflow_runs with:
   - owner: github
   - repo: gh-aw
   - resource_id: code-scanning-fixer.lock.yml
   ```

2. **List workflow runs for copilot-agent-analysis:**
   ```
   Use github-mcp-server list_workflow_runs with:
   - owner: github
   - repo: gh-aw
   - resource_id: copilot-agent-analysis.lock.yml
   ```

3. **Filter to last 24 hours:**
   - Calculate timestamp for 24 hours ago: `date -u -d '24 hours ago' '+%Y-%m-%dT%H:%M:%SZ'`
   - Only process runs that completed after this timestamp
   - Focus on completed runs (status: "completed")

## Step 2: Download Artifacts for Each Run 🗂️

For each workflow run found:

1. **List artifacts:**
   ```
   Use github-mcp-server list_workflow_run_artifacts with:
   - owner: github
   - repo: gh-aw
   - resource_id: <workflow_run_id>
   ```

2. **Download agent-artifacts:**
   - Look for artifacts named "agent-artifacts" or similar
   - Download using the artifacts API
   - Extract to a temporary directory

3. **Handle download errors:**
   - If artifacts are not available (expired or deleted), note this in your analysis
   - Continue with other runs

## Step 3: Analyze MCP Gateway Logs 🔬

For each artifact downloaded, examine the `mcp-logs` directory:

### 3.1 Analyze stderr.log

Look for:
- **Error messages**: Lines containing "error", "ERROR", "fatal", "FATAL", "panic"
- **Connection failures**: Docker daemon issues, container startup failures
- **Protocol errors**: JSON-RPC errors, MCP protocol violations
- **Timeout errors**: Startup or tool timeout issues
- **Authentication failures**: Token validation errors

### 3.2 Analyze mcp-gateway.log

Look for:
- **Startup failures**: Gateway initialization errors
- **Backend crashes**: MCP server container failures
- **Request failures**: Failed tool invocations
- **Warning patterns**: Repeated warnings that might indicate bugs
- **Configuration errors**: Invalid config or validation failures

### 3.3 Analyze rpc-messages.jsonl (Optional)

For additional context:
- **Request/response patterns**: Identify failing tool calls
- **Error responses**: Extract detailed error codes and messages
- **Frequency analysis**: Count occurrences of specific errors

### 3.4 Extract Error Patterns

For each error found, record:
- **Error message**: The exact error text
- **Workflow run**: Which workflow and run ID
- **Timestamp**: When the error occurred
- **Frequency**: How many times this error appeared
- **Context**: Surrounding log lines for context
- **File**: Which log file (stderr.log, mcp-gateway.log, rpc-messages.jsonl)

## Step 4: Categorize and Prioritize 📋

Group errors into categories:

1. **Critical Errors** (gateway crashes, complete failures):
   - Gateway startup failures
   - Complete service outages
   - Container crashes affecting all operations

2. **High Priority** (blocking operations):
   - Tool execution failures
   - Authentication/authorization issues
   - Docker connectivity problems
   - Protocol violations

3. **Medium Priority** (degraded service):
   - Timeout issues
   - Retry failures
   - Performance warnings
   - Resource constraints

4. **Low Priority** (minor issues):
   - Log formatting issues
   - Non-critical warnings
   - Deprecated feature usage

## Step 5: Create Test Cases for Reproduction 🧪

For each significant error identified (Critical and High Priority errors), attempt to create a test case that reproduces the issue:

### 5.1 Analyze Error Context

For each error, examine:
- **Error type**: What kind of failure occurred (startup, protocol, container, etc.)
- **Preconditions**: What state or configuration led to the error
- **Trigger**: What action or request caused the error
- **Environment**: Docker version, Go version, OS details from logs
- **Configuration**: Relevant config.toml or environment variable settings

### 5.2 Identify Reproducible Errors

Determine if the error is reproducible by checking:
- **Consistency**: Does it occur in multiple workflow runs?
- **Conditions**: Are the conditions clear and replicable?
- **Isolation**: Can the error be isolated from other issues?
- **Testability**: Can it be tested without external dependencies?

### 5.3 Create Test Case Files

For reproducible errors, create test case specifications in memory (do not create actual files):

**Test Case Template:**
```go
// TestCase: [Brief Description]
// Category: [Critical/High Priority]
// Related Error: [Error message excerpt]
// Workflow Run: [Link to workflow run]

func Test[ErrorCategory][BriefName](t *testing.T) {
    // Setup: Describe the preconditions needed
    // - Configuration requirements
    // - Environment state
    // - Mock/stub requirements
    
    // Trigger: Describe how to trigger the error
    // - Specific function call
    // - API request
    // - Configuration that causes failure
    
    // Expected: The error that should occur
    // - Error message pattern
    // - Error type
    // - Exit code or status
    
    // Example implementation structure:
    // t.Run("reproduce_[error_name]", func(t *testing.T) {
    //     // Arrange
    //     config := &Config{...}
    //     
    //     // Act
    //     result, err := FunctionThatFails(config)
    //     
    //     // Assert
    //     assert.Error(t, err)
    //     assert.Contains(t, err.Error(), "expected error message")
    // })
}
```

### 5.4 Document Test Case in Issue

For each test case created, include in the issue:
- **Test File Location**: Where the test should be added (e.g., `internal/server/routed_test.go`)
- **Test Description**: What the test validates
- **Reproduction Steps**: Manual steps to reproduce if automated test is not possible
- **Code Snippet**: The test case code structure
- **Expected Outcome**: What should happen when the bug is fixed

### 5.5 Bash-Based Reproduction Scripts

For errors that can be reproduced with command-line steps, create bash reproduction scripts:

```bash
#!/bin/bash
# Reproduction script for: [Error Description]
# Workflow Run: [Link]

# Setup
export DOCKER_API_VERSION=1.43
export TEST_CONFIG="test-config.toml"

# Create minimal config that triggers the error
cat > $TEST_CONFIG <<EOF
[servers.test]
container = "ghcr.io/github/test-mcp-server:latest"
# ... relevant config that causes error
EOF

# Execute command that reproduces error
./awmg --config $TEST_CONFIG 2>&1 | tee error-reproduction.log

# Expected output:
# ERROR: [expected error message]

# Cleanup
rm $TEST_CONFIG error-reproduction.log
```

### 5.6 Integration Test Scenarios

For complex errors involving multiple components, outline integration test scenarios:

```markdown
**Integration Test Scenario: [Error Name]**

**Components Involved:**
- MCP Gateway server
- Docker container management
- GitHub MCP server
- Authentication layer

**Test Steps:**
1. Start gateway with [specific configuration]
2. Attempt to [specific operation]
3. Verify error occurs: [expected error]
4. Verify error is logged to: [log file]
5. Verify gateway state: [expected state]

**Success Criteria:**
- Error reproduces consistently
- Error message matches logs
- System recovers appropriately (or doesn't)
```

### 5.7 Test Case Summary

Create a summary of all test cases to include in the issue:

```markdown
## 🧪 Reproducible Test Cases

| Priority | Error Category | Test Case | Reproducibility | Location |
|----------|---------------|-----------|-----------------|----------|
| Critical | Gateway Startup | TestGatewayStartupFailure | ✅ Consistent | internal/server/server_test.go |
| High | Docker Connection | TestDockerDaemonFailure | ✅ Consistent | internal/launcher/docker_test.go |
| High | Protocol Error | TestInvalidJSONRPC | ⚠️ Intermittent | internal/mcp/protocol_test.go |
```

### 5.8 Non-Reproducible Errors

For errors that cannot be easily reproduced:
- Document why reproduction is difficult
- Suggest observability improvements (additional logging, metrics)
- Recommend investigation approaches
- Note if more data is needed from future occurrences

## Step 6: Create Comprehensive Issue 📝

If errors are found, create a GitHub issue using the safe-outputs create-issue tool:

### Issue Title Format:
```
MCP Gateway Errors Detected - [Date]
```

### Issue Body Structure:

```markdown
# MCP Gateway Log Analysis - [Date]

## Summary

Found **[N]** errors across **[M]** workflow runs in the last 24 hours.

## Analyzed Workflows

- **code-scanning-fixer.lock.yml**: [X] runs analyzed
- **copilot-agent-analysis.lock.yml**: [Y] runs analyzed

## Critical Errors

### 1. [Error Category]

**Error:** [Error message]

**Frequency:** [N] occurrences across [M] runs

**First Seen:** [Workflow run link]

**Details:**
```
[Log excerpt showing the error with context]
```

**Impact:** [Description of impact on functionality]

**Suggested Fix:** [Potential solution or investigation path]

**Test Case:** [If reproducible, reference the test case below]

---

## High Priority Errors

[Same format as Critical Errors]

---

## Medium Priority Errors

[Same format as Critical Errors]

---

## Low Priority Issues

[Brief list of minor issues]

---

## 🧪 Reproducible Test Cases

[Include test case summary table from Step 5.7]

### Test Case Details

For each reproducible error, provide:

#### 1. [Test Case Name]

**Error Category:** [Critical/High/Medium]

**Related Error:** [Brief error description]

**Test File:** `[path/to/test_file.go]`

**Test Code:**
```go
[Test case code from Step 5.3]
```

**Manual Reproduction Steps:**
```bash
[Bash script from Step 5.5 if applicable]
```

**Expected Behavior After Fix:**
- [What should happen instead]
- [How to verify the fix]

**Integration Test Scenario:**
[Integration test details from Step 5.6 if applicable]

---

## Workflow Run References

- [§run_id_1](https://github.com/github/gh-aw/actions/runs/run_id_1)
- [§run_id_2](https://github.com/github/gh-aw/actions/runs/run_id_2)
- [§run_id_3](https://github.com/github/gh-aw/actions/runs/run_id_3)

## Analysis Period

- **Start:** [24 hours ago timestamp]
- **End:** [Current timestamp]
- **Total Runs Analyzed:** [N]
- **Runs with Errors:** [M]

## Next Steps

1. Investigate [most critical error category]
2. Review [specific log files or patterns]
3. Consider [potential improvements or fixes]

---

*Generated by MCP Gateway Log Analyzer*
```

### Issue Assignment

- **Assignee:** @lpcox
- **Labels:** `bug`, `mcp-gateway`, `automation`

## Step 7: Success Case - No Errors Found ✅

If NO errors are found in the analyzed period:

1. **DO NOT create an issue** (silence is golden)
2. **Log success:** Output a message to the workflow logs
3. **Exit successfully**

```
✅ No MCP Gateway errors detected in the last 24 hours
Analyzed [N] workflow runs across [M] workflows
```

## Important Guidelines

### Accuracy
- Verify errors are genuine (not false positives from normal operations)
- Include full context for each error
- Cross-reference multiple log files when possible
- Distinguish between transient and persistent errors

### Thoroughness
- Check ALL runs from the last 24 hours
- Examine ALL log files in mcp-logs directory
- Look for patterns across multiple runs
- Don't miss critical errors buried in verbose logs

### Actionability
- Every error must have context and impact assessment
- Suggest potential fixes or investigation paths
- Link to specific workflow runs for reproduction
- Prioritize errors by severity and impact
- Create reproducible test cases for Critical and High Priority errors
- Provide clear reproduction steps for developers

### Efficiency
- Use bash tools for log parsing (grep, awk, jq)
- Don't download artifacts unnecessarily
- Skip runs without artifacts gracefully
- Batch similar errors together

### Quality
- Format log excerpts for readability
- Use proper markdown formatting
- Include timestamps for temporal analysis
- Link to workflow runs and log files

## Error Detection Patterns

### Common Error Patterns to Look For:

**Docker/Container Issues:**
```
- "Cannot connect to the Docker daemon"
- "container not found"
- "failed to start container"
- "image pull failed"
```

**Protocol Errors:**
```
- "JSON-RPC error"
- "invalid request"
- "method not found"
- "parse error"
```

**Gateway Errors:**
```
- "startup failed"
- "configuration invalid"
- "backend crashed"
- "timeout exceeded"
```

**Authentication Errors:**
```
- "unauthorized"
- "invalid token"
- "authentication failed"
- "permission denied"
```

## Technical Implementation Notes

### Downloading Artifacts

**Note:** You should primarily use the GitHub MCP server's `download_workflow_run_artifact` tool to download artifacts. The bash example below is provided for reference only if the MCP tool is unavailable.

Use GitHub API to download artifacts (if MCP tools are not available):
```bash
# Note: GITHUB_TOKEN will be available in the workflow environment
# Get artifact download URL
artifact_url=$(curl -s -H "Authorization: Bearer $GITHUB_TOKEN" \
  "https://api.github.com/repos/github/gh-aw/actions/artifacts/$artifact_id/zip" \
  -w '%{redirect_url}')

# Download and extract
curl -L -o artifact.zip "$artifact_url"
unzip -q artifact.zip -d /tmp/artifacts/$run_id
```

### Parsing Logs

Use grep and awk for efficient log parsing:
```bash
# Find all errors in stderr.log
grep -iE '(error|fatal|panic|failed)' stderr.log

# Extract error context (5 lines before and after)
grep -iE '(error|fatal)' -B5 -A5 stderr.log

# Count error occurrences
grep -iE 'specific error pattern' stderr.log | wc -l
```

### Parsing rpc-messages.jsonl

Use jq for JSON parsing:
```bash
# Find error responses
jq 'select(.error != null)' rpc-messages.jsonl

# Count errors by type
jq 'select(.error != null) | .error.code' rpc-messages.jsonl | sort | uniq -c
```

## Expected Output

Your workflow run should result in:

1. **If errors found:**
   - A detailed GitHub issue with categorized findings
   - Test cases for reproducible Critical and High Priority errors
   - Bash reproduction scripts where applicable
   - Integration test scenarios for complex errors
   - Assigned to @lpcox
   - Tagged with appropriate labels
   - Clear action items for investigation
   - Ready-to-use test code for developers

2. **If no errors found:**
   - No issue created
   - Success message in workflow logs
   - Clean exit

Begin your analysis! Fetch recent workflow runs, download artifacts, analyze logs for errors, create reproduction test cases, and report comprehensive findings.