---
name: Smoke Long Session
description: Validates MCP Gateway timeout robustness across long-running unified sessions, idle reconnections, and tool timeout enforcement
on:
  schedule: weekly
  workflow_dispatch:
permissions:
  contents: read
  issues: read
  copilot-requests: write

engine:
  id: copilot
strict: false
imports:
  - shared/go-make.md
network:
  allowed:
    - defaults
    - go
tools:
  cache-memory: true
  bash: ["*"]
  edit:
runtimes:
  go:
    version: "1.26"
steps:
  - name: Set up Go
    uses: actions/setup-go@d8cbb8045d7f43706b9177cceb36c1f91a6644d5  # v7.1.0
    with:
      go-version-file: go.mod
      cache: true
safe-outputs:
  threat-detection:
    enabled: false
  create-issue:
    title-prefix: "[smoke-long-session] "
    labels: [smoke-test, timeout-testing, automation]
    expires: 7d
    group: true
    close-older-issues: true
  messages:
    footer: "> ⏱️ *Long-session timeout smoke test by [{workflow_name}]({run_url})*"
    run-started: "⏱️ [{workflow_name}]({run_url}) is starting long-session timeout validation..."
    run-success: "⏱️ [{workflow_name}]({run_url}) completed. All timeout scenarios validated. ✅"
    run-failure: "⏱️ [{workflow_name}]({run_url}) reports {status}. Timeout robustness regression detected. ⚠️"
timeout-minutes: 120
---

# Smoke Test: MCP Gateway Long-Session Timeout Robustness

This workflow validates that the MCP Gateway correctly handles long-lived sessions,
idle reconnections, tool timeout enforcement, and graceful session expiry.
No external network access or Docker is required — everything runs locally.

## Overview of Timeout Layers Under Test

| Timeout | Value | Scope |
|---------|-------|-------|
| Session timeout (unified) | 45m (configured) | StreamableHTTP session lifetime |
| Tool timeout | 60s (default) | Per `tools/call` execution |
| Connect timeout | 30s (default) | Backend transport establishment |

## Test Execution

**IMPORTANT**: Execute every step via bash. Track results in a local results file.
If any assertion fails, record it as a failure and continue the remaining tests.
At the end, create an issue with the full results only if at least one test failed.

### Step 1: Build the Gateway Binary

Use `mcpscripts-make` to build the project:

```
mcpscripts-make tool with args: "build"
```

Verify the binary exists at `./awmg`.

### Step 2: Start a Mock MCP HTTP Backend

Write and start a self-contained Python mock MCP backend. The backend must:
- Respond to `initialize` (JSON-RPC 2.0)
- Respond to `notifications/initialized` (HTTP 202)
- Respond to `tools/list` with two tools: `echo_tool` (instant) and `slow_tool` (delays 90 seconds)
- Respond to `tools/call` — `echo_tool` returns immediately; `slow_tool` sleeps 90 seconds before responding

Write the following Python script to `/tmp/smoke-long-session/mock_backend.py`:

```python
#!/usr/bin/env python3
"""Minimal MCP JSON-RPC HTTP backend for smoke testing."""
import json
import time
import sys
from http.server import HTTPServer, BaseHTTPRequestHandler

TOOLS = [
    {
        "name": "echo_tool",
        "description": "Echoes back a message instantly",
        "inputSchema": {
            "type": "object",
            "properties": {"message": {"type": "string"}},
        },
    },
    {
        "name": "slow_tool",
        "description": "Sleeps 90 seconds then responds (for timeout testing)",
        "inputSchema": {"type": "object", "properties": {}},
    },
]


class MCPHandler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        print(f"[mock-backend] {fmt % args}", file=sys.stderr, flush=True)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length)
        try:
            req = json.loads(body)
        except Exception:
            self.send_response(400)
            self.end_headers()
            return

        method = req.get("method", "")
        req_id = req.get("id")

        if method == "notifications/initialized":
            self.send_response(202)
            self.end_headers()
            return

        if method == "initialize":
            result = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "protocolVersion": "2024-11-05",
                    "capabilities": {"tools": {}},
                    "serverInfo": {"name": "mock-backend", "version": "1.0.0"},
                },
            }
        elif method == "tools/list":
            result = {"jsonrpc": "2.0", "id": req_id, "result": {"tools": TOOLS}}
        elif method == "tools/call":
            params = req.get("params", {})
            tool_name = params.get("name", "")
            if tool_name == "slow_tool":
                time.sleep(90)
            result = {
                "jsonrpc": "2.0",
                "id": req_id,
                "result": {
                    "content": [{"type": "text", "text": f"ok from {tool_name}"}]
                },
            }
        else:
            result = {
                "jsonrpc": "2.0",
                "id": req_id,
                "error": {"code": -32601, "message": f"Method not found: {method}"},
            }

        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(json.dumps(result).encode())


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18765
    server = HTTPServer(("127.0.0.1", port), MCPHandler)
    print(f"[mock-backend] Listening on 127.0.0.1:{port}", file=sys.stderr, flush=True)
    server.serve_forever()
```

