package middleware

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testGetSessionID is a helper function for tests that returns a session ID from context
func testGetSessionID(ctx context.Context) string {
	if sessionID, ok := ctx.Value("test-session-id").(string); ok {
		return sessionID
	}
	return "test-session"
}

func TestGenerateRandomID(t *testing.T) {
	// Generate multiple IDs and ensure they're unique
	ids := make(map[string]bool)
	for i := 0; i < 100; i++ {
		id := generateRandomID()
		assert.NotEmpty(t, id, "ID should not be empty")
		assert.False(t, ids[id], "ID should be unique")
		ids[id] = true
	}
}

// payloadMetadataToMap converts PayloadMetadata to map[string]interface{} for test assertions
// This allows tests to remain unchanged while working with the new struct type
func payloadMetadataToMap(t *testing.T, data interface{}) map[string]interface{} {
	t.Helper()
	pm, ok := data.(PayloadMetadata)
	if !ok {
		t.Fatalf("expected PayloadMetadata, got %T", data)
	}
	jsonBytes, err := json.Marshal(pm)
	require.NoError(t, err)
	var result map[string]interface{}
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)
	return result
}

func TestApplyJqSchema(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected string
	}{
		{
			name:     "simple object",
			input:    map[string]interface{}{"name": "test", "count": 42},
			expected: `{"count":"number","name":"string"}`,
		},
		{
			name:     "nested object",
			input:    map[string]interface{}{"user": map[string]interface{}{"id": 123, "active": true}},
			expected: `{"user":{"active":"boolean","id":"number"}}`,
		},
		{
			name:     "array with objects",
			input:    map[string]interface{}{"items": []interface{}{map[string]interface{}{"id": 1, "name": "test"}}},
			expected: `{"items":[{"id":"number","name":"string"}]}`,
		},
		{
			name:     "empty array",
			input:    map[string]interface{}{"items": []interface{}{}},
			expected: `{"items":[]}`,
		},
		{
			name: "complex nested structure",
			input: map[string]interface{}{
				"total_count": 1000,
				"items": []interface{}{
					map[string]interface{}{
						"login":    "user1",
						"id":       123,
						"verified": true,
					},
				},
			},
			expected: `{"items":[{"id":"number","login":"string","verified":"boolean"}],"total_count":"number"}`,
		},
		{
			name:     "null value",
			input:    map[string]interface{}{"value": nil},
			expected: `{"value":"null"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := applyJqSchema(context.Background(), tt.input)
			require.NoError(t, err, "applyJqSchema should not return error")
			// Convert result to JSON string for comparison
			resultJSON, err := json.Marshal(result)
			require.NoError(t, err, "Should marshal result to JSON")
			assert.JSONEq(t, tt.expected, string(resultJSON), "Schema should match expected")
		})
	}
}

// TestApplyJqSchema_SingleOutputContract verifies that the walk_schema filter produces
// exactly one output value. This documents the invariant that the iterator yields a single
// result, catching any future filter changes that accidentally produce multiple outputs.
func TestApplyJqSchema_SingleOutputContract(t *testing.T) {
	require := require.New(t)

	require.Nil(jqSchemaCompileErr, "jq schema filter must compile without error")
	require.NotNil(jqSchemaCode, "jq schema compiled code must not be nil")

	inputs := []interface{}{
		map[string]interface{}{"name": "test", "count": 42},
		[]interface{}{map[string]interface{}{"id": 1}},
		map[string]interface{}{"nested": map[string]interface{}{"a": []interface{}{1, 2, 3}}},
	}

	for _, input := range inputs {
		iter := jqSchemaCode.RunWithContext(context.Background(), input)

		// First call must return a value
		v, ok := iter.Next()
		require.True(ok, "walk_schema should produce at least one output")
		_, isErr := v.(error)
		require.False(isErr, "walk_schema should not produce an error: %v", v)

		// Second call must signal exhaustion (no more values)
		v2, ok2 := iter.Next()
		require.False(ok2, "walk_schema should produce exactly one output, got second value: %v", v2)
	}
}

func TestSavePayload(t *testing.T) {
	// Create temporary directory for test
	baseDir := filepath.Join(os.TempDir(), "test-jq-payloads")
	defer os.RemoveAll(baseDir)

	sessionID := "test-session-123"
	queryID := "test-query-456"
	payload := []byte(`{"test": "data"}`)

	filePath, err := savePayload(baseDir, "", sessionID, queryID, payload)
	require.NoError(t, err, "savePayload should not return error")

	// Verify file exists
	assert.FileExists(t, filePath, "Payload file should exist")

	// Verify file content
	content, err := os.ReadFile(filePath)
	require.NoError(t, err, "Should be able to read payload file")
	assert.Equal(t, payload, content, "File content should match payload")

	// Verify directory structure
	expectedDir := filepath.Join(baseDir, sessionID, queryID)
	assert.DirExists(t, expectedDir, "Directory should exist")
}

func TestSavePayloadWithPathPrefix(t *testing.T) {
	// Create temporary directory for test
	baseDir := filepath.Join(os.TempDir(), "test-jq-payloads-prefix")
	defer os.RemoveAll(baseDir)

	sessionID := "test-session-789"
	queryID := "test-query-abc"
	payload := []byte(`{"test": "data with prefix"}`)
	pathPrefix := "/workspace/payloads"

	returnedPath, err := savePayload(baseDir, pathPrefix, sessionID, queryID, payload)
	require.NoError(t, err, "savePayload should not return error")

	// Verify the actual file was written to the baseDir (filesystem path)
	actualFilePath := filepath.Join(baseDir, sessionID, queryID, "payload.json")
	assert.FileExists(t, actualFilePath, "Payload file should exist at actual filesystem path")

	// Verify file content
	content, err := os.ReadFile(actualFilePath)
	require.NoError(t, err, "Should be able to read payload file")
	assert.Equal(t, payload, content, "File content should match payload")

	// Verify the returned path uses the pathPrefix (remapped for client)
	expectedReturnPath := filepath.Join(pathPrefix, sessionID, queryID, "payload.json")
	assert.Equal(t, expectedReturnPath, returnedPath, "Returned path should use pathPrefix")
	assert.NotEqual(t, actualFilePath, returnedPath, "Returned path should differ from actual filesystem path")
}

func TestWrapToolHandler(t *testing.T) {
	// Create temporary directory for test
	baseDir := filepath.Join(os.TempDir(), "test-jq-payloads")
	defer os.RemoveAll(baseDir)

	// Create a mock handler
	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, map[string]interface{}{
			"message": "success",
			"data": map[string]interface{}{
				"id":    123,
				"items": []interface{}{map[string]interface{}{"name": "test"}},
			},
		}, nil
	}

	// Wrap the handler with size threshold of 10 bytes (payload will exceed this)
	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 10, testGetSessionID)

	// Call the wrapped handler
	result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

	// Assertions
	require.NoError(t, err, "Wrapped handler should not return error")
	require.NotNil(t, result, "Result should not be nil")
	assert.False(t, result.IsError, "Result should not be an error")

	// Verify the result Content field contains the transformed response
	require.NotEmpty(t, result.Content, "Result should have Content")
	textContent, ok := result.Content[0].(*sdk.TextContent)
	require.True(t, ok, "Content should be TextContent")
	require.NotEmpty(t, textContent.Text, "TextContent should have text")

	// Parse the JSON from Content
	var contentMap map[string]interface{}
	err = json.Unmarshal([]byte(textContent.Text), &contentMap)
	require.NoError(t, err, "Content should be valid JSON")

	// Verify transformed response in Content field
	assert.Contains(t, contentMap, "payloadPath", "Content should contain payloadPath")
	assert.Contains(t, contentMap, "payloadPreview", "Content should contain payloadPreview")
	assert.Contains(t, contentMap, "payloadSchema", "Content should contain payloadSchema")
	assert.Contains(t, contentMap, "originalSize", "Content should contain originalSize")

	// Verify schema is present
	schema := contentMap["payloadSchema"]
	assert.NotNil(t, schema, "Schema should not be nil")

	// Also verify rewritten response in data return value (for internal use)
	dataMap := payloadMetadataToMap(t, data)
	assert.Contains(t, dataMap, "payloadPath", "Data should contain payloadPath")

	// Clean up test directory
	defer os.RemoveAll(filepath.Join("/tmp", "gh-awmg"))
}

func TestWrapToolHandler_ErrorHandling(t *testing.T) {
	// Create temporary directory for test
	baseDir := filepath.Join(os.TempDir(), "test-jq-payloads")
	defer os.RemoveAll(baseDir)

	t.Run("handler returns error", func(t *testing.T) {
		mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
			return &sdk.CallToolResult{IsError: true}, nil, assert.AnError
		}

		wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 1024, testGetSessionID)
		result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

		assert.Error(t, err, "Should return error from handler")
		assert.Nil(t, data, "Data should be nil on error")
		assert.True(t, result.IsError, "Result should indicate error")
	})

	t.Run("handler returns nil data", func(t *testing.T) {
		mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
			return &sdk.CallToolResult{IsError: false}, nil, nil
		}

		wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 1024, testGetSessionID)
		result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

		assert.NoError(t, err, "Should not return error")
		assert.Nil(t, data, "Data should remain nil")
		assert.False(t, result.IsError, "Result should not indicate error")
	})
}

func TestWrapToolHandler_LongPayload(t *testing.T) {
	// Create temporary directory for test
	baseDir := filepath.Join(os.TempDir(), "test-jq-payloads")
	defer os.RemoveAll(baseDir)

	// Create a handler that returns a large payload
	largeData := map[string]interface{}{
		"message": strings.Repeat("x", 1000),
	}

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, largeData, nil
	}

	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 100, testGetSessionID)
	result, _, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

	require.NoError(t, err, "Should not return error")
	require.NotNil(t, result, "Result should not be nil")

	// Verify Content field contains the transformed response
	require.NotEmpty(t, result.Content, "Result should have Content")
	textContent, ok := result.Content[0].(*sdk.TextContent)
	require.True(t, ok, "Content should be TextContent")

	// Parse the JSON from Content
	var contentMap map[string]interface{}
	err = json.Unmarshal([]byte(textContent.Text), &contentMap)
	require.NoError(t, err, "Content should be valid JSON")

	// Verify preview truncation in Content field
	preview := contentMap["payloadPreview"].(string)
	assert.LessOrEqual(t, len(preview), PayloadPreviewSize+3, "Preview should be truncated to PayloadPreviewSize chars + '...'")
	assert.True(t, strings.HasSuffix(preview, "..."), "Preview should end with '...'")
}

// TestPayloadStorage_SessionIsolation verifies that payloads are stored in session-specific directories
func TestPayloadStorage_SessionIsolation(t *testing.T) {
	// Create temporary directory for test
	baseDir := t.TempDir()

	// Define two different session IDs
	session1 := "session-alpha-123"
	session2 := "session-beta-456"

	// Create a mock handler
	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, map[string]interface{}{
			"data": "test-payload",
		}, nil
	}

	// Create session ID getters for each session
	getSession1 := func(ctx context.Context) string { return session1 }
	getSession2 := func(ctx context.Context) string { return session2 }

	// Call handler for session 1
	wrapped1 := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 5, getSession1)
	_, data1, err := wrapped1(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})
	require.NoError(t, err)

	// Call handler for session 2
	wrapped2 := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 5, getSession2)
	_, data2, err := wrapped2(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})
	require.NoError(t, err)

	// Extract payload paths
	dataMap1 := payloadMetadataToMap(t, data1)
	dataMap2 := payloadMetadataToMap(t, data2)

	payloadPath1 := dataMap1["payloadPath"].(string)
	payloadPath2 := dataMap2["payloadPath"].(string)

	// Verify paths are different
	assert.NotEqual(t, payloadPath1, payloadPath2, "Different sessions should have different payload paths")

	// Verify session directories exist and are isolated
	session1Dir := filepath.Join(baseDir, session1)
	session2Dir := filepath.Join(baseDir, session2)

	assert.DirExists(t, session1Dir, "Session 1 directory should exist")
	assert.DirExists(t, session2Dir, "Session 2 directory should exist")

	// Verify payload paths contain respective session IDs
	assert.Contains(t, payloadPath1, session1, "Payload path 1 should contain session 1 ID")
	assert.Contains(t, payloadPath2, session2, "Payload path 2 should contain session 2 ID")

	// Verify payloads are not in each other's directories
	assert.NotContains(t, payloadPath1, session2, "Payload 1 should not be in session 2 directory")
	assert.NotContains(t, payloadPath2, session1, "Payload 2 should not be in session 1 directory")

	// Verify files exist at the correct paths
	assert.FileExists(t, payloadPath1, "Payload file 1 should exist")
	assert.FileExists(t, payloadPath2, "Payload file 2 should exist")
}

// TestPayloadStorage_LargePayloadPreserved verifies that the complete large payload is stored to disk
func TestPayloadStorage_LargePayloadPreserved(t *testing.T) {
	baseDir := t.TempDir()

	// Create a large payload (> PayloadPreviewSize chars to trigger truncation)
	largeContent := strings.Repeat("This is a large payload content. ", 100) // ~3400 chars
	largePayload := map[string]interface{}{
		"total_count": 1000,
		"items": []interface{}{
			map[string]interface{}{
				"id":          12345,
				"name":        "test-item",
				"description": largeContent,
				"metadata": map[string]interface{}{
					"author": "test-author",
					"tags":   []interface{}{"tag1", "tag2", "tag3"},
				},
			},
		},
	}

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, largePayload, nil
	}

	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 1024, testGetSessionID)
	_, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})
	require.NoError(t, err)

	dataMap := payloadMetadataToMap(t, data)

	// Get the payload path
	payloadPath := dataMap["payloadPath"].(string)
	assert.FileExists(t, payloadPath, "Payload file should exist")

	// Read the stored payload
	storedContent, err := os.ReadFile(payloadPath)
	require.NoError(t, err, "Should be able to read stored payload")

	// Verify stored payload is valid JSON
	var storedPayload map[string]interface{}
	err = json.Unmarshal(storedContent, &storedPayload)
	require.NoError(t, err, "Stored payload should be valid JSON")

	// Verify complete payload structure is preserved
	assert.Equal(t, float64(1000), storedPayload["total_count"], "total_count should be preserved")

	items := storedPayload["items"].([]interface{})
	require.Len(t, items, 1, "Should have 1 item")

	item := items[0].(map[string]interface{})
	assert.Equal(t, float64(12345), item["id"], "Item ID should be preserved")
	assert.Equal(t, "test-item", item["name"], "Item name should be preserved")
	assert.Equal(t, largeContent, item["description"], "Complete large description should be preserved")

	metadata := item["metadata"].(map[string]interface{})
	assert.Equal(t, "test-author", metadata["author"], "Metadata author should be preserved")

	// Verify originalSize matches stored content size
	originalSize := int(dataMap["originalSize"].(float64))
	assert.Equal(t, len(storedContent), originalSize, "originalSize should match stored content size")
}

// TestPayloadStorage_DirectoryStructure verifies the directory structure {baseDir}/{sessionID}/{queryID}/payload.json
func TestPayloadStorage_DirectoryStructure(t *testing.T) {
	baseDir := t.TempDir()
	sessionID := "test-session-dir-check"

	getSessionID := func(ctx context.Context) string { return sessionID }

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, map[string]interface{}{"test": "data"}, nil
	}

	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 5, getSessionID)
	_, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})
	require.NoError(t, err)

	// Access QueryID directly from the struct (it's excluded from JSON via json:"-")
	pm, ok := data.(PayloadMetadata)
	require.True(t, ok, "data should be PayloadMetadata")
	queryID := pm.QueryID
	payloadPath := pm.PayloadPath

	// Verify the expected directory structure
	expectedDir := filepath.Join(baseDir, sessionID, queryID)
	expectedPath := filepath.Join(expectedDir, "payload.json")

	assert.Equal(t, expectedPath, payloadPath, "Payload path should match expected structure")
	assert.DirExists(t, expectedDir, "Query directory should exist")

	// Verify the file is named payload.json
	assert.True(t, strings.HasSuffix(payloadPath, "payload.json"), "File should be named payload.json")

	// Verify queryID is a valid hex string (32 chars = 16 bytes in hex)
	assert.Len(t, queryID, 32, "Query ID should be 32 hex characters")
}

// TestPayloadStorage_MultipleRequestsSameSession verifies that multiple requests from the same session
// create separate query directories
func TestPayloadStorage_MultipleRequestsSameSession(t *testing.T) {
	baseDir := t.TempDir()
	sessionID := "same-session"

	getSessionID := func(ctx context.Context) string { return sessionID }

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, map[string]interface{}{"request": "data"}, nil
	}

	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 5, getSessionID)

	// Make multiple requests
	var payloadPaths []string
	var queryIDs []string

	for i := 0; i < 5; i++ {
		_, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})
		require.NoError(t, err)

		// Access QueryID directly from the struct (it's excluded from JSON via json:"-")
		pm, ok := data.(PayloadMetadata)
		require.True(t, ok, "data should be PayloadMetadata")
		payloadPaths = append(payloadPaths, pm.PayloadPath)
		queryIDs = append(queryIDs, pm.QueryID)
	}

	// Verify all payload paths are unique
	pathSet := make(map[string]bool)
	for _, path := range payloadPaths {
		assert.False(t, pathSet[path], "Payload paths should be unique")
		pathSet[path] = true
	}

	// Verify all query IDs are unique
	idSet := make(map[string]bool)
	for _, id := range queryIDs {
		assert.False(t, idSet[id], "Query IDs should be unique")
		idSet[id] = true
	}

	// Verify all files exist
	for _, path := range payloadPaths {
		assert.FileExists(t, path, "Each payload file should exist")
	}

	// Verify all are in the same session directory but different query directories
	sessionDir := filepath.Join(baseDir, sessionID)
	for _, path := range payloadPaths {
		assert.True(t, strings.HasPrefix(path, sessionDir), "All payloads should be in session directory")
	}
}

// TestPayloadStorage_FilePermissions verifies that payload directories and files have secure permissions
func TestPayloadStorage_FilePermissions(t *testing.T) {
	baseDir := t.TempDir()
	sessionID := "test-permissions"
	queryID := "test-query-perms"
	payload := []byte(`{"secure": "data"}`)

	filePath, err := savePayload(baseDir, "", sessionID, queryID, payload)
	require.NoError(t, err)

	// Check directory permissions
	dirPath := filepath.Dir(filePath)
	dirInfo, err := os.Stat(dirPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), dirInfo.Mode().Perm(), "Directory should have 0755 permissions")

	// Check file permissions
	fileInfo, err := os.Stat(filePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), fileInfo.Mode().Perm(), "File should have 0600 permissions")
}

// TestPayloadStorage_DefaultSessionID verifies behavior when session ID is empty
func TestPayloadStorage_DefaultSessionID(t *testing.T) {
	baseDir := t.TempDir()

	// Return empty string to trigger default behavior
	getEmptySessionID := func(ctx context.Context) string { return "" }

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, map[string]interface{}{"test": "data"}, nil
	}

	// Use 5-byte threshold to ensure storage happens for this 15-byte payload
	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 5, getEmptySessionID)
	_, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})
	require.NoError(t, err)

	dataMap := payloadMetadataToMap(t, data)
	payloadPath := dataMap["payloadPath"].(string)

	// Verify the payload is stored in "default" session directory
	assert.Contains(t, payloadPath, "default", "Empty session should use 'default' directory")
	assert.FileExists(t, payloadPath, "Payload file should exist")
}

func TestShouldApplyMiddleware(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		expected bool
	}{
		{
			name:     "regular tool",
			toolName: "github___search_code",
			expected: true,
		},
		{
			name:     "sys tool",
			toolName: "sys___init",
			expected: false,
		},
		{
			name:     "another sys tool",
			toolName: "sys___list_servers",
			expected: false,
		},
		{
			name:     "tool with sys in name but not prefix",
			toolName: "mysys___tool",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldApplyMiddleware(tt.toolName)
			assert.Equal(t, tt.expected, result, "ShouldApplyMiddleware result should match expected")
		})
	}
}

func TestApplyJqSchema_ErrorCases(t *testing.T) {
	t.Run("handles complex recursive structures", func(t *testing.T) {
		// Create a deeply nested structure
		input := map[string]interface{}{
			"level1": map[string]interface{}{
				"level2": map[string]interface{}{
					"level3": map[string]interface{}{
						"value": "deep",
					},
				},
			},
		}

		result, err := applyJqSchema(context.Background(), input)
		require.NoError(t, err, "Should handle deeply nested structures")
		assert.NotNil(t, result, "Result should not be nil")

		// Verify the schema is correctly nested
		schema, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be a map")
		assert.Contains(t, schema, "level1", "Should contain level1")
	})

	t.Run("handles context cancellation", func(t *testing.T) {
		// Create a cancelled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		input := map[string]interface{}{"test": "data"}

		// The query should complete quickly, but context cancellation should be handled gracefully
		// Note: For this simple query, it may complete before cancellation is processed
		_, err := applyJqSchema(ctx, input)

		// Either succeeds (query completed before cancellation) or fails with context error
		if err != nil {
			assert.Contains(t, err.Error(), "context", "Error should mention context if cancelled")
		}
	})
}

// TestApplyJqSchema_TimeoutBehavior tests the timeout enforcement functionality
func TestApplyJqSchema_TimeoutBehavior(t *testing.T) {
	t.Run("applies default timeout when context has no deadline", func(t *testing.T) {
		// Use a context without a deadline
		ctx := context.Background()

		// Use simple input that completes quickly
		input := map[string]interface{}{"name": "test", "count": 42}

		result, err := applyJqSchema(ctx, input)
		require.NoError(t, err, "Should complete successfully with default timeout")
		assert.NotNil(t, result, "Result should not be nil")

		// Verify the result is correct
		schema, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be a map")
		assert.Equal(t, "string", schema["name"], "Name field should have string type")
		assert.Equal(t, "number", schema["count"], "Count field should have number type")
	})

	t.Run("preserves existing context deadline", func(t *testing.T) {
		// Create a context with a generous deadline (10 seconds)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		input := map[string]interface{}{"test": "data"}

		result, err := applyJqSchema(ctx, input)
		require.NoError(t, err, "Should complete successfully with existing deadline")
		assert.NotNil(t, result, "Result should not be nil")

		// Verify the result is correct
		schema, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be a map")
		assert.Equal(t, "string", schema["test"], "Test field should have string type")
	})

	t.Run("respects short context timeout", func(t *testing.T) {
		// Create a context with a very short timeout (1 nanosecond)
		// This is likely to timeout before query completion
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
		defer cancel()

		// Use input that takes some time to process
		input := map[string]interface{}{
			"items": make([]interface{}, 1000),
		}

		_, err := applyJqSchema(ctx, input)

		// Either completes (if fast enough) or times out
		if err != nil {
			assert.Contains(t, err.Error(), "context", "Timeout error should mention context")
		}
	})

	t.Run("handles large arrays within timeout", func(t *testing.T) {
		// Create a large array (but within v0.12.18's 2^29 element limit)
		// Use 10,000 elements as a reasonable test size
		items := make([]interface{}, 10000)
		for i := 0; i < 10000; i++ {
			items[i] = map[string]interface{}{
				"id":   i,
				"name": "item" + string(rune(i)),
			}
		}

		input := map[string]interface{}{
			"total_count": 10000,
			"items":       items,
		}

		// Use background context (will get default 5s timeout)
		result, err := applyJqSchema(context.Background(), input)
		require.NoError(t, err, "Should handle large array within timeout")
		assert.NotNil(t, result, "Result should not be nil")

		// Verify schema structure
		schema, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be a map")
		assert.Equal(t, "number", schema["total_count"], "Should have number type for total_count")

		// Verify array schema (should have one element representing the schema)
		itemsSchema, ok := schema["items"].([]interface{})
		require.True(t, ok, "Items should be an array")
		require.Len(t, itemsSchema, 1, "Array schema should have one element")
	})

	t.Run("handles deeply nested structures within timeout", func(t *testing.T) {
		// Create a deeply nested structure (10 levels)
		var createNested func(depth int) map[string]interface{}
		createNested = func(depth int) map[string]interface{} {
			if depth == 0 {
				return map[string]interface{}{
					"value": "leaf",
					"id":    42,
				}
			}
			return map[string]interface{}{
				"level": depth,
				"child": createNested(depth - 1),
			}
		}

		input := createNested(10)

		result, err := applyJqSchema(context.Background(), input)
		require.NoError(t, err, "Should handle deeply nested structure within timeout")
		assert.NotNil(t, result, "Result should not be nil")

		// Verify top level schema
		schema, ok := result.(map[string]interface{})
		require.True(t, ok, "Result should be a map")
		assert.Equal(t, "number", schema["level"], "Level should have number type")
		assert.Contains(t, schema, "child", "Should contain child field")
	})

	t.Run("returns compilation error when init failed", func(t *testing.T) {
		// Save the current compiled code and error
		originalCode := jqSchemaCode
		originalErr := jqSchemaCompileErr

		// Simulate compilation failure
		jqSchemaCode = nil
		jqSchemaCompileErr = assert.AnError

		// Restore after test
		defer func() {
			jqSchemaCode = originalCode
			jqSchemaCompileErr = originalErr
		}()

		input := map[string]interface{}{"test": "data"}
		_, err := applyJqSchema(context.Background(), input)

		require.Error(t, err, "Should return error when compilation failed")
		assert.Contains(t, err.Error(), "not compiled", "Error should mention compilation failure")
	})
}

// TestApplyJqSchema_ContextTimeout tests timeout behavior with various context configurations
func TestApplyJqSchema_ContextTimeout(t *testing.T) {
	t.Run("context without deadline gets default timeout", func(t *testing.T) {
		// This test verifies that DefaultJqTimeout is applied
		ctx := context.Background()
		input := map[string]interface{}{"key": "value"}

		start := time.Now()
		result, err := applyJqSchema(ctx, input)
		elapsed := time.Since(start)

		require.NoError(t, err, "Should complete successfully")
		assert.NotNil(t, result, "Result should not be nil")
		// Should complete much faster than the default timeout
		assert.Less(t, elapsed, DefaultJqTimeout, "Should complete before default timeout")
	})

	t.Run("context with deadline is preserved", func(t *testing.T) {
		// Create a context with a custom deadline
		customTimeout := 500 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), customTimeout)
		defer cancel()

		input := map[string]interface{}{"test": "data"}

		start := time.Now()
		result, err := applyJqSchema(ctx, input)
		elapsed := time.Since(start)

		require.NoError(t, err, "Should complete successfully")
		assert.NotNil(t, result, "Result should not be nil")
		// Should complete much faster than the custom timeout
		assert.Less(t, elapsed, customTimeout, "Should complete before custom timeout")
	})

	t.Run("canceled context returns error", func(t *testing.T) {
		// Create a canceled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		input := map[string]interface{}{"test": "data"}

		_, err := applyJqSchema(ctx, input)

		// For simple queries, it might complete before cancellation is detected
		// If error occurs, it should be context-related
		if err != nil {
			assert.Contains(t, err.Error(), "context", "Error should mention context cancellation")
		}
	})
}

// TestPayloadSizeThreshold_SmallPayload verifies that payloads smaller than or equal to the threshold
// are returned inline without file storage
func TestPayloadSizeThreshold_SmallPayload(t *testing.T) {
	baseDir := t.TempDir()

	// Create a small payload (well under 1KB)
	smallPayload := map[string]interface{}{
		"message": "success",
		"count":   42,
	}

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, smallPayload, nil
	}

	// Set threshold to 1024 bytes - payload should be ~40 bytes
	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 1024, testGetSessionID)
	result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

	require.NoError(t, err, "Should not return error")
	require.NotNil(t, result, "Result should not be nil")
	assert.False(t, result.IsError, "Result should not indicate error")

	// Verify the data returned is the original payload, not metadata
	dataMap, ok := data.(map[string]interface{})
	require.True(t, ok, "Data should be original payload map")
	assert.Equal(t, "success", dataMap["message"], "Original message should be preserved")
	assert.Equal(t, 42, dataMap["count"], "Original count should be preserved")

	// Verify no PayloadMetadata fields are present
	assert.NotContains(t, dataMap, "queryID", "Should not have queryID field")
	assert.NotContains(t, dataMap, "payloadPath", "Should not have payloadPath field")
	assert.NotContains(t, dataMap, "payloadPreview", "Should not have payloadPreview field")

	// Verify no files were created in the payload directory
	entries, err := os.ReadDir(baseDir)
	require.NoError(t, err, "Should be able to read baseDir")
	assert.Empty(t, entries, "No files should be created for small payloads")
}

// TestPayloadSizeThreshold_LargePayload verifies that payloads larger than the threshold
// are stored to disk and return metadata
func TestPayloadSizeThreshold_LargePayload(t *testing.T) {
	baseDir := t.TempDir()

	// Create a large payload (> 1KB)
	largeContent := strings.Repeat("x", 1500)
	largePayload := map[string]interface{}{
		"message": largeContent,
	}

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, largePayload, nil
	}

	// Set threshold to 1024 bytes - payload should be ~1520 bytes
	wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 1024, testGetSessionID)
	result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

	require.NoError(t, err, "Should not return error")
	require.NotNil(t, result, "Result should not be nil")

	// Verify the data returned is PayloadMetadata, not the original payload
	pm, ok := data.(PayloadMetadata)
	require.True(t, ok, "Data should be PayloadMetadata")
	assert.NotEmpty(t, pm.QueryID, "Should have queryID")
	assert.NotEmpty(t, pm.PayloadPath, "Should have payloadPath")
	assert.True(t, pm.OriginalSize > 1024, "Original size should exceed threshold")

	// Verify file was created
	assert.FileExists(t, pm.PayloadPath, "Payload file should exist")

	// Verify file content matches original payload
	fileContent, err := os.ReadFile(pm.PayloadPath)
	require.NoError(t, err, "Should be able to read payload file")

	var storedPayload map[string]interface{}
	err = json.Unmarshal(fileContent, &storedPayload)
	require.NoError(t, err, "Stored payload should be valid JSON")
	assert.Equal(t, largeContent, storedPayload["message"], "Stored content should match original")
}

// TestPayloadSizeThreshold_ExactBoundary verifies behavior at the exact threshold boundary
func TestPayloadSizeThreshold_ExactBoundary(t *testing.T) {
	baseDir := t.TempDir()

	// Create a payload that will be exactly at or very close to the threshold
	// JSON encoding adds quotes, braces, etc., so we need to account for that
	threshold := 100

	t.Run("exactly at threshold", func(t *testing.T) {
		// Create data that marshals to exactly 100 bytes
		// {"message":"xxxx..."} where total is 100 bytes
		// We need 100 - len(`{"message":""}`) = 100 - 13 = 87 characters
		content := strings.Repeat("x", 86)
		payload := map[string]interface{}{
			"message": content,
		}

		mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
			return &sdk.CallToolResult{IsError: false}, payload, nil
		}

		wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", threshold, testGetSessionID)
		result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

		require.NoError(t, err, "Should not return error")
		require.NotNil(t, result, "Result should not be nil")

		// Verify payload is returned inline (size <= threshold)
		dataMap, ok := data.(map[string]interface{})
		require.True(t, ok, "Data should be original payload at exact threshold")
		assert.Equal(t, content, dataMap["message"], "Original message should be preserved")
	})

	t.Run("one byte over threshold", func(t *testing.T) {
		// Create data that marshals to 101 bytes (one over threshold)
		content := strings.Repeat("x", 87)
		payload := map[string]interface{}{
			"message": content,
		}

		mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
			return &sdk.CallToolResult{IsError: false}, payload, nil
		}

		wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", threshold, testGetSessionID)
		result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

		require.NoError(t, err, "Should not return error")
		require.NotNil(t, result, "Result should not be nil")

		// Verify payload is stored to disk (size > threshold)
		pm, ok := data.(PayloadMetadata)
		require.True(t, ok, "Data should be PayloadMetadata when over threshold")
		assert.NotEmpty(t, pm.PayloadPath, "Should have payloadPath")
		assert.FileExists(t, pm.PayloadPath, "Payload file should exist")
	})
}

// TestPayloadSizeThreshold_CustomThreshold verifies that custom thresholds work correctly
func TestPayloadSizeThreshold_CustomThreshold(t *testing.T) {
	baseDir := t.TempDir()

	payload := map[string]interface{}{
		"data": strings.Repeat("x", 200),
	}

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, payload, nil
	}

	t.Run("low threshold triggers storage", func(t *testing.T) {
		// Use very low threshold (50 bytes) - should trigger storage
		wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 50, testGetSessionID)
		_, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

		require.NoError(t, err)
		pm, ok := data.(PayloadMetadata)
		require.True(t, ok, "Should store with low threshold")
		assert.FileExists(t, pm.PayloadPath, "File should be created")
	})

	t.Run("high threshold returns inline", func(t *testing.T) {
		// Use very high threshold (10000 bytes) - should return inline
		wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", 10000, testGetSessionID)
		_, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

		require.NoError(t, err)
		dataMap, ok := data.(map[string]interface{})
		require.True(t, ok, "Should return inline with high threshold")
		assert.NotContains(t, dataMap, "payloadPath", "Should not have payloadPath")
	})
}

// TestThresholdBehavior_SmallPayloadsAsIs verifies that payloads smaller than threshold
// are delivered as-is without any file storage or transformation
func TestThresholdBehavior_SmallPayloadsAsIs(t *testing.T) {
	baseDir := t.TempDir()

	tests := []struct {
		name      string
		payload   map[string]interface{}
		threshold int
		comment   string
	}{
		{
			name:      "tiny payload with 1KB threshold",
			payload:   map[string]interface{}{"status": "ok"},
			threshold: 1024,
			comment:   "14-byte payload should be returned inline",
		},
		{
			name:      "small JSON with 500 byte threshold",
			payload:   map[string]interface{}{"message": "success", "count": 5, "active": true},
			threshold: 500,
			comment:   "~42-byte payload should be returned inline",
		},
		{
			name:      "medium payload with 1KB threshold",
			payload:   map[string]interface{}{"data": strings.Repeat("x", 200)},
			threshold: 1024,
			comment:   "~214-byte payload should be returned inline",
		},
		{
			name: "structured data with default threshold",
			payload: map[string]interface{}{
				"user":      "alice",
				"action":    "login",
				"timestamp": 1234567890,
				"metadata": map[string]interface{}{
					"ip":    "192.168.1.1",
					"agent": "Mozilla/5.0",
				},
			},
			threshold: 1024,
			comment:   "~120-byte structured payload should be returned inline",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
				return &sdk.CallToolResult{IsError: false}, tt.payload, nil
			}

			wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", tt.threshold, testGetSessionID)
			result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

			require.NoError(t, err, "Should not return error")
			require.NotNil(t, result, "Result should not be nil")
			assert.False(t, result.IsError, "Result should not indicate error")

			// Verify data is returned as-is (not PayloadMetadata)
			dataMap, ok := data.(map[string]interface{})
			require.True(t, ok, "Data should be original payload map, not PayloadMetadata: %s", tt.comment)

			// Verify no PayloadMetadata fields
			assert.NotContains(t, dataMap, "queryID", "Should not have queryID field")
			assert.NotContains(t, dataMap, "payloadPath", "Should not have payloadPath field")
			assert.NotContains(t, dataMap, "payloadPreview", "Should not have payloadPreview field")
			assert.NotContains(t, dataMap, "payloadSchema", "Should not have payloadSchema field")

			// Verify original data is preserved
			payloadJSON, _ := json.Marshal(tt.payload)
			dataJSON, _ := json.Marshal(dataMap)
			assert.JSONEq(t, string(payloadJSON), string(dataJSON), "Original data should be preserved")

			// Verify no files were created
			entries, err := os.ReadDir(baseDir)
			require.NoError(t, err)
			assert.Empty(t, entries, "No files should be created for small payloads: %s", tt.comment)
		})
	}
}

// TestThresholdBehavior_LargePayloadsUsePayloadDir verifies that payloads larger than threshold
// use the payloadDir for file storage and return metadata
func TestThresholdBehavior_LargePayloadsUsePayloadDir(t *testing.T) {
	baseDir := t.TempDir()

	tests := []struct {
		name      string
		payload   map[string]interface{}
		threshold int
		comment   string
	}{
		{
			name:      "payload exceeds 100 byte threshold",
			payload:   map[string]interface{}{"data": strings.Repeat("x", 200)},
			threshold: 100,
			comment:   "~214-byte payload should use file storage",
		},
		{
			name:      "large text exceeds 1KB threshold",
			payload:   map[string]interface{}{"content": strings.Repeat("Lorem ipsum ", 100)},
			threshold: 1024,
			comment:   "~1200-byte payload should use file storage",
		},
		{
			name: "complex nested structure exceeds 500 byte threshold",
			payload: map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{"id": 1, "name": strings.Repeat("a", 100)},
					map[string]interface{}{"id": 2, "name": strings.Repeat("b", 100)},
					map[string]interface{}{"id": 3, "name": strings.Repeat("c", 100)},
					map[string]interface{}{"id": 4, "name": strings.Repeat("d", 100)},
				},
			},
			threshold: 400,
			comment:   "~460-byte nested structure should use file storage",
		},
		{
			name:      "moderate payload with very low threshold",
			payload:   map[string]interface{}{"message": "hello world", "count": 42},
			threshold: 10,
			comment:   "~35-byte payload exceeds 10-byte threshold, should use file storage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
				return &sdk.CallToolResult{IsError: false}, tt.payload, nil
			}

			wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", tt.threshold, testGetSessionID)
			result, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

			require.NoError(t, err, "Should not return error")
			require.NotNil(t, result, "Result should not be nil")

			// Verify data is PayloadMetadata, not original payload
			pm, ok := data.(PayloadMetadata)
			require.True(t, ok, "Data should be PayloadMetadata for large payload: %s", tt.comment)

			// Verify PayloadMetadata fields are present
			assert.NotEmpty(t, pm.QueryID, "Should have queryID")
			assert.NotEmpty(t, pm.PayloadPath, "Should have payloadPath")
			assert.NotEmpty(t, pm.PayloadPreview, "Should have payloadPreview")
			assert.NotNil(t, pm.PayloadSchema, "Should have payloadSchema")
			assert.True(t, pm.OriginalSize > tt.threshold, "Original size should exceed threshold: %s", tt.comment)

			// Verify file was created at the specified path
			assert.FileExists(t, pm.PayloadPath, "Payload file should exist at path: %s", tt.comment)

			// Verify payloadPath contains baseDir
			assert.Contains(t, pm.PayloadPath, baseDir, "Payload path should be under baseDir")

			// Verify file content matches original payload
			fileContent, err := os.ReadFile(pm.PayloadPath)
			require.NoError(t, err, "Should be able to read payload file")

			var storedPayload map[string]interface{}
			err = json.Unmarshal(fileContent, &storedPayload)
			require.NoError(t, err, "Stored payload should be valid JSON")

			originalJSON, _ := json.Marshal(tt.payload)
			storedJSON, _ := json.Marshal(storedPayload)
			assert.JSONEq(t, string(originalJSON), string(storedJSON), "Stored content should match original: %s", tt.comment)

			// Verify originalSize matches file size
			assert.Equal(t, len(fileContent), pm.OriginalSize, "OriginalSize should match file size")
		})
	}
}

// TestThresholdBehavior_MixedPayloads verifies that the same handler with the same threshold
// correctly handles both small (inline) and large (file storage) payloads
func TestThresholdBehavior_MixedPayloads(t *testing.T) {
	baseDir := t.TempDir()
	threshold := 100 // 100 bytes

	// Create a handler factory that returns different payload sizes
	createHandler := func(payload map[string]interface{}) func(context.Context, *sdk.CallToolRequest, interface{}) (*sdk.CallToolResult, interface{}, error) {
		return func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
			return &sdk.CallToolResult{IsError: false}, payload, nil
		}
	}

	// Small payload (under threshold)
	smallPayload := map[string]interface{}{"status": "ok", "code": 200}
	smallHandler := createHandler(smallPayload)
	wrappedSmall := WrapToolHandler(smallHandler, "test_tool", baseDir, "", threshold, testGetSessionID)

	_, smallData, err := wrappedSmall(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})
	require.NoError(t, err)

	// Verify small payload is returned inline
	smallMap, ok := smallData.(map[string]interface{})
	require.True(t, ok, "Small payload should be returned as-is")
	assert.Equal(t, "ok", smallMap["status"], "Small payload data should be preserved")
	assert.NotContains(t, smallMap, "payloadPath", "Small payload should not have payloadPath")

	// Large payload (over threshold)
	largePayload := map[string]interface{}{"data": strings.Repeat("x", 200)}
	largeHandler := createHandler(largePayload)
	wrappedLarge := WrapToolHandler(largeHandler, "test_tool", baseDir, "", threshold, testGetSessionID)

	_, largeData, err := wrappedLarge(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})
	require.NoError(t, err)

	// Verify large payload uses file storage
	pm, ok := largeData.(PayloadMetadata)
	require.True(t, ok, "Large payload should return PayloadMetadata")
	assert.NotEmpty(t, pm.PayloadPath, "Large payload should have payloadPath")
	assert.FileExists(t, pm.PayloadPath, "Large payload file should exist")

	// Verify files created - should only have large payload
	entries, err := os.ReadDir(baseDir)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "Should have created files for large payload only")
}

// TestThresholdBehavior_ConfigurableThresholds verifies that different threshold values
// correctly determine whether payloads are stored or returned inline
func TestThresholdBehavior_ConfigurableThresholds(t *testing.T) {
	baseDir := t.TempDir()

	// Create a payload that's ~50 bytes
	payload := map[string]interface{}{"data": strings.Repeat("x", 30)}
	payloadJSON, _ := json.Marshal(payload)
	payloadSize := len(payloadJSON)

	t.Logf("Test payload size: %d bytes", payloadSize)

	mockHandler := func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
		return &sdk.CallToolResult{IsError: false}, payload, nil
	}

	testCases := []struct {
		name              string
		threshold         int
		expectFileStorage bool
	}{
		{"threshold 10 bytes - expect storage", 10, true},
		{"threshold 20 bytes - expect storage", 20, true},
		{"threshold 40 bytes - expect storage", 40, true},
		{"threshold 50 bytes - expect inline", 50, false},
		{"threshold 100 bytes - expect inline", 100, false},
		{"threshold 1KB - expect inline", 1024, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := WrapToolHandler(mockHandler, "test_tool", baseDir, "", tc.threshold, testGetSessionID)
			_, data, err := wrapped(context.Background(), &sdk.CallToolRequest{}, map[string]interface{}{})

			require.NoError(t, err)

			if tc.expectFileStorage {
				pm, ok := data.(PayloadMetadata)
				require.True(t, ok, "Should return PayloadMetadata when size (%d) > threshold (%d)", payloadSize, tc.threshold)
				assert.NotEmpty(t, pm.PayloadPath, "Should have payloadPath")
				assert.FileExists(t, pm.PayloadPath, "File should exist")
			} else {
				dataMap, ok := data.(map[string]interface{})
				require.True(t, ok, "Should return original data when size (%d) <= threshold (%d)", payloadSize, tc.threshold)
				assert.NotContains(t, dataMap, "payloadPath", "Should not have payloadPath")
			}
		})
	}
}
