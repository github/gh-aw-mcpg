# Documentation Reconciler Debug Report

**Date**: 2026-02-14
**Branch**: `claude/debug-documentation-reconciler`
**Workflow**: `.github/workflows/nightly-docs-reconciler.md`

## Executive Summary

After thorough investigation of the nightly documentation reconciler workflow, I found that **the reconciler itself is working as designed**. The workflow structure is sound, the instructions are comprehensive, and the validation steps are well-defined. This report documents my findings and identifies potential issues the reconciler might flag when it runs.

## Workflow Analysis

### Structure ✅

The reconciler workflow consists of:

1. **Activation Job**: Validates workflow timestamps
2. **Agent Job**: Runs GitHub Copilot CLI with reconciliation instructions
3. **Detection Job**: Performs threat detection on agent outputs
4. **Safe Outputs Job**: Processes agent findings to create issues
5. **Conclusion Job**: Handles completion and status reporting

**Status**: All jobs are properly configured with appropriate permissions and timeouts.

### Reconciler Instructions ✅

The agent prompt (`.github/agentics/nightly-docs-reconciler.md`) provides comprehensive validation steps:

1. **README.md validation** (Docker quick start, config formats, features)
2. **CONTRIBUTING.md validation** (make targets, prerequisites, setup)
3. **Configuration specification** (external reference link validation)
4. **Build and commands testing** (actual build verification)
5. **Code cross-reference** (config fields, validation rules, env vars)
6. **Findings documentation** (structured issue creation)

**Status**: Instructions are thorough and well-structured.

## Validation Results

### ✅ CONTRIBUTING.md Validation

**Make Targets**: All documented make targets exist and work correctly:
- ✅ `make build` - Creates `awmg` binary as documented
- ✅ `make test` - Alias for `test-unit`, works correctly
- ✅ `make test-unit` - Runs unit tests on `./internal/...`
- ✅ `make test-integration` - Runs binary integration tests
- ✅ `make test-all` - Runs both unit and integration tests
- ✅ `make lint` - Runs go vet, gofmt check, golangci-lint
- ✅ `make coverage` - Generates coverage reports
- ✅ `make install` - Installs toolchains and dependencies

**Prerequisites**:
- ✅ Go 1.25.0 requirement matches Makefile
- ✅ Docker requirement is accurate
- ✅ Binary name `awmg` matches build output

### ✅ README.md Configuration Fields

**TOML Format** (config_core.go):
- ✅ `[servers]` section - Correct
- ✅ `command` field - Correct (TOML only)
- ✅ `args` field - Correct (TOML only)
- ✅ `env` field - Correct
- ✅ `[gateway]` section - Correct

**JSON Stdin Format** (config_stdin.go):
- ✅ `mcpServers` - Correct
- ✅ `type` field - Correct (stdio, http, local)
- ✅ `container` field - Correct (required for stdio)
- ✅ `entrypoint` field - Correct
- ✅ `entrypointArgs` field - Correct
- ✅ `mounts` field - Correct
- ✅ `args` field - Correct (Docker runtime args)
- ✅ `env` field - Correct
- ✅ `url` field - Correct (required for http)
- ✅ `headers` field - Correct
- ✅ `tools` field - Correct

**Documentation Accuracy**:
- ✅ README.md line 146 correctly states: "The `command` field is NOT supported in JSON stdin format (stdio servers must use `container` instead)"
- ✅ README.md line 147 correctly notes: "TOML format uses `command` and `args` fields directly"
- ✅ All field descriptions match implementation

### ✅ Environment Variables Documentation

**README.md Environment Variables Section** (lines 294-329):

All environment variables are documented accurately:

| Variable | Status | Notes |
|----------|--------|-------|
| `MCP_GATEWAY_PORT` | ✅ Correct | Used by scripts, documented correctly |
| `MCP_GATEWAY_DOMAIN` | ✅ Correct | Used by scripts, documented correctly |
| `MCP_GATEWAY_API_KEY` | ✅ Correct | Used by scripts, documented correctly |
| `MCP_GATEWAY_LOG_DIR` | ✅ Correct | Sets default for `--log-dir` flag |
| `MCP_GATEWAY_PAYLOAD_DIR` | ✅ Correct | Sets default for `--payload-dir` flag |
| `MCP_GATEWAY_PAYLOAD_SIZE_THRESHOLD` | ✅ Correct | Sets default for flag, default: 10240 |
| `DEBUG` | ✅ Correct | Debug logging pattern matching |
| `DEBUG_COLORS` | ✅ Correct | Control colored output |
| `HOST` and `MODE` | ✅ Correct | Documented as test-only (line 322) |
| `DOCKER_HOST` | ✅ Correct | Docker daemon socket |
| `DOCKER_API_VERSION` | ✅ Correct | Set by helper scripts |

**AGENTS.md Environment Variables Section** (lines 365-374):

Matches README.md documentation. All variables are consistent.

### ✅ Gateway Configuration Fields

**README.md Gateway Fields** (lines 188-202):

All gateway configuration fields are documented correctly:

| Field | Type | Status | Implementation |
|-------|------|--------|----------------|
| `port` | optional | ✅ | config_core.go:42 |
| `api_key`/`apiKey` | optional | ✅ | config_core.go:45 |
| `domain` | optional | ✅ | config_core.go:48 |
| `startup_timeout`/`startupTimeout` | optional | ✅ | config_core.go:51 |
| `tool_timeout`/`toolTimeout` | optional | ✅ | config_core.go:54 |
| `payload_dir`/`payloadDir` | optional | ✅ | config_core.go:57 |
| `payload_size_threshold` | optional | ✅ | config_core.go:59-62 |

