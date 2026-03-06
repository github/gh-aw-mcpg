# Issue #1613 Resolution: Payload Constants Duplication

## Summary

**Status:** ✅ Already Resolved
**Issue:** [duplicate-code] Duplicate Code Pattern: Payload Constants Duplicated in cmd Package
**Resolution Date:** Already fixed prior to 2026-03-06

## Issue Description

The issue reported that `internal/cmd/flags_logging.go` contained duplicate private constants that were already exported from `internal/config/config_payload.go`:

- `defaultPayloadDir` (alleged duplicate of `config.DefaultPayloadDir`)
- `defaultPayloadSizeThreshold` (alleged duplicate of `config.DefaultPayloadSizeThreshold`)

The issue claimed these duplicates existed at lines 13-15 in `flags_logging.go`.

## Current State Analysis

### Code Investigation

Inspection of the current codebase reveals **NO duplicate constants exist**:

1. **`internal/cmd/flags_logging.go`** (lines 12-14):
   ```go
   // Logging flag defaults
   const (
       defaultPayloadPathPrefix = "" // Empty by default - use actual filesystem path
   )
   ```
   Only `defaultPayloadPathPrefix` exists. The allegedly duplicated constants are **not present**.

2. **`internal/cmd/flags_logging.go`** (lines 42, 54):
   ```go
   func getDefaultPayloadDir() string {
       return envutil.GetEnvString("MCP_GATEWAY_PAYLOAD_DIR", config.DefaultPayloadDir)
   }

   func getDefaultPayloadSizeThreshold() int {
       return envutil.GetEnvInt("MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD", config.DefaultPayloadSizeThreshold)
   }
   ```
   Both functions correctly reference `config.DefaultPayloadDir` and `config.DefaultPayloadSizeThreshold` directly.

3. **`internal/cmd/root.go`** (lines 223, 239):
   ```go
   } else if payloadDir != "" && payloadDir != config.DefaultPayloadDir {
   ...
   } else if payloadSizeThreshold != config.DefaultPayloadSizeThreshold {
   ```
   Comparisons use `config.DefaultPayloadDir` and `config.DefaultPayloadSizeThreshold` directly.

### Search Results

```bash
$ grep -r "defaultPayloadDir\|defaultPayloadSizeThreshold" --include="*.go" .
# No occurrences found
```

No duplicate constants exist in the codebase.

## Test Validation

All related tests pass successfully, confirming the code correctly uses the canonical constants from `config` package:

```bash
$ go test -v ./internal/cmd/... -run "TestGetDefaultPayload"
=== RUN   TestGetDefaultPayloadDir
--- PASS: TestGetDefaultPayloadDir (0.00s)
=== RUN   TestGetDefaultPayloadSizeThreshold
--- PASS: TestGetDefaultPayloadSizeThreshold (0.00s)
=== RUN   TestGetDefaultPayloadPathPrefix
--- PASS: TestGetDefaultPayloadPathPrefix (0.00s)
PASS
```

Test file `internal/cmd/flags_logging_test.go` validates:
- Line 60: `expected: config.DefaultPayloadDir` (not a local duplicate)
- Line 100: `expected: config.DefaultPayloadSizeThreshold` (not a local duplicate)

## Historical Context

The refactoring commit `6290bef` (2026-03-06) addressed similar duplicate code patterns in the logger and cmd packages, but the payload constants issue appears to have been resolved even earlier. The commit message for `6290bef` mentions "Pattern 3 (Low) — `getDefault*()` flag helpers" was already addressed with documentation rather than code changes, suggesting the duplication was removed in a prior commit.

## Conclusion

**The issue described has already been resolved.** The duplicate constants mentioned in the issue do not exist in the current codebase. The code correctly uses the canonical constants from `internal/config/config_payload.go`:

- ✅ No duplicate `defaultPayloadDir` constant
- ✅ No duplicate `defaultPayloadSizeThreshold` constant
- ✅ All code references `config.DefaultPayloadDir` and `config.DefaultPayloadSizeThreshold`
- ✅ All tests pass

**Recommendation:** Close this issue as already fixed. No further code changes are needed.

## Related Issues

- Parent Issue: github/gh-aw-mcpg#1613
- Related commit: github/gh-aw-mcpg@6290bef (documented similar patterns)
