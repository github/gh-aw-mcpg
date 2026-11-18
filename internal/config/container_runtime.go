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

// containerRuntimeArgs returns the portion of a Docker-compatible command
// argument list preceding the image. It understands the runtime options used
// by gateway configuration; callers retain the original arguments if an image
// boundary cannot be derived.
func containerRuntimeArgs(args []string) ([]string, bool) {
	runIndex := -1
	for i, arg := range args {
		if strings.EqualFold(arg, "run") {
			runIndex = i
			break
		}
	}
	if runIndex == -1 {
		logConfig.Printf("containerRuntimeArgs: no %q token found in %d args, cannot derive image boundary", "run", len(args))
		return nil, false
	}

	for i := runIndex + 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append([]string(nil), args[:i+1]...), i+1 < len(args)
		}
		if strings.HasPrefix(arg, "--") {
			if !strings.Contains(arg, "=") && containerRunOptionTakesValue[arg] {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if containerRunOptionTakesValue[arg] {
				i++
			}
			continue
		}
		return append([]string(nil), args[:i]...), true
	}
	logConfig.Printf("containerRuntimeArgs: reached end of args after %q at index %d without finding the image token", "run", runIndex)
	return nil, false
}

var containerRunOptionTakesValue = map[string]bool{
	"--add-host":           true,
	"--annotation":         true,
	"--attach":             true,
	"--blkio-weight":       true,
	"--cap-add":            true,
	"--cap-drop":           true,
	"--cgroup-parent":      true,
	"--cgroupns":           true,
	"--cidfile":            true,
	"--cpu-period":         true,
	"--cpu-quota":          true,
	"--cpu-shares":         true,
	"--cpus":               true,
	"--cpuset-cpus":        true,
	"--cpuset-mems":        true,
	"--detach-keys":        true,
	"--device":             true,
	"--dns":                true,
	"--dns-option":         true,
	"--dns-search":         true,
	"--entrypoint":         true,
	"--env":                true,
	"--env-file":           true,
	"--expose":             true,
	"--gpus":               true,
	"--group-add":          true,
	"--health-cmd":         true,
	"--hostname":           true,
	"--ipc":                true,
	"--label":              true,
	"--label-file":         true,
	"--log-driver":         true,
	"--log-opt":            true,
	"--mac-address":        true,
	"--memory":             true,
	"--memory-reservation": true,
	"--memory-swap":        true,
	"--mount":              true,
	"--name":               true,
	"--network":            true,
	"--oom-score-adj":      true,
	"--pid":                true,
	"--pids-limit":         true,
	"--platform":           true,
	"--publish":            true,
	"--restart":            true,
	"--rootfs":             true,
	"--runtime":            true,
	"--security-opt":       true,
	"--shm-size":           true,
	"--stop-signal":        true,
	"--stop-timeout":       true,
	"--sysctl":             true,
	"--tmpfs":              true,
	"--ulimit":             true,
	"--user":               true,
	"--userns":             true,
	"--uts":                true,
	"--volume":             true,
	"--volumes-from":       true,
	"--workdir":            true,
	"-a":                   true,
	"-e":                   true,
	"-h":                   true,
	"-l":                   true,
	"-p":                   true,
	"-u":                   true,
	"-v":                   true,
	"-w":                   true,
}
