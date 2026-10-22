package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/config"
)

// serverCreator defines a function type for creating HTTP servers
type serverCreator func(addr string, us *UnifiedServer, apiKeys []string, hmacSecret string) *http.Server

// TestHealthEndpoint tests the health endpoint across different server modes and scenarios
func TestHealthEndpoint(t *testing.T) {
	tests := []struct {
		name          string
		createServer  serverCreator
		apiKey        string
		expectServers int
		serverConfigs map[string]*config.ServerConfig
	}{
		{
			name:          "RoutedMode/EmptyConfig",
			createServer:  CreateHTTPServerForRoutedMode,
			apiKey:        "",
			expectServers: 0,
			serverConfigs: map[string]*config.ServerConfig{},
		},
		{
			name:          "UnifiedMode/EmptyConfig",
			createServer:  CreateHTTPServerForMCP,
			apiKey:        "",
			expectServers: 0,
			serverConfigs: map[string]*config.ServerConfig{},
		},
		{
			name:          "RoutedMode/WithApiKey",
			createServer:  CreateHTTPServerForRoutedMode,
			apiKey:        "test-api-key",
			expectServers: 0,
			serverConfigs: map[string]*config.ServerConfig{},
		},
		{
			name:          "UnifiedMode/WithApiKey",
			createServer:  CreateHTTPServerForMCP,
			apiKey:        "test-api-key",
			expectServers: 0,
			serverConfigs: map[string]*config.ServerConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create config
			cfg := &config.Config{
				Servers: tt.serverConfigs,
			}

			// Create unified server
			ctx := context.Background()
			us, err := NewUnified(ctx, cfg)
			require.NoError(t, err, "Failed to create unified server")
			t.Cleanup(func() { us.Close() })

			// Create HTTP server using the provided creator function
			httpServer := tt.createServer(":0", us, keysOrNil(tt.apiKey), "")

			// Create test request
			req := httptest.NewRequest("GET", "/health", nil)
			w := httptest.NewRecorder()

			// Execute request
			httpServer.Handler.ServeHTTP(w, req)

			// Verify response
			verifyHealthResponse(t, w, tt.expectServers)
		})
	}
}

// TestHealthEndpoint_NoAuthRequired tests that health endpoint works without authentication
func TestHealthEndpoint_NoAuthRequired(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	// Create config
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	// Create unified server
	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(err, "Failed to create unified server")
	t.Cleanup(func() { us.Close() })

	// Create HTTP server WITH API key (health should still work without auth)
	httpServer := CreateHTTPServerForRoutedMode(":0", us, []string{"test-api-key"}, "")

	// Create test request WITHOUT Authorization header
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()

	// Execute request
	httpServer.Handler.ServeHTTP(w, req)

	// Health endpoint should work without auth
	assert.Equal(http.StatusOK, w.Code, "Health endpoint should not require authentication")

	// Verify basic response structure
	var response map[string]interface{}
	err = json.NewDecoder(w.Body).Decode(&response)
	require.NoError(err, "Failed to decode JSON response")

	assert.Contains(response, "status", "Response should contain 'status' field")
	assert.Contains(response, "specVersion", "Response should contain 'specVersion' field")
	assert.Contains(response, "gatewayVersion", "Response should contain 'gatewayVersion' field")
}

