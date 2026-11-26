package middleware

// Tests targeting previously uncovered branches in jqschema.go:
//   - inferSchema default case (custom types via reflect)
//   - savePayload error paths (unwritable directories)
//   - WrapToolHandler with custom struct data (triggers default branch in type switch)
//   - WrapToolHandler when savePayload fails (continues and returns metadata on save failure)
//   - runJqCode type error handling (ValueError and runtime type errors)
//   - CompileToolResponseFilterWithVars (variable injection)
//   - filter compilation cache (CompileToolResponseFilter cache hit)

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/jqutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/itchyny/gojq"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// inferSchema default case (lines 104-115 in jqschema.go)
// ---------------------------------------------------------------------------

// customIntType has kind reflect.Int32, which is NOT listed in the explicit
// type-switch cases (those only list the untyped int/int32 etc. kinds), so it
// falls through to the default branch and is classified via reflect.
type customIntType int32

// customUintType similarly exercises the uint reflect path.
type customUintType uint64

// customFloatType exercises the float reflect path.
type customFloatType float32

// myStruct is an arbitrary struct that falls through to the inner "string" default.
type myStruct struct {
	X int
	Y string
}

func TestInferSchema_DefaultCaseFallback(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name:     "custom named int type returns number",
			input:    customIntType(42),
			expected: "number",
		},
		{
			name:     "custom named uint type returns number",
			input:    customUintType(100),
			expected: "number",
		},
		{
			name:     "custom named float type returns number",
			input:    customFloatType(3.14),
			expected: "number",
		},
		{
			name:     "struct type returns string",
			input:    myStruct{X: 1, Y: "hello"},
			expected: "string",
		},
		{
			name:     "chan type returns string",
			input:    make(chan int),
			expected: "string",
		},
		{
			// []int is NOT []interface{} so it falls through to default.
			name:     "typed int slice returns string",
			input:    []int{1, 2, 3},
			expected: "string",
		},
		{
			// map[string]string is NOT map[string]interface{} so it falls through.
			name:     "typed string map returns string",
			input:    map[string]string{"key": "value"},
			expected: "string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferSchema(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestInferSchema_DefaultCaseNestedInMap exercises the default branch when a
// custom type appears as a value inside a map[string]interface{}.
func TestInferSchema_DefaultCaseNestedInMap(t *testing.T) {
	input := map[string]interface{}{
		"normal":  float64(1),
		"custom":  customIntType(99),
		"strType": []int{1, 2, 3},
	}
	got := inferSchema(input)
	schema, ok := got.(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "number", schema["normal"])
	assert.Equal(t, "number", schema["custom"])  // customIntType → reflect.Int32 → "number"
	assert.Equal(t, "string", schema["strType"]) // []int → reflect.Slice → "string"
}

// ---------------------------------------------------------------------------
// savePayload error paths (lines 231-256 in jqschema.go)
// ---------------------------------------------------------------------------

// TestSavePayload_MkdirAllFailure covers the os.MkdirAll error path by using a
// base directory whose parent is read-only (preventing subdirectory creation).
func TestSavePayload_MkdirAllFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission model differs on Windows")
	}

	// Create a temporary directory and make it read-only so that MkdirAll
	// cannot create any subdirectories inside it.
	readOnlyDir := t.TempDir()
	require.NoError(t, os.Chmod(readOnlyDir, 0444))
	t.Cleanup(func() {
		// Restore permissions so t.TempDir cleanup can remove the directory.
		_ = os.Chmod(readOnlyDir, 0755)
	})

	// Skip the test when running as root (root can write to read-only dirs).
	if os.Getuid() == 0 {
		t.Skip("skipping read-only directory test when running as root")
	}

	_, err := savePayload(readOnlyDir, "", "session1", "query1", []byte(`{"key":"value"}`))
	require.Error(t, err, "savePayload should return an error when MkdirAll fails")
	assert.ErrorContains(t, err, "failed to create payload directory")
}

