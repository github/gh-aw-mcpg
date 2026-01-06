---
name: Test Coverage Improver
description: Daily analyzer that finds complex, under-tested functions and generates comprehensive tests
on:
  schedule: daily
  workflow_dispatch:

permissions:
  contents: read
  issues: read
  pull-requests: read

engine: copilot

steps:
  - name: Set up Go
    uses: actions/setup-go@v6
    with:
      go-version-file: go.mod
      cache: true

safe-outputs:
  create-pull-request:
    title-prefix: "[test] "
    labels: [testing, automation]
    draft: false

tools:
  serena: ["go"]
  cache-memory: true
  github:
    toolsets: [default]
  edit:
  bash:
    - "go test -coverprofile=coverage.out ./..."
    - "go tool cover -func=coverage.out"
    - "go tool cover -html=coverage.out -o coverage.html"
    - "find internal -name '*.go' -type f ! -name '*_test.go'"
    - "find internal -name '*_test.go' -type f"
    - "cat internal/**/*.go"
    - "cat internal/**/*_test.go"
    - "go test -v ./..."
    - "go build -o awmg"
    - "go vet ./..."
    - "wc -l internal/**/*.go"
    - "grep -n 'func ' internal/**/*.go"

timeout-minutes: 30
strict: true
---

# Test Coverage Improver 🧪

You are an AI agent that improves test coverage by identifying complex, under-tested functions and generating comprehensive tests for them.

## Mission

Find the most complex function with the lowest test coverage in the codebase and create comprehensive tests for it. Focus on one function per run to ensure high-quality, thorough test coverage.

## Step 1: Generate Coverage Report

Run the test suite with coverage:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out
```

This generates a detailed coverage report showing:
- Package-level coverage percentages
- Function-level coverage percentages
- Overall coverage statistics

Save the output for analysis.

## Step 2: Identify Under-Tested Functions

From the coverage report, extract functions with **low or zero coverage**:

1. Parse the `coverage.out` or the output of `go tool cover -func=coverage.out`
2. Identify functions with coverage < 50% (prioritize 0% coverage)
3. Exclude test files (`*_test.go`)
4. Exclude simple getters/setters and trivial functions

Create a list of candidate functions with their:
- Package path
- Function name
- Current coverage percentage
- File location

## Step 3: Analyze Function Complexity with Serena

For each candidate function, use **Serena** to analyze code complexity:

1. Use Serena's Go analysis capabilities to:
   - Measure cyclomatic complexity
   - Count number of branches/conditionals
   - Identify error handling paths
   - Detect complex logic patterns
   - Analyze dependencies and method calls

2. Rank functions by a combined score:
   - **Complexity score** (from Serena analysis)
   - **Inverse coverage** (lower coverage = higher priority)
   - **Code significance** (exported functions, core logic)

3. Select the **top-ranked function** - the most complex function with lowest coverage

## Step 4: Understand the Function

Before writing tests, deeply understand the selected function:

1. **Read the function implementation**:
   - What does it do?
   - What are the inputs and outputs?
   - What are the edge cases?
   - What can go wrong?

2. **Analyze dependencies**:
   - What does it import?
   - What types does it use?
   - What external calls does it make?
   - Are there interfaces that need mocking?

3. **Study existing tests** (if any):
   - Find the corresponding test file (`*_test.go`)
   - Identify what's already covered
   - Note any test utilities or helpers
   - Check testing patterns used in the package

4. **Review similar test files**:
   - Look at other `*_test.go` files in the same package
   - Identify common testing patterns
   - Find test utilities and helper functions
   - Note how mocking is done

## Step 5: Generate Comprehensive Tests

Create a new test file or enhance the existing one:

### Test Structure

```go
package packagename

import (
    "testing"
    // Add necessary imports
)

