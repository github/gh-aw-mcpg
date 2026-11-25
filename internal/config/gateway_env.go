package config

// gateway_env.go — gateway-specific environment variable helpers.
//
// This file intentionally layers on top of internal/envutil rather than
// calling os.Getenv directly. The layering is deliberate:
//
//   - internal/envutil provides generic, typed environment-variable accessors
//     (GetEnvString, GetEnvIntRaw, …) with no knowledge of MCP Gateway semantics.
//
//   - This file adds gateway-specific validation rules (port ranges, timeout
//     bounds, feature-flag defaults) on top of those primitives, keeping the
//     higher-level policy separate from the lower-level reading mechanism.
//
// Keep this file focused on shared accessor/validation helpers used by
// gateway config loading. Feature-specific policy may live in its own package
// (for example, guard-policy parsing and command flag wiring).

import (
	"errors"
	"fmt"
	"time"

	"github.com/github/gh-aw-mcpg/internal/envutil"
)

// ToolTimeoutMin is the minimum allowed value for toolTimeout (seconds).
const ToolTimeoutMin = 10

// EnvLogDir selects the gateway log directory.
const EnvLogDir = "MCP_GATEWAY_LOG_DIR"

func parseAndValidateIntEnv(envKey string, validate func(int) *ValidationError) (int, bool, error) {
	value, present, err := envutil.GetEnvIntRaw(envKey)
	if !present {
		logConfig.Printf("%s not set in environment", envKey)
		return 0, false, nil
	}

	if err != nil {
		logConfig.Printf("%s is not a valid integer: %v", envKey, err)
		return 0, false, fmt.Errorf("invalid %s value", envKey)
	}

	if validationErr := validate(value); validationErr != nil {
		logConfig.Printf("%s=%d failed validation: %s", envKey, value, validationErr.Message)
		return 0, false, errors.New(validationErr.Message)
	}

	logConfig.Printf("%s resolved to %d", envKey, value)
	return value, true, nil
}

// GetGatewayAgentIDFromEnv returns the gateway agent identifier from environment.
// New name MCP_GATEWAY_AGENT_ID takes precedence over deprecated MCP_GATEWAY_API_KEY.
func GetGatewayAgentIDFromEnv() string {
	agentID := envutil.GetEnvString("MCP_GATEWAY_AGENT_ID", "")
	legacy := envutil.GetEnvString("MCP_GATEWAY_API_KEY", "")

	if agentID != "" {
		if legacy != "" {
			logConfig.Print("DEPRECATION: MCP_GATEWAY_API_KEY is set but ignored because MCP_GATEWAY_AGENT_ID is present")
		}
		logConfig.Print("MCP_GATEWAY_AGENT_ID found in environment")
		return agentID
	}

	if legacy != "" {
		logConfig.Print("DEPRECATION: MCP_GATEWAY_API_KEY is deprecated; use MCP_GATEWAY_AGENT_ID")
		return legacy
	}

	logConfig.Print("MCP_GATEWAY_AGENT_ID not set in environment")
	return ""
}

// GetGatewayToolTimeoutFromEnv returns the MCP_GATEWAY_TOOL_TIMEOUT value, parsed as int.
// Returns (0, false) when the environment variable is not set or empty.
// Returns an error when the variable is set but invalid (non-integer or below minimum of 10).
func GetGatewayToolTimeoutFromEnv() (int, bool, error) {
	return parseAndValidateIntEnv("MCP_GATEWAY_TOOL_TIMEOUT", func(timeout int) *ValidationError {
		return TimeoutMinimum(timeout, ToolTimeoutMin, "MCP_GATEWAY_TOOL_TIMEOUT", "MCP_GATEWAY_TOOL_TIMEOUT")
	})
}

// GetGatewaySessionTimeoutFromEnv returns MCP_GATEWAY_SESSION_TIMEOUT as a duration.
// Defaults to 6 hours when the variable is unset or invalid.
func GetGatewaySessionTimeoutFromEnv() time.Duration {
	return envutil.GetEnvDuration("MCP_GATEWAY_SESSION_TIMEOUT", 6*time.Hour)
}

// toolTimeoutEnvOrDefault returns the value of MCP_GATEWAY_TOOL_TIMEOUT when set and valid,
// otherwise DefaultToolTimeout. Invalid env var values are logged and ignored (fallback to default).
func toolTimeoutEnvOrDefault() int {
	timeout, ok, err := GetGatewayToolTimeoutFromEnv()
	if err != nil {
		logConfig.Printf("MCP_GATEWAY_TOOL_TIMEOUT is invalid, falling back to default %d: %v", DefaultToolTimeout, err)
		return DefaultToolTimeout
	}
	if !ok {
		return DefaultToolTimeout
	}
	return timeout
}