// TestSavePayload_WriteFileFailure covers the os.WriteFile error path by using a
// directory path that resolves to an existing file (so the path cannot be a dir).
func TestSavePayload_WriteFileFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission model differs on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("skipping file-permission test when running as root")
	}

	// Create the target directory structure that savePayload would create,
	// then place a read-only file at the exact payload path.
	baseDir := t.TempDir()
	sessionID := "test-session"
	queryID := "test-query"
	payloadDir := filepath.Join(baseDir, sessionID, queryID)
	require.NoError(t, os.MkdirAll(payloadDir, 0755))

	// Write a read-only file at the exact location savePayload would write.
	payloadFile := filepath.Join(payloadDir, "payload.json")
	require.NoError(t, os.WriteFile(payloadFile, []byte("existing"), 0444))
	t.Cleanup(func() { _ = os.Chmod(payloadFile, 0644) })

	_, err := savePayload(baseDir, "", sessionID, queryID, []byte(`{"key":"value"}`))
	// On Linux, writing to a 0444 file returns "permission denied"
	require.Error(t, err, "savePayload should return an error when WriteFile fails")
	assert.ErrorContains(t, err, "failed to write payload file")
}

// ---------------------------------------------------------------------------
// WrapToolHandler with custom struct data (default branch in type switch)
// ---------------------------------------------------------------------------

// TestWrapToolHandler_CustomStructData exercises the default arm of the data
// type switch inside WrapToolHandler (lines 388-391 in jqschema.go).  A plain
// Go struct is returned as the data value so that none of the explicit cases
// (map[string]interface{}, []interface{}, string, float64, bool, nil,
// json.Number) match.
func TestWrapToolHandler_CustomStructData(t *testing.T) {
	baseDir := t.TempDir()

	type ToolOutput struct {
		Count int    `json:"count"`
		Label string `json:"label"`
	}

	// Return a custom struct as data.
	customData := ToolOutput{Count: 42, Label: "hello"}
	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, customData, nil
	}

	// Use threshold 0 so the large-payload path is always taken.
	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 0, testGetSessionID)
	result, _, err := wrapped(context.Background(), &sdk.CallToolRequest{}, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)

	// Verify the response is a metadata object (large-payload path taken).
	require.NotEmpty(t, result.Content)
	textContent, ok := result.Content[0].(*sdk.TextContent)
	require.True(t, ok)

	var meta map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &meta))
	// payloadSchema for {"count": 42, "label": "hello"} should be a map with "number" and "string".
	schema, ok := meta["payloadSchema"].(map[string]interface{})
	require.True(t, ok, "payloadSchema should be an object, got: %T %v", meta["payloadSchema"], meta["payloadSchema"])
	assert.Equal(t, "number", schema["count"])
	assert.Equal(t, "string", schema["label"])
}

// ---------------------------------------------------------------------------
// CompileToolResponseFilter error paths
// ---------------------------------------------------------------------------

// TestCompileToolResponseFilter_ValidFilter verifies that a valid jq expression
// compiles without error and returns non-nil code.
func TestCompileToolResponseFilter_ValidFilter(t *testing.T) {
	code, err := CompileToolResponseFilter(".")
	require.NoError(t, err)
	require.NotNil(t, code)
}

// TestCompileToolResponseFilter_ParseError verifies that a syntactically invalid
// jq expression returns a descriptive error from the parse phase.
func TestCompileToolResponseFilter_ParseError(t *testing.T) {
	tests := []struct {
		name   string
		filter string
	}{
		{name: "unclosed paren", filter: "(.foo"},
		{name: "bare pipe", filter: "| .foo"},
		{name: "invalid token", filter: "!!!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, err := CompileToolResponseFilter(tt.filter)
			require.Error(t, err)
			assert.Nil(t, code)
			require.ErrorContains(t, err, "failed to parse tool response filter")
			require.ErrorContains(t, err, "offset")
			require.ErrorContains(t, err, "token")
		})
	}
}

