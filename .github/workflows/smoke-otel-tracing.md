---
name: Smoke OTel Tracing
description: Validates MCP Gateway OpenTelemetry tracing — provider initialization, span export, parent context propagation, and HTTP backend auth
on:
  schedule: weekly
  workflow_dispatch:
permissions:
  contents: read
  issues: read
  actions: read
  copilot-requests: write

observability:
  otlp:
    endpoint:
      - url: ${{ secrets.GH_AW_OTEL_SENTRY_ENDPOINT }}
        headers:
          x-sentry-auth: ${{ secrets.GH_AW_OTEL_SENTRY_AUTHORIZATION }}

engine:
  id: copilot
strict: false
imports:
  - shared/go-make.md
network:
  allowed:
    - defaults
    - go
    - "*.ingest.us.sentry.io"
tools:
  cache-memory: true
  bash: ["*"]
  edit:
runtimes:
  go:
    version: "1.26"
sandbox:
  mcp:
    container: "ghcr.io/github/gh-aw-mcpg"
    version: "latest"
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
    title-prefix: "[smoke-otel-tracing] "
    labels: [smoke-test, otel, tracing, automation]
    expires: 7d
    group: true
    close-older-issues: true
  messages:
    footer: "> 📡 *OTel tracing smoke test by [{workflow_name}]({run_url})*"
    run-started: "📡 [{workflow_name}]({run_url}) is starting OTel tracing validation..."
    run-success: "📡 [{workflow_name}]({run_url}) completed. All tracing scenarios validated. ✅"
    run-failure: "📡 [{workflow_name}]({run_url}) reports {status}. OTel tracing regression detected. ⚠️"
timeout-minutes: 30
---

# Smoke Test: MCP Gateway OTel Tracing

This workflow validates that the MCP Gateway correctly handles OpenTelemetry tracing across
all key scenarios: provider initialization, span export, parent trace context propagation,
HTTP backend authentication, and graceful shutdown with span flush.

Core validation still runs locally using Go unit tests and the compiled `awmg` binary.
This workflow also exports traces to Sentry via `observability.otlp` when
`GH_AW_OTEL_SENTRY_ENDPOINT` and `GH_AW_OTEL_SENTRY_AUTHORIZATION` are configured.

## Context

The MCP Gateway has two independent OTel concerns:

1. **Tracing provider** (`internal/tracing/`) — Initializes the OTLP/HTTP exporter,
   resolves W3C parent context from configured `traceId`/`spanId`, registers the global
   tracer provider, and flushes spans on shutdown.

2. **HTTP handler instrumentation** (`internal/server/`, `internal/tracing/http.go`) —
   Wraps request handlers with `gateway.request` spans, propagates `traceparent` headers,
   and creates tool-level spans (`mcp.tool_call`, `gateway.backend.execute`) in unified mode.

Recent gh-aw changes that affect MCPG tracing:
- **#28524**: `observability.otlp.headers` now supports object form (map) in addition to
  deprecated string form — MCPG receives these as the `opentelemetry.headers` field in
  stdin JSON config
- **#28511**: Agent span now uses `gen_ai.*` semantic conventions and `SPAN_KIND_CLIENT` —
  these are emitted by the gh-aw JS layer, not MCPG, but MCPG spans should appear as
  siblings in the same trace

## Test Scenarios

Run the following validation scenarios using `go test` and the compiled binary.
File an issue only if a test fails or a regression is detected.

### Scenario 1: Provider Initialization

Verify that `internal/tracing/` correctly initializes the OTLP provider:

```bash
cd /Users/lpcox/Desktop/ai/gh-aw-mcpg
go test -v -run 'TestInitProvider' ./internal/tracing/ 2>&1
```

Expected: All `TestInitProvider_*` tests pass — noop provider when no endpoint,
SDK provider when endpoint is set, correct sampler selection, header parsing,
and W3C parent context resolution.

### Scenario 2: Parent Context Propagation

Verify that `traceId`/`spanId` from config produce a valid remote parent span context:

```bash
go test -v -run 'TestParentContext' ./internal/tracing/ 2>&1
```

Expected: All `TestParentContext_*` tests pass — valid traceId+spanId produce a remote
span context, missing spanId generates a random one, invalid values are rejected gracefully.

### Scenario 3: HTTP Handler Span Creation