// TestHealthEndpoint_ResponseFields tests specific response field validation
func TestHealthEndpoint_ResponseFields(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		validate  func(t *testing.T, value interface{})
	}{
		{
			name:      "StatusField/ValidValues",
			fieldName: "status",
			validate: func(t *testing.T, value interface{}) {
				t.Helper()
				require := require.New(t)
				assert := assert.New(t)

				status, ok := value.(string)
				require.True(ok, "Expected 'status' to be string, got %T", value)
				assert.Contains([]string{"healthy", "unhealthy"}, status, "Status must be 'healthy' or 'unhealthy'")
			},
		},
		{
			name:      "SpecVersionField/MustMatch",
			fieldName: "specVersion",
			validate: func(t *testing.T, value interface{}) {
				t.Helper()
				require := require.New(t)
				assert := assert.New(t)

				specVersion, ok := value.(string)
				require.True(ok, "Expected 'specVersion' to be string, got %T", value)
				assert.Equal(MCPGatewaySpecVersion, specVersion, "specVersion must match gateway spec")
				assert.Equal("1.18.0", specVersion, "specVersion must advertise spec 1.18.0 compliance")
			},
		},
		{
			name:      "GatewayVersionField/NotEmpty",
			fieldName: "gatewayVersion",
			validate: func(t *testing.T, value interface{}) {
				t.Helper()
				require := require.New(t)
				assert := assert.New(t)

				gatewayVersion, ok := value.(string)
				require.True(ok, "Expected 'gatewayVersion' to be string, got %T", value)
				assert.NotEmpty(gatewayVersion, "gatewayVersion must not be empty")
			},
		},
		{
			// T-HLT-009: gatewayVersion uses semantic versioning format (spec §8.1.1)
			name:      "GatewayVersionField/SemverFormat",
			fieldName: "gatewayVersion",
			validate: func(t *testing.T, value interface{}) {
				t.Helper()
				require := require.New(t)
				assert := assert.New(t)

				gatewayVersion, ok := value.(string)
				require.True(ok, "Expected 'gatewayVersion' to be string, got %T", value)
				semverPattern := regexp.MustCompile(`^\d+\.\d+\.\d+`)
				assert.True(semverPattern.MatchString(gatewayVersion),
					"gatewayVersion must use semantic versioning format (MAJOR.MINOR.PATCH), got: %s", gatewayVersion)
			},
		},
		{
			name:      "ServersField/IsMap",
			fieldName: "servers",
			validate: func(t *testing.T, value interface{}) {
				t.Helper()
				require := require.New(t)
				assert := assert.New(t)

				servers, ok := value.(map[string]interface{})
				require.True(ok, "Expected 'servers' to be map[string]interface{}, got %T", value)
				// With empty config, servers map should be empty
				assert.Empty(servers, "Expected empty servers map with no configured servers")
			},
		},
	}

	// Create test server once for all field validation tests
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}
	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(t, err, "Failed to create unified server")
	defer us.Close()

	httpServer := CreateHTTPServerForRoutedMode(":0", us, nil, "")

	// Get response once
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(w, req)

	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response), "Failed to decode JSON response")

	// Run field validation tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			value, exists := response[tt.fieldName]
			require.True(t, exists, "Expected field '%s' to exist in response", tt.fieldName)
			tt.validate(t, value)
		})
	}
}

// verifyHealthResponse is a helper that validates the health endpoint response
func verifyHealthResponse(t *testing.T, w *httptest.ResponseRecorder, expectServers int) {
	t.Helper()
	assert := assert.New(t)
	require := require.New(t)

	// Check status code
	assert.Equal(http.StatusOK, w.Code, "Expected HTTP 200 OK")

	// Check content type
	contentType := w.Header().Get("Content-Type")
	assert.Equal("application/json", contentType, "Expected JSON content type")

	// Check response body
	var response map[string]interface{}
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(err, "Failed to decode JSON response")

	// Check required fields exist
	requiredFields := []string{"status", "specVersion", "gatewayVersion", "servers"}
	for _, field := range requiredFields {
		assert.Contains(response, field, "Response should contain '%s' field", field)
	}

	// Check status field
	status, ok := response["status"].(string)
	require.True(ok, "Expected 'status' field to be a string")
	assert.Contains([]string{"healthy", "unhealthy"}, status, "Status must be 'healthy' or 'unhealthy'")

	// Check specVersion field
	specVersion, ok := response["specVersion"].(string)
	require.True(ok, "Expected 'specVersion' field to be a string")
	assert.Equal(MCPGatewaySpecVersion, specVersion, "specVersion must match gateway spec")

	// Check gatewayVersion field
	gatewayVersion, ok := response["gatewayVersion"].(string)
	require.True(ok, "Expected 'gatewayVersion' field to be a string")
	assert.NotEmpty(gatewayVersion, "gatewayVersion must not be empty")

	// Check servers field
	servers, ok := response["servers"].(map[string]interface{})
	require.True(ok, "Expected 'servers' field to be a map")
	assert.Len(servers, expectServers, "Expected %d servers in response", expectServers)
}

// TestHealthEndpoint_ContentType tests that health endpoint returns correct content type
func TestHealthEndpoint_ContentType(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(err)
	t.Cleanup(func() { us.Close() })

	httpServer := CreateHTTPServerForRoutedMode(":0", us, nil, "")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(w, req)

	contentType := w.Header().Get("Content-Type")
	assert.Equal("application/json", contentType, "Content-Type must be application/json")
}