// TestCompileToolResponseFilter_EnvDisabled verifies that $ENV access is
// blocked in compiled tool response filters (defense-in-depth). The compiled
// code object itself is used directly (via applyToolResponseFilter) to
// confirm that the WithEnvironLoader option was applied at compile time.
func TestCompileToolResponseFilter_EnvDisabled(t *testing.T) {
	// $ENV should resolve to an empty object (not real env vars) when
	// WithEnvironLoader returns nil.
	code, err := CompileToolResponseFilter(". + {env: $ENV}")
	require.NoError(t, err)
	require.NotNil(t, code)

	// Use the compiled code object directly so we verify the options
	// applied during CompileToolResponseFilter, not a re-compile.
	result, runErr := applyToolResponseFilter(context.Background(), code, map[string]interface{}{"a": 1})
	require.NoError(t, runErr)
	m, ok := result.(map[string]interface{})
	require.True(t, ok)
	// Original input field must be preserved by the merge.
	assert.Equal(t, 1, m["a"])
	// $ENV must be an empty object, not the real process environment.
	env, ok := m["env"].(map[string]interface{})
	require.True(t, ok, "env should be an object, got: %T", m["env"])
	assert.Empty(t, env)
}

// TestApplyToolResponseFilter_ParseError verifies that ApplyToolResponseFilter
// propagates compile errors from CompileToolResponseFilter.
func TestApplyToolResponseFilter_ParseError(t *testing.T) {
	_, err := ApplyToolResponseFilter(context.Background(), "(.invalid", map[string]interface{}{"a": 1})
	require.Error(t, err)
	require.ErrorContains(t, err, "failed to parse tool response filter")
	require.ErrorContains(t, err, "offset")
	require.ErrorContains(t, err, "token")
}

// ---------------------------------------------------------------------------
// wrapToolHandler with invalid filter (compile-error warning path)
// ---------------------------------------------------------------------------

// TestWrapToolHandlerWithFilter_InvalidFilterFallsBack verifies that when
// WrapToolHandlerWithFilter is given an unparseable jq filter, it logs a
// warning and continues without filtering (i.e. the handler still succeeds
// and returns the original data).
func TestWrapToolHandlerWithFilter_InvalidFilterFallsBack(t *testing.T) {
	baseDir := t.TempDir()

	payload := map[string]interface{}{"key": "value"}
	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, payload, nil
	}

	// Use a syntactically invalid filter. wrapToolHandler should log a warning
	// and proceed as if no filter was set (filterCode == nil). Use a threshold
	// large enough to keep this small payload inline so we can assert that the
	// original data is returned unchanged.
	wrapped := WrapToolHandlerWithFilter(mockHandler, "test_tool", baseDir, "", 1024, testGetSessionID, "(.invalid")
	result, returnedData, err := wrapped(context.Background(), &sdk.CallToolRequest{}, nil)

	// The call must succeed despite the invalid filter, and the original
	// unfiltered payload must be preserved.
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, payload, returnedData)
}

// ---------------------------------------------------------------------------
// WrapToolHandler when savePayload fails
// (lines 358-365: save error is logged but processing continues)
// ---------------------------------------------------------------------------

