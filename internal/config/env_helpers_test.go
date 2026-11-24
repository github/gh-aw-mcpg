package config

import (
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/envutil"
)

func ValidateExecutionEnvironment() *EnvValidationResult {
	return ValidateExecutionEnvironmentForRuntime(DefaultContainerRuntime)
}

func ValidateContainerizedEnvironment(containerID string) *EnvValidationResult {
	return ValidateContainerizedEnvironmentForRuntime(containerID, DefaultContainerRuntime)
}

func GetGatewayPortFromEnv() (int, error) {
	port, ok, err := parseAndValidateIntEnv("MCP_GATEWAY_PORT", func(port int) *ValidationError {
		return PortRange(port, "MCP_GATEWAY_PORT")
	})
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("MCP_GATEWAY_PORT environment variable not set")
	}
	return port, nil
}

func GetGatewayDomainFromEnv() string {
	domain := envutil.GetEnvString("MCP_GATEWAY_DOMAIN", "")
	if domain != "" {
		logConfig.Printf("MCP_GATEWAY_DOMAIN=%q", domain)
	}
	return domain
}
