---
name: Test Improver
description: Daily analyzer that reviews test files and suggests improvements for better testify usage, increased coverage, and cleaner tests
on:
  schedule: daily
  workflow_dispatch:

permissions:
  contents: read
  issues: read
  pull-requests: read
  copilot-requests: write

network:
  allowed:
    - defaults
    - containers
    - go

steps:
  - name: Set up Go
    uses: actions/setup-go@v7.1.0
    with:
      go-version-file: go.mod
      cache: true

  - name: Discover test files
    run: |
      mkdir -p /tmp/gh-aw/data
      find internal -name '*_test.go' -type f | while read -r f; do
        pkg=$(head -1 "$f" | sed 's/^package //')
        lines=$(wc -l < "$f")
        funcs=$(grep -c '^func Test' "$f" || true)
        printf '{"path":"%s","package":"%s","lines":%d,"funcs":%d}\n' "$f" "$pkg" "$lines" "$funcs"
      done | jq -s '.' > /tmp/gh-aw/data/test-files.json

  - name: Check for existing PR
    env:
      GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
      EXPR_GITHUB_REPOSITORY: ${{ github.repository }}
    run: |
      gh pr list --repo "$EXPR_GITHUB_REPOSITORY" \
        --state open --search "[test-improver]" \
        --json number,title --limit 5 \
        > /tmp/gh-aw/data/existing-prs.json

safe-outputs:
  threat-detection:
    enabled: false
  create-pull-request:
    title-prefix: "[test-improver] "
    labels: [testing, improvement, automation]
    draft: true
  noop:
    max: 1

tools:
  cache-memory: true
  github:
    mode: gh-proxy
    toolsets: [default]
    allowed-repos: ["github/gh-aw-mcpg"]
    min-integrity: unapproved
  edit:
  bash: true

max-ai-credits: 2500
timeout-minutes: 30
strict: true
---

# Test Improver 🧹

Improve one Go test file per run: better assertions, more coverage, cleaner structure.

## Context

This project uses testify (`github.com/stretchr/testify`). Prefer `require` for fatal checks and `assert` for non-fatal. Use bound asserters (`assert := assert.New(t)`) for files with many assertions.

## Step 1: Select a Test File

Read `/tmp/gh-aw/data/test-files.json` for the pre-computed inventory. Also check `/tmp/gh-aw/data/existing-prs.json` — if an open `[test-improver]` PR exists, call `noop` and stop.

Use `cache-memory` to check which files were previously improved. Select a file that:
- Has manual error checking instead of testify assertions
- Has low coverage of the corresponding implementation
- Has repetitive code that could be table-driven
- Was NOT recently modified (check `git log --oneline -3 <file>`)

Avoid integration tests in `test/integration/`.

## Step 2: Analyze

1. Read the test file and its corresponding implementation file
2. Run coverage on the specific package only:
   ```bash
   go test -coverprofile=/tmp/coverage.out ./internal/<package>/
   go tool cover -func=/tmp/coverage.out
   ```
3. Identify: uncovered branches, missing edge cases, poor assertion patterns

## Step 3: Improve

Focus on:
- **Testify usage**: Replace manual `if err != nil { t.Fatal(...) }` with `require.NoError(t, err)`, use `assert.Equal`, `assert.Contains`, etc.
- **Coverage**: Add tests for uncovered error paths and edge cases
- **Structure**: Convert repetitive tests to table-driven with `t.Run()`
- **Stability**: Use `t.Cleanup()`, avoid timing dependencies, mock externals

Preserve all existing passing tests. Follow project conventions in `internal/testutil/`.

## Step 4: Verify

```bash
go test -v ./internal/<package>/
go test -count=3 ./internal/<package>/
go vet ./internal/<package>/
gofmt -l <test_file>
```

## Step 5: Output

**If improvements were made**: Create a PR via `create-pull-request` with title `Improve tests for <PackageName>`. Include in the body: file analyzed, improvements made, coverage before/after, and test output.

**If no improvements needed**: Call `noop`.

Save the improved file path to `cache-memory` for future runs.