// TestWrapToolHandler_SavePayloadFailure verifies that when savePayload cannot
// write the file (e.g. unwritable base directory), WrapToolHandler logs the
// error but still returns a metadata response (schema + preview) rather than
// propagating the failure to the caller.
func TestWrapToolHandler_SavePayloadFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission model differs on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("skipping read-only directory test when running as root")
	}

	// Make baseDir read-only so that MkdirAll cannot create subdirectories.
	baseDir := t.TempDir()
	require.NoError(t, os.Chmod(baseDir, 0444))
	t.Cleanup(func() { _ = os.Chmod(baseDir, 0755) })

	largeData := map[string]interface{}{
		"items": make([]interface{}, 100),
	}
	for i := range largeData["items"].([]interface{}) {
		largeData["items"].([]interface{})[i] = map[string]interface{}{"id": float64(i)}
	}

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, largeData, nil
	}

	// Threshold of 1 forces large-payload path.
	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 1, testGetSessionID)
	result, _, err := wrapped(context.Background(), &sdk.CallToolRequest{}, nil)

	// WrapToolHandler must not propagate the savePayload error.
	require.NoError(t, err, "WrapToolHandler must not return an error when savePayload fails")
	require.NotNil(t, result)

	// Assert the observable failure-specific behavior: the wrapper still returns
	// metadata (schema + preview), but no payload path/file is produced.
	require.Len(t, result.Content, 1)
	textContent, ok := result.Content[0].(*sdk.TextContent)
	require.True(t, ok, "expected TextContent in result")

	var meta map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(textContent.Text), &meta))
	assert.Contains(t, meta, "payloadSchema", "expected metadata response to include payloadSchema when payload saving fails")
	assert.Contains(t, meta, "payloadPreview", "expected metadata response to include payloadPreview when payload saving fails")
	// payloadPath should be empty (no file was saved).
	assert.Empty(t, meta["payloadPath"], "payloadPath should be empty when savePayload fails")

	entries, readErr := os.ReadDir(baseDir)
	require.NoError(t, readErr)
	assert.Empty(t, entries, "savePayload failure path should not leave any saved payload files or directories behind")
}

// ---------------------------------------------------------------------------
// runJqCode type error and ValueError handling
// ---------------------------------------------------------------------------

// TestApplyToolResponseFilter_TypeError verifies that runtime type errors from
// jq (e.g., applying a function to an incompatible value type) produce an error
// containing both the gojq error message and the null-safety hint.
func TestApplyToolResponseFilter_TypeError(t *testing.T) {
	tests := []struct {
		name    string
		filter  string
		input   any
		wantMsg string
	}{
		{
			// sort on null triggers a func0TypeError at runtime.
			name:    "sort on null",
			filter:  ". | sort",
			input:   nil,
			wantMsg: "check filter handles null/missing fields",
		},
		{
			// Adding string and number triggers a binopTypeError.
			name:    "add string and number",
			filter:  ". + 1",
			input:   "hello",
			wantMsg: "check filter handles null/missing fields",
		},
		{
			// ascii_upcase on a number triggers a func0TypeError.
			name:    "ascii_upcase on number",
			filter:  ". | ascii_upcase",
			input:   42.0,
			wantMsg: "check filter handles null/missing fields",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ApplyToolResponseFilter(context.Background(), tt.filter, tt.input)
			require.Error(t, err)
			require.ErrorContains(t, err, tt.wantMsg)
			// The gojq error message should be preserved in the wrapping.
			require.ErrorContains(t, err, "type error")
		})
	}
}

// TestApplyToolResponseFilter_ValueError verifies that errors produced by jq's
// error/1 builtin (catchable try-catch errors) are returned without the
// null-safety hint (they are intentional, not type errors).
func TestApplyToolResponseFilter_ValueError(t *testing.T) {
	// jq's error/1 produces a catchable ValueError, not a type error.
	// The wrapper should NOT add the null-safety hint.
	_, err := ApplyToolResponseFilter(context.Background(), `error("deliberate")`, map[string]any{"a": 1})
	require.Error(t, err)
	require.ErrorContains(t, err, "tool response filter error")
	assert.NotContains(t, err.Error(), "check filter handles null/missing fields")
}

// ---------------------------------------------------------------------------
// CompileToolResponseFilterWithVars
// ---------------------------------------------------------------------------