Verify that `WrapHTTPHandler` creates server spans and continues remote traces:

```bash
go test -v -run 'TestWrapHTTPHandler' ./internal/tracing/ 2>&1
```

Expected: `TestWrapHTTPHandler_ContinuesRemoteTrace` and `TestWrapHTTPHandler_GeneratesRootSpan`
both pass — incoming `traceparent` headers are honored, and fresh root spans are created when
no trace context is present.

### Scenario 4: Tool Call Span Instrumentation (Unified Mode)

Verify that `callBackendTool` in `internal/server/unified.go` creates `mcp.tool_call` and
`gateway.backend.execute` spans with the correct attributes:

```bash
go test -v -run 'TestCallBackend|TestToolCall|TestCircuitBreaker' ./internal/server/ 2>&1
```

Expected: Tool call spans include `mcp.server`, `mcp.method`, `mcp.tool`, and
`http.status_code` attributes per spec §4.1.3.6. Denied tools record errors.

### Scenario 5: Header Parsing

Verify that OTLP export headers are correctly parsed from the comma-separated string format
(which is how MCPG receives them from the gh-aw runner, even when the workflow uses the new
object form from #28524):

```bash
go test -v -run 'TestParseHeaders' ./internal/tracing/ 2>&1
```

Expected: `key=value,key2=value2` format is parsed correctly, edge cases handled.

### Scenario 6: HTTP Backend Auth (Sentry Pattern)

Verify that HTTP-type MCP backends (like Sentry) receive auth headers correctly.
The MCPG expands `${SENTRY_API_KEY}` from env into the connection config and injects
headers on every outgoing request. When the token is invalid, the gateway should log
a clear error and continue with remaining backends.

```bash
go test -v -run 'TestHTTP|TestConnect' ./internal/mcp/ 2>&1
```

Expected: Connection tests pass. Auth header injection works. Invalid credentials
produce clear error messages, not panics.

### Scenario 7: Graceful Shutdown and Span Flush

Verify that `Provider.Shutdown` calls `ForceFlush` + `Shutdown` on the SDK provider,
ensuring all buffered spans are exported before the process exits:

```bash
go test -v -run 'TestInitProvider_WithEndpoint_ReturnsSdkProvider' ./internal/tracing/ 2>&1
```

Expected: The SDK provider shutdown path is exercised. The 5-second timeout
in `shutdownTracingProviderWithTimeout` is sufficient for normal span batches.

### Scenario 8: Full Integration — Binary with OTel Config

Build the binary and verify it accepts OTel configuration via stdin JSON:

```bash
make build 2>&1

# Start the gateway with OTel config and verify it initializes tracing
echo '{"mcpServers":{},"gateway":{"opentelemetry":{"endpoint":"https://localhost:4318/v1/traces","traceId":"e3dd6ea3d5866246548b640e777a88b9","spanId":"d202e2e693f6fd4c"}}}' | timeout 5 ./awmg --listen 127.0.0.1:0 --routed 2>&1 || true
```

Expected output should include:
- `OpenTelemetry tracing enabled`
- `W3C parent context resolved: traceId=e3dd6ea3d5866246548b640e777a88b9`

## Known Gaps (Do NOT File Issues For These)

These are known limitations documented in issue investigations, not regressions:

1. **Routed mode has no tool-level spans** — `mcp.tool_call` spans only exist in unified mode.
   The routed mode creates HTTP-level `gateway.request` spans via `WrapHTTPHandler` but does
   not instrument individual tool calls. This is by design for now.

2. **Parent trace linkage requires `traceparent` headers** — The configured `traceId`/`spanId`
   from stdin JSON are applied to the process-level context, but HTTP request handlers get
   fresh contexts per request. Gateway spans link to the parent trace only when the client
   sends `traceparent` headers, or in unified mode where the process context is used.

3. **Schema URL conflict warning** — `resource.New()` may warn about conflicting schema URLs
   (`1.26.0` vs `1.40.0`) due to OTel SDK dependency version skew. This is non-fatal;
   the provider falls back to `resource.Empty()` and spans still export.

## Reporting

If all scenarios pass, report success with a brief summary of test counts.
If any scenario fails, file an issue with:
- The failing scenario number and name
- Full test output including the error
- The Go version and any relevant environment details
