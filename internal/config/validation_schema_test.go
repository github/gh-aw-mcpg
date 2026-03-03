package config

import (
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/version"
	"github.com/stretchr/testify/assert"
)

func TestValidateJSONSchema(t *testing.T) {
	tests := []struct {
		name      string
		config    string
		shouldErr bool
		errorMsg  string
	}{
		{
			name: "valid minimal config",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: false,
		},
		{
			name: "valid config with all fields",
			config: `{
				"mcpServers": {
					"github": {
						"type": "stdio",
						"container": "ghcr.io/github/github-mcp-server:latest",
						"entrypoint": "/bin/bash",
						"entrypointArgs": ["--verbose"],
						"mounts": ["/host:/container:ro"],
						"env": {"TOKEN": "value"},
						"args": ["--flag"]
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key",
					"startupTimeout": 30,
					"toolTimeout": 60
				}
			}`,
			shouldErr: false,
		},
		{
			name: "valid http server config",
			config: `{
				"mcpServers": {
					"remote": {
						"type": "http",
						"url": "https://api.example.com/mcp",
						"headers": {"Authorization": "Bearer token"}
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: false,
		},
		{
			name: "missing required field - mcpServers",
			config: `{
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "missing required field - gateway",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "missing required field - gateway.port",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "missing required field - gateway.domain",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "missing required field - gateway.apiKey",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "missing required field - stdio server container",
			config: `{
				"mcpServers": {
					"github": {
						"type": "stdio"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "missing required field - http server url",
			config: `{
				"mcpServers": {
					"remote": {
						"type": "http"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "invalid port - too high",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 99999,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "invalid port - zero",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 0,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "invalid timeout - zero",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key",
					"startupTimeout": 0
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "additional properties not allowed at root",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				},
				"unknownField": "value"
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "additional properties not allowed in stdio server",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest",
						"unknownField": "value"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "additional properties not allowed in http server",
			config: `{
				"mcpServers": {
					"remote": {
						"type": "http",
						"url": "https://api.example.com/mcp",
						"unknownField": "value"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "additional properties not allowed in gateway",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"apiKey": "test-key",
					"unknownField": "value"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJSONSchema([]byte(tt.config))

			if tt.shouldErr {
				assert.Error(t, err)
				if tt.errorMsg != "" && err != nil && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing %q, got: %v", tt.errorMsg, err)
				}
			} else {
				assert.NoError(t, err, "Unexpected error")
			}
		})
	}
}

func TestValidateStringPatterns(t *testing.T) {
	tests := []struct {
		name      string
		config    *StdinConfig
		shouldErr bool
		errorMsg  string
	}{
		{
			name: "valid container pattern",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "ghcr.io/owner/image:latest",
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "valid container pattern - no tag",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "ghcr.io/owner/image",
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "valid container pattern - version tag",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "ghcr.io/owner/image:v1.2.3",
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "invalid container pattern - starts with special char",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "/invalid/image:latest",
					},
				},
			},
			shouldErr: true,
			errorMsg:  "does not match required pattern",
		},
		{
			name: "valid mount pattern",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "test:latest",
						Mounts:    []string{"/host/path:/container/path:ro"},
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "invalid mount without mode",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "test:latest",
						Mounts:    []string{"/host/path:/container/path"},
					},
				},
			},
			shouldErr: true,
		},
		{
			name: "valid http url pattern",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type: "http",
						URL:  "https://api.example.com/mcp",
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "valid http url pattern - http scheme",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type: "http",
						URL:  "http://localhost:8080/mcp",
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "invalid url pattern - no scheme",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type: "http",
						URL:  "api.example.com/mcp",
					},
				},
			},
			shouldErr: true,
			errorMsg:  "does not match required pattern",
		},
		{
			name: "valid domain - localhost",
			config: &StdinConfig{
				Gateway: &StdinGatewayConfig{
					Port:   intPtr(8080),
					Domain: "localhost",
				},
			},
			shouldErr: false,
		},
		{
			name: "valid domain - host.docker.internal",
			config: &StdinConfig{
				Gateway: &StdinGatewayConfig{
					Port:   intPtr(8080),
					Domain: "host.docker.internal",
				},
			},
			shouldErr: false,
		},
		{
			name: "valid domain - variable expression",
			config: &StdinConfig{
				Gateway: &StdinGatewayConfig{
					Port:   intPtr(8080),
					Domain: "${MCP_GATEWAY_DOMAIN}",
				},
			},
			shouldErr: false,
		},
		{
			name: "invalid domain - other string",
			config: &StdinConfig{
				Gateway: &StdinGatewayConfig{
					Port:   intPtr(8080),
					Domain: "example.com",
				},
			},
			shouldErr: true,
			errorMsg:  "must be 'localhost', 'host.docker.internal', or a variable expression",
		},
		{
			name: "valid timeout values",
			config: &StdinConfig{
				Gateway: &StdinGatewayConfig{
					Port:           intPtr(8080),
					Domain:         "localhost",
					StartupTimeout: intPtr(30),
					ToolTimeout:    intPtr(60),
				},
			},
			shouldErr: false,
		},
		{
			name: "invalid entrypoint - empty string",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:       "stdio",
						Container:  "test:latest",
						Entrypoint: "   ",
					},
				},
			},
			shouldErr: true,
			errorMsg:  "entrypoint cannot be empty or whitespace only",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStringPatterns(tt.config)

			if tt.shouldErr {
				assert.Error(t, err)
				if tt.errorMsg != "" && err != nil && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing %q, got: %v", tt.errorMsg, err)
				}
			} else {
				assert.NoError(t, err, "Unexpected error")
			}
		})
	}
}

// TestEnhancedErrorMessages verifies that validation errors include version and detailed context
func TestEnhancedErrorMessages(t *testing.T) {
	// Set a test version
	version.Set("v1.2.3-test")

	tests := []struct {
		name          string
		config        string
		expectInError []string
	}{
		{
			name: "additional property error includes version and details",
			config: `{
"mcpServers": {
"github": {
"container": "ghcr.io/github/github-mcp-server:latest",
"unknownField": "value"
}
},
"gateway": {
"port": 8080,
"domain": "localhost",
"apiKey": "test-key"
}
}`,
			expectInError: []string{
				"v1.2.3-test",
				"Location:",
				"Error:",
				"Details:",
				"https://raw.githubusercontent.com/github/gh-aw/v0.52.1/docs/public/schemas/mcp-gateway-config.schema.json",
			},
		},
		{
			name: "missing required field error includes version and details",
			config: `{
"mcpServers": {
"github": {
"container": "ghcr.io/github/github-mcp-server:latest"
}
},
"gateway": {
"port": 8080,
"domain": "localhost"
}
}`,
			expectInError: []string{
				"v1.2.3-test",
				"Location:",
				"Error:",
				"Details:",
			},
		},
		{
			name: "invalid port value error includes version and details",
			config: `{
"mcpServers": {
"github": {
"container": "ghcr.io/github/github-mcp-server:latest"
}
},
"gateway": {
"port": 99999,
"domain": "localhost",
"apiKey": "test-key"
}
}`,
			expectInError: []string{
				"v1.2.3-test",
				"Location:",
				"Error:",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateJSONSchema([]byte(tt.config))

			if err == nil {
				t.Errorf("Expected error but got none")
				return
			}

			errStr := err.Error()
			for _, expected := range tt.expectInError {
				if !strings.Contains(errStr, expected) {
					t.Errorf("Expected error to contain %q, but it didn't.\nFull error:\n%s", expected, errStr)
				}
			}
		})
	}
}

// TestSchemaCaching verifies that the schema is compiled once and cached for reuse
func TestSchemaCaching(t *testing.T) {
	// Note: We can't fully reset the package-level sync.Once, but we can verify
	// that multiple calls to getOrCompileSchema return the same schema instance

	schema1, err1 := getOrCompileSchema()
	assert.NoError(t, err1, "First schema compilation should succeed")
	assert.NotNil(t, schema1, "First schema should not be nil")

	schema2, err2 := getOrCompileSchema()
	assert.NoError(t, err2, "Second schema retrieval should succeed")
	assert.NotNil(t, schema2, "Second schema should not be nil")

	// Verify that both calls return the exact same schema instance (pointer equality)
	// This confirms caching is working correctly
	if schema1 != schema2 {
		t.Error("Expected both calls to return the same cached schema instance")
	}

	// Verify the cached schema can actually validate configurations
	validConfig := `{
"mcpServers": {
"test": {
"container": "ghcr.io/test/server:latest"
}
},
"gateway": {
"port": 8080,
"domain": "localhost",
"apiKey": "test-key"
}
}`

	err := validateJSONSchema([]byte(validConfig))
	assert.NoError(t, err, "Validation with cached schema should succeed")
}

// TestSchemaURLConfiguration verifies that the schema URL is configurable
func TestSchemaURLConfiguration(t *testing.T) {
	// Verify the schema URL is properly set
	// This test documents the schema URL configuration for version pinning

	// The current implementation uses 'main' branch
	// For production, consider pinning to a specific commit SHA or version tag
	expectedPattern := "https://raw.githubusercontent.com/github/gh-aw/"

	// We can't directly test the package-level schemaURL variable,
	// but we can verify that the schema compiles and validates correctly
	schema, err := getOrCompileSchema()
	assert.NoError(t, err, "Schema compilation should succeed")
	assert.NotNil(t, schema, "Schema should not be nil")

	// Verify that the schema works for validation
	validConfig := `{
"mcpServers": {
"test": {
"container": "ghcr.io/test/server:latest"
}
},
"gateway": {
"port": 8080,
"domain": "localhost",
"apiKey": "test-key"
}
}`

	err = validateJSONSchema([]byte(validConfig))
	assert.NoError(t, err, "Validation should succeed with configured schema URL")

	// Document the version pinning approach in test output
	t.Logf("Schema URL pattern: %s", expectedPattern)
	t.Logf("For production builds, consider pinning to: %s<commit-sha>/...", expectedPattern)
	t.Logf("Or use a version tag: %sv1.0.0/...", expectedPattern)
}