Start the backend in the background:

```bash
mkdir -p /tmp/smoke-long-session
python3 /tmp/smoke-long-session/mock_backend.py 18765 &>/tmp/smoke-long-session/mock_backend.log &
MOCK_PID=$!
echo "Mock backend PID: $MOCK_PID"
sleep 2
# Verify it started
curl -sf -X POST http://127.0.0.1:18765 \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}' \
  | grep -q '"serverInfo"' && echo "✓ Mock backend started" || echo "✗ Mock backend failed to start"
```

### Step 3: Start the MCP Gateway in Unified Mode

Write the gateway config to `/tmp/smoke-long-session/config.json`.
Note: `toolTimeout` (camelCase) is the correct JSON field name for the stdin config
format (`tool_timeout` is used only in TOML). It is explicitly set to 60 (matching
the current default) so the Step 7 timeout enforcement test has a documented, stable
expectation even if the default changes in future versions.

```json
{
  "mcpServers": {
    "mock": {
      "type": "http",
      "url": "http://127.0.0.1:18765"
    }
  },
  "gateway": {
    "port": 18766,
    "domain": "localhost",
    "agentId": "smoke-test-key",
    "toolTimeout": 60
  }
}
```

Start the gateway in unified mode with a 45-minute session timeout:

```bash
MCP_GATEWAY_SESSION_TIMEOUT=45m \
  ./awmg --config-stdin --listen 127.0.0.1:18766 --unified \
  < /tmp/smoke-long-session/config.json \
  &>/tmp/smoke-long-session/gateway.log &
GW_PID=$!
echo "Gateway PID: $GW_PID"
# Wait for gateway to be ready
for i in $(seq 1 30); do
  curl -sf http://127.0.0.1:18766/health && echo "✓ Gateway ready" && break
  sleep 1
done
```

### Step 4: Initialize the Unified Session

Send an `initialize` request to establish the unified session. Capture the
`Mcp-Session-Id` response header — it must remain consistent for all subsequent calls.

```bash
INIT_RESP=$(curl -si -X POST http://127.0.0.1:18766/mcp \
  -H 'Authorization: smoke-test-key' \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke-long-session","version":"1.0"}}}')
SESSION_ID=$(echo "$INIT_RESP" | grep -i 'mcp-session-id:' | awk '{print $2}' | tr -d '\r')
echo "Session ID: $SESSION_ID"
```

Send the `notifications/initialized` acknowledgement:

```bash
curl -s -X POST http://127.0.0.1:18766/mcp \
  -H "Authorization: smoke-test-key" \
  -H "Content-Type: application/json" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  -o /dev/null
```

Record the session start time:

```bash
SESSION_START=$(date +%s)
echo "Session started at $(date -Iseconds)"
```

### Step 5: Session Survival Test

Make a baseline call at T=0, then at T=15m, T=25m, and T=35m.
All calls should succeed because the 45-minute session timeout has not elapsed.

Define a helper function inline:

