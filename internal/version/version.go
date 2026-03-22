// Package version manages the MCP gateway version string.
// The version is set once at application startup and read across multiple packages
// for error reporting, health checks, and MCP client implementation info.
package version

// gatewayVersion stores the gateway version string, used across multiple packages
// for error reporting, health checks, and MCP client implementation info.
// It defaults to "0.0.0-dev" (a valid semantic version pre-release identifier) and
// should be set once at startup.
//
// Thread-safety note: This variable is written once at application startup
// (in SetVersion) before any concurrent access, and read-only thereafter.
// No mutex is needed as the write happens before any goroutines are spawned.
var gatewayVersion = "0.0.0-dev"

// Set updates the gateway version string if the provided version is non-empty.
// This should be called once at application startup from main.
func Set(v string) {
	if v != "" {
		gatewayVersion = v
	}
}

// Get returns the current gateway version string.
func Get() string {
	return gatewayVersion
}
