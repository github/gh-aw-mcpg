package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/version"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompilerAssertsFormat(t *testing.T) {
	for name, newCompiler := range map[string]func() *jsonschema.Compiler{
		"default":            newCompiler,
		"with schema loader": newCompilerWithSchemaLoader,
	} {
		t.Run(name, func(t *testing.T) {
			const schemaURL = "https://test.example.com/schema.json"
			schemaDoc, err := jsonschema.UnmarshalJSON(strings.NewReader(`{
				"type": "object",
				"properties": {"url": {"type": "string", "format": "uri"}}
			}`))
			require.NoError(t, err)

			compiler := newCompiler()
			require.NoError(t, compiler.AddResource(schemaURL, schemaDoc))
			schema, err := compiler.Compile(schemaURL)
			require.NoError(t, err)

			require.Error(t, schema.Validate(map[string]any{"url": "not a URI"}))
			require.NoError(t, schema.Validate(map[string]any{"url": "https://example.com"}))
		})
	}
}

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
					"agentId": "test-key"
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
					"agentId": "test-key",
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
						"caCertFile": "/tmp/private-ca.pem",
						"headers": {"Authorization": "Bearer token"}
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: false,
		},
		{
			name: "valid topology hostname domain (network-isolation)",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest"
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "awmg-mcpg",
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key",
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
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key"
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
					"agentId": "test-key",
					"unknownField": "value"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "invalid mount - two-part format rejected by schema",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest",
						"mounts": ["/host/path:/container/path"]
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "invalid mount - relative source path rejected by schema",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest",
						"mounts": ["host/path:/container/path:ro"]
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
				}
			}`,
			shouldErr: true,
			errorMsg:  "validation error",
		},
		{
			name: "invalid mount - relative destination path rejected by schema",
			config: `{
				"mcpServers": {
					"github": {
						"container": "ghcr.io/github/github-mcp-server:latest",
						"mounts": ["/host/path:container/path:ro"]
					}
				},
				"gateway": {
					"port": 8080,
					"domain": "localhost",
					"agentId": "test-key"
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
				require.Error(t, err)
				if tt.errorMsg != "" {
					require.ErrorContains(t, err, tt.errorMsg)
				}
			} else {
				assert.NoError(t, err, "Unexpected error")
			}
		})
	}
}