```bash
# Helper: call echo_tool and assert success
call_echo() {
  local label="$1"
  local now=$(date +%s)
  local elapsed=$(( (now - SESSION_START) / 60 ))
  echo ">>> $label (T+${elapsed}m): calling echo_tool..."
  RESP=$(curl -s -X POST http://127.0.0.1:18766/mcp \
    -H "Authorization: smoke-test-key" \
    -H "Content-Type: application/json" \
    -H "Accept: application/json, text/event-stream" \
    -H "Mcp-Session-Id: $SESSION_ID" \
    -d '{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"mock___echo_tool","arguments":{"message":"ping"}}}')
  if echo "$RESP" | grep -q '"result"'; then
    echo "✓ $label passed (T+${elapsed}m)"
    echo "PASS: $label" >> /tmp/smoke-long-session/results.txt
  else
    echo "✗ $label FAILED (T+${elapsed}m): $RESP"
    echo "FAIL: $label — response: $RESP" >> /tmp/smoke-long-session/results.txt
  fi
}

# T=0 baseline
call_echo "T0 baseline call"

# Sleep 15 minutes, then call at T=15
echo "Sleeping 900s (15 minutes)..."
sleep 900
call_echo "T15 session survival call"

# Sleep 10 more minutes, call at T=25
echo "Sleeping 600s (10 minutes)..."
sleep 600
call_echo "T25 session survival call"

# Sleep 10 more minutes, call at T=35 (under 45m timeout)
echo "Sleeping 600s (10 minutes)..."
sleep 600
call_echo "T35 session survival call"
```

### Step 6: Idle Reconnection Test

After the T=35m call there has been a ~10-minute idle period between T=25 and T=35.
Verify that the T=35 call above already demonstrates idle reconnection.
Additionally, verify the tools list is still accessible (backend connections re-established):

```bash
TOOLS_RESP=$(curl -s -X POST http://127.0.0.1:18766/mcp \
  -H "Authorization: smoke-test-key" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":10,"method":"tools/list","params":{}}')
if echo "$TOOLS_RESP" | grep -q '"tools"'; then
  echo "✓ Idle reconnection: tools/list succeeded after idle period"
  echo "PASS: idle reconnection (tools/list after T=35m)" >> /tmp/smoke-long-session/results.txt
else
  echo "✗ Idle reconnection FAILED: $TOOLS_RESP"
  echo "FAIL: idle reconnection — response: $TOOLS_RESP" >> /tmp/smoke-long-session/results.txt
fi
```

### Step 7: Tool Timeout Enforcement Test

Call `slow_tool` which sleeps 90 seconds on the backend.
The gateway's `toolTimeout` is 60 seconds, so it should return a timeout error
within ~60 seconds, NOT hang until the backend finishes.

```bash
echo ">>> Tool timeout test: calling slow_tool (expects timeout error after ~60s)..."
TIMEOUT_START=$(date +%s)
# --max-time 95: allow 5s beyond the 90s backend sleep; the gateway should
# interrupt and return an error well before this, at ~60s (toolTimeout).
TIMEOUT_RESP=$(curl -s --max-time 95 -X POST http://127.0.0.1:18766/mcp \
  -H "Authorization: smoke-test-key" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":20,"method":"tools/call","params":{"name":"mock__slow_tool","arguments":{}}}')
TIMEOUT_END=$(date +%s)
TIMEOUT_ELAPSED=$(( TIMEOUT_END - TIMEOUT_START ))
echo "Tool timeout response arrived in ${TIMEOUT_ELAPSED}s: $TIMEOUT_RESP"

# The gateway must return an error response within the tool_timeout window (60s ±15s).
# A response arriving at 75s+ indicates the gateway did not enforce the timeout.
# A response arriving without an error field means it passed through instead of being cut.
if [ "$TIMEOUT_ELAPSED" -le 75 ] && echo "$TIMEOUT_RESP" | grep -q '"error"'; then
  echo "✓ Tool timeout enforcement: error returned in ${TIMEOUT_ELAPSED}s (within 75s threshold)"
  echo "PASS: tool timeout enforcement (error in ${TIMEOUT_ELAPSED}s)" >> /tmp/smoke-long-session/results.txt
elif [ "$TIMEOUT_ELAPSED" -gt 75 ]; then
  echo "✗ Tool timeout FAILED: gateway took ${TIMEOUT_ELAPSED}s — did not enforce 60s tool_timeout"
  echo "FAIL: tool timeout enforcement — gateway took ${TIMEOUT_ELAPSED}s (exceeded 75s threshold)" >> /tmp/smoke-long-session/results.txt
else
  echo "✗ Tool timeout FAILED: expected error in response, got: $TIMEOUT_RESP"
  echo "FAIL: tool timeout enforcement — no error in response: $TIMEOUT_RESP" >> /tmp/smoke-long-session/results.txt
fi
```

