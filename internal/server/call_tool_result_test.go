package server

import (
	"fmt"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/mcp"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvertToCallToolResult_VariousFormats tests the conversion helper
// This test verifies the fix for the bug where nil CallToolResult was returned
func TestConvertToCallToolResult_VariousFormats(t *testing.T) {
	tests := []struct {
		name           string
		input          interface{}
		expectError    bool
		expectIsError  bool
		expectContents int
		validateText   string // Optional: expected text in first content item
	}{
		{
			name: "simple text response - typical GitHub MCP response",
			input: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Hello, world!",
					},
				},
				"isError": false,
			},
			expectError:    false,
			expectIsError:  false,
			expectContents: 1,
			validateText:   "Hello, world!",
		},
		{
			name: "multiple content items",
			input: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "First item",
					},
					map[string]interface{}{
						"type": "text",
						"text": "Second item",
					},
				},
				"isError": false,
			},
			expectError:    false,
			expectIsError:  false,
			expectContents: 2,
		},
		{
			name: "error response",
			input: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": "Error occurred",
					},
				},
				"isError": true,
			},
			expectError:    false,
			expectIsError:  true,
			expectContents: 1,
		},
		{
			name: "empty content - should still return valid CallToolResult",
			input: map[string]interface{}{
				"content": []interface{}{},
				"isError": false,
			},
			expectError:    false,
			expectIsError:  false,
			expectContents: 0,
		},
		{
			name: "real GitHub search_pull_requests response structure",
			input: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type": "text",
						"text": `{"total_count":4530,"incomplete_results":false,"items":[{"id":123,"number":456}]}`,
					},
				},
			},
			expectError:    false,
			expectIsError:  false,
			expectContents: 1,
		},
		{
			name: "non-map content item is rejected",
			input: map[string]interface{}{
				"content": []interface{}{"not-a-map"},
			},
			expectError: true,
		},
		{
			name: "image content without data is rejected",
			input: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type":     "image",
						"mimeType": "image/png",
					},
				},
			},
			expectError: true,
		},
		{
			name: "image content with invalid base64 data is rejected",
			input: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type": "image",
						"data": "!!!not-base64",
					},
				},
			},
			expectError: true,
		},
		{
			name: "image content with unsupported data type is rejected",
			input: map[string]interface{}{
				"content": []interface{}{
					map[string]interface{}{
						"type": "image",
						"data": 42,
					},
				},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := mcp.ConvertToCallToolResult(tt.input)

			if tt.expectError {
				require.Error(t, err)
				assert.Nil(t, result, "Result should be nil on error")
				return
			}

			require.NoError(t, err, "Conversion should succeed")
			require.NotNil(t, result, "CRITICAL: CallToolResult must NOT be nil - this was the bug!")
			assert.Equal(t, tt.expectIsError, result.IsError, "IsError flag should match expected value")
			assert.Len(t, result.Content, tt.expectContents, "Content length should match expected")

			// Validate text content if specified
			if tt.validateText != "" && len(result.Content) > 0 {
				textContent, ok := result.Content[0].(*sdk.TextContent)
				require.True(t, ok, "First content item should be TextContent")
				assert.Equal(t, tt.validateText, textContent.Text, "Text content should match")
			}

			t.Logf("✓ Converted to CallToolResult with %d content items, IsError=%v",
				len(result.Content), result.IsError)
		})
	}
}

// TestConvertToCallToolResult_NilCheck explicitly tests the critical fix
// Before the fix, callBackendTool returned (nil, finalResult, nil)
// After the fix, it should return (&CallToolResult{...}, finalResult, nil)
func TestConvertToCallToolResult_NilCheck(t *testing.T) {
	// Simulate what a backend returns
	backendResponse := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": "Backend response",
			},
		},
	}

	result, err := mcp.ConvertToCallToolResult(backendResponse)

	require.NoError(t, err, "Conversion should not error")

	// THE CRITICAL ASSERTION - this is what was failing before the fix
	assert.NotNil(t, result, "CRITICAL BUG FIX: CallToolResult MUST NOT be nil!")

	// Additional validations
	assert.False(t, result.IsError, "Should not be an error")
	assert.NotNil(t, result.Content, "Content should not be nil")
	assert.NotEmpty(t, result.Content)

	t.Log("✓ CallToolResult is properly non-nil and structured")
}

// TestNewErrorCallToolResult tests the error CallToolResult helper
func TestNewErrorCallToolResult(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		expectedMsg string
	}{
		{
			name:        "simple error",
			err:         fmt.Errorf("test error"),
			expectedMsg: "test error",
		},
		{
			name:        "formatted error",
			err:         fmt.Errorf("formatted error: %s", "test"),
			expectedMsg: "formatted error: test",
		},
		{
			name:        "nil error fallback",
			err:         nil,
			expectedMsg: "unknown error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, data, err := mcp.NewErrorCallToolResult(tt.err)

			// The returned error is the input error as-is, or a fallback when the input is nil
			require.Error(t, err, "NewErrorCallToolResult should always return a non-nil error")
			require.EqualError(t, err, tt.expectedMsg, "Returned error message should match")
			if tt.err != nil {
				assert.Equal(t, tt.err, err, "Non-nil error should be returned as-is")
			}

			// Verify data is nil
			assert.Nil(t, data, "Data should be nil for error results")

			// Verify CallToolResult is properly structured
			require.NotNil(t, result, "CallToolResult should not be nil")
			assert.True(t, result.IsError, "IsError should be true")

			// Verify error message is included in content
			require.Len(t, result.Content, 1, "Should have one content item with error message")
			textContent, ok := result.Content[0].(*sdk.TextContent)
			require.True(t, ok, "Content should be TextContent")
			assert.Equal(t, tt.expectedMsg, textContent.Text, "Error message should be in content")

			t.Logf("✓ Error CallToolResult properly created with IsError=%v, message=%q", result.IsError, textContent.Text)
		})
	}
}