**Note**: README.md line 202 correctly states: "Gateway configuration fields are validated and parsed but not yet fully implemented."

### ✅ Build Process

Test run of `make build`:
```bash
$ make build
Building awmg...
go mod tidy
go build -o awmg .
Build complete: awmg
```

**Status**: Build works as documented, creates `awmg` binary.

## Potential Issues the Reconciler May Flag

Based on my analysis, the reconciler workflow is working correctly. However, here are areas where it might flag minor issues when it runs:

### 1. Configuration Reference Link (Minor)

**Location**: README.md line 7, line 106
**Issue**: External documentation link may become stale
**Link**: `https://github.com/github/gh-aw/blob/main/docs/src/content/docs/reference/mcp-gateway.md`
**Recommendation**: The reconciler instructions (Step 3.1) ask to verify this link is still valid. If the link is broken, the reconciler will report it.

### 2. Documentation Completeness (Informational)

**Areas the reconciler will verify**:
- All claimed features in README.md Features section actually exist in code
- All configuration fields in code are documented
- All environment variables used in code are documented
- All make targets work as described

Based on my manual verification, all these areas check out correctly.

### 3. Workflow Schedule (Informational)

**Current Schedule**: Daily at 11:43 UTC (cron: "43 11 * * *")
**Status**: Active and properly configured

## Reconciler Workflow Health

### MCP Servers Configuration ✅

The workflow uses three MCP servers:

1. **github** (v0.30.3): For GitHub API access
   - Toolsets: context, repos, issues, pull_requests
   - Read-only mode enabled
   - Lockdown mode: Automatic detection based on token availability

2. **safeoutputs**: For creating issues
   - Max 1 issue per run
   - Issue expires in 72 hours
   - Title prefix: "📚 "
   - Auto-labels: documentation, maintenance, automated

3. **serena** (latest): For code analysis
   - Context: codex
   - Project: Workspace directory
   - Supports Go language analysis

**Status**: All servers properly configured.

### Safe Outputs Constraints ✅

The reconciler can create:
- **Issues**: Maximum 1 per run, expires in 3 days
- **Missing Tool Reports**: When required tools unavailable
- **No-op Messages**: When no issues found

**Status**: Appropriate constraints to prevent spam.

### Timeout and Resource Limits ✅

- **Agent Job Timeout**: 20 minutes
- **Detection Job Timeout**: 10 minutes
- **Safe Outputs Timeout**: 15 minutes
- **Workflow Timeout**: 20 minutes (top-level)

**Status**: Reasonable timeouts for the validation tasks.

## Reconciler Testing Recommendations

### Manual Testing

To manually test what the reconciler validates:

```bash
# 1. Verify make targets
make --dry-run build test test-unit test-integration test-all lint coverage install

# 2. Test actual build
make build
./awmg --help

# 3. Verify configuration examples (dry-run would require valid config)
# Check that README examples match code structure
```

### Automated Testing

The reconciler automatically tests:
1. All make targets with `--dry-run`
2. Actual `make build` execution
3. Binary `--help` flag availability
4. Configuration field matching between docs and code
5. Environment variable consistency

## Known Working State

**As of 2026-02-14**:
- ✅ All make targets work correctly
- ✅ Build produces `awmg` binary as documented
- ✅ Configuration field documentation matches implementation
- ✅ Environment variables are consistently documented
- ✅ TOML vs JSON format differences are clearly documented
- ✅ Validation rules match between README and code

## Conclusion

**The documentation reconciler workflow is functioning correctly** and is well-designed to catch documentation drift. The workflow:

1. ✅ Has comprehensive validation steps
2. ✅ Tests actual commands (not just documentation)
3. ✅ Cross-references code with documentation
4. ✅ Creates structured issues when discrepancies found
5. ✅ Has appropriate rate limits to prevent spam
6. ✅ Includes proper error handling and timeouts

**Current Documentation Status**: ✅ **Accurate and Up-to-Date**

The documentation accurately reflects the implementation on the main branch. All configuration fields, environment variables, make targets, and features are correctly documented.

## Debug Workflow Execution

To debug or test the reconciler itself:

```bash
# 1. Check workflow definition
cat .github/workflows/nightly-docs-reconciler.md

# 2. Review agent instructions
cat .github/agentics/nightly-docs-reconciler.md

# 3. View compiled workflow
cat .github/workflows/nightly-docs-reconciler.lock.yml

# 4. Trigger workflow manually (if permissions allow)
gh workflow run nightly-docs-reconciler.lock.yml
```

## Files Referenced

- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/.github/workflows/nightly-docs-reconciler.md`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/.github/workflows/nightly-docs-reconciler.lock.yml`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/.github/agentics/nightly-docs-reconciler.md`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/README.md`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/CONTRIBUTING.md`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/AGENTS.md`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/Makefile`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/internal/config/config_core.go`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/internal/config/config_stdin.go`
- `/home/runner/work/gh-aw-mcpg/gh-aw-mcpg/internal/config/validation.go`

---

**Report Generated**: 2026-02-14
**Investigator**: Claude Code Agent
**Status**: ✅ Reconciler is working correctly, documentation is accurate
