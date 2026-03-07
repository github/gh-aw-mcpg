package server

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// makeCallToolRequest creates a CallToolRequest with the given raw arguments bytes.
// Pass nil to simulate a request with no Arguments field.
func makeCallToolRequest(arguments json.RawMessage) *sdk.CallToolRequest {
	return &sdk.CallToolRequest{
		Params: &sdk.CallToolParamsRaw{
			Arguments: arguments,
		},
	}
}

// TestParseToolArguments tests parseToolArguments with all code paths.
func TestParseToolArguments(t *testing.T) {
	tests := []struct {
		name       string
		arguments  json.RawMessage
		wantArgs   map[string]interface{}
		wantErrMsg string
	}{
		{
			name:      "nil arguments returns empty map",
			arguments: nil,
			wantArgs:  map[string]interface{}{},
		},
		{
			name:      "empty JSON object returns empty map",
			arguments: json.RawMessage(`{}`),
			wantArgs:  map[string]interface{}{},
		},
		{
			name:      "simple string argument",
			arguments: json.RawMessage(`{"query":"hello world"}`),
			wantArgs: map[string]interface{}{
				"query": "hello world",
			},
		},
		{
			name:      "multiple string arguments",
			arguments: json.RawMessage(`{"owner":"github","repo":"gh-aw-mcpg","state":"open"}`),
			wantArgs: map[string]interface{}{
				"owner": "github",
				"repo":  "gh-aw-mcpg",
				"state": "open",
			},
		},
		{
			name:      "numeric argument",
			arguments: json.RawMessage(`{"limit":100}`),
			wantArgs: map[string]interface{}{
				"limit": float64(100), // JSON numbers unmarshal to float64
			},
		},
		{
			name:      "boolean argument",
			arguments: json.RawMessage(`{"include_closed":true,"verbose":false}`),
			wantArgs: map[string]interface{}{
				"include_closed": true,
				"verbose":        false,
			},
		},
		{
			name:      "nested object argument",
			arguments: json.RawMessage(`{"config":{"timeout":30,"retry":true}}`),
			wantArgs: map[string]interface{}{
				"config": map[string]interface{}{
					"timeout": float64(30),
					"retry":   true,
				},
			},
		},
		{
			name:      "array argument",
			arguments: json.RawMessage(`{"labels":["bug","enhancement"]}`),
			wantArgs: map[string]interface{}{
				"labels": []interface{}{"bug", "enhancement"},
			},
		},
		{
			name:      "null string value in argument",
			arguments: json.RawMessage(`{"optional_field":null}`),
			wantArgs: map[string]interface{}{
				"optional_field": nil,
			},
		},
		{
			name:      "mixed argument types",
			arguments: json.RawMessage(`{"name":"test","count":5,"active":true,"tags":["a","b"]}`),
			wantArgs: map[string]interface{}{
				"name":   "test",
				"count":  float64(5),
				"active": true,
				"tags":   []interface{}{"a", "b"},
			},
		},
		{
			name:       "invalid JSON returns error",
			arguments:  json.RawMessage(`{invalid json`),
			wantErrMsg: "failed to parse arguments",
		},
		{
			name:       "JSON array at top level returns error",
			arguments:  json.RawMessage(`["a","b","c"]`),
			wantErrMsg: "failed to parse arguments",
		},
		{
			name:       "JSON string at top level returns error",
			arguments:  json.RawMessage(`"just a string"`),
			wantErrMsg: "failed to parse arguments",
		},
		{
			name:       "JSON number at top level returns error",
			arguments:  json.RawMessage(`42`),
			wantErrMsg: "failed to parse arguments",
		},
		{
			name:      "empty string value argument",
			arguments: json.RawMessage(`{"description":""}`),
			wantArgs: map[string]interface{}{
				"description": "",
			},
		},
		{
			name:      "argument with unicode content",
			arguments: json.RawMessage(`{"message":"こんにちは世界"}`),
			wantArgs: map[string]interface{}{
				"message": "こんにちは世界",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := makeCallToolRequest(tt.arguments)

			got, err := parseToolArguments(req)

			if tt.wantErrMsg != "" {
				require.Error(t, err, "parseToolArguments() should return error for %s", tt.name)
				assert.Contains(t, err.Error(), tt.wantErrMsg,
					"parseToolArguments() error message should contain %q", tt.wantErrMsg)
				assert.Nil(t, got, "parseToolArguments() should return nil map on error")
			} else {
				require.NoError(t, err, "parseToolArguments() should not return error for %s", tt.name)
				require.NotNil(t, got, "parseToolArguments() should return non-nil map")
				assert.Equal(t, tt.wantArgs, got, "parseToolArguments() returned unexpected result")
			}
		})
	}
}

