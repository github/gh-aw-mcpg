package config

import (
	"fmt"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/util"
)

const (
	containerRuntimeDocker = "docker"
	containerRuntimePodman = "podman"
)

func isNonEmptyWhitespace(s string) bool {
	return strings.TrimSpace(s) == "" && s != ""
}

func normalizeContainerRuntime(runtime string) string {
	return util.NormalizeStringCI(runtime)
}

func runtimeCommandForName(runtime string) string {
	if normalizeContainerRuntime(runtime) == containerRuntimePodman {
		return containerRuntimePodman
	}
	return containerRuntimeDocker
}

func validateContainerRuntimeValue(value, fieldName string) error {
	if isNonEmptyWhitespace(value) {
		return fmt.Errorf("%s must not be empty or whitespace-only when set", fieldName)
	}
	normalized := normalizeContainerRuntime(value)
	switch normalized {
	case "", containerRuntimeDocker, containerRuntimePodman:
		return nil
	default:
		return fmt.Errorf("%s must be one of: docker, podman (got: %q)", fieldName, value)
	}
}

func configuredContainerRuntimeName(configRuntime string) string {
	runtime := normalizeContainerRuntime(configRuntime)
	if runtime == "" {
		runtime = DefaultContainerRuntime
	}
	return runtime
}

func effectiveContainerRuntimeName(configRuntime string) string {
	runtime := configuredContainerRuntimeName(configRuntime)

	// Env override takes precedence over config.
	if envRuntimeRaw := envutil.GetEnvString("MCP_GATEWAY_CONTAINER_RUNTIME", ""); envRuntimeRaw != "" {
		if isNonEmptyWhitespace(envRuntimeRaw) {
			logConfig.Printf("Ignoring whitespace-only MCP_GATEWAY_CONTAINER_RUNTIME value")
		} else if err := validateContainerRuntimeValue(envRuntimeRaw, "MCP_GATEWAY_CONTAINER_RUNTIME"); err != nil {
			logConfig.Printf("Ignoring invalid MCP_GATEWAY_CONTAINER_RUNTIME=%q: %v", envRuntimeRaw, err)
		} else {
			runtime = normalizeContainerRuntime(envRuntimeRaw)
			logConfig.Printf("Container runtime overridden by MCP_GATEWAY_CONTAINER_RUNTIME: %q", runtime)
		}
	}
	logConfig.Printf("Effective container runtime name: %q (config=%q)", runtime, configRuntime)
	return runtime
}

// trimmedContainerRuntimeCommand returns strings.TrimSpace of gateway.ContainerRuntimeCommand,
// or "" when gateway is nil.
func trimmedContainerRuntimeCommand(gateway *GatewayConfig) string {
	if gateway == nil {
		return ""
	}
	return strings.TrimSpace(gateway.ContainerRuntimeCommand)
}

func effectiveContainerRuntimeCommand(gateway *GatewayConfig) string {
	if gateway != nil && gateway.Dockerless {
		if cmd := trimmedContainerRuntimeCommand(gateway); cmd != "" {
			return cmd
		}
		return containerRuntimePodman
	}

	runtime := DefaultContainerRuntime
	if gateway != nil {
		runtime = gateway.ContainerRuntime
	}
	command := runtimeCommandForName(effectiveContainerRuntimeName(runtime))
	if cmd := trimmedContainerRuntimeCommand(gateway); cmd != "" {
		logConfig.Printf("Container runtime command overridden by gateway config: %q", cmd)
		command = cmd
	}
	logConfig.Printf("Effective container runtime command: %q", command)
	return command
}

func configuredContainerRuntimeCommand(gateway *GatewayConfig) string {
	command := runtimeCommandForName(DefaultContainerRuntime)
	if gateway != nil {
		command = runtimeCommandForName(configuredContainerRuntimeName(gateway.ContainerRuntime))
	}
	if cmd := trimmedContainerRuntimeCommand(gateway); cmd != "" {
		command = cmd
	}
	logConfig.Printf("Configured container runtime command: %q", command)
	return command
}

// EffectiveContainerRuntimeCommand returns the runtime executable used for container launches.
func EffectiveContainerRuntimeCommand(gateway *GatewayConfig) string {
	return effectiveContainerRuntimeCommand(gateway)
}