// TestCompileToolResponseFilterWithVars_Basic verifies that a filter compiled
// with variable declarations runs correctly when the variable is supplied to
// RunWithContext.
func TestCompileToolResponseFilterWithVars_Basic(t *testing.T) {
	code, err := CompileToolResponseFilterWithVars(". + {id: $toolID}", []string{"$toolID"})
	require.NoError(t, err)
	require.NotNil(t, code)

	ctx := context.Background()
	iter := code.RunWithContext(ctx, map[string]any{"name": "test"}, "my-tool")
	v, ok := iter.Next()
	require.True(t, ok)
	require.NotNil(t, v)
	result, ok := v.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "test", result["name"])
	assert.Equal(t, "my-tool", result["id"])
}

// TestCompileToolResponseFilterWithVars_EnvDisabled verifies that $ENV access
// is blocked even when variable names are provided (defense-in-depth).
func TestCompileToolResponseFilterWithVars_EnvDisabled(t *testing.T) {
	code, err := CompileToolResponseFilterWithVars(". + {env: $ENV, id: $toolID}", []string{"$toolID"})
	require.NoError(t, err)
	require.NotNil(t, code)

	ctx := context.Background()
	iter := code.RunWithContext(ctx, map[string]any{}, "my-tool")
	v, ok := iter.Next()
	require.True(t, ok)
	result, ok := v.(map[string]any)
	require.True(t, ok)
	env, ok := result["env"].(map[string]any)
	require.True(t, ok, "env should be an empty object")
	assert.Empty(t, env)
}

// TestCompileToolResponseFilterWithVars_ParseError verifies that a syntactically
// invalid filter returns a parse error.
func TestCompileToolResponseFilterWithVars_ParseError(t *testing.T) {
	code, err := CompileToolResponseFilterWithVars("(.invalid", []string{"$x"})
	require.Error(t, err)
	assert.Nil(t, code)
	require.ErrorContains(t, err, "failed to parse tool response filter")
	require.ErrorContains(t, err, "offset")
	require.ErrorContains(t, err, "token")
}

// TestCompileToolResponseFilterWithVars_UndeclaredVar verifies that a filter
// referencing an undeclared variable fails to compile.
func TestCompileToolResponseFilterWithVars_UndeclaredVar(t *testing.T) {
	code, err := CompileToolResponseFilterWithVars(". + {id: $undeclared}", []string{})
	require.Error(t, err)
	assert.Nil(t, code)
	assert.ErrorContains(t, err, "failed to compile tool response filter")
}

// TestCompileToolResponseFilter_GojqBuiltinRegressionCoverage captures gojq
// semantics fixed in v0.12.19 so future gojq upgrades keep compatibility for
// user-authored filters that rely on these builtins.
func TestCompileToolResponseFilter_GojqBuiltinRegressionCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filter   string
		input    any
		expected []any
	}{
		{
			name:     "gsub replacement emits multiple values",
			filter:   `gsub("a"; ("x","y"))`,
			input:    "ab",
			expected: []any{"xb", "yb"},
		},
		{
			name:     "sub replacement emits multiple values",
			filter:   `sub("a"; ("x","y"))`,
			input:    "ab",
			expected: []any{"xb", "yb"},
		},
		{
			name:     "join uses add semantics with null separator",
			filter:   `join(null)`,
			input:    []any{"a", nil, "b"},
			expected: []any{"ab"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, err := CompileToolResponseFilter(tc.filter)
			require.NoError(t, err)
			require.NotNil(t, code)

			iter := code.RunWithContext(context.Background(), tc.input)
			var actual []any
			for {
				v, ok := iter.Next()
				if !ok {
					break
				}
				actual = append(actual, v)
			}

			assert.Equal(t, tc.expected, actual)
		})
	}
}

// ---------------------------------------------------------------------------
// Filter compilation cache
// ---------------------------------------------------------------------------

