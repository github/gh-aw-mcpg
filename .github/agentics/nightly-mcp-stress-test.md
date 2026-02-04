<!-- This prompt will be imported in the agentic workflow .github/workflows/nightly-mcp-stress-test.md at runtime. -->
<!-- You can edit this file to modify the agent behavior without recompiling the workflow. -->

<!-- NOTATION GUIDE:
     - {PLACEHOLDER} format = markdown template placeholders for the agent to substitute (e.g., {TEST_SESSION}, {DATE})
     - ${VARIABLE} format = shell environment variables or JSON variable expressions (e.g., ${API_KEY}, ${GITHUB_TOKEN})
     - Bash code blocks use ${VARIABLE} for actual shell variable references
     - Markdown report templates use {PLACEHOLDER} for agent substitution
-->

# Nightly MCP Server Stress Test 🧪

You are an AI agent that performs comprehensive stress testing of the MCP Gateway by testing 14 well-known MCP servers that are already configured and accessible through the gateway.

## Mission

Test the MCP Gateway's ability to handle multiple diverse MCP servers simultaneously. For each server, attempt to discover and invoke at least one tool. Track successes, failures, and categorize issues (authentication, protocol, timeout, etc.).

## Important: MCP Gateway is Pre-Configured

**The MCP Gateway is already running and configured with 14 MCP servers via the `mcp-servers` configuration in the workflow.**

You do NOT need to:
- ❌ Build the gateway (`make build`)
- ❌ Start the gateway (`./awmg`)
- ❌ Create a configuration file
- ❌ Launch Docker containers

The gateway is provided by the workflow infrastructure and handles all Docker container launching outside the AWF environment.

## Available MCP Servers

The following 14 MCP servers are pre-configured and accessible via the gateway:

1. **github** - GitHub MCP Server (ghcr.io/github/github-mcp-server:v0.30.2)
2. **filesystem** - Filesystem MCP Server (mcp/filesystem)
3. **memory** - Memory MCP Server (mcp/memory)
4. **sqlite** - SQLite MCP Server (mcp/sqlite)
5. **brave-search** - Brave Search MCP Server (mcp/brave-search)
6. **fetch** - Fetch MCP Server (mcp/fetch)
7. **puppeteer** - Puppeteer MCP Server (mcp/puppeteer)
8. **slack** - Slack MCP Server (mcp/slack)
9. **gdrive** - Google Drive MCP Server (mcp/gdrive)
10. **google-maps** - Google Maps MCP Server (mcp/google-maps)
11. **everart** - EverArt MCP Server (mcp/everart)
12. **sentry** - Sentry MCP Server (mcp/sentry)
13. **git** - Git MCP Server (mcp/git)
14. **time** - Time MCP Server (mcp/time)

**Note:** The following servers were removed due to unavailability in Docker Hub:
- ~~postgres~~ (archived/read-only)
- ~~sequential-thinking~~ (not available as mcp/sequential-thinking)
- ~~aws-kb-retrieval~~ (not available)
- ~~linear~~ (not available)
- ~~raygun~~ (not available)
- ~~axiom~~ (not available)

## Step 1: Initialize Test Session 📋

1. **Generate a unique test session ID:**
   ```bash
   TEST_SESSION="stress-test-$(date +%Y%m%d-%H%M%S)"
   echo "Test session: $TEST_SESSION"
   ```

2. **Set up test directories:**
   ```bash
   # Create test directories for results
   mkdir -p /tmp/mcp-stress-results
   mkdir -p /tmp/mcp-stress-test
   ```

3. **Verify gateway connectivity:**
   
   The MCP Gateway is accessible at the MCP tool endpoint. You can test connectivity by attempting to list tools from a server.

## Step 2: Test Each MCP Server 🔬

The MCP servers are accessible as MCP tools through the gateway. For each server, you should attempt to:

1. **Discover available tools** from the server
2. **Invoke a simple test tool** (if available)
3. **Record the result** (success, authentication error, timeout, protocol error, or other failure)

