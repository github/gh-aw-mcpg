package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/sys"
)

var logEnv = logger.New("config:validation_env")

// RequiredEnvVars lists the environment variables that must be set for the gateway to operate
var RequiredEnvVars = []string{
	"MCP_GATEWAY_PORT",
	"MCP_GATEWAY_DOMAIN",
	"MCP_GATEWAY_API_KEY",
}

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
	result.IsContainerized, result.ContainerID = sys.DetectContainerID()
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
		logDir = DefaultLogDir
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
