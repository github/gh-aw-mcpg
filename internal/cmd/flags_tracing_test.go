package cmd

import (
	"testing"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestGetDefaultOTLPEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		setEnv   bool
		expected string
	}{
		{
			name:     "no env var - returns empty string (tracing disabled)",
			setEnv:   false,
			expected: "",
		},
		{
			name:     "env var set - returns configured endpoint",
			envValue: "http://localhost:4318",
			setEnv:   true,
			expected: "http://localhost:4318",
		},
		{
			name:     "env var set to HTTPS endpoint",
			envValue: "https://otel.example.com/v1/traces",
			setEnv:   true,
			expected: "https://otel.example.com/v1/traces",
		},
		{
			name:     "env var set to empty - returns empty string",
			envValue: "",
			setEnv:   true,
			expected: "",
		},
		{
			name:     "env var set to GHES collector URL",
			envValue: "https://otel-collector.mycompany.ghe.com",
			setEnv:   true,
			expected: "https://otel-collector.mycompany.ghe.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", tt.envValue)
			} else {
				t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			}

			got := getDefaultOTLPEndpoint()
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestGetDefaultOTLPServiceName(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		setEnv   bool
		expected string
	}{
		{
			name:     "no env var - returns default service name",
			setEnv:   false,
			expected: config.DefaultTracingServiceName,
		},
		{
			name:     "env var set - returns configured service name",
			envValue: "my-gateway",
			setEnv:   true,
			expected: "my-gateway",
		},
		{
			name:     "env var set to empty - returns default",
			envValue: "",
			setEnv:   true,
			expected: config.DefaultTracingServiceName,
		},
		{
			name:     "env var set to custom name with spaces",
			envValue: "my custom gateway",
			setEnv:   true,
			expected: "my custom gateway",
		},
		{
			name:     "env var set to standard service name pattern",
			envValue: "github-mcp-gateway-prod",
			setEnv:   true,
			expected: "github-mcp-gateway-prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setEnv {
				t.Setenv("OTEL_SERVICE_NAME", tt.envValue)
			} else {
				t.Setenv("OTEL_SERVICE_NAME", "")
			}

			got := getDefaultOTLPServiceName()
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestDefaultOTLPValues verifies the default values used when tracing flags are not configured.
func TestDefaultOTLPValues(t *testing.T) {
	t.Run("default service name is mcp-gateway", func(t *testing.T) {
		assert.Equal(t, "mcp-gateway", config.DefaultTracingServiceName,
			"DefaultTracingServiceName constant should be 'mcp-gateway'")
	})

	t.Run("default sample rate is 1.0", func(t *testing.T) {
		assert.Equal(t, float64(1.0), config.DefaultTracingSampleRate,
			"DefaultTracingSampleRate should be 1.0 (100%% sampling)")
	})

	t.Run("endpoint default enables disabled tracing", func(t *testing.T) {
		t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
		endpoint := getDefaultOTLPEndpoint()
		assert.Empty(t, endpoint, "Empty endpoint disables tracing per design")
	})
}
