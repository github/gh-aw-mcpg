package sys

import (
	"bufio"
	"os"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logSys = logger.New("sys:container")

// containerIndicators lists the cgroup path substrings that indicate a container environment.
var containerIndicators = []string{"docker", "containerd", "kubepods", "lxc"}

// IsRunningInContainer detects if the current process is running inside a container.
func IsRunningInContainer() bool {
	detected, _ := DetectContainerID()
	return detected
}

// DetectContainerID detects if the current process is running inside a container
// and attempts to extract the container ID from cgroup entries.
// It returns (isContainer, containerID). The containerID may be empty even when
// a container is detected (e.g., via /.dockerenv or environment variable).
func DetectContainerID() (bool, string) {
	logSys.Print("Detecting container environment")

	// Method 1: Check for /.dockerenv file (Docker-specific)
	if _, err := os.Stat("/.dockerenv"); err == nil {
		logSys.Print("Container detected via /.dockerenv")
		// Still try to extract container ID from cgroup
		if id := extractContainerIDFromCgroup(); id != "" {
			return true, id
		}
		return true, ""
	}

	// Method 2: Check cgroup files for container indicators.
	// Try /proc/1/cgroup first, then fall back to /proc/self/cgroup for setups
	// where PID 1 doesn't reflect the current process's cgroup (e.g., host PID namespace).
	for _, cgroupPath := range []string{"/proc/1/cgroup", "/proc/self/cgroup"} {
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
	if os.Getenv("RUNNING_IN_CONTAINER") == "true" {
		logSys.Print("Container detected via RUNNING_IN_CONTAINER env var")
		return true, ""
	}

	logSys.Print("No container indicators found, running on host")
	return false, ""
}

// extractContainerIDFromCgroup reads cgroup files and tries to extract a container ID.
// It checks /proc/1/cgroup first, then falls back to /proc/self/cgroup.
func extractContainerIDFromCgroup() string {
	for _, cgroupPath := range []string{"/proc/1/cgroup", "/proc/self/cgroup"} {
		data, err := os.ReadFile(cgroupPath)
		if err != nil {
			continue
		}
		if id := extractContainerIDFromContent(string(data)); id != "" {
			return id
		}
	}
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