### Testing Approach

For each of the 14 configured MCP servers:

1. **Attempt to use a simple, safe tool from that server**
   - For `github`: Try calling a read-only tool like `search_repositories` or `list_issues`
   - For `filesystem`: Try listing a directory (if supported)
   - For `memory`: Try reading or listing (if supported)
   - For `time`: Try getting current time (if supported)
   - For other servers: Use the simplest, safest read-only operation available

2. **If tool access fails, determine the failure type:**
   - **Authentication Required**: Error mentions missing token, API key, or authentication
   - **Protocol Error**: Malformed JSON-RPC or MCP protocol violation
   - **Timeout**: Request exceeds timeout period
   - **Container Error**: Docker container failed to start or crashed
   - **Tool Not Available**: Server has no tools or requested tool doesn't exist
   - **Other**: Any unexpected error

3. **Keep track of results** for each server in a structured format

### Testing Strategy

**Note**: You may encounter authentication failures for many servers that require API keys or tokens. This is EXPECTED and should be documented, not considered a critical failure.

- Test servers **sequentially** (one at a time) to avoid overwhelming the gateway
- Use **simple operations** that don't require complex parameters
- Prefer **read-only** operations to avoid side effects
- If a server requires authentication you don't have, **record it and move on**
- **Continue testing** all 14 servers even if some fail

### Example Test Pattern

For the GitHub server (which has authentication configured):
```bash
# You can directly use MCP tools configured in the workflow
# The MCP gateway handles the routing automatically
# Example: Use bash to log your testing approach
echo "Testing github server..."
```

Then attempt to use a GitHub MCP tool. If it works, record success. If it fails, record the error and category.

For servers without authentication:
- Attempt to use a tool
- If it fails due to missing authentication, document the required token
- Move to the next server

## Step 3: Categorize Results 📊

After testing all 14 servers, categorize the results:

**Success Categories:**
- ✅ **Fully Functional**: Server responded and tool executed successfully
- ✅ **Partially Functional**: Server responded but some tools require auth

**Failure Categories:**
- ❌ **Authentication Required**: Server needs API key/token not provided
- ❌ **Protocol Error**: JSON-RPC or MCP protocol issues
- ❌ **Timeout**: Server didn't respond within timeout period
- ❌ **Container Error**: Docker container failed to start
- ❌ **Other Error**: Unexpected failures

## Step 4: Generate Test Report 📝

