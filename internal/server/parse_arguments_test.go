package server

import (
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseToolArguments verifies all branches of the parseToolArguments helper.
func TestParseToolArguments(t *testing.T) {
	tests := []struct {
		name      string
		arguments json.RawMessage // passed via req.Params.Arguments
		wantArgs  map[string]interface{}
		wantErr   bool
		errSubstr string
	}{
		{
			name:      "nil arguments returns empty map",
			arguments: nil,
			wantArgs:  map[string]interface{}{},
			wantErr:   false,
		},
		{
			name:      "empty JSON object returns empty map",
			arguments: json.RawMessage(`{}`),
			wantArgs:  map[string]interface{}{},
			wantErr:   false,
		},
		{
			name:      "valid arguments with string value",
			arguments: json.RawMessage(`{"key": "value"}`),
			wantArgs:  map[string]interface{}{"key": "value"},
			wantErr:   false,
		},
		{
			name:      "valid arguments with multiple types",
			arguments: json.RawMessage(`{"query":"fmt.Println","limit":10,"enabled":true}`),
			wantArgs: map[string]interface{}{
				"query":   "fmt.Println",
				"limit":   float64(10),
				"enabled": true,
			},
			wantErr: false,
		},
		{
			name:      "valid arguments with nested object",
			arguments: json.RawMessage(`{"filter":{"repo":"github/test"}}`),
			wantArgs: map[string]interface{}{
				"filter": map[string]interface{}{"repo": "github/test"},
			},
			wantErr: false,
		},
		{
			name:      "invalid JSON returns error",
			arguments: json.RawMessage(`this is not json`),
			wantArgs:  nil,
			wantErr:   true,
			errSubstr: "failed to parse arguments",
		},
		{
			name:      "JSON array returns error (not an object)",
			arguments: json.RawMessage(`["a","b"]`),
			wantArgs:  nil,
			wantErr:   true,
			errSubstr: "failed to parse arguments",
		},
		{
			name:      "truncated JSON returns error",
			arguments: json.RawMessage(`{"key": "val`),
			wantArgs:  nil,
			wantErr:   true,
			errSubstr: "failed to parse arguments",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			req := &sdk.CallToolRequest{}
			req.Params.Arguments = tt.arguments

			got, err := parseToolArguments(req)

			if tt.wantErr {
				require.Error(err)
				if tt.errSubstr != "" {
					assert.Contains(err.Error(), tt.errSubstr)
				}
				assert.Nil(got)
				return
			}

			require.NoError(err)
			assert.Equal(tt.wantArgs, got)
		})
	}
}

// TestParseToolArguments_NilArgumentsNeverErrors verifies that a nil Arguments field
// always returns an empty map without error (the happy path for tools with no args).
func TestParseToolArguments_NilArgumentsNeverErrors(t *testing.T) {
	req := &sdk.CallToolRequest{}
	// Params.Arguments is zero-value (nil).

	got, err := parseToolArguments(req)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Empty(t, got)
}

// TestParseToolArguments_ValidArgumentsPreservedExactly verifies that the parsed map
// contains exactly the fields from the JSON input with correct Go types.
func TestParseToolArguments_ValidArgumentsPreservedExactly(t *testing.T) {
	req := &sdk.CallToolRequest{}
	req.Params.Arguments = json.RawMessage(`{
		"str_val":  "hello",
		"int_val":  42,
		"bool_val": false,
		"null_val": null
	}`)

	got, err := parseToolArguments(req)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, "hello", got["str_val"])
	assert.Equal(t, float64(42), got["int_val"]) // JSON numbers unmarshal as float64
	assert.Equal(t, false, got["bool_val"])
	assert.Nil(t, got["null_val"])
}