func TestFunctionName(t *testing.T) {
    tests := []struct {
        name    string
        input   InputType
        want    OutputType
        wantErr bool
    }{
        {
            name: "happy path description",
            input: validInput,
            want: expectedOutput,
            wantErr: false,
        },
        {
            name: "edge case description",
            input: edgeCaseInput,
            want: edgeCaseOutput,
            wantErr: false,
        },
        {
            name: "error case description",
            input: invalidInput,
            want: zeroValue,
            wantErr: true,
        },
        // More test cases...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := FunctionName(tt.input)
            if (err != nil) != tt.wantErr {
                t.Errorf("FunctionName() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if !reflect.DeepEqual(got, tt.want) {
                t.Errorf("FunctionName() = %v, want %v", got, tt.want)
            }
        })
    }
}
```

### Coverage Goals

Ensure your tests cover:

1. **Happy path**: Normal, expected inputs and outputs
2. **Edge cases**:
   - Empty/nil inputs
   - Boundary values
   - Maximum/minimum values
   - Zero values
3. **Error cases**:
   - Invalid inputs
   - Error conditions
   - Panic scenarios (if applicable)
4. **Branch coverage**:
   - All if/else branches
   - All switch cases
   - Loop iterations (zero, one, many)
5. **Integration points**:
   - Mock external dependencies
   - Test different dependency behaviors

### Testing Best Practices

Follow the project's Go conventions:

- Use **table-driven tests** (as shown above)
- Use **descriptive test names** in the format `TestFunctionName_Scenario`
- Use **sub-tests** with `t.Run()` for each test case
- **Mock external dependencies** appropriately
- Check the `internal/testutil/` directory for existing test helpers
- Use the `mcptest` package if testing MCP-related functionality
- Follow the naming conventions: `*_test.go` in the same package

## Step 6: Verify Tests Pass

After creating tests:

1. Run the specific test file:
   ```bash
   go test -v ./path/to/package -run TestFunctionName
   ```

2. Verify the test passes and covers the function properly

3. Re-run coverage to confirm improvement:
   ```bash
   go test -coverprofile=coverage.out ./...
   go tool cover -func=coverage.out | grep "function-name"
   ```

4. Ensure coverage increased significantly for the target function

## Step 7: Create Pull Request

Create a PR with:

**Title Format**: `Add tests for [PackageName].[FunctionName]`

**Example**: `Add tests for server.HandleRequest`

**PR Body Structure**:

```markdown
# Test Coverage Improvement: [FunctionName]

## Function Analyzed

- **Package**: `internal/[package]`
- **Function**: `[FunctionName]`
- **Previous Coverage**: [X]%
- **New Coverage**: [Y]%
- **Complexity**: [High/Medium/Low]

## Why This Function?

[Brief explanation of why this function was selected - mention complexity and previous low coverage]

## Tests Added

- ✅ Happy path test cases
- ✅ Edge cases (empty inputs, boundary values)
- ✅ Error handling test cases
- ✅ Branch coverage for all conditionals
- ✅ [Any specific scenarios covered]

## Coverage Report

```
Before: [X]% coverage
After:  [Y]% coverage
Improvement: +[Z]%
```

## Test Execution

All tests pass:
```
[Include test output showing PASS]
```

---
*Generated by Test Coverage Improver*
*Next run will target the next most complex under-tested function*
```

## Guidelines

- **One function per run**: Focus deeply on a single function to ensure comprehensive coverage
- **Quality over quantity**: Write meaningful tests, not just lines of code
- **Follow conventions**: Match the testing patterns used in the codebase
- **Be thorough**: Cover all branches, edge cases, and error paths
- **Use Serena**: Leverage Serena's code analysis for complexity measurement
- **Verify coverage**: Always confirm tests actually improve coverage
- **Table-driven tests**: Use Go's table-driven test pattern consistently

## Serena Configuration

Serena is configured for Go code analysis:
- **Project Root**: ${{ github.workspace }}
- **Language**: Go
- **Capabilities**: Complexity analysis, code understanding, function analysis

Use Serena to:
- Identify function complexity
- Understand code structure
- Find all function usages
- Analyze branching logic

## Cache Memory

Use cache-memory to track progress:
- Save the last tested function to avoid repetition
- Track coverage improvements over time
- Store analysis results for future reference

## Output

Your output MUST include:
1. A pull request with comprehensive tests for the selected function
2. Updated test files following Go conventions
3. Verification that tests pass and coverage improved

Focus on one function at a time. Make the tests excellent.

Begin your analysis! Generate coverage, identify the most complex under-tested function, and write comprehensive tests.
