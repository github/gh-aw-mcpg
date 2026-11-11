# Labels Module

This module has been reorganized into focused sub-modules for improved maintainability and code organization.

## Module Structure

```
labels/
├── mod.rs              - Public API, MCP response handling, and tests
├── constants.rs        - Label strings, field names, and configuration constants
├── helpers.rs          - Common utility functions for labeling
├── backend.rs          - Backend API calls for contribution trust verification
├── tool_rules.rs       - Tool-specific label rule application
├── response_paths.rs   - Path-based response labeling (preferred format)
└── response_items.rs   - Item-based response labeling (legacy format)
```

## Purpose of Each File

### constants.rs (61 lines)
Contains all constant values used throughout the labeling system:
- `label_constants` - Standard label strings (secret, private:user, approved:github, etc.)
- `field_names` - JSON field name constants for consistent extraction
- File pattern constants for secret detection
- Buffer sizes and limits

### helpers.rs (250 lines)
Common utility functions including:
- Label generation helpers (secret_label, writer_integrity, etc.)
- JSON extraction functions (get_string_field, extract_repo_info, etc.)
- Integrity determination (pr_integrity, issue_integrity)
- User classification helpers (`is_blocked_user`, `is_trusted_user`)
- Note: the current helpers API does not include a separate `is_bot` helper

### backend.rs (92 lines)
Backend API calls for verifying user status:
- `count_merged_prs()` - Check user's merged PR count in a repository
- `is_verified_contributor()` - Determine if user has trusted contributor status

### tool_rules.rs (213 lines)
The `apply_tool_labels()` function applies tool-specific labeling rules based on:
- Tool name (get_issue, list_pull_requests, search_code, etc.)
- Tool arguments
- Repository context

Each tool type gets appropriate:
- Secrecy labels (secret, private:repo, empty for public)
- Integrity labels (`merged:repo`, `approved:repo`, `unapproved:repo`, or empty for untrusted)
- Description strings

### response_paths.rs (341 lines)
**Preferred response labeling format**

Generates path-based labels using RFC 6901 JSON Pointers:
- More efficient (no JSON cloning)
- Works well with large result sets
- Returns JSON paths like `/items/0`, `/items/1` pointing to labeled objects

Use for:
- Collection responses (lists of issues, PRs, commits, etc.)
- Large result sets where memory efficiency matters

### response_items.rs (391 lines)
**Legacy response labeling format**

Generates item-based labels by cloning data:
- Clones entire JSON objects for each item
- More memory intensive
- Legacy format for backward compatibility

Use only when:
- Path-based labeling is not suitable
- You need the data embedded with labels
- Working with single-item responses

### mod.rs (419 lines)
Public module interface:
- Re-exports public API from sub-modules
- Contains `extract_mcp_response()` for unwrapping MCP responses
- Defines response labeling structures (PathLabelEntry, PathLabelResult)
- Contains comprehensive test suite (22 tests)

## Key Design Decisions

### Performance: Path-Based vs Item-Based Labeling

**Prefer path-based labeling** (`label_response_paths`) over item-based (`label_response_items`):
- Avoids cloning large JSON objects (issues, PRs, commits)
- More efficient for collections (up to 100 items per response)
- Uses JSON Pointers to reference data in place

**Use item-based labeling** only when:
- Filtering or transforming results
- Single-item responses where cloning cost is negligible
- Backward compatibility with legacy code

### Label Helpers Return Owned Data

Functions like `secret_label()`, `writer_integrity()` return `Vec<String>` rather than iterators because:
1. They create fresh, owned data that doesn't exist before the call
2. The Vec is immediately consumed/moved in all usage sites
3. Collections are small (0-2 items), so overhead is minimal
4. Returning iterators would require `Box<dyn Iterator>` or complex lifetimes

Compare with `maintainers()` and `contributors()` in permissions.rs which return iterators because they reference existing data.

## Future Improvements

Areas identified for further refactoring (from quality evaluation):

1. **Split large functions** - `label_response_paths` (309 lines) and `label_response_items` (375 lines) could be split into per-tool handler functions

2. **Reduce string duplication** - More field extraction could use `get_str_or()` to borrow strings and constants from `field_names`

3. **Extract common patterns** - Both response functions have similar match patterns that could be unified

## Testing

All labeling logic is covered by 22 unit tests in `mod.rs::tests`. Run with:
```bash
cd rust-guard && cargo test --lib
```

Tests cover:
- Integrity level determination
- Label helper functions
- JSON extraction utilities
- Tool-specific label rules
- Edge cases (empty inputs, missing fields)
