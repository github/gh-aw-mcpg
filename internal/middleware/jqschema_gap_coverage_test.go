package middleware

// Tests targeting previously uncovered branches in jqschema.go:
//   - compileToolResponseFilterInternal: cache poisoned with a non-*gojq.Code
//     value (defensive branch that should never happen in practice)
//   - tryApplyToolResponseFilter: json.Marshal(filteredPayload) failure for the
//     text-content path (filter produces a non-JSON-serializable value such as
//     +Inf)
//   - wrapToolHandler: successful schema generation contract surrounding the
//     json.Marshal(rewrittenResponse) fallback branch

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// compileToolResponseFilterInternal: poisoned cache entry (lines 292-295)
// ---------------------------------------------------------------------------

// TestCompileToolResponseFilter_PoisonedCacheEntry verifies the defensive
// "should never happen" branch that fires when filterCodeCache holds a value
// that is not a *gojq.Code for the given key. This can only occur if another
// part of the code (or a test) stores an unexpected type under the same key,
// but the function must still fail safely with a descriptive error rather
// than panicking on the type assertion.
func TestCompileToolResponseFilter_PoisonedCacheEntry(t *testing.T) {
	// Use a filter string that is exceedingly unlikely to collide with any
	// other test's cache key.
	const filter = "__poisoned_cache_key_test__"

	// Poison the shared cache with a value of the wrong type.
	filterCodeCache.Store(filter, "not-a-gojq-code")
	t.Cleanup(func() { filterCodeCache.Delete(filter) })

	code, err := CompileToolResponseFilter(filter)
	require.Error(t, err, "poisoned cache entry should produce an error rather than panicking")
	assert.Nil(t, code)
	assert.ErrorContains(t, err, "internal error: unexpected cached value type string for filter")
}

// ---------------------------------------------------------------------------
// tryApplyToolResponseFilter: filtered text payload marshal failure (lines 376-380)
// ---------------------------------------------------------------------------

// TestWrapToolHandlerWithFilter_TextPayloadMarshalFailure verifies that when
// the jq filter applied to a text-content JSON payload produces a value that
// cannot be marshaled back to JSON (such as +Inf, produced by the built-in
// "infinite" jq function), tryApplyToolResponseFilter logs the failure and
// falls back to the original response instead of propagating an error.
func TestWrapToolHandlerWithFilter_TextPayloadMarshalFailure(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()

	originalResult := &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: `{"value":1}`}},
	}
	originalData := map[string]any{"value": float64(1)}

	mockHandler := func(_ context.Context, _ *sdk.CallToolRequest, _ any) (*sdk.CallToolResult, any, error) {
		return originalResult, originalData, nil
	}

	// "infinite" always evaluates to +Inf regardless of input, which
	// json.Marshal cannot serialize ("json: unsupported value: +Inf").
	wrapped := WrapToolHandlerWithFilter(mockHandler, "test_tool", baseDir, "", 0, testGetSessionID, "infinite")
	result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, nil)

	require.NoError(t, err, "marshal failure inside the filter path should not propagate as a handler error")
	require.NotNil(t, result)

	// The filter fallback returns the original result/data pair to the
	// oversized-payload path. With sizeThreshold = 0 and a valid temp
	// directory, that original payload must be wrapped into PayloadMetadata;
	// if a regression leaves data as the filtered +Inf value, the later
	// json.Marshal(data) fails and this assertion catches the missing metadata.
	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(*sdk.TextContent)
	require.True(t, ok)
	meta, ok := data.(PayloadMetadata)
	require.True(t, ok, "expected PayloadMetadata for original payload after filter marshal fallback, got %T", data)
	assert.Equal(t, `{"value":1}`, meta.PayloadPreview, "preview should reflect the original unfiltered payload")
	assert.Contains(t, tc.Text, `"payloadPreview":"{\"value\":1}"`, "rewritten response should include original payload preview")

}

// ---------------------------------------------------------------------------
// wrapToolHandler: successful schema generation contract (regression guard for
// the code paths surrounding the json.Marshal(rewrittenResponse) branch at
// lines 680-685; the walk_schema jq filter used internally always produces
// JSON-serializable string schema values, so the marshal-failure branch
// itself cannot be triggered through the public API without an internal
// hook, but the surrounding fallback contract is still verified here).
// ---------------------------------------------------------------------------

// TestWrapToolHandler_SchemaGenerationSuccess_ReturnsPayloadMetadata verifies
// that a normal oversized payload with successful schema generation produces
// a well-formed PayloadMetadata response (the success path that immediately
// precedes the marshal-failure fallback branch).
func TestWrapToolHandler_SchemaGenerationSuccess_ReturnsPayloadMetadata(t *testing.T) {
	t.Parallel()
	baseDir := t.TempDir()

	originalResult := &sdk.CallToolResult{
		Content: []sdk.Content{&sdk.TextContent{Text: "result"}},
		Meta:    sdk.Meta{"requestId": "request-123"},
	}
	originalData := map[string]any{"key": "value"}

	mockHandler := func(_ context.Context, _ *sdk.CallToolRequest, _ any) (*sdk.CallToolResult, any, error) {
		return originalResult, originalData, nil
	}

	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 0, testGetSessionID)
	result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsError)
	assert.Equal(t, originalResult.Meta, result.Meta)
	assert.NotSame(t, originalResult, result)
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(*sdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, `"payloadPath"`)
	assert.Equal(t, &sdk.TextContent{Text: "result"}, originalResult.Content[0])
	meta, ok := data.(PayloadMetadata)
	require.True(t, ok, "expected PayloadMetadata for oversized payload with successful schema generation")
	assert.NotEmpty(t, meta.PayloadPath)
	assert.NotNil(t, meta.PayloadSchema)
}
