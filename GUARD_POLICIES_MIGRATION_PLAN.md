# Guard Policies Migration Plan

## Overview

This document outlines the plan to evolve the `lpcox/github-difc` branch's experimental guard-policies implementation to use the main branch's guard-policies format, enabling a clean rebase without conflicts.

## Problem Statement

The main branch (commit 79aaed9) added guard-policies support in `ServerConfig.GuardPolicies`, while the experimental `lpcox/github-difc` branch implements guard policies differently using:
- A separate `Guards` configuration section in `Config`
- `GuardConfig` struct with `Policy` field
- `AllowOnly` policy structure with `repos` and `integrity` fields

These implementations overlap but differ in structure and location, requiring careful migration.

## Key Differences

### 1. Configuration Location

**Experimental (lpcox/github-difc)**:
```toml
[servers.github]
type = "stdio"
container = "ghcr.io/github/github-mcp-server:latest"
guard = "github-guard"  # References guard by name

[guards.github-guard]
type = "wasm"
[guards.github-guard.policy.allowonly]
repos = ["github/*"]
integrity = "reader"
```

**Main Branch**:
```toml
[servers.github]
type = "stdio"
container = "ghcr.io/github/github-mcp-server:latest"

[servers.github.guard_policies.github]
repos = ["github/*"]
min-integrity = "reader"
```

### 2. Structure Differences

| Aspect | Experimental | Main Branch |
|--------|-------------|-------------|
| Location | `Config.Guards` map | `ServerConfig.GuardPolicies` map |
| Policy Structure | `policy.allowonly.repos`, `policy.allowonly.integrity` | `github.repos`, `github.min-integrity` |
| Field Name | `integrity` | `min-integrity` |
| Reference | `ServerConfig.Guard` string field | Direct inclusion in `GuardPolicies` |

### 3. JSON Format Differences

**Experimental**:
```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "container": "...",
      "guard": "github-guard"
    }
  },
  "guards": {
    "github-guard": {
      "type": "wasm",
      "policy": {
        "allowonly": {
          "repos": ["github/*"],
          "integrity": "reader"
        }
      }
    }
  }
}
```

**Main Branch**:
```json
{
  "mcpServers": {
    "github": {
      "type": "stdio",
      "container": "...",
      "guard-policies": {
        "github": {
          "repos": ["github/*"],
          "min-integrity": "reader"
        }
      }
    }
  }
}
```

## Migration Strategy

### Phase 1: Adapt guard_policy.go

The experimental branch has a comprehensive `guard_policy.go` file with:
- `GuardPolicy` struct with `AllowOnly` field
- `AllowOnlyPolicy` with `Repos` and `Integrity` fields
- Validation and normalization functions
- JSON marshaling/unmarshaling with case-insensitive keys

**Actions**:
1. **Keep the validation logic** - It's well-tested and comprehensive
2. **Update structure** to accept main branch format:
   - Support both `allowonly` (experimental) and server-specific keys (main)
   - Map `integrity` to `min-integrity`
   - Create adapter functions for backward compatibility during migration
3. **Add server-specific parsing** - Support `github`, `jira`, etc. as top-level keys

### Phase 2: Update Configuration Structures

**Actions**:
1. Remove `Guards` map from `Config` struct
2. Remove `GuardConfig` struct (no longer needed)
3. Remove `Guard` string field from `ServerConfig` (already done in main)
4. Keep `GuardPolicies` map in `ServerConfig` (already in main)
5. Update `EnableDIFC` and `DIFCMode` fields to work with new structure

### Phase 3: Update Tests

**Actions**:
1. Migrate tests in `config_guardpolicies_test.go` to use main branch format
2. Update test cases to use server-specific keys (`github.repos`, `github.min-integrity`)
3. Keep validation test coverage for:
   - `repos` formats: `"all"`, `"public"`, `["owner/repo", "owner/*", "owner/prefix*"]`
   - `min-integrity` values: `"none"`, `"reader"`, `"writer"`, `"merged"`
4. Add tests for both TOML and JSON formats with new structure