// TestCompileToolResponseFilter_ConcurrentRuns verifies that cached code can be
// shared across goroutines without mixing inputs, variables, or iterator results.
func TestCompileToolResponseFilter_ConcurrentRuns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filter   string
		varNames []string
	}{
		{name: "plain", filter: `.[] | {value: (. + 1)}`},
		{name: "with variables", filter: `.[] | {value: (. + $offset)}`, varNames: []string{"$offset"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compile := func() (*gojq.Code, error) {
				if len(tt.varNames) > 0 {
					return CompileToolResponseFilterWithVars(tt.filter, tt.varNames)
				}
				return CompileToolResponseFilter(tt.filter)
			}

			code, err := compile()
			require.NoError(t, err)
			require.NotNil(t, code)

			for worker := range 16 {
				t.Run(fmt.Sprintf("worker-%d", worker), func(t *testing.T) {
					t.Parallel()

					cached, err := compile()
					require.NoError(t, err)
					require.Same(t, code, cached)

					for iteration := range 32 {
						seed := worker*1000 + iteration*10
						input := []any{seed, seed + 1, seed + 2}
						offset := 1
						var values []any
						if len(tt.varNames) > 0 {
							offset = seed + 3
							values = []any{offset}
						}

						iter := cached.RunWithContext(context.Background(), input, values...)
						for item := range input {
							value, ok := iter.Next()
							require.True(t, ok, "iterator should yield every input item")
							assert.Equal(t, map[string]any{"value": seed + item + offset}, value)
						}
						_, ok := iter.Next()
						assert.False(t, ok, "iterator should be exhausted")
					}
				})
			}
		})
	}
}

// TestCompileToolResponseFilter_CacheHit verifies that calling
// CompileToolResponseFilter twice with the same expression returns
// the same *gojq.Code pointer (cache hit).
func TestCompileToolResponseFilter_CacheHit(t *testing.T) {
	// Use a unique filter to avoid interference with other tests.
	filter := ". | {cached: true}"
	code1, err := CompileToolResponseFilter(filter)
	require.NoError(t, err)
	require.NotNil(t, code1)

	code2, err := CompileToolResponseFilter(filter)
	require.NoError(t, err)
	require.NotNil(t, code2)

	// Pointer equality proves the same compiled object was returned from the cache.
	assert.Same(t, code1, code2, "CompileToolResponseFilter should return cached code for identical filters")
}

// TestCompileToolResponseFilterWithVars_CacheHit verifies that calling
// CompileToolResponseFilterWithVars twice with the same (filter, varNames) pair
// returns the same *gojq.Code pointer (cache hit).
func TestCompileToolResponseFilterWithVars_CacheHit(t *testing.T) {
	// Use a unique filter to avoid interference with other tests.
	filter := ". | {cached: true, id: $toolID}"
	varNames := []string{"$toolID"}

	code1, err := CompileToolResponseFilterWithVars(filter, varNames)
	require.NoError(t, err)
	require.NotNil(t, code1)

	code2, err := CompileToolResponseFilterWithVars(filter, varNames)
	require.NoError(t, err)
	require.NotNil(t, code2)

	// Pointer equality proves the same compiled object was returned from the cache.
	assert.Same(t, code1, code2, "CompileToolResponseFilterWithVars should return cached code for identical (filter, varNames) pairs")
}

// TestCompileToolResponseFilterWithVars_DifferentVarsCacheMiss verifies that
// the same filter string compiled with different variable name lists produces
// distinct cache entries (different *gojq.Code pointers).
func TestCompileToolResponseFilterWithVars_DifferentVarsCacheMiss(t *testing.T) {
	// Use a filter that doesn't reference any specific variable so it compiles
	// successfully with any varNames list.
	filter := ". | {ok: true}"
	code1, err := CompileToolResponseFilterWithVars(filter, []string{"$a"})
	require.NoError(t, err)
	require.NotNil(t, code1)

	// Same filter string but a different variable name list → distinct cache key.
	code2, err := CompileToolResponseFilterWithVars(filter, []string{"$a", "$b"})
	require.NoError(t, err)
	require.NotNil(t, code2)

	assert.NotSame(t, code1, code2, "CompileToolResponseFilterWithVars should use distinct cache entries for different varNames")
}