// TestParseToolArguments_NilArgumentsAlwaysReturnsNewMap verifies that calling
// parseToolArguments with nil arguments multiple times returns distinct maps.
func TestParseToolArguments_NilArgumentsAlwaysReturnsNewMap(t *testing.T) {
	req := makeCallToolRequest(nil)

	got1, err1 := parseToolArguments(req)
	require.NoError(t, err1)

	got2, err2 := parseToolArguments(req)
	require.NoError(t, err2)

	// Both should be empty but they should not be the same map pointer
	assert.Empty(t, got1, "First result should be empty map")
	assert.Empty(t, got2, "Second result should be empty map")
	// Mutating one should not affect the other
	got1["key"] = "value"
	assert.Empty(t, got2, "Mutating first map should not affect second map")
}

// TestParseToolArguments_ErrorWrapping verifies the error message wraps the
// underlying JSON parse error with the expected prefix.
func TestParseToolArguments_ErrorWrapping(t *testing.T) {
	req := makeCallToolRequest(json.RawMessage(`{bad json}`))

	_, err := parseToolArguments(req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse arguments:",
		"error should be wrapped with 'failed to parse arguments:'")
}

// TestGetPayloadSizeThreshold verifies the getter returns the configured threshold.
func TestGetPayloadSizeThreshold(t *testing.T) {
	t.Run("custom positive threshold is used directly", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{},
			Gateway: &config.GatewayConfig{
				PayloadSizeThreshold: 1024,
			},
		}
		us, err := NewUnified(context.Background(), cfg)
		require.NoError(t, err)
		defer us.Close()

		got := us.GetPayloadSizeThreshold()
		assert.Equal(t, 1024, got,
			"GetPayloadSizeThreshold() should return the custom configured threshold")
	})

	t.Run("zero threshold falls back to default", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{},
			Gateway: &config.GatewayConfig{
				PayloadSizeThreshold: 0,
			},
		}
		us, err := NewUnified(context.Background(), cfg)
		require.NoError(t, err)
		defer us.Close()

		got := us.GetPayloadSizeThreshold()
		assert.Equal(t, config.DefaultPayloadSizeThreshold, got,
			"GetPayloadSizeThreshold() should return the default threshold when zero is configured")
	})

	t.Run("nil gateway config falls back to default", func(t *testing.T) {
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{},
			// no Gateway field
		}
		us, err := NewUnified(context.Background(), cfg)
		require.NoError(t, err)
		defer us.Close()

		got := us.GetPayloadSizeThreshold()
		assert.Equal(t, config.DefaultPayloadSizeThreshold, got,
			"GetPayloadSizeThreshold() should return the default threshold when Gateway is nil")
	})

	t.Run("large custom threshold", func(t *testing.T) {
		const largeThreshold = 10 * 1024 * 1024 // 10 MB
		cfg := &config.Config{
			Servers: map[string]*config.ServerConfig{},
			Gateway: &config.GatewayConfig{
				PayloadSizeThreshold: largeThreshold,
			},
		}
		us, err := NewUnified(context.Background(), cfg)
		require.NoError(t, err)
		defer us.Close()

		got := us.GetPayloadSizeThreshold()
		assert.Equal(t, largeThreshold, got,
			"GetPayloadSizeThreshold() should return large custom thresholds correctly")
	})
}

// TestGetServerStatus verifies that GetServerStatus returns an entry for every
// configured server, each with status "running".
func TestGetServerStatus(t *testing.T) {
	tests := []struct {
		name    string
		servers map[string]*config.ServerConfig
	}{
		{
			name:    "no servers",
			servers: map[string]*config.ServerConfig{},
		},
		{
			name: "single server",
			servers: map[string]*config.ServerConfig{
				"github": {Command: "docker", Args: []string{}},
			},
		},
		{
			name: "multiple servers",
			servers: map[string]*config.ServerConfig{
				"github": {Command: "docker", Args: []string{}},
				"fetch":  {Command: "docker", Args: []string{}},
				"slack":  {Command: "docker", Args: []string{}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Servers: tt.servers}
			us, err := NewUnified(context.Background(), cfg)
			require.NoError(t, err)
			defer us.Close()

			status := us.GetServerStatus()

			assert.Len(t, status, len(tt.servers),
				"GetServerStatus() should return one entry per configured server")

			for serverID := range tt.servers {
				s, ok := status[serverID]
				require.True(t, ok, "GetServerStatus() should include entry for server %q", serverID)
				assert.Equal(t, "running", s.Status,
					"GetServerStatus() should report server %q as 'running'", serverID)
			}
		})
	}
}