func TestValidateJSONSchemaContainerDigest(t *testing.T) {
	digest := strings.Repeat("a1", 32)
	tests := []struct {
		name      string
		container string
		shouldErr bool
	}{
		{name: "untagged image", container: "ghcr.io/owner/image"},
		{name: "tagged image", container: "ghcr.io/owner/image:v1.2.3"},
		{name: "digest without tag", container: "ghcr.io/owner/image@sha256:" + digest},
		{name: "digest with tag", container: "ghcr.io/owner/image:v1.2.3@sha256:" + digest},
		{name: "uppercase hex digest", container: "ghcr.io/owner/image@sha256:" + strings.ToUpper(digest)},
		{name: "empty digest", container: "ghcr.io/owner/image@sha256:", shouldErr: true},
		{name: "short digest", container: "ghcr.io/owner/image@sha256:" + digest[:63], shouldErr: true},
		{name: "long digest", container: "ghcr.io/owner/image@sha256:" + digest + "a", shouldErr: true},
		{name: "non-hex digest", container: "ghcr.io/owner/image@sha256:" + strings.Repeat("g", 64), shouldErr: true},
		{name: "unsupported algorithm", container: "ghcr.io/owner/image@sha512:" + digest, shouldErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := fmt.Sprintf(`{
				"mcpServers": {"test": {"type": "stdio", "container": %q}},
				"gateway": {"port": 8080, "domain": "localhost", "agentId": "test-key"}
			}`, tt.container)
			err := validateJSONSchema([]byte(config))
			if tt.shouldErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
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
			name: "valid container pattern - tag with sha256 digest",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "ghcr.io/owner/image:v1.2.3@sha256:2763823c67a0adca3fce6e3bdfee41a674e3bf22f0e6b2eee94ed3a72ebcd519",
					},
				},
			},
			shouldErr: false,
		},
		{
			name: "valid container pattern - sha256 digest only",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "ghcr.io/owner/image@sha256:2763823c67a0adca3fce6e3bdfee41a674e3bf22f0e6b2eee94ed3a72ebcd519",
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
			name: "mount validation is deferred",
			config: &StdinConfig{
				MCPServers: map[string]*StdinServerConfig{
					"test": {
						Type:      "stdio",
						Container: "test:latest",
						Mounts:    []string{"/host/path:/container/path"},
					},
				},
			},
			shouldErr: false,
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
			name: "valid domain - topology hostname (network-isolation)",
			config: &StdinConfig{
				Gateway: &StdinGatewayConfig{
					Port:   intPtr(8080),
					Domain: "awmg-mcpg",
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
			errorMsg:  "must be 'localhost', 'host.docker.internal', an RFC-1123 hostname label",
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
			err := validateRuleBasedPatterns(tt.config)

			if tt.shouldErr {
				require.Error(t, err)
				if tt.errorMsg != "" {
					require.ErrorContains(t, err, tt.errorMsg)
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
"agentId": "test-key"
}
}`,
			expectInError: []string{
				"v1.2.3-test",
				"Location:",
				"Error:",
				"Details:",
				SchemaURL,
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
"agentId": "test-key"
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

			require.Error(t, err, "Expected validation to fail for: %s", tt.name)
			for _, expected := range tt.expectInError {
				assert.ErrorContains(t, err, expected)
			}
		})
	}
}

// TestSchemaCaching verifies that the schema is compiled once and cached for reuse
func TestSchemaCaching(t *testing.T) {
	// Note: We can't fully reset the package-level sync.Once, but we can verify
	// that multiple calls to getOrCompileSchema return the same schema instance

	schema1, err1 := getOrCompileSchema()
	require.NoError(t, err1, "First schema compilation should succeed")
	assert.NotNil(t, schema1, "First schema should not be nil")

	schema2, err2 := getOrCompileSchema()
	require.NoError(t, err2, "Second schema retrieval should succeed")
	assert.NotNil(t, schema2, "Second schema should not be nil")

	// Verify that both calls return the exact same schema instance (pointer equality)
	// This confirms caching is working correctly
	assert.Same(t, schema1, schema2, "Both calls should return the same cached schema instance")

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
"agentId": "test-key"
}
}`

	err := validateJSONSchema([]byte(validConfig))
	assert.NoError(t, err, "Validation with cached schema should succeed")
}

// TestSchemaConfiguration verifies that the embedded schema is bundled correctly
func TestSchemaConfiguration(t *testing.T) {
	// Verify the embedded schema bytes are non-empty
	assert.NotEmpty(t, embeddedSchemaBytes, "Embedded schema should not be empty")

	// Verify that the schema compiles and validates correctly
	schema, err := getOrCompileSchema()
	require.NoError(t, err, "Schema compilation should succeed")
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
"agentId": "test-key"
}
}`

	err = validateJSONSchema([]byte(validConfig))
	require.NoError(t, err, "Validation should succeed with embedded schema")

	t.Logf("Embedded schema size: %d bytes", len(embeddedSchemaBytes))
}

// TestFixSchemaBytes_keepaliveInterval verifies that keepaliveInterval is accepted by
// the schema after fixSchemaBytes is applied (spec §4.1.3.5).
// Prior to this fix, the field was silently rejected with additionalProperties:false
// even though the Go struct already supported it.
func TestFixSchemaBytes_keepaliveInterval(t *testing.T) {
	validConfig := `{
"mcpServers": {
"github": {
"container": "ghcr.io/github/github-mcp-server:latest"
}
},
"gateway": {
"port": 8080,
"domain": "localhost",
"agentId": "test-key",
"keepaliveInterval": 300
}
}`

	err := validateJSONSchema([]byte(validConfig))
	assert.NoError(t, err, "keepaliveInterval should be accepted by the schema (spec §4.1.3.5)")
}

// TestFixSchemaBytes_keepaliveIntervalNegative verifies that -1 (disable) is accepted.
func TestFixSchemaBytes_keepaliveIntervalNegative(t *testing.T) {
	validConfig := `{
"mcpServers": {
"github": {
"container": "ghcr.io/github/github-mcp-server:latest"
}
},
"gateway": {
"port": 8080,
"domain": "localhost",
"agentId": "test-key",
"keepaliveInterval": -1
}
}`

	err := validateJSONSchema([]byte(validConfig))
	assert.NoError(t, err, "keepaliveInterval -1 (disable) should be accepted by the schema")
}

// TestSchema_OpenTelemetryConfig verifies that the JSON schema accepts and rejects
// opentelemetry configurations correctly, covering currently defined schema fields.
func TestSchema_OpenTelemetryConfig(t *testing.T) {
	validBase := `{
		"mcpServers": {"s": {"container": "img:latest"}},
		"gateway": {"port": 8080, "domain": "localhost", "agentId": "test-key", "opentelemetry": %s}
	}`

	tests := []struct {
		name      string
		otel      string
		shouldErr bool
		errMsg    string
	}{
		{
			name:      "valid minimal opentelemetry (endpoint only)",
			otel:      `{"endpoint": "https://otel.example.com"}`,
			shouldErr: false,
		},
		{
			name:      "valid opentelemetry with http endpoint",
			otel:      `{"endpoint": "http://127.0.0.1:4318/v1/traces"}`,
			shouldErr: false,
		},
		{
			name:      "invalid opentelemetry with headers",
			otel:      `{"endpoint": "https://otel.example.com", "headers": "Authorization=Bearer token"}`,
			shouldErr: true,
			errMsg:    "headers",
		},
		{
			name:      "valid opentelemetry with all fields",
			otel:      `{"endpoint": "https://otel.example.com", "traceId": "4bf92f3577b34da6a3ce929d0e0e4736", "spanId": "00f067aa0ba902b7", "serviceName": "my-svc"}`,
			shouldErr: false,
		},
		{
			name:      "valid opentelemetry with serviceName only",
			otel:      `{"endpoint": "https://otel.example.com", "serviceName": "custom-svc"}`,
			shouldErr: false,
		},
		{
			name:      "invalid opentelemetry missing endpoint",
			otel:      `{"serviceName": "svc"}`,
			shouldErr: true,
			errMsg:    "endpoint",
		},
		{
			name:      "invalid opentelemetry unknown field",
			otel:      `{"endpoint": "https://otel.example.com", "unknownField": "value"}`,
			shouldErr: true,
			errMsg:    "unknownField",
		},
		{
			name:      "invalid opentelemetry non-http scheme",
			otel:      `{"endpoint": "grpc://otel.example.com:4317"}`,
			shouldErr: true,
			errMsg:    "endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := fmt.Sprintf(validBase, tt.otel)
			err := validateJSONSchema([]byte(cfg))
			if tt.shouldErr {
				require.Error(t, err, "expected schema validation to fail")
				if tt.errMsg != "" {
					require.ErrorContains(t, err, tt.errMsg)
				}
			} else {
				assert.NoError(t, err, "expected schema validation to pass")
			}
		})
	}
}

// TestValidateJSONSchema_CustomSchemasPropertyNamesLocation verifies that invalid
// customSchemas key names are reported with the propertyNames instance path.
func TestValidateJSONSchema_CustomSchemasPropertyNamesLocation(t *testing.T) {
	config := `{
		"mcpServers": {
			"github": {
				"container": "ghcr.io/github/github-mcp-server:latest"
			}
		},
		"gateway": {
			"port": 8080,
			"domain": "localhost",
			"agentId": "test-key"
		},
		"customSchemas": {
			"Bad_Name": "https://example.com/schema.json"
		}
	}`

	err := validateJSONSchema([]byte(config))
	require.Error(t, err)
	assert.ErrorContains(t, err, "Error: 'Bad_Name' does not match pattern '^[a-z][a-z0-9-]*$'")
}