### Step 8: Graceful Session Expiry Test

Wait until the session has exceeded the 45-minute timeout, then send a new request
using the same session ID. The gateway should return a clean error (e.g., 404 or a
JSON-RPC error indicating the session is unknown), not crash or hang silently.

Calculate remaining time to reach the 45-minute mark and sleep until then:

```bash
# SESSION_TIMEOUT_SECONDS must match MCP_GATEWAY_SESSION_TIMEOUT=45m set in Step 3
SESSION_TIMEOUT_SECONDS=$((45 * 60))  # derived from 45m to keep in sync with Step 3
NOW=$(date +%s)
SESSION_AGE=$(( NOW - SESSION_START ))
REMAINING=$(( SESSION_TIMEOUT_SECONDS - SESSION_AGE ))
if [ "$REMAINING" -gt 0 ]; then
  echo "Sleeping ${REMAINING}s to reach session timeout boundary (45m)..."
  sleep "$REMAINING"
fi
# Add a small buffer to ensure timeout has triggered
sleep 30
echo "Session age is now $(( ($(date +%s) - SESSION_START) / 60 )) minutes"

EXPIRY_RESP=$(curl -si -X POST http://127.0.0.1:18766/mcp \
  -H "Authorization: smoke-test-key" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "Mcp-Session-Id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":30,"method":"tools/call","params":{"name":"mock__echo_tool","arguments":{"message":"after-expiry"}}}')
HTTP_STATUS=$(echo "$EXPIRY_RESP" | head -1 | awk '{print $2}')
echo "Session expiry response (HTTP $HTTP_STATUS): $EXPIRY_RESP"

# Accept either a 4xx HTTP status or a JSON-RPC error — both indicate clean rejection
if (echo "$EXPIRY_RESP" | grep -qE '^HTTP/[0-9.]+ 4[0-9][0-9]') || \
   (echo "$EXPIRY_RESP" | grep -q '"error"'); then
  echo "✓ Graceful session expiry: gateway returned clean rejection (HTTP $HTTP_STATUS)"
  echo "PASS: graceful session expiry (HTTP $HTTP_STATUS)" >> /tmp/smoke-long-session/results.txt
else
  echo "✗ Graceful session expiry FAILED: unexpected response: $EXPIRY_RESP"
  echo "FAIL: graceful session expiry — unexpected response: $EXPIRY_RESP" >> /tmp/smoke-long-session/results.txt
fi
```

### Step 9: Cleanup

Stop the gateway and mock backend:

```bash
kill "$GW_PID" 2>/dev/null || true
kill "$MOCK_PID" 2>/dev/null || true
echo "Cleanup complete"
```

### Step 10: Report Results

Read the results file and determine overall pass/fail:

```bash
cat /tmp/smoke-long-session/results.txt || true
FAILURES=$(grep -c "^FAIL:" /tmp/smoke-long-session/results.txt 2>/dev/null || echo 0)
PASSES=$(grep -c "^PASS:" /tmp/smoke-long-session/results.txt 2>/dev/null || echo 0)
echo "Results: $PASSES passed, $FAILURES failed"
```

**Only if there are failures**, create an issue with this exact format:

- Title: "[smoke-long-session] ${{ github.run_id }}"
- Body:

```
## MCP Gateway Long-Session Timeout Smoke Test — FAILED

**Run**: ${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}
**Trigger**: ${{ github.event_name }}
**Timestamp**: [populate with $(date -Iseconds)]

### Test Results

| Scenario | Status |
|----------|--------|
| T0 baseline call | ✅/❌ |
| T15 session survival call | ✅/❌ |
| T25 session survival call | ✅/❌ |
| T35 session survival call | ✅/❌ |
| Idle reconnection (tools/list after T=35m) | ✅/❌ |
| Tool timeout enforcement (slow_tool > 60s) | ✅/❌ |
| Graceful session expiry (call after 45m) | ✅/❌ |

### Failure Details

[Include the raw FAIL lines from results.txt and any relevant gateway/backend logs from
/tmp/smoke-long-session/gateway.log and /tmp/smoke-long-session/mock_backend.log]

### Gateway Log (last 50 lines)

\`\`\`
[last 50 lines of /tmp/smoke-long-session/gateway.log]
\`\`\`
```

If all tests pass, do NOT create an issue — simply print a success summary.