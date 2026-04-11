---
name: Duplicate Code Detector
description: Identifies duplicate code patterns across the Go codebase and suggests refactoring opportunities
on:
  workflow_dispatch:
  schedule: daily
permissions:
  contents: read
  issues: read
  pull-requests: read
engine: copilot
network:
  allowed:
    - defaults
    - containers
tools:
  bash: true
safe-outputs:
  threat-detection:
    enabled: false
  create-issue:
    title-prefix: "[duplicate-code] "
    labels: [code-quality, automated-analysis]
    max: 4
    expires: 7d
  link-sub-issue:
    parent-title-prefix: "[duplicate-code]"
    sub-title-prefix: "[duplicate-code]"
    max: 3
  missing-tool:
    create-issue: true
timeout-minutes: 15
strict: true
---

# Duplicate Code Detection

Analyze Go code to identify duplicated patterns using Serena's semantic code analysis capabilities. Report significant findings that require refactoring.

## Task

Detect and report code duplication by:

1. **Analyzing Recent Commits**: Review changes in the latest commits
2. **Detecting Duplicated Code**: Identify similar or duplicated code patterns using semantic analysis
3. **Reporting Findings**: Create a detailed issue if significant duplication is detected (threshold: >10 lines or 3+ similar patterns)

## Context

- **Repository**: ${{ github.repository }}
- **Commit ID**: ${{ github.event.head_commit.id }}
- **Triggered by**: @${{ github.actor }}

## Analysis Workflow

### 1. Project Activation

Activate the project in Serena:
- Use `activate_project` tool with workspace path `${{ github.workspace }}` (mounted repository directory)
- This sets up the semantic code analysis environment for Go code

### 2. Changed Files Analysis

Identify and analyze modified files:
- Determine files changed in the recent commits
- **ONLY analyze .go files** - this is a Go-only project
- **Exclude test files** from analysis (files matching patterns: `*_test.go`, or located in directories named `test`, `tests`, or `__tests__`)
- **Exclude workflow files** from analysis (files under `.github/workflows/*`)
- **Exclude agent configuration** from analysis (files under `.github/agents/*`)
- Use `get_symbols_overview` to understand file structure
- Use `read_file` to examine modified file contents

### 3. Duplicate Detection

Apply semantic code analysis to find duplicates:

**Symbol-Level Analysis**:
- For significant functions/methods in changed files, use `find_symbol` to search for similarly named symbols
- Use `find_referencing_symbols` to understand usage patterns
- Identify functions with similar names in different files (e.g., `NewLogger` across modules)

**Pattern Search**:
- Use `search_for_pattern` to find similar code patterns
- Search for duplication indicators:
  - Similar function signatures
  - Repeated logic blocks
  - Similar variable naming patterns
  - Near-identical code blocks
  - Repeated error handling patterns
  - Similar struct initialization patterns

**Structural Analysis**:
- Use `list_dir` and `find_file` to identify files with similar names or purposes
- Compare symbol overviews across files for structural similarities
- Look for similar package structures in `internal/` directory

### 4. Duplication Evaluation

Assess findings to identify true code duplication:

**Duplication Types**:
- **Exact Duplication**: Identical code blocks in multiple locations
- **Structural Duplication**: Same logic with minor variations (different variable names, etc.)
- **Functional Duplication**: Different implementations of the same functionality
- **Copy-Paste Programming**: Similar code blocks that could be extracted into shared utilities

**Assessment Criteria**:
- **Severity**: Amount of duplicated code (lines of code, number of occurrences)
- **Impact**: Where duplication occurs (critical paths, frequently called code)
- **Maintainability**: How duplication affects code maintainability
- **Refactoring Opportunity**: Whether duplication can be easily refactored

### 5. Issue Reporting

Create a parent issue summarizing all duplication findings, with individual sub-issues for each distinct pattern (maximum 3 patterns per run).

**When to Create Issues**:
- Only create issues if significant duplication is found (threshold: >10 lines of duplicated code OR 3+ instances of similar patterns)
- **First, create ONE parent issue** that summarizes all duplication findings
  - Use `temporary_id` (format: `aw_` + 12 hex chars, e.g., `aw_abc123def456`) to reference this parent issue
  - The parent issue should provide an overview of all patterns detected