Create a comprehensive test report documenting your findings.
   
   # Parse for errors
   grep -i error /tmp/mcp-stress-test/logs/*.log > /tmp/mcp-stress-results/errors.txt
   ```

3. **Analyze gateway performance:**
   - Check for memory leaks
   - Measure startup time for each server
   - Count total requests and failures
   - Identify slowest servers

## Step 5: Generate Test Report 📊

Create a comprehensive test report with the following sections:

### Summary Statistics

```markdown
## Test Summary

- **Test Session:** {TEST_SESSION}
- **Date:** {DATE}
- **Total Servers Tested:** 14
- **Successful Servers:** X
- **Failed Servers:** Y
- **Authentication Required:** Z

### Success Rate

- Overall: X% (X/14 servers)
- With Authentication: Y% (Y/Z authenticated servers if applicable)
- Without Authentication: Z% (Z/W non-authenticated servers if applicable)
```

### Server Results Table

```markdown
| Server Name | Status | Result | Issue Type | Notes |
|-------------|--------|--------|------------|-------|
| github | ✅ Success | Tool executed | - | GitHub token provided, tests passed |
| slack | ❌ Failed | Auth Required | Authentication | Needs SLACK_BOT_TOKEN |
| filesystem | ❌ Failed | Tool unavailable | Container | Unable to start container |
| ... | ... | ... | ... | ... |
```

### Detailed Failure Analysis

For each failed server, include:

```markdown
#### Server: {server-name}

**Status:** ❌ Failed

**Issue Type:** Authentication Required (or Protocol Error, Timeout, Container Error, etc.)

**Error Message:**
```
Error: Missing or invalid SLACK_BOT_TOKEN environment variable
```

**Analysis:**
{Brief explanation of what went wrong and why}

**Required Action:**
{What needs to be done to fix this - e.g., add token to secrets, update configuration, etc.}
```

### Test Execution Notes

```markdown
## Test Execution

- **MCP Gateway:** Provided by sandbox.mcp (gateway container) with mcp-servers configuration
- **Test Duration:** Xm Ys
- **Servers Tested Sequentially:** Yes
- **Any Gateway Issues:** None / {describe if any}
```

## Step 5: Create GitHub Issues 🎫

Based on the test results, create GitHub issues:

### For Authentication-Required Servers

If any servers failed due to missing authentication:

1. **Create an issue for EACH server** using safe-outputs:

```markdown
Title: [mcp-stress-test] {Server Name} requires {AUTH_TOKEN_NAME} authentication

Body:
# MCP Server Authentication Required: {Server Name}

The nightly stress test detected that the **{Server Name}** MCP server requires authentication to function properly.

## Test Details

- **Test Session:** {TEST_SESSION}
- **Test Date:** {DATE}
- **Server Container:** {container-image}

## Required Authentication Token

**Environment Variable:** `{AUTH_TOKEN_NAME}`

**Token Type:** {token-type} (e.g., API Key, OAuth Token, Service Account)

**How to Obtain:**
1. Visit: {documentation-url}
2. Create an account or sign in
3. Generate {token-type}
4. Add to repository secrets as `{AUTH_TOKEN_NAME}`

## Error Message

```
{error-message-from-test}
```

## Suggested Configuration

Add the following to your MCP Gateway configuration:

```json
{
  "{server-name}": {
    "type": "stdio",
    "container": "{container-image}",
    "env": {
      "{AUTH_TOKEN_NAME}": "${{{AUTH_TOKEN_NAME}}}"
    }
  }
}
```

## Next Steps

- [ ] Obtain {AUTH_TOKEN_NAME} token
- [ ] Add token to repository secrets
- [ ] Update stress test configuration
- [ ] Verify server works in next nightly test

---
*Generated by Nightly MCP Stress Test*
*Test Session: {TEST_SESSION}*
```

### For Other Failures

If there are servers that failed for reasons other than authentication:

1. **Create a SINGLE summary issue** with all non-auth failures:

```markdown
Title: [mcp-stress-test] Server Failures Detected - {DATE}

Body:
# MCP Server Stress Test Failures

The nightly stress test detected {N} servers with non-authentication failures.

## Test Summary

- **Test Session:** {TEST_SESSION}
- **Test Date:** {DATE}
- **Total Failures:** {N}

## Failed Servers

### 1. {Server Name} - Protocol Error

**Container:** {container-image}

**Issue Type:** Protocol Error

**Error:**
```
{error-message}
```

**Analysis:**
{brief-analysis-of-what-went-wrong}

**Suggested Investigation:**
- [ ] Check container logs
- [ ] Verify MCP protocol compatibility
- [ ] Test server outside gateway

---

### 2. {Server Name} - Timeout

**Container:** {container-image}

**Issue Type:** Timeout (exceeded 30s)

**Error:**
```
{error-message}
```

**Analysis:**
{brief-analysis}

**Suggested Investigation:**
- [ ] Increase toolTimeout in configuration
- [ ] Check server performance
- [ ] Verify container resources

---

## Gateway Logs

Key errors from gateway logs:

```
{relevant-gateway-log-excerpts}
```

## Test Configuration

The test used the following configuration:
- Startup Timeout: 60s
- Tool Timeout: 30s
- Port: 3000

## Next Steps

1. Investigate each failure category
2. Apply fixes or configuration changes
3. Re-run stress test to verify

---
*Generated by Nightly MCP Stress Test*
*Test Session: {TEST_SESSION}*

**Full Test Results:** See workflow run artifacts
```

## Step 6: Save Test Results 💾

Save the test report and results:

```bash
# Save the test report to the results directory
# Create a summary file with test results
cat > /tmp/mcp-stress-results/summary.txt << EOF
Test Session: ${TEST_SESSION}
Date: $(date -u +"%Y-%m-%dT%H:%M:%SZ")
Total Servers: 14
Successful: {count}
Failed: {count}
Authentication Required: {count}
EOF

# Save the detailed report as markdown
# The report should be created during Step 4
```

## Important Guidelines

### Testing Best Practices

1. **Non-Destructive Testing:**
   - Only use read-only operations
   - Don't create/modify/delete real data
   - Use test/sandbox environments when available

2. **Error Handling:**
   - Catch all errors gracefully
   - Continue testing remaining servers on failure
   - Don't let one failure stop entire test

3. **Timing:**
   - Allow adequate startup time for containers
   - Use reasonable timeouts (30s for tools, 60s for startup)
   - Measure and report timing metrics

### Issue Creation Guidelines

1. **Authentication Issues:**
   - Create ONE issue PER server that needs auth
   - Include clear instructions for obtaining tokens
   - Provide documentation links
   - Suggest exact configuration

2. **Other Failures:**
   - Create ONE summary issue for all non-auth failures
   - Group by failure type
   - Include enough detail for debugging
   - Suggest investigation steps

3. **Issue Quality:**
   - Use clear, descriptive titles
   - Include test session ID for traceability
   - Provide complete error messages
   - Add suggested next steps
   - Label appropriately

### Performance Considerations

- Test servers **sequentially** (not in parallel) to avoid overwhelming the gateway
- Continue testing all servers even if some fail
- Record timing for each test operation
- Document any unusual delays or timeouts

## Expected Workflow Behavior

### Success Case (All Servers Accessible)

If ALL 14 servers successfully respond to tool calls:

1. **DO NOT create any issues** (silence is success)
2. **Log summary to workflow output:**
   ```
   ✅ All 14 MCP servers passed stress test
   Test Session: stress-test-YYYYMMDD-HHMMSS
   Total Test Duration: Xm Ys
   ```
3. **Upload test report as artifact** for reference

### Partial Success (Some Failures)

If SOME servers fail:

1. **Create authentication issues** for each server needing auth (one issue per server)
2. **Create summary issue** for other failures (one issue covering all non-auth failures)
3. **Log summary:**
   ```
   ⚠️ MCP Stress Test completed with failures
   Success: X/14 servers
   Auth Required: Y servers (created Y issues)
   Other Failures: Z servers (created 1 summary issue)
   ```

### Complete Failure (Gateway Not Accessible)

If the MCP Gateway is not accessible:

1. **Create critical issue** about gateway connectivity
2. **Include error messages** from connection attempts
3. **Mark as high priority**

## Artifact Upload

The workflow will automatically upload the following as artifacts (configured in post-steps):

1. **Test Results** - `/tmp/mcp-stress-results/`
2. **Any logs generated** - `/tmp/mcp-stress-test/logs/`

Ensure you save your test report and results to `/tmp/mcp-stress-results/` for artifact upload.

## Notes

- This is a **nightly** workflow - results accumulate over time to detect regressions
- Focus on **breadth** over depth - test many servers quickly
- **MCP Gateway is managed by the workflow infrastructure** - you don't need to start/stop it
- Keep **runtime under 15 minutes** - stress test should be fast
- **Document findings** - every failure should be actionable
- **Expected result**: Many servers will require authentication - this is normal and should be documented

## Summary

**Your task**: Test all 14 pre-configured MCP servers through the MCP Gateway, document which ones work, which ones need authentication, and which ones have other issues. Create appropriate GitHub issues for failures. Save results to `/tmp/mcp-stress-results/` for artifact upload.

Begin the stress test!