// TestGetServerStatus_ReturnsCopy verifies that mutations to the returned map
// do not affect subsequent calls.
func TestGetServerStatus_ReturnsCopy(t *testing.T) {
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"github": {Command: "docker", Args: []string{}},
		},
	}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	defer us.Close()

	// Mutate the result of the first call
	status1 := us.GetServerStatus()
	status1["injected-server"] = ServerStatus{Status: "error"}

	// The second call should not see the injected server
	status2 := us.GetServerStatus()
	assert.NotContains(t, status2, "injected-server",
		"GetServerStatus() should not be affected by mutations to previous results")
}

// TestIsDIFCEnabled verifies the getter reflects the enableDIFC config field.
func TestIsDIFCEnabled(t *testing.T) {
	tests := []struct {
		name        string
		enableDIFC  bool
		wantEnabled bool
	}{
		{
			name:        "DIFC disabled by default",
			enableDIFC:  false,
			wantEnabled: false,
		},
		{
			name:        "DIFC enabled via config",
			enableDIFC:  true,
			wantEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Servers:    map[string]*config.ServerConfig{},
				EnableDIFC: tt.enableDIFC,
			}
			us, err := NewUnified(context.Background(), cfg)
			require.NoError(t, err)
			defer us.Close()

			got := us.IsDIFCEnabled()
			assert.Equal(t, tt.wantEnabled, got,
				"IsDIFCEnabled() should reflect the configured enableDIFC value")
		})
	}
}

// TestSetAndGetHTTPShutdown verifies that SetHTTPShutdown and GetHTTPShutdown
// work correctly as a getter/setter pair.
func TestSetAndGetHTTPShutdown(t *testing.T) {
	cfg := &config.Config{Servers: map[string]*config.ServerConfig{}}
	us, err := NewUnified(context.Background(), cfg)
	require.NoError(t, err)
	defer us.Close()

	t.Run("nil by default", func(t *testing.T) {
		got := us.GetHTTPShutdown()
		assert.Nil(t, got, "GetHTTPShutdown() should return nil before SetHTTPShutdown is called")
	})

	t.Run("returns set function", func(t *testing.T) {
		called := false
		shutdownFn := func(ctx context.Context) error {
			called = true
			return nil
		}
		us.SetHTTPShutdown(shutdownFn)

		got := us.GetHTTPShutdown()
		require.NotNil(t, got, "GetHTTPShutdown() should return the set function")

		// Invoke the returned function and verify it's the same one
		err := got(context.Background())
		require.NoError(t, err)
		assert.True(t, called, "Returned function should be the same one that was set")
	})

	t.Run("can override with a new function", func(t *testing.T) {
		callCount := 0
		newShutdownFn := func(ctx context.Context) error {
			callCount++
			return nil
		}
		us.SetHTTPShutdown(newShutdownFn)

		got := us.GetHTTPShutdown()
		require.NotNil(t, got)
		_ = got(context.Background())
		assert.Equal(t, 1, callCount, "Only the new shutdown function should be called")
	})

	t.Run("can be set to nil", func(t *testing.T) {
		us.SetHTTPShutdown(nil)
		got := us.GetHTTPShutdown()
		assert.Nil(t, got, "GetHTTPShutdown() should return nil after SetHTTPShutdown(nil)")
	})

	t.Run("returns error from shutdown function", func(t *testing.T) {
		expectedErr := errors.New("shutdown failed")
		us.SetHTTPShutdown(func(ctx context.Context) error {
			return expectedErr
		})

		got := us.GetHTTPShutdown()
		require.NotNil(t, got)
		err := got(context.Background())
		assert.ErrorIs(t, err, expectedErr,
			"GetHTTPShutdown() should return a function that propagates errors")
	})
}
