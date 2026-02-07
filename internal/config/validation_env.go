package config

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/config/rules"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logEnv = logger.New("config:validation_env")

// RequiredEnvVars lists the environment variables that must be set for the gateway to operate
var RequiredEnvVars = []string{
	"MCP_GATEWAY_PORT",
	"MCP_GATEWAY_DOMAIN",
	"MCP_GATEWAY_API_KEY",
}

// containerIDPattern validates that a container ID only contains valid characters (hex digits)
// Container IDs are 64 character hex strings, but short form (12 chars) is also valid
var containerIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

// EnvValidationResult holds the result of environment validation.
// It captures various aspects of the execution environment including
// containerization status, Docker accessibility, and validation errors/warnings.
//
// This type implements the error interface through its Error() method,
// which returns a formatted error message containing all validation failures.
// Use IsValid() to check if all critical validations passed before attempting
// to start the gateway.
//
// Fields:
//   - IsContainerized: Whether the gateway is running inside a Docker container
//   - ContainerID: The Docker container ID if containerized
//   - DockerAccessible: Whether the Docker daemon is accessible
//   - MissingEnvVars: List of required environment variables that are not set
//   - PortMapped: Whether the gateway port is mapped to the host (containerized mode)
//   - StdinInteractive: Whether stdin is interactive (containerized mode)
//   - LogDirMounted: Whether the log directory is mounted (containerized mode)
//   - ValidationErrors: Critical errors that prevent the gateway from starting
//   - ValidationWarnings: Non-critical issues that should be addressed
type EnvValidationResult struct {
	IsContainerized    bool
	ContainerID        string
	DockerAccessible   bool
	MissingEnvVars     []string
	PortMapped         bool
	StdinInteractive   bool
	LogDirMounted      bool
	ValidationErrors   []string
	ValidationWarnings []string
}

// IsValid returns true if all critical validations passed
func (r *EnvValidationResult) IsValid() bool {
	return len(r.ValidationErrors) == 0
}

// Error returns a combined error message for all validation errors
func (r *EnvValidationResult) Error() string {
	if r.IsValid() {
		return ""
	}
	return fmt.Sprintf("Environment validation failed:\n  - %s", strings.Join(r.ValidationErrors, "\n  - "))
}

// ValidateExecutionEnvironment performs comprehensive validation of the execution environment
// It checks Docker accessibility, required environment variables, and containerization status
func ValidateExecutionEnvironment() *EnvValidationResult {
	logEnv.Print("Starting execution environment validation")
	result := &EnvValidationResult{}

	// Check if running in a containerized environment
	result.IsContainerized, result.ContainerID = detectContainerized()
	logEnv.Printf("Containerization check: isContainerized=%v, containerID=%s", result.IsContainerized, result.ContainerID)

	// Check Docker daemon accessibility
	result.DockerAccessible = checkDockerAccessible()
	if !result.DockerAccessible {
		logEnv.Print("Docker daemon is not accessible")
		result.ValidationErrors = append(result.ValidationErrors,
			"Docker daemon is not accessible. Ensure the Docker socket is mounted or Docker is running.")
	}

	// Check required environment variables
	result.MissingEnvVars = checkRequiredEnvVars()
	if len(result.MissingEnvVars) > 0 {
		logEnv.Printf("Missing required environment variables: %v", result.MissingEnvVars)
		result.ValidationErrors = append(result.ValidationErrors,
			fmt.Sprintf("Required environment variables not set: %s", strings.Join(result.MissingEnvVars, ", ")))
	}

	logEnv.Printf("Validation complete: valid=%v, errors=%d, warnings=%d", result.IsValid(), len(result.ValidationErrors), len(result.ValidationWarnings))
	return result
}

// ValidateContainerizedEnvironment performs additional validation for containerized mode
// This is called by run_containerized.sh through the binary or by the Go code directly
func ValidateContainerizedEnvironment(containerID string) *EnvValidationResult {
	logEnv.Printf("Starting containerized environment validation: containerID=%s", containerID)
	result := ValidateExecutionEnvironment()
	result.IsContainerized = true
	result.ContainerID = containerID

	if containerID == "" {
		logEnv.Print("Container ID could not be determined")
		result.ValidationErrors = append(result.ValidationErrors,
			"Container ID could not be determined. Are you running in a Docker container?")
		return result
	}

	// Validate port mapping
	port := os.Getenv("MCP_GATEWAY_PORT")
	if port != "" {
		logEnv.Printf("Checking port mapping: port=%s", port)
		portMapped, err := checkPortMapping(containerID, port)
		if err != nil {
			result.ValidationWarnings = append(result.ValidationWarnings,
				fmt.Sprintf("Could not verify port mapping: %v", err))
		} else if !portMapped {
			result.ValidationErrors = append(result.ValidationErrors,
				fmt.Sprintf("MCP_GATEWAY_PORT (%s) is not mapped to a host port. Use: -p <host_port>:%s", port, port))
		}
		result.PortMapped = portMapped
		logEnv.Printf("Port mapping result: mapped=%v", portMapped)
	}

	// Check if stdin is interactive (requires -i flag)
	result.StdinInteractive = checkStdinInteractive(containerID)
	logEnv.Printf("Stdin interactive check: interactive=%v", result.StdinInteractive)
	if !result.StdinInteractive {
		result.ValidationErrors = append(result.ValidationErrors,
			"Container was not started with -i flag. Stdin is required for configuration input.")
	}

	// Check if log directory is mounted (warning only)
	logDir := os.Getenv("MCP_GATEWAY_LOG_DIR")
	if logDir == "" {
		logDir = "/tmp/gh-aw/mcp-logs"
	}
	result.LogDirMounted = checkLogDirMounted(containerID, logDir)
	logEnv.Printf("Log directory mount check: mounted=%v, logDir=%s", result.LogDirMounted, logDir)
	if !result.LogDirMounted {
		result.ValidationWarnings = append(result.ValidationWarnings,
			fmt.Sprintf("Log directory %s is not mounted. Logs will not persist outside the container. Use: -v /path/on/host:%s", logDir, logDir))
	}

	logEnv.Printf("Containerized validation complete: valid=%v, errors=%d, warnings=%d", result.IsValid(), len(result.ValidationErrors), len(result.ValidationWarnings))
	return result
}

