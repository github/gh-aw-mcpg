package sys

import (
	"bufio"
	"os"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logSys = logger.ForFile()

// containerIndicators lists the cgroup path substrings that indicate a container environment.
var containerIndicators = []string{"docker", "containerd", "kubepods", "lxc"}

// defaultCgroupPaths are the cgroup files checked for container indicators, in order.
var defaultCgroupPaths = []string{"/proc/1/cgroup", "/proc/self/cgroup"}

// dockerEnvPath is the Docker-specific sentinel file checked by Method 1.
const dockerEnvPath = "/.dockerenv"

// IsRunningInContainer detects if the current process is running inside a container.
func IsRunningInContainer() bool {
	detected, _ := DetectContainerID()
	return detected
}

// DetectContainerID detects if the current process is running inside a container
// and attempts to extract the container ID from cgroup entries.
// It returns (isContainer, containerID). The containerID may be empty even when
// a container is detected (e.g., via dockerEnvPath or environment variable).
func DetectContainerID() (bool, string) {
	return detectContainerIDWithPaths(dockerEnvPath, defaultCgroupPaths)
}

// detectContainerIDWithPaths is the testable implementation of DetectContainerID.
// dockerEnv is the path of the Docker sentinel file; cgroupPaths is the ordered
// list of cgroup files to probe for container indicators.
func detectContainerIDWithPaths(dockerEnv string, cgroupPaths []string) (bool, string) {
	logSys.Print("Detecting container environment")

	// Method 1: Check for Docker sentinel file.
	if _, err := os.Stat(dockerEnv); err == nil {
		logSys.Printf("Container detected via %s", dockerEnv)
		// Still try to extract container ID from cgroup
		if id := extractContainerIDFromCgroupFiles(cgroupPaths); id != "" {
			return true, id
		}
		return true, ""
	}

	// Method 2: Check cgroup files for container indicators.
	// Try /proc/1/cgroup first, then fall back to /proc/self/cgroup for setups
	// where PID 1 doesn't reflect the current process's cgroup (e.g., host PID namespace).
	for _, cgroupPath := range cgroupPaths {
		data, err := os.ReadFile(cgroupPath)
		if err != nil {
			continue
		}
		content := string(data)
		for _, indicator := range containerIndicators {
			if strings.Contains(content, indicator) {
				logSys.Printf("Container detected via %s", cgroupPath)
				if id := extractContainerIDFromContent(content); id != "" {
					return true, id
				}
				return true, ""
			}
		}
	}

	// Method 3: Check environment variable (set by Dockerfile)
	if envutil.GetEnvString("RUNNING_IN_CONTAINER", "") == "true" {
		logSys.Print("Container detected via RUNNING_IN_CONTAINER env var")
		return true, ""
	}

	logSys.Print("No container indicators found, running on host")
	return false, ""
}

// extractContainerIDFromCgroup reads cgroup files and tries to extract a container ID.
// It checks /proc/1/cgroup first, then falls back to /proc/self/cgroup.
func extractContainerIDFromCgroup() string {
	return extractContainerIDFromCgroupFiles(defaultCgroupPaths)
}

// extractContainerIDFromCgroupFiles reads the given cgroup files in order and
// returns the first container ID found, or "" if none is extractable.
func extractContainerIDFromCgroupFiles(paths []string) string {
	logSys.Printf("Scanning %d cgroup file(s) for container ID", len(paths))
	for _, cgroupPath := range paths {
		data, err := os.ReadFile(cgroupPath)
		if err != nil {
			logSys.Printf("Skipping cgroup file (unreadable): path=%s, err=%v", cgroupPath, err)
			continue
		}
		if id := extractContainerIDFromContent(string(data)); id != "" {
			logSys.Printf("Extracted container ID from cgroup file: path=%s, id=%s", cgroupPath, id)
			return id
		}
		logSys.Printf("No container ID found in cgroup file: path=%s", cgroupPath)
	}
	logSys.Print("No container ID found in any cgroup file")
	return ""
}

// extractContainerIDFromContent parses cgroup content line-by-line and extracts a container ID.
// It looks for path segments following "docker" or "containerd" that are at least 12 characters long.
func extractContainerIDFromContent(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "docker") || strings.Contains(line, "containerd") {
			parts := strings.Split(line, "/")
			for i, part := range parts {
				if (part == "docker" || part == "containerd") && i+1 < len(parts) {
					containerID := parts[i+1]
					if len(containerID) >= 12 {
						return containerID
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		logSys.Printf("Error scanning cgroup content: %v", err)
	}
	return ""
}
