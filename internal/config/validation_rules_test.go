package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validationErrAsError(err *ValidationError) error {
	if err == nil {
		return nil
	}
	return err
}

func TestPortRange(t *testing.T) {
	tests := []struct {
		name      string
		port      int
		jsonPath  string
		shouldErr bool
		errMsg    string
	}{
		{
			name:      "valid port 8080",
			port:      8080,
			jsonPath:  "gateway.port",
			shouldErr: false,
		},
		{
			name:      "valid port 1",
			port:      1,
			jsonPath:  "gateway.port",
			shouldErr: false,
		},
		{
			name:      "valid port 65535",
			port:      65535,
			jsonPath:  "gateway.port",
			shouldErr: false,
		},
		{
			name:      "invalid port 0",
			port:      0,
			jsonPath:  "gateway.port",
			shouldErr: true,
			errMsg:    "port must be between 1 and 65535",
		},
		{
			name:      "invalid port 65536",
			port:      65536,
			jsonPath:  "gateway.port",
			shouldErr: true,
			errMsg:    "port must be between 1 and 65535",
		},
		{
			name:      "invalid negative port",
			port:      -1,
			jsonPath:  "gateway.port",
			shouldErr: true,
			errMsg:    "port must be between 1 and 65535",
		},
		{
			name:      "invalid port 100000",
			port:      100000,
			jsonPath:  "gateway.port",
			shouldErr: true,
			errMsg:    "port must be between 1 and 65535",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PortRange(tt.port, tt.jsonPath)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestTimeoutPositive(t *testing.T) {
	tests := []struct {
		name       string
		timeout    int
		fieldName  string
		jsonPath   string
		shouldErr  bool
		errMsg     string
		suggestion string
	}{
		{
			name:      "valid timeout 30",
			timeout:   30,
			fieldName: "startupTimeout",
			jsonPath:  "gateway.startupTimeout",
			shouldErr: false,
		},
		{
			name:      "valid timeout 1",
			timeout:   1,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: false,
		},
		{
			name:      "valid large timeout",
			timeout:   3600,
			fieldName: "startupTimeout",
			jsonPath:  "gateway.startupTimeout",
			shouldErr: false,
		},
		{
			name:       "invalid timeout 0",
			timeout:    0,
			fieldName:  "startupTimeout",
			jsonPath:   "gateway.startupTimeout",
			shouldErr:  true,
			errMsg:     "startupTimeout must be a positive integer (>= 1), got 0",
			suggestion: "Use a positive number of seconds (e.g., 30)",
		},
		{
			name:       "invalid negative timeout",
			timeout:    -10,
			fieldName:  "toolTimeout",
			jsonPath:   "gateway.toolTimeout",
			shouldErr:  true,
			errMsg:     "toolTimeout must be a positive integer (>= 1), got -10",
			suggestion: "Use a positive number of seconds (e.g., 30)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := TimeoutPositive(tt.timeout, tt.fieldName, tt.jsonPath)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
				assert.Equal(t, tt.fieldName, err.Field, "Field name should match")
				assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestPositiveInteger(t *testing.T) {
	tests := []struct {
		name       string
		value      int
		fieldName  string
		jsonPath   string
		shouldErr  bool
		errMsg     string
		suggestion string
	}{
		{
			name:      "valid value 1",
			value:     1,
			fieldName: "payloadSizeThreshold",
			jsonPath:  "gateway.payloadSizeThreshold",
			shouldErr: false,
		},
		{
			name:      "valid larger value",
			value:     524288,
			fieldName: "payload_size_threshold",
			jsonPath:  "gateway.payload_size_threshold",
			shouldErr: false,
		},
		{
			name:       "zero rejected",
			value:      0,
			fieldName:  "payloadSizeThreshold",
			jsonPath:   "gateway.payloadSizeThreshold",
			shouldErr:  true,
			errMsg:     "payloadSizeThreshold must be a positive integer (>= 1), got 0",
			suggestion: "Use a positive integer (>= 1) for payloadSizeThreshold",
		},
		{
			name:       "negative value rejected",
			value:      -1,
			fieldName:  "payload_size_threshold",
			jsonPath:   "gateway.payload_size_threshold",
			shouldErr:  true,
			errMsg:     "payload_size_threshold must be a positive integer (>= 1), got -1",
			suggestion: "Use a positive integer (>= 1) for payload_size_threshold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := PositiveInteger(tt.value, tt.fieldName, tt.jsonPath)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
				assert.Equal(t, tt.fieldName, err.Field, "Field name should match")
				assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestValidatePositiveIntegerRule(t *testing.T) {
	tests := []struct {
		name         string
		value        int
		fieldName    string
		jsonPath     string
		logLabel     string
		failureLabel string
		suggestion   string
		shouldErr    bool
		errMsg       string
	}{
		{
			name:         "valid value passes",
			value:        1,
			fieldName:    "toolTimeout",
			jsonPath:     "gateway.toolTimeout",
			logLabel:     "positive timeout",
			failureLabel: "Positive timeout",
			suggestion:   "Use a positive number of seconds (e.g., 30)",
			shouldErr:    false,
		},
		{
			name:         "invalid value preserves custom failure label and suggestion",
			value:        0,
			fieldName:    "payloadSizeThreshold",
			jsonPath:     "gateway.payloadSizeThreshold",
			logLabel:     "positive integer",
			failureLabel: "Positive integer",
			suggestion:   "Use a positive integer (>= 1) for payloadSizeThreshold",
			shouldErr:    true,
			errMsg:       "payloadSizeThreshold must be a positive integer (>= 1), got 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePositiveIntegerRule(tt.value, tt.fieldName, tt.jsonPath, tt.logLabel, tt.failureLabel, tt.suggestion)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, tt.fieldName, err.Field, "Field name should match")
				assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
				assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")
				return
			}

			require.NoError(t, validationErrAsError(err), "Unexpected validation error")
		})
	}
}

func TestValidateMinimumRule(t *testing.T) {
	tests := []struct {
		name       string
		value      int
		min        int
		shouldErr  bool
		errMsg     string
		suggestion string
	}{
		{
			name:      "value at minimum passes",
			value:     10,
			min:       10,
			shouldErr: false,
		},
		{
			name:       "value below minimum returns expected message",
			value:      9,
			min:        10,
			shouldErr:  true,
			errMsg:     "toolTimeout must be at least 10, got 9",
			suggestion: "Use a value of at least 10 seconds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMinimumRule(
				tt.value,
				tt.min,
				"toolTimeout",
				"gateway.toolTimeout",
				"timeout minimum",
				"Timeout minimum",
				fmt.Sprintf("is below minimum %d", tt.min),
				fmt.Sprintf("toolTimeout must be at least %d, got %d", tt.min, tt.value),
				fmt.Sprintf("Use a value of at least %d seconds", tt.min),
			)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")
				return
			}

			require.NoError(t, validationErrAsError(err), "Unexpected validation error")
		})
	}
}

func TestValidateInclusiveRangeRule(t *testing.T) {
	tests := []struct {
		name      string
		value     int
		shouldErr bool
		errMsg    string
	}{
		{
			name:      "value in range passes",
			value:     8080,
			shouldErr: false,
		},
		{
			name:      "value below range returns expected message",
			value:     0,
			shouldErr: true,
			errMsg:    "port must be between 1 and 65535, got 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInclusiveRangeRule(
				tt.value,
				1,
				65535,
				"port",
				"gateway.port",
				"port range",
				"Port",
				"out of range",
				fmt.Sprintf("port must be between 1 and 65535, got %d", tt.value),
				"Use a valid port number (e.g., 8080)",
			)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, "Use a valid port number (e.g., 8080)", err.Suggestion, "Suggestion should match")
				return
			}

			require.NoError(t, validationErrAsError(err), "Unexpected validation error")
		})
	}
}