// detectContainerized checks if we're running inside a Docker container
// It examines /proc/self/cgroup to detect container environment and extract container ID
func detectContainerized() (bool, string) {
	file, err := os.Open("/proc/self/cgroup")
	if err != nil {
		// If we can't read cgroup, we're likely not in a container
		return false, ""
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Docker containers have docker paths in cgroup
		if strings.Contains(line, "docker") || strings.Contains(line, "containerd") {
			// Extract container ID from the path
			// Format is typically: 0::/docker/<container_id>
			parts := strings.Split(line, "/")
			for i, part := range parts {
				if (part == "docker" || part == "containerd") && i+1 < len(parts) {
					containerID := parts[i+1]
					// Container IDs are 64 hex characters (or 12 for short form)
					if len(containerID) >= 12 {
						return true, containerID
					}
				}
			}
			// Found docker/containerd reference but couldn't extract ID
			return true, ""
		}
	}

	// Also check for .dockerenv file
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true, ""
	}

	return false, ""
}

// checkDockerAccessible verifies that the Docker daemon is accessible
func checkDockerAccessible() bool {
	// First check if the Docker socket exists
	socketPath := os.Getenv("DOCKER_HOST")
	if socketPath == "" {
		socketPath = "/var/run/docker.sock"
	} else {
		// Parse unix:// prefix if present
		socketPath = strings.TrimPrefix(socketPath, "unix://")
	}
	logEnv.Printf("Checking Docker socket accessibility: socketPath=%s", socketPath)

	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		logEnv.Printf("Docker socket not found: socketPath=%s", socketPath)
		return false
	}

	// Try to run docker info to verify connectivity
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	accessible := cmd.Run() == nil
	logEnv.Printf("Docker daemon check: accessible=%v", accessible)
	return accessible
}

// checkRequiredEnvVars checks if all required environment variables are set
func checkRequiredEnvVars() []string {
	var missing []string
	for _, envVar := range RequiredEnvVars {
		if os.Getenv(envVar) == "" {
			missing = append(missing, envVar)
		}
	}
	return missing
}

// validateContainerID validates that the container ID is safe to use in commands
// Container IDs should only contain lowercase hex characters (a-f, 0-9)
func validateContainerID(containerID string) error {
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
// NOTE: This function is intentionally placed in the validation package despite being a Docker
// operation helper. It is tightly coupled to the containerized validation checks in this file
// and is only used within validation_env.go (5 call sites). Moving it to a separate package
// would increase complexity without providing clear benefits. If additional Docker helper
// functions emerge in the future (3+ helpers), consider creating an internal/docker/ package.
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
	if err := validateContainerID(containerID); err != nil {
		return "", err
	}

	cmd := exec.Command("docker", "inspect", "--format", formatTemplate, containerID)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect failed: %w", err)
	}

	return strings.TrimSpace(string(output)), nil
}

// checkPortMapping uses docker inspect to verify that the specified port is mapped
func checkPortMapping(containerID, port string) (bool, error) {
	output, err := runDockerInspect(containerID, "{{json .NetworkSettings.Ports}}")
	if err != nil {
		return false, err
	}

	// Parse the port from the output
	portKey := fmt.Sprintf("%s/tcp", port)

	// Check if the port is in the output with a host binding
	// The format is like: {"8000/tcp":[{"HostIp":"0.0.0.0","HostPort":"8000"}]}
	return strings.Contains(output, portKey) && strings.Contains(output, "HostPort"), nil
}

// checkStdinInteractive uses docker inspect to verify the container was started with -i flag
func checkStdinInteractive(containerID string) bool {
	output, err := runDockerInspect(containerID, "{{.Config.OpenStdin}}")
	if err != nil {
		return false
	}

	return output == "true"
}

// checkLogDirMounted uses docker inspect to verify the log directory is mounted
func checkLogDirMounted(containerID, logDir string) bool {
	output, err := runDockerInspect(containerID, "{{json .Mounts}}")
	if err != nil {
		return false
	}

	// Check if the log directory is in the mounts
	return strings.Contains(output, logDir)
}

// GetGatewayPortFromEnv returns the MCP_GATEWAY_PORT value, parsed as int
func GetGatewayPortFromEnv() (int, error) {
	portStr := os.Getenv("MCP_GATEWAY_PORT")
	if portStr == "" {
		return 0, fmt.Errorf("MCP_GATEWAY_PORT environment variable not set")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid MCP_GATEWAY_PORT value: %s", portStr)
	}

	if validationErr := rules.PortRange(port, "MCP_GATEWAY_PORT"); validationErr != nil {
		return 0, fmt.Errorf("%s", validationErr.Message)
	}

	return port, nil
}

// GetGatewayDomainFromEnv returns the MCP_GATEWAY_DOMAIN value
func GetGatewayDomainFromEnv() string {
	return os.Getenv("MCP_GATEWAY_DOMAIN")
}

// GetGatewayAPIKeyFromEnv returns the MCP_GATEWAY_API_KEY value
func GetGatewayAPIKeyFromEnv() string {
	return os.Getenv("MCP_GATEWAY_API_KEY")
}
