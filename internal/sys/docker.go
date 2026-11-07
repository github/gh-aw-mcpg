package sys

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logDocker = logger.ForFile()

// containerIDPattern validates that a container ID only contains valid characters (hex digits).
// Container IDs are 64 character hex strings, but short form (12 chars) is also valid.
var containerIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// CheckDockerAccessible verifies that the Docker daemon is accessible.
func CheckDockerAccessible() bool {
	// First check if the Docker socket exists
	socketPath := os.Getenv("DOCKER_HOST")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	} else {
		// Parse unix:// prefix if present
		socketPath = strings.TrimPrefix(socketPath, "unix://")
	}
	logDocker.Printf("Checking Docker socket accessibility: socketPath=%s", socketPath)

	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		logDocker.Printf("Docker socket not found: socketPath=%s", socketPath)
		return false
	}

	// Try to run docker info to verify connectivity
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	accessible := cmd.Run() == nil
	logDocker.Printf("Docker daemon check: accessible=%v", accessible)
	return accessible
}

// CheckContainerRuntimeAccessible verifies that the selected container runtime can be used.
func CheckContainerRuntimeAccessible(command string) bool {
	if filepath.Base(command) == "docker" {
		return CheckDockerAccessible()
	}

	logDocker.Printf("Checking container runtime accessibility: command=%s", command)
	cmd := exec.Command(command, "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	accessible := cmd.Run() == nil
	logDocker.Printf("Container runtime check: command=%s, accessible=%v", command, accessible)
	return accessible
}

// ValidateContainerID validates that the container ID is safe to use in commands.
// Container IDs should only contain lowercase hex characters (a-f, 0-9).
func ValidateContainerID(containerID string) error {
	if containerID == "" {
		return fmt.Errorf("container ID is empty")
	}
	if !containerIDPattern.MatchString(containerID) {
		return fmt.Errorf("container ID contains invalid characters: must be 12-64 hex characters")
	}
	return nil
}

// runDockerInspect is a helper function that executes docker inspect with a given format template.
// It validates the container ID before running the command and returns the output as a string.
//
// Security Note: This is an internal helper function that should only be called with
// hardcoded format templates defined within this package. The formatTemplate parameter
// is not validated as it is never exposed to user input.
//
// Parameters:
//   - containerID: The Docker container ID to inspect (validated before use)
//   - formatTemplate: The Go template format string for docker inspect (e.g., "{{.Config.OpenStdin}}")
//
// Returns:
//   - output: The trimmed output from docker inspect
//   - error: Any validation or command execution error
func runDockerInspect(containerID, formatTemplate string) (string, error) {
	if err := ValidateContainerID(containerID); err != nil {
		logDocker.Printf("Rejected docker inspect: invalid container ID: %v", err)
		return "", err
	}

	logDocker.Printf("Running docker inspect: containerID=%s, format=%s", containerID, formatTemplate)

	cmd := exec.Command("docker", "inspect", "--format", formatTemplate, containerID)
	output, err := cmd.Output()
	if err != nil {
		logDocker.Printf("Docker inspect failed: containerID=%s, format=%s, err=%v", containerID, formatTemplate, err)
		return "", fmt.Errorf("docker inspect failed: %w", err)
	}

	result := strings.TrimSpace(string(output))
	logDocker.Printf("Docker inspect succeeded: output_len=%d", len(result))
	return result, nil
}

// IsHostNetworkMode uses docker inspect to determine whether the container was
// started with `--network host`. Host-networked containers do not have published
// port mappings in NetworkSettings.Ports (Docker discards them), so port-mapping
// checks must be skipped for them.
func IsHostNetworkMode(containerID string) (bool, error) {
	output, err := runDockerInspect(containerID, "{{.HostConfig.NetworkMode}}")
	if err != nil {
		return false, err
	}

	hostNetwork := output == "host"
	logDocker.Printf("Network mode check: containerID=%s, networkMode=%s, host=%v", containerID, output, hostNetwork)
	return hostNetwork, nil
}

// CheckPortMapping uses docker inspect to verify that the specified port is mapped.
func CheckPortMapping(containerID, port string) (bool, error) {
	logDocker.Printf("Checking port mapping: containerID=%s, port=%s", containerID, port)

	output, err := runDockerInspect(containerID, "{{json .NetworkSettings.Ports}}")
	if err != nil {
		return false, err
	}

	// Parse the port from the output
	portKey := fmt.Sprintf("%s/tcp", port)

	// Check if the port is in the output with a host binding
	// The format is like: {"8000/tcp":[{"HostIp":"0.0.0.0","HostPort":"8000"}]}
	mapped := strings.Contains(output, portKey) && strings.Contains(output, "HostPort")
	logDocker.Printf("Port mapping check result: port=%s, mapped=%v", portKey, mapped)
	return mapped, nil
}

// IsHostNetwork reports whether the container uses the host network.
func IsHostNetwork(containerID string) (bool, error) {
	logDocker.Printf("Checking network mode: containerID=%s", containerID)

	output, err := runDockerInspect(containerID, "{{.HostConfig.NetworkMode}}")
	if err != nil {
		return false, err
	}

	hostNetwork := strings.EqualFold(output, "host")
	logDocker.Printf("Network mode check result: hostNetwork=%v", hostNetwork)
	return hostNetwork, nil
}

// CheckStdinInteractive uses docker inspect to verify the container was started with -i flag.
func CheckStdinInteractive(containerID string) bool {
	output, err := runDockerInspect(containerID, "{{.Config.OpenStdin}}")
	if err != nil {
		logDocker.Printf("Stdin interactive check failed, assuming not interactive: containerID=%s", containerID)
		return false
	}

	interactive := output == "true"
	logDocker.Printf("Stdin interactive check: containerID=%s, interactive=%v", containerID, interactive)
	return interactive
}

// CheckLogDirMounted uses docker inspect to verify the log directory is mounted.
func CheckLogDirMounted(containerID, logDir string) bool {
	output, err := runDockerInspect(containerID, "{{json .Mounts}}")
	if err != nil {
		logDocker.Printf("Log dir mount check failed, assuming not mounted: containerID=%s, logDir=%s", containerID, logDir)
		return false
	}

	// Check if the log directory is in the mounts
	mounted := strings.Contains(output, logDir)
	logDocker.Printf("Log dir mount check: containerID=%s, logDir=%s, mounted=%v", containerID, logDir, mounted)
	return mounted
}