func TestCompileToolResponseFilterWithVars_DoesNotReusePlainFilterCache(t *testing.T) {
	// Same filter text compiled through different APIs must not share cache entries,
	// because plain and variable-enabled paths have different cache key types/options.
	filter := ". | {ok: true}"

	plainCode, err := CompileToolResponseFilter(filter)
	require.NoError(t, err)
	require.NotNil(t, plainCode)

	varCode, err := CompileToolResponseFilterWithVars(filter, nil)
	require.NoError(t, err)
	require.NotNil(t, varCode)

	assert.NotSame(t, plainCode, varCode, "plain and var-enabled compilation should use distinct cache entries")
}

func TestToolResponseFilterVarsCacheKey_NoSeparatorCollision(t *testing.T) {
	t.Parallel()

	key1 := toolResponseFilterVarsCacheKey{
		filter:      "a\x00b",
		varNamesKey: buildVarNamesCacheKey([]string{"$c"}),
	}
	key2 := toolResponseFilterVarsCacheKey{
		filter:      "a",
		varNamesKey: buildVarNamesCacheKey([]string{"$b", "$c"}),
	}

	assert.NotEqual(t, key1, key2)
}

func TestCompileOptsWithVariables_DoesNotMutateSharedSecureOpts(t *testing.T) {
	t.Parallel()

	initialLen := len(secureCompileOpts)
	opts := jqutil.CompileOptsWithVariables([]string{"$toolID"})

	assert.Len(t, secureCompileOpts, initialLen)
	assert.Len(t, opts, initialLen+1)
}

// ---------------------------------------------------------------------------
// auditObservedURLDomains
// ---------------------------------------------------------------------------

// TestAuditObservedURLDomains exercises all early-exit and domain-logging
// branches of the unexported auditObservedURLDomains helper.
//
// These sub-tests are intentionally NOT run in parallel because they mutate
// the global URL-domain audit flag.
func TestAuditObservedURLDomains(t *testing.T) {
	// Safety net: restore the global flag after all sub-tests finish.
	t.Cleanup(func() { logger.SetURLDomainAuditEnabled(false) })

	t.Run("no-op when audit is disabled", func(t *testing.T) {
		logger.SetURLDomainAuditEnabled(false)
		// Must not panic; returns without extracting domains or logging.
		auditObservedURLDomains("github___list_repos", map[string]any{"url": "https://example.com"})
	})

	t.Run("no-op when data is nil", func(t *testing.T) {
		logger.SetURLDomainAuditEnabled(true)
		t.Cleanup(func() { logger.SetURLDomainAuditEnabled(false) })
		// Nil data triggers the `data == nil` short-circuit.
		auditObservedURLDomains("github___list_repos", nil)
	})

	t.Run("returns without logging when no URLs found in data", func(t *testing.T) {
		logger.SetURLDomainAuditEnabled(true)
		t.Cleanup(func() { logger.SetURLDomainAuditEnabled(false) })
		// Data contains no URLs so ExtractURLDomainsFromValue returns nil;
		// len(domains)==0 triggers the early return before LogObservedURLDomains.
		auditObservedURLDomains("github___list_repos", map[string]any{"key": "no url here"})
	})

	t.Run("calls LogObservedURLDomains when URLs are present in data", func(t *testing.T) {
		logger.SetURLDomainAuditEnabled(true)
		t.Cleanup(func() { logger.SetURLDomainAuditEnabled(false) })
		// Data contains a URL; LogObservedURLDomains is invoked (it is a no-op
		// when the global ObservedURLDomainsLogger is uninitialized, but the
		// call site is exercised for coverage).
		auditObservedURLDomains("github___list_repos", map[string]any{"url": "https://example.com/api"})
	})
}