func TestMountFormat(t *testing.T) {
	tests := []struct {
		name      string
		mount     string
		jsonPath  string
		index     int
		shouldErr bool
		errMsg    string
	}{
		{
			name:      "valid ro mount",
			mount:     "/host/path:/container/path:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: false,
		},
		{
			name:      "valid rw mount",
			mount:     "/var/data:/app/data:rw",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: false,
		},
		{
			name:      "valid mount with special chars",
			mount:     "/home/user/my-app:/app/data:ro",
			jsonPath:  "mcpServers.github",
			index:     1,
			shouldErr: false,
		},
		{
			name:      "invalid mount without mode",
			mount:     "/host/path:/container/path",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "invalid mount format",
		},
		{
			name:      "invalid format - too many colons",
			mount:     "/host/path:/container/path:ro:extra",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "invalid mount format",
		},
		{
			name:      "invalid format - empty source",
			mount:     ":/container/path:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "mount source cannot be empty",
		},
		{
			name:      "invalid format - empty dest",
			mount:     "/host/path::ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "mount destination cannot be empty",
		},
		{
			name:      "invalid mode",
			mount:     "/host/path:/container/path:invalid",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "invalid mount mode",
		},
		{
			name:      "invalid mode - uppercase",
			mount:     "/host/path:/container/path:RO",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "invalid mount mode",
		},
		{
			name:      "invalid source - relative path",
			mount:     "relative/path:/container/path:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "mount source must be an absolute path",
		},
		{
			name:      "invalid dest - relative path",
			mount:     "/host/path:relative/path:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "mount destination must be an absolute path",
		},
		{
			name:      "invalid source - dot relative",
			mount:     "./config:/app/config:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "mount source must be an absolute path",
		},
		{
			name:      "invalid dest - dot relative",
			mount:     "/host/config:./config:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "mount destination must be an absolute path",
		},
		{
			name:      "invalid source - parent relative",
			mount:     "../config:/app/config:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "mount source must be an absolute path",
		},
		{
			name:      "invalid dest - parent relative",
			mount:     "/host/config:../config:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: true,
			errMsg:    "mount destination must be an absolute path",
		},
		{
			name:      "valid mount - root paths",
			mount:     "/:/root:ro",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: false,
		},
		{
			name:      "valid mount - deep nested paths",
			mount:     "/var/lib/docker/volumes/data:/app/data/volumes:rw",
			jsonPath:  "mcpServers.github",
			index:     0,
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := MountFormat(tt.mount, tt.jsonPath, tt.index)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, fmt.Sprintf("%s.mounts[%d]", tt.jsonPath, tt.index), err.JSONPath, "JSONPath should match expected pattern")
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestValidationError_Error(t *testing.T) {
	tests := []struct {
		name          string
		valErr        *ValidationError
		wantSubstr    []string
		notWantSubstr []string
	}{
		{
			name: "error with suggestion",
			valErr: &ValidationError{
				Field:      "port",
				Message:    "port must be between 1 and 65535",
				JSONPath:   "gateway.port",
				Suggestion: "Use a valid port number",
			},
			wantSubstr: []string{
				"Configuration error at gateway.port",
				"port must be between 1 and 65535",
				"Suggestion: Use a valid port number",
			},
		},
		{
			name: "error without suggestion",
			valErr: &ValidationError{
				Field:    "timeout",
				Message:  "timeout must be positive",
				JSONPath: "gateway.startupTimeout",
			},
			wantSubstr: []string{
				"Configuration error at gateway.startupTimeout",
				"timeout must be positive",
			},
			// Verify suggestion section is completely absent when empty
			notWantSubstr: []string{"Suggestion:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.valErr.Error()

			for _, substr := range tt.wantSubstr {
				assert.Contains(t, errStr, substr, "Error string should contain expected substring")
			}
			for _, substr := range tt.notWantSubstr {
				assert.NotContains(t, errStr, substr, "Error string should NOT contain %q when suggestion is empty", substr)
			}
		})
	}
}

func TestUnsupportedType(t *testing.T) {
	tests := []struct {
		name       string
		fieldName  string
		actualType string
		jsonPath   string
		suggestion string
		wantSubstr []string
	}{
		{
			name:       "unsupported server type grpc",
			fieldName:  "type",
			actualType: "grpc",
			jsonPath:   "mcpServers.github",
			suggestion: "Use 'stdio' for standard input/output transport or 'http' for HTTP transport",
			wantSubstr: []string{
				"type",
				"unsupported server type 'grpc'",
				"mcpServers.github.type",
				"Use 'stdio'",
			},
		},
		{
			name:       "unsupported server type websocket",
			fieldName:  "type",
			actualType: "websocket",
			jsonPath:   "mcpServers.my-server",
			suggestion: "Use 'http' for HTTP transport",
			wantSubstr: []string{
				"unsupported server type 'websocket'",
				"mcpServers.my-server.type",
			},
		},
		{
			name:       "unsupported empty type",
			fieldName:  "type",
			actualType: "",
			jsonPath:   "mcpServers.test",
			suggestion: "Specify a valid server type",
			wantSubstr: []string{
				"unsupported server type ''",
				"mcpServers.test.type",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := UnsupportedType(tt.fieldName, tt.actualType, tt.jsonPath, tt.suggestion)

			assert.Equal(t, tt.fieldName, err.Field, "Field should match")
			assert.Contains(t, err.Message, tt.actualType, "Message should contain actual type")
			assert.Contains(t, err.JSONPath, tt.jsonPath, "JSONPath should contain json path")
			assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")

			errStr := err.Error()
			for _, substr := range tt.wantSubstr {
				assert.Contains(t, errStr, substr, "Error string should contain expected substring")
			}
		})
	}
}

func TestUndefinedVariable(t *testing.T) {
	tests := []struct {
		name       string
		varName    string
		jsonPath   string
		wantSubstr []string
	}{
		{
			name:     "undefined env variable with simple name",
			varName:  "MY_VAR",
			jsonPath: "mcpServers.github.env.TOKEN",
			wantSubstr: []string{
				"undefined environment variable referenced: MY_VAR",
				"mcpServers.github.env.TOKEN",
				"Set the environment variable MY_VAR",
			},
		},
		{
			name:     "undefined env variable with PAT token name",
			varName:  "GITHUB_PERSONAL_ACCESS_TOKEN",
			jsonPath: "mcpServers.github.env.GITHUB_PERSONAL_ACCESS_TOKEN",
			wantSubstr: []string{
				"undefined environment variable referenced: GITHUB_PERSONAL_ACCESS_TOKEN",
				"Set the environment variable GITHUB_PERSONAL_ACCESS_TOKEN",
			},
		},
		{
			name:     "undefined env variable with config dir",
			varName:  "GITHUB_CONFIG_DIR",
			jsonPath: "mcpServers.github.env.CONFIG_PATH",
			wantSubstr: []string{
				"undefined environment variable referenced: GITHUB_CONFIG_DIR",
				"Set the environment variable GITHUB_CONFIG_DIR",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := UndefinedVariable(tt.varName, tt.jsonPath)

			assert.Equal(t, "env variable", err.Field, "Field should be 'env variable'")
			assert.Contains(t, err.Message, tt.varName, "Message should contain variable name")
			assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
			assert.Contains(t, err.Suggestion, tt.varName, "Suggestion should contain variable name")

			errStr := err.Error()
			for _, substr := range tt.wantSubstr {
				assert.Contains(t, errStr, substr, "Error string should contain expected substring")
			}
		})
	}
}

func TestMissingRequired(t *testing.T) {
	tests := []struct {
		name       string
		fieldName  string
		serverType string
		jsonPath   string
		suggestion string
		wantSubstr []string
	}{
		{
			name:       "missing container field",
			fieldName:  "container",
			serverType: "stdio",
			jsonPath:   "mcpServers.github",
			suggestion: "Add a 'container' field (e.g., \"ghcr.io/owner/image:tag\")",
			wantSubstr: []string{
				"container",
				"'container' is required",
				"stdio servers",
				"mcpServers.github",
			},
		},
		{
			name:       "missing url field",
			fieldName:  "url",
			serverType: "HTTP",
			jsonPath:   "mcpServers.httpServer",
			suggestion: "Add a 'url' field (e.g., \"https://example.com/mcp\")",
			wantSubstr: []string{
				"url",
				"'url' is required",
				"HTTP servers",
				"mcpServers.httpServer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := MissingRequired(tt.fieldName, tt.serverType, tt.jsonPath, tt.suggestion)

			assert.Equal(t, tt.fieldName, err.Field, "Field should match")
			assert.Contains(t, err.Message, tt.fieldName, "Message should contain field name")
			assert.Contains(t, err.Message, tt.serverType, "Message should contain server type")
			assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
			assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")

			errStr := err.Error()
			for _, substr := range tt.wantSubstr {
				assert.Contains(t, errStr, substr, "Error string should contain expected substring")
			}
		})
	}
}

func TestUnsupportedField(t *testing.T) {
	tests := []struct {
		name       string
		fieldName  string
		message    string
		jsonPath   string
		suggestion string
		wantSubstr []string
	}{
		{
			name:       "unsupported command field",
			fieldName:  "command",
			message:    "'command' field is not supported (stdio servers must use 'container')",
			jsonPath:   "mcpServers.github",
			suggestion: "Remove 'command' field and use 'container' instead",
			wantSubstr: []string{
				"command",
				"not supported",
				"mcpServers.github",
			},
		},
		{
			name:       "unsupported args field",
			fieldName:  "args",
			message:    "'args' field is not supported in JSON stdin format",
			jsonPath:   "mcpServers.myserver",
			suggestion: "Remove 'args' field; use 'container' with image arguments instead",
			wantSubstr: []string{
				"args",
				"not supported",
				"mcpServers.myserver",
			},
		},
		{
			name:       "unsupported field with empty suggestion",
			fieldName:  "deprecated_field",
			message:    "'deprecated_field' has been removed",
			jsonPath:   "mcpServers.legacy",
			suggestion: "",
			wantSubstr: []string{
				"deprecated_field",
				"mcpServers.legacy",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := UnsupportedField(tt.fieldName, tt.message, tt.jsonPath, tt.suggestion)

			assert.Equal(t, tt.fieldName, err.Field, "Field should match")
			assert.Equal(t, tt.message, err.Message, "Message should match")
			assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
			assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")

			errStr := err.Error()
			for _, substr := range tt.wantSubstr {
				assert.Contains(t, errStr, substr, "Error string should contain expected substring")
			}
		})
	}
}

func TestAppendConfigDocsFooter(t *testing.T) {
	var sb strings.Builder
	AppendConfigDocsFooter(&sb)

	result := sb.String()

	wantSubstr := []string{
		"Please check your configuration",
		ConfigSpecURL,
		"JSON Schema reference",
		SchemaURL,
	}

	for _, substr := range wantSubstr {
		assert.Contains(t, result, substr, "Footer should contain expected substring")
	}
}

func TestDocumentationURLConstants(t *testing.T) {
	assert.NotEmpty(t, ConfigSpecURL, "ConfigSpecURL should not be empty")
	assert.NotEmpty(t, SchemaURL, "SchemaURL should not be empty")
	assert.True(t, strings.HasPrefix(ConfigSpecURL, "https://"), "ConfigSpecURL should start with https://")
	assert.True(t, strings.HasPrefix(SchemaURL, "https://"), "SchemaURL should start with https://")
}

func TestInvalidPattern(t *testing.T) {
	tests := []struct {
		name       string
		fieldName  string
		value      string
		jsonPath   string
		suggestion string
		wantSubstr []string
	}{
		{
			name:       "container field does not match pattern",
			fieldName:  "container",
			value:      "invalid image",
			jsonPath:   "mcpServers.github",
			suggestion: "Use a valid container image reference (e.g., 'ghcr.io/owner/image:tag')",
			wantSubstr: []string{
				"container",
				"'invalid image' does not match required pattern",
				"mcpServers.github",
			},
		},
		{
			name:       "url field does not match pattern",
			fieldName:  "url",
			value:      "not-a-url",
			jsonPath:   "mcpServers.http-server",
			suggestion: "Use a valid URL starting with http:// or https://",
			wantSubstr: []string{
				"url",
				"'not-a-url' does not match required pattern",
				"mcpServers.http-server",
			},
		},
		{
			name:       "mounts field does not match pattern",
			fieldName:  "mounts",
			value:      "bad:mount",
			jsonPath:   "mcpServers.myserver.mounts[0]",
			suggestion: "Use format 'source:dest:mode'",
			wantSubstr: []string{
				"mounts",
				"'bad:mount' does not match required pattern",
				"mcpServers.myserver.mounts[0]",
			},
		},
		{
			name:       "empty value does not match pattern",
			fieldName:  "container",
			value:      "",
			jsonPath:   "mcpServers.empty",
			suggestion: "Provide a non-empty container image",
			wantSubstr: []string{
				"container",
				"'' does not match required pattern",
				"mcpServers.empty",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := InvalidPattern(tt.fieldName, tt.value, tt.jsonPath, tt.suggestion)

			require.NotNil(t, err, "Expected validation error but got none")
			assert.Equal(t, tt.fieldName, err.Field, "Field should match")
			assert.Contains(t, err.Message, tt.fieldName, "Message should contain field name")
			assert.Contains(t, err.Message, tt.value, "Message should contain the invalid value")
			assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
			assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")

			errStr := err.Error()
			for _, substr := range tt.wantSubstr {
				assert.Contains(t, errStr, substr, "Error string should contain expected substring")
			}
		})
	}
}

func TestInvalidValue(t *testing.T) {
	tests := []struct {
		name       string
		fieldName  string
		message    string
		jsonPath   string
		suggestion string
		wantSubstr []string
	}{
		{
			name:       "entrypoint cannot be empty or whitespace",
			fieldName:  "entrypoint",
			message:    "entrypoint cannot be empty or whitespace only",
			jsonPath:   "mcpServers.github",
			suggestion: "Provide a non-empty entrypoint command",
			wantSubstr: []string{
				"entrypoint",
				"entrypoint cannot be empty or whitespace only",
				"mcpServers.github",
			},
		},
		{
			name:       "domain contains invalid characters",
			fieldName:  "domain",
			message:    "domain 'my domain' contains spaces which are not allowed",
			jsonPath:   "gateway.domain",
			suggestion: "Use a valid hostname without spaces",
			wantSubstr: []string{
				"domain",
				"contains spaces",
				"gateway.domain",
			},
		},
		{
			name:       "customSchemas type constraint",
			fieldName:  "customSchemas.mytype",
			message:    "type 'mytype' must not shadow built-in types",
			jsonPath:   "customSchemas.mytype",
			suggestion: "Use a different type name that does not conflict with built-in types",
			wantSubstr: []string{
				"customSchemas.mytype",
				"must not shadow",
				"customSchemas.mytype",
			},
		},
		{
			name:       "empty message",
			fieldName:  "field",
			message:    "",
			jsonPath:   "gateway.field",
			suggestion: "",
			wantSubstr: []string{
				"gateway.field",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := InvalidValue(tt.fieldName, tt.message, tt.jsonPath, tt.suggestion)

			require.NotNil(t, err, "Expected validation error but got none")
			assert.Equal(t, tt.fieldName, err.Field, "Field should match")
			assert.Equal(t, tt.message, err.Message, "Message should match exactly")
			assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
			assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")

			errStr := err.Error()
			for _, substr := range tt.wantSubstr {
				assert.Contains(t, errStr, substr, "Error string should contain expected substring")
			}
		})
	}
}

func TestSchemaValidationError(t *testing.T) {
	tests := []struct {
		name       string
		serverType string
		message    string
		jsonPath   string
		suggestion string
		wantSubstr []string
	}{
		{
			name:       "schema fetch failure for stdio type",
			serverType: "stdio",
			message:    "failed to fetch custom schema",
			jsonPath:   "mcpServers.github",
			suggestion: "Check network connectivity or specify a local schema",
			wantSubstr: []string{
				"failed to fetch custom schema",
				"for server type 'stdio'",
				"mcpServers.github",
			},
		},
		{
			name:       "schema parse failure for http type",
			serverType: "http",
			message:    "failed to parse JSON schema",
			jsonPath:   "mcpServers.api-server",
			suggestion: "Ensure the schema is valid JSON Schema",
			wantSubstr: []string{
				"failed to parse JSON schema",
				"for server type 'http'",
				"mcpServers.api-server",
			},
		},
		{
			name:       "schema validation failure",
			serverType: "local",
			message:    "server configuration does not match schema",
			jsonPath:   "mcpServers.local-server",
			suggestion: "Review the server configuration against the schema",
			wantSubstr: []string{
				"does not match schema",
				"for server type 'local'",
				"mcpServers.local-server",
			},
		},
		{
			name:       "field is always type",
			serverType: "stdio",
			message:    "schema mismatch",
			jsonPath:   "mcpServers.test",
			suggestion: "",
			wantSubstr: []string{
				"schema mismatch for server type 'stdio'",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SchemaValidationError(tt.serverType, tt.message, tt.jsonPath, tt.suggestion)

			require.NotNil(t, err, "Expected validation error but got none")
			// SchemaValidationError always sets Field to "type"
			assert.Equal(t, "type", err.Field, "Field should always be 'type'")
			assert.Contains(t, err.Message, tt.message, "Message should contain the provided message")
			assert.Contains(t, err.Message, tt.serverType, "Message should contain the server type")
			assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
			assert.Equal(t, tt.suggestion, err.Suggestion, "Suggestion should match")

			errStr := err.Error()
			for _, substr := range tt.wantSubstr {
				assert.Contains(t, errStr, substr, "Error string should contain expected substring")
			}
		})
	}
}

func TestRequiredStringField(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		fieldName  string
		jsonPath   string
		suggestion string
		shouldErr  bool
		errMsg     string
	}{
		{
			name:       "valid non-empty string",
			value:      "approved",
			fieldName:  "min-integrity",
			jsonPath:   "allow-only.min-integrity",
			suggestion: "Specify the minimum integrity level",
			shouldErr:  false,
		},
		{
			name:       "empty string returns error",
			value:      "",
			fieldName:  "min-integrity",
			jsonPath:   "allow-only.min-integrity",
			suggestion: "Specify the minimum integrity level",
			shouldErr:  true,
			errMsg:     "min-integrity is required",
		},
		{
			name:       "custom field name in error",
			value:      "",
			fieldName:  "container",
			jsonPath:   "mcpServers.github.container",
			suggestion: "Add a container image",
			shouldErr:  true,
			errMsg:     "container is required",
		},
		{
			name:       "suggestion included in error",
			value:      "",
			fieldName:  "url",
			jsonPath:   "mcpServers.http-server.url",
			suggestion: "Provide an HTTPS URL",
			shouldErr:  true,
			errMsg:     "Provide an HTTPS URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := RequiredStringField(tt.value, tt.fieldName, tt.jsonPath, tt.suggestion)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				require.ErrorContains(t, err, tt.errMsg)
				require.ErrorContains(t, err, tt.jsonPath)
				assert.Equal(t, tt.fieldName, err.Field)
				assert.Equal(t, tt.suggestion, err.Suggestion)
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestNonEmptyString(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		fieldName string
		jsonPath  string
		shouldErr bool
		errMsg    string
	}{
		{
			name:      "valid non-empty string",
			value:     "/tmp/payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "valid single character",
			value:     "x",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "valid whitespace-only string is not empty",
			value:     "   ",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "empty string",
			value:     "",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "payloadDir is required",
		},
		{
			name:      "empty string with different field",
			value:     "",
			fieldName: "apiKey",
			jsonPath:  "gateway.apiKey",
			shouldErr: true,
			errMsg:    "apiKey is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NonEmptyString(tt.value, tt.fieldName, tt.jsonPath)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				require.ErrorContains(t, err, tt.errMsg)
				assert.ErrorContains(t, err, tt.jsonPath)
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestAbsolutePath(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		fieldName string
		jsonPath  string
		shouldErr bool
		errMsg    string
	}{
		{
			name:      "valid Unix absolute path",
			value:     "/tmp/payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "valid Unix root path",
			value:     "/",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "valid Unix nested path",
			value:     "/var/lib/payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "valid Windows absolute path - C drive",
			value:     "C:\\payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "valid Windows absolute path - D drive",
			value:     "D:\\temp\\payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "valid Windows absolute path - lowercase drive",
			value:     "c:\\payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: false,
		},
		{
			name:      "invalid relative Unix path",
			value:     "tmp/payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "must be an absolute path",
		},
		{
			name:      "invalid relative path with dot",
			value:     "./payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "must be an absolute path",
		},
		{
			name:      "invalid relative path with double dot",
			value:     "../payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "must be an absolute path",
		},
		{
			name:      "invalid Windows relative path",
			value:     "payloads\\data",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "must be an absolute path",
		},
		{
			name:      "invalid Windows path without backslash",
			value:     "C:payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "must be an absolute path",
		},
		{
			name:      "invalid Windows path with forward slash",
			value:     "C:/payloads",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "must be an absolute path",
		},
		{
			name:      "invalid Windows path - drive letter and colon only (too short)",
			value:     "C:",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "must be an absolute path",
		},
		{
			name:      "invalid single character",
			value:     "C",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "must be an absolute path",
		},
		{
			name:      "empty string",
			value:     "",
			fieldName: "payloadDir",
			jsonPath:  "gateway.payloadDir",
			shouldErr: true,
			errMsg:    "cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AbsolutePath(tt.value, tt.fieldName, tt.jsonPath)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				require.ErrorContains(t, err, tt.errMsg)
				assert.ErrorContains(t, err, tt.jsonPath)
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestTimeoutMinimum(t *testing.T) {
	tests := []struct {
		name      string
		timeout   int
		min       int
		fieldName string
		jsonPath  string
		shouldErr bool
		errMsg    string
	}{
		{
			name:      "value equals minimum",
			timeout:   10,
			min:       10,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: false,
		},
		{
			name:      "value above minimum",
			timeout:   30,
			min:       10,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: false,
		},
		{
			name:      "value below minimum",
			timeout:   5,
			min:       10,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: true,
			errMsg:    "toolTimeout must be at least 10, got 5",
		},
		{
			name:      "zero below positive minimum",
			timeout:   0,
			min:       1,
			fieldName: "startupTimeout",
			jsonPath:  "gateway.startupTimeout",
			shouldErr: true,
			errMsg:    "startupTimeout must be at least 1, got 0",
		},
		{
			name:      "negative value below minimum",
			timeout:   -5,
			min:       10,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: true,
			errMsg:    "toolTimeout must be at least 10, got -5",
		},
		{
			name:      "zero minimum, zero value",
			timeout:   0,
			min:       0,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := TimeoutMinimum(tt.timeout, tt.min, tt.fieldName, tt.jsonPath)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
				assert.Equal(t, tt.fieldName, err.Field, "Field name should match")
				assert.NotEmpty(t, err.Suggestion, "Suggestion should be non-empty")
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestTimeoutRange(t *testing.T) {
	tests := []struct {
		name      string
		timeout   int
		min       int
		max       int
		fieldName string
		jsonPath  string
		shouldErr bool
		errMsg    string
	}{
		{
			name:      "value equals minimum",
			timeout:   10,
			min:       10,
			max:       120,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: false,
		},
		{
			name:      "value equals maximum",
			timeout:   120,
			min:       10,
			max:       120,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: false,
		},
		{
			name:      "value within range",
			timeout:   60,
			min:       10,
			max:       120,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: false,
		},
		{
			name:      "value below minimum",
			timeout:   5,
			min:       10,
			max:       120,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: true,
			errMsg:    "toolTimeout must be between 10 and 120, got 5",
		},
		{
			name:      "value above maximum",
			timeout:   200,
			min:       10,
			max:       120,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: true,
			errMsg:    "toolTimeout must be between 10 and 120, got 200",
		},
		{
			name:      "negative value below minimum",
			timeout:   -1,
			min:       10,
			max:       120,
			fieldName: "toolTimeout",
			jsonPath:  "gateway.toolTimeout",
			shouldErr: true,
			errMsg:    "toolTimeout must be between 10 and 120, got -1",
		},
		{
			name:      "suggestion includes midpoint",
			timeout:   0,
			min:       10,
			max:       90,
			fieldName: "startupTimeout",
			jsonPath:  "gateway.startupTimeout",
			shouldErr: true,
			errMsg:    "startupTimeout must be between 10 and 90, got 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := timeoutRange(tt.timeout, tt.min, tt.max, tt.fieldName, tt.jsonPath)

			if tt.shouldErr {
				require.NotNil(t, err, "Expected validation error but got none")
				assert.Contains(t, err.Message, tt.errMsg, "Error message should contain expected text")
				assert.Equal(t, tt.jsonPath, err.JSONPath, "JSONPath should match")
				assert.Equal(t, tt.fieldName, err.Field, "Field name should match")
				assert.NotEmpty(t, err.Suggestion, "Suggestion should be non-empty")
				if tt.name == "suggestion includes midpoint" {
					expectedMidpoint := tt.min + (tt.max-tt.min)/2
					assert.Contains(t, err.Suggestion, fmt.Sprintf("%d", expectedMidpoint), "Suggestion should include the midpoint example value")
				}
			} else {
				require.NoError(t, validationErrAsError(err), "Unexpected validation error")
			}
		})
	}
}

func TestValidateOptionalInt(t *testing.T) {
	tests := []struct {
		name      string
		ptr       *int
		validateF func(int) *ValidationError
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "nil pointer returns nil without calling validateFn",
			ptr:       nil,
			validateF: func(int) *ValidationError { panic("should not be called") },
			wantErr:   false,
		},
		{
			name:      "non-nil valid value returns nil",
			ptr:       intPtr(80),
			validateF: func(v int) *ValidationError { return PortRange(v, "gateway.port") },
			wantErr:   false,
		},
		{
			name:      "non-nil invalid value returns error",
			ptr:       intPtr(0),
			validateF: func(v int) *ValidationError { return PortRange(v, "gateway.port") },
			wantErr:   true,
			errMsg:    "port must be between 1 and 65535",
		},
		{
			name: "closure can embed additional guard (zero means skip)",
			ptr:  intPtr(0),
			validateF: func(v int) *ValidationError {
				if v == 0 {
					return nil // sentinel: skip validation
				}
				return TimeoutMinimum(v, 10, "toolTimeout", "mcpServers.s.toolTimeout")
			},
			wantErr: false,
		},
		{
			name: "closure returns error when value is below minimum",
			ptr:  intPtr(5),
			validateF: func(v int) *ValidationError {
				return TimeoutMinimum(v, 10, "toolTimeout", "mcpServers.s.toolTimeout")
			},
			wantErr: true,
			errMsg:  "toolTimeout must be at least 10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOptionalInt(tt.ptr, "test log message", tt.validateF)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