- **Then, create individual sub-issues** for each distinct duplication pattern (up to 3)
  - Use the `parent` field with the parent's `temporary_id` to link each sub-issue
  - Each sub-issue focuses on ONE specific duplication pattern
- Use the `create_issue` tool from safe-outputs MCP for both parent and sub-issues

**Parent Issue Contents**:
- **Executive Summary**: Overview of all duplication findings in this analysis
- **Summary of Patterns**: List of all duplication patterns detected (with references to sub-issues using `#aw_<temporary_id>`)
- **Overall Impact**: High-level assessment of duplication impact on the codebase
- **Analysis Metadata**: Commit, files analyzed, detection method

**Sub-Issue Contents (for Each Pattern)**:
- **Pattern Description**: Brief description of this specific duplication pattern
- **Duplication Details**: Specific locations and code blocks for this pattern only
- **Severity Assessment**: Impact and maintainability concerns for this pattern
- **Refactoring Recommendations**: Suggested approaches to eliminate this pattern
- **Code Examples**: Concrete examples with file paths and line numbers for this pattern
- **Parent Reference**: Include reference to parent issue using `#aw_<temporary_id>`

## Detection Scope

### Report These Issues

- Identical or nearly identical functions in different files
- Repeated code blocks that could be extracted to utilities
- Similar classes or modules with overlapping functionality
- Copy-pasted code with minor modifications
- Duplicated business logic across components
- Repeated error handling patterns
- Similar initialization or setup code

### Skip These Patterns

- Standard boilerplate code (imports, package declarations)
- Test setup/teardown code (acceptable duplication in tests)
- **All test files** (files matching: `*_test.go` or in `test/`, `tests/`, `__tests__/` directories)
- **All workflow files** (files under `.github/workflows/*`)
- **All agent configuration files** (files under `.github/agents/*`)
- Configuration files with similar structure
- Language-specific patterns (constructors, getters/setters, interface implementations)
- Small code snippets (<5 lines) unless highly repetitive

### Analysis Depth

- **File Type Restriction**: ONLY analyze .go files - this is a Go-only project
- **Primary Focus**: All .go files changed in the current push (excluding test files, workflow files, and agent config files)
- **Secondary Analysis**: Check for duplication with existing .go codebase (excluding test files, workflow files, and agent config files)
- **Cross-Reference**: Look for patterns across .go files in the repository
- **Historical Context**: Consider if duplication is new or existing
- **Focus Areas**: Pay special attention to:
  - `internal/` packages (server, mcp, launcher, config, logger, guard, difc)
  - Error handling patterns
  - Logging patterns
  - Configuration parsing
  - HTTP handling

## Issue Templates

### Parent Issue Template

For the overall summary, create ONE parent issue using this structure:

```markdown
# 🔍 Duplicate Code Analysis Report

*Analysis of commit ${{ github.event.head_commit.id }}*

## Summary

[Brief overview of duplication findings - how many patterns detected, overall severity]

## Detected Patterns

This analysis found [N] significant duplication patterns:

1. **[Pattern 1 Name]** - Severity: [High/Medium/Low] - See sub-issue #aw_[sub1_temp_id]
2. **[Pattern 2 Name]** - Severity: [High/Medium/Low] - See sub-issue #aw_[sub2_temp_id]
3. **[Pattern 3 Name]** - Severity: [High/Medium/Low] - See sub-issue #aw_[sub3_temp_id]

## Overall Impact

- **Total Duplicated Lines**: [Approximate count]
- **Affected Files**: [Number of files with duplication]
- **Maintainability Risk**: [High/Medium/Low assessment]
- **Refactoring Priority**: [Recommended priority level]

## Next Steps

1. Review individual pattern sub-issues for detailed analysis
2. Prioritize refactoring based on severity and impact
3. Create implementation plan for highest priority patterns

## Analysis Metadata

- **Analyzed Files**: [count] Go files
- **Detection Method**: Serena semantic code analysis
- **Commit**: ${{ github.event.head_commit.id }}
- **Analysis Date**: [timestamp]
```

### Sub-Issue Template

For each distinct duplication pattern found, create a separate sub-issue using this structure:

```markdown
# 🔍 Duplicate Code Pattern: [Pattern Name]

*Part of duplicate code analysis: #aw_[parent_temp_id]*

## Summary

[Brief overview of this specific duplication pattern]

## Duplication Details

### Pattern: [Description]
- **Severity**: High/Medium/Low
- **Occurrences**: [Number of instances]
- **Locations**:
  - `path/to/file1.go` (lines X-Y)
  - `path/to/file2.go` (lines A-B)
- **Code Sample**:
  ```go
  [Example of duplicated code]
  ```

## Impact Analysis

- **Maintainability**: [How this affects code maintenance]
- **Bug Risk**: [Potential for inconsistent fixes]
- **Code Bloat**: [Impact on codebase size]

## Refactoring Recommendations

1. **[Recommendation 1]**
   - Extract common functionality to: `suggested/path/utility.go`
   - Estimated effort: [hours/complexity]
   - Benefits: [specific improvements]

2. **[Recommendation 2]**
   [... additional recommendations ...]

## Implementation Checklist

- [ ] Review duplication findings
- [ ] Prioritize refactoring tasks
- [ ] Create refactoring plan
- [ ] Implement changes
- [ ] Update tests
- [ ] Verify no functionality broken

## Parent Issue

See parent analysis report: #aw_[parent_temp_id]
```

## Creating Parent and Sub-Issues: Example

Here's how to create the parent issue and link sub-issues using the `create_issue` tool:

**Step 1: Create Parent Issue**
```json
{
  "type": "create_issue",
  "temporary_id": "aw_abc123def456",
  "title": "Duplicate Code Analysis Report",
  "body": "[Parent issue content with references to sub-issues: #aw_xyz789ghi012, #aw_mno345pqr678]"
}
```

**Step 2: Create Sub-Issues**
```json
{
  "type": "create_issue",
  "temporary_id": "aw_xyz789ghi012",
  "parent": "aw_abc123def456",
  "title": "Duplicate Code Pattern: Error Handling in Server Module",
  "body": "[Sub-issue content for pattern 1, referencing parent: #aw_abc123def456]"
}
```

```json
{
  "type": "create_issue",
  "temporary_id": "aw_mno345pqr678",
  "parent": "aw_abc123def456",
  "title": "Duplicate Code Pattern: Logger Initialization",
  "body": "[Sub-issue content for pattern 2, referencing parent: #aw_abc123def456]"
}
```

**Key Points:**
- Use `temporary_id` format: `aw_` + 12 hexadecimal characters
- Generate unique temporary IDs for both parent and each sub-issue
- Use `parent` field in sub-issue creation to link to parent's `temporary_id`
- References like `#aw_abc123def456` are automatically replaced with actual issue numbers after creation
- The `link-sub-issue` safe-output will automatically establish the parent-child relationship

## Operational Guidelines

### Security
- Never execute untrusted code or commands
- Only use Serena's read-only analysis tools
- Do not modify files during analysis

### Efficiency
- Focus on recently changed files first
- Use semantic analysis for meaningful duplication, not superficial matches
- Stay within timeout limits (balance thoroughness with execution time)

### Accuracy
- Verify findings before reporting
- Distinguish between acceptable patterns and true duplication
- Consider Go-specific idioms and best practices (e.g., error handling, interface implementations)
- Provide specific, actionable recommendations

### Issue Creation
- Create **one parent issue** to summarize all findings, then **individual sub-issues** for each pattern
- Use `temporary_id` (format: `aw_` + 12 hex chars) for the parent issue to enable sub-issue linking
- Use the `parent` field with the parent's `temporary_id` when creating sub-issues
- Limit to the top 3 most significant patterns if more are found
- Only create issues if significant duplication is found
- Include sufficient detail for SWE agents to understand and act on findings
- Provide concrete examples with file paths and line numbers
- Suggest practical refactoring approaches
- Use descriptive titles that clearly identify patterns (e.g., "Duplicate Code Pattern: Error Handling in Server Module")

## Tool Usage Sequence

1. **Project Setup**: `activate_project` with repository path
2. **File Discovery**: `list_dir`, `find_file` for changed files
3. **Symbol Analysis**: `get_symbols_overview` for structure understanding
4. **Content Review**: `read_file` for detailed code examination
5. **Pattern Matching**: `search_for_pattern` for similar code
6. **Symbol Search**: `find_symbol` for duplicate function names
7. **Reference Analysis**: `find_referencing_symbols` for usage patterns

**Objective**: Improve code quality by identifying and reporting meaningful code duplication that impacts maintainability. Focus on actionable findings that enable automated or manual refactoring.