// TestHealthEndpoint_HTTPMethods tests health endpoint with different HTTP methods
func TestHealthEndpoint_HTTPMethods(t *testing.T) {
	require := require.New(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(err)
	t.Cleanup(func() { us.Close() })

	httpServer := CreateHTTPServerForRoutedMode(":0", us, nil, "")

	tests := []struct {
		name   string
		method string
		expect int
	}{
		{
			name:   "GET/Succeeds",
			method: "GET",
			expect: http.StatusOK,
		},
		{
			name:   "POST/Allowed",
			method: "POST",
			expect: http.StatusOK,
		},
		{
			name:   "HEAD/Allowed",
			method: "HEAD",
			expect: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)

			req := httptest.NewRequest(tt.method, "/health", nil)
			w := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(w, req)

			assert.Equal(tt.expect, w.Code, "Expected status %d for method %s", tt.expect, tt.method)
		})
	}
}

// TestHealthEndpoint_MultipleServers tests health response with multiple configured servers
func TestHealthEndpoint_MultipleServers(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	// Create config with multiple servers
	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"server1": {
				Command: "echo",
				Args:    []string{"server1"},
			},
			"server2": {
				Command: "echo",
				Args:    []string{"server2"},
			},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(err)
	t.Cleanup(func() { us.Close() })

	httpServer := CreateHTTPServerForRoutedMode(":0", us, nil, "")

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	httpServer.Handler.ServeHTTP(w, req)

	var response map[string]interface{}
	err = json.NewDecoder(w.Body).Decode(&response)
	require.NoError(err)

	servers, ok := response["servers"].(map[string]interface{})
	require.True(ok, "Expected 'servers' field to be a map")
	assert.Len(servers, 2, "Expected 2 servers in response")
}

// TestHealthEndpoint_BothModes tests that both routed and unified modes produce valid health responses
func TestHealthEndpoint_BothModes(t *testing.T) {
	tests := []struct {
		name         string
		createServer serverCreator
	}{
		{
			name:         "RoutedMode",
			createServer: CreateHTTPServerForRoutedMode,
		},
		{
			name:         "UnifiedMode",
			createServer: CreateHTTPServerForMCP,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			cfg := &config.Config{
				Servers: map[string]*config.ServerConfig{},
			}

			ctx := context.Background()
			us, err := NewUnified(ctx, cfg)
			require.NoError(err)
			t.Cleanup(func() { us.Close() })

			httpServer := tt.createServer(":0", us, nil, "")

			req := httptest.NewRequest("GET", "/health", nil)
			w := httptest.NewRecorder()
			httpServer.Handler.ServeHTTP(w, req)

			assert.Equal(http.StatusOK, w.Code, "%s should return 200 OK", tt.name)

			var response map[string]interface{}
			err = json.NewDecoder(w.Body).Decode(&response)
			require.NoError(err, "%s response should be valid JSON", tt.name)

			// Both modes should have the same required fields
			requiredFields := []string{"status", "specVersion", "gatewayVersion", "servers"}
			for _, field := range requiredFields {
				assert.Contains(response, field, "%s response should contain '%s' field", tt.name, field)
			}
		})
	}
}

// TestBuildHealthResponse_HealthyStatus tests BuildHealthResponse with all healthy servers
func TestBuildHealthResponse_HealthyStatus(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(err)
	t.Cleanup(func() { us.Close() })

	response := BuildHealthResponse(us)

	assert.Equal("healthy", response.Status, "Status should be 'healthy' when no servers configured")
	assert.Equal(MCPGatewaySpecVersion, response.SpecVersion, "specVersion should match gateway spec")
	assert.NotEmpty(response.GatewayVersion, "gatewayVersion should not be empty")
	assert.NotNil(response.Servers, "Servers map should not be nil")
}

// TestGetServerStatus_ErrorOnFailedLaunch verifies servers report "error" when launch fails
func TestGetServerStatus_ErrorOnFailedLaunch(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"server-a": {Type: "stdio", Command: "docker", Args: []string{"run", "test"}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(err)
	t.Cleanup(func() { us.Close() })

	status := us.GetServerStatus()
	require.Contains(status, "server-a")
	assert.Equal("error", status["server-a"].Status, "Server should report 'error' after failed launch")
	assert.Equal(0, status["server-a"].Uptime, "Uptime should be 0 for errored server")
}

// TestBuildHealthResponse_UnhealthyOnError verifies that failed servers make the gateway unhealthy
func TestBuildHealthResponse_UnhealthyOnError(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	cfg := &config.Config{
		Servers: map[string]*config.ServerConfig{
			"server-a": {Type: "stdio", Command: "docker", Args: []string{"run", "test"}},
		},
	}

	ctx := context.Background()
	us, err := NewUnified(ctx, cfg)
	require.NoError(err)
	t.Cleanup(func() { us.Close() })

	response := BuildHealthResponse(us)
	assert.Equal("unhealthy", response.Status, "Gateway should be unhealthy when a server has errors")
}