### Phase 4: Update Guard Interface Integration

**Actions**:
1. Update server initialization code to pass `GuardPolicies` from `ServerConfig`
2. Update guard registry to accept policies in main branch format
3. Ensure DIFC evaluation works with new policy structure
4. Update `internal/guard/guard_test.go` if needed

### Phase 5: Update Documentation

**Actions**:
1. Update `GUARD_RESPONSE_LABELING.md` to reflect new format
2. Update configuration examples in README
3. Update TOML examples in `config.example.toml`
4. Add migration guide for users upgrading from experimental branch

### Phase 6: Handle Edge Cases

**Actions**:
1. **Backward compatibility**: Consider if we need to support reading old format temporarily
2. **Migration tool**: Possibly create a tool to convert old configs to new format
3. **Validation**: Ensure validation errors are clear about expected format
4. **DIFC integration**: Verify DIFC mode and enforcement still work correctly

## Implementation Order

1. ✅ Create this migration plan document
2. Update `guard_policy.go`:
   - Add support for server-specific keys (e.g., `github`)
   - Add field name mapping (`integrity` → `min-integrity`)
   - Keep existing validation logic
   - Add backward compatibility if needed
3. Update `config_core.go`:
   - Remove `Guards` map
   - Remove `GuardConfig` struct
   - Ensure `ServerConfig.GuardPolicies` is properly used
4. Update `config_guardpolicies_test.go`:
   - Migrate all tests to use new format
   - Verify both TOML and JSON formats work
5. Update guard interface and integration:
   - Update how guards receive policy configuration
   - Update launcher/server code if needed
6. Update documentation:
   - `GUARD_RESPONSE_LABELING.md`
   - `README.md`
   - `AGENTS.md`
   - Example configs
7. Test and verify:
   - Run `make test-all`
   - Run `make lint`
   - Run `make agent-finished`
   - Manual testing with actual guard policies

## Success Criteria

- [ ] All tests pass with new guard-policies format
- [ ] Configuration validates correctly in both TOML and JSON
- [ ] Guard policies work with DIFC enforcement
- [ ] Documentation is updated and accurate
- [ ] Branch can be rebased onto main without conflicts
- [ ] No breaking changes to other functionality
- [ ] `make agent-finished` passes completely

## Risk Mitigation

1. **Keep validation logic**: The experimental branch has excellent validation that should be preserved
2. **Incremental changes**: Make small, testable changes rather than big rewrites
3. **Test coverage**: Maintain or improve test coverage during migration
4. **DIFC functionality**: Ensure DIFC features still work correctly after migration
5. **Clear errors**: Validation errors should clearly indicate the expected format

## Files to Modify

### Core Configuration Files
- `internal/config/config_core.go` - Remove Guards, keep GuardPolicies
- `internal/config/guard_policy.go` - Adapt to support main format
- `internal/config/validation.go` - Update validation if needed

### Test Files
- `internal/config/config_guardpolicies_test.go` - Migrate all tests
- `internal/config/config_test.go` - Update any related tests

### Guard Implementation Files
- `internal/guard/guard.go` - Update interface if needed
- `internal/guard/guard_test.go` - Update tests
- `internal/guard/registry.go` - Update how policies are passed

### Documentation Files
- `docs/GUARD_RESPONSE_LABELING.md` - Update format examples
- `README.md` - Update configuration section
- `AGENTS.md` - Update if guard references exist
- `config.example.toml` - Already updated in main

### Server Integration Files
- `internal/launcher/launcher.go` - May need updates for policy passing
- `internal/server/unified.go` - May need updates for DIFC integration
- `internal/server/routed.go` - May need updates for DIFC integration

## Timeline

This migration should be completed in phases, with testing after each phase:
1. Phase 1-2 (Configuration): Update structures and validation
2. Phase 3 (Tests): Migrate all tests
3. Phase 4 (Integration): Update guard interface usage
4. Phase 5 (Documentation): Update all documentation
5. Phase 6 (Verification): Final testing and validation

Each phase should be committed separately for easier review and potential rollback if needed.
