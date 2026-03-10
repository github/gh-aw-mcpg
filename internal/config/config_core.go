// Package config provides configuration loading and parsing.
//
// # TOML Configuration Parsing
//
// This package uses BurntSushi/toml v1.6.0+ for robust TOML parsing with:
//   - TOML 1.1 specification support (default in v1.6.0+)
//   - Column-level error reporting (Position.Line, Position.Col)
//   - Duplicate key detection (improved in v1.6.0)
//   - Metadata tracking for unknown field detection
//
// # Design Patterns
//
// Streaming Decoder: Uses toml.NewDecoder() for memory efficiency with large configs
// Error Reporting: Extracts line/column from ParseError for precise error messages
// Unknown Fields: Uses MetaData.Undecoded() for typo warnings (not hard errors)
// Validation: Multi-layer approach (parse → schema → field-level → variable expansion)
//
// # TOML 1.1 Features Used
//
//   - Multi-line inline arrays: newlines allowed in array definitions
//   - Improved duplicate detection: duplicate keys now properly reported as errors
//   - Large float encoding: proper round-trip with exponent syntax
//
// This file defines the core configuration types that are stable and rarely change.
package config

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

// Core constants for configuration defaults
const (
	DefaultPort           = 3000
	DefaultStartupTimeout = 60  // seconds
	DefaultToolTimeout    = 120 // seconds
)

// Config represents the internal gateway configuration.
// Feature-specific fields are added in their respective config_*.go files.
type Config struct {
	// Servers maps server names to their configurations
	Servers map[string]*ServerConfig `toml:"servers" json:"servers"`

	// Guards maps guard names to their configurations
	Guards map[string]*GuardConfig `toml:"guards" json:"guards,omitempty"`

	// Gateway holds global gateway settings
	Gateway *GatewayConfig `toml:"gateway" json:"gateway,omitempty"`

	// DIFCMode specifies the guards enforcement mode: strict (default), filter, or propagate
	// strict: deny access that violates guards rules
	// filter: silently remove tools/resources that violate guards rules
	// propagate: auto-adjust agent labels on reads to allow access
	DIFCMode string `toml:"guards_mode" json:"guards_mode,omitempty"`

	// SequentialLaunch launches servers sequentially instead of in parallel
	SequentialLaunch bool `toml:"sequential_launch" json:"sequential_launch,omitempty"`

	// GuardPolicy optionally overrides per-guard policy via CLI/environment precedence.
	GuardPolicy *GuardPolicy `toml:"-" json:"-"`

	// GuardPolicySource describes where GuardPolicy was resolved from (cli|env|config|legacy).
	GuardPolicySource string `toml:"-" json:"-"`
}

// GatewayConfig holds global gateway settings.
// Feature-specific fields are added in their respective config_*.go files.
type GatewayConfig struct {
	// Port is the HTTP port to listen on
	Port int `toml:"port" json:"port,omitempty"`

	// APIKey is the authentication key for the gateway
	APIKey string `toml:"api_key" json:"api_key,omitempty"`

	// Domain is the gateway domain for external access
	Domain string `toml:"domain" json:"domain,omitempty"`

	// StartupTimeout is the maximum time (seconds) to wait for server startup
	StartupTimeout int `toml:"startup_timeout" json:"startup_timeout,omitempty"`

	// ToolTimeout is the maximum time (seconds) to wait for tool execution
	ToolTimeout int `toml:"tool_timeout" json:"tool_timeout,omitempty"`

	// PayloadDir is the directory for storing large payloads
	PayloadDir string `toml:"payload_dir" json:"payload_dir,omitempty"`

	// PayloadPathPrefix is the path prefix to use when returning payloadPath to clients.
	// This allows remapping the host filesystem path to a path accessible in the client/agent container.
	// If empty, the actual filesystem path (PayloadDir) is returned.
	// Example: If PayloadDir="/tmp/jq-payloads" and PayloadPathPrefix="/workspace/payloads",
	// then payloadPath will be "/workspace/payloads/{sessionID}/{queryID}/payload.json"
	PayloadPathPrefix string `toml:"payload_path_prefix" json:"payload_path_prefix,omitempty"`

	// PayloadSizeThreshold is the size threshold (in bytes) for storing payloads to disk.
	// Payloads larger than this threshold are stored to disk, smaller ones are returned inline.
	// Default: 524288 bytes (512KB)
	PayloadSizeThreshold int `toml:"payload_size_threshold" json:"payload_size_threshold,omitempty"`
}

// GetAPIKey returns the gateway API key, handling a nil Gateway safely.
func (c *Config) GetAPIKey() string {
	if c.Gateway == nil {
		return ""
	}
	return c.Gateway.APIKey
}

// ServerConfig represents an individual MCP server configuration.
type ServerConfig struct {
	// Type is the server type: "stdio" or "http"
	Type string `toml:"type" json:"type,omitempty"`

	// Command is the executable command (for stdio servers)
	Command string `toml:"command" json:"command,omitempty"`

	// Args are the command arguments (for stdio servers)
	Args []string `toml:"args" json:"args,omitempty"`

	// Env holds environment variables for the server
	Env map[string]string `toml:"env" json:"env,omitempty"`

	// WorkingDirectory is the working directory for the server
	WorkingDirectory string `toml:"working_directory" json:"working_directory,omitempty"`

	// URL is the HTTP endpoint (for http servers)
	URL string `toml:"url" json:"url,omitempty"`

	// Headers are HTTP headers to send (for http servers)
	Headers map[string]string `toml:"headers" json:"headers,omitempty"`

	// Tools is an optional list of tools to filter/expose
	Tools []string `toml:"tools" json:"tools,omitempty"`

	// Registry is the URI to the installation location in an MCP registry (informational)
	Registry string `toml:"registry" json:"registry,omitempty"`

	// GuardPolicies holds guard policies for access control at the MCP gateway level.
	// The structure is server-specific. For GitHub MCP server, see the GitHub guard policy schema.
	GuardPolicies map[string]interface{} `toml:"guard_policies" json:"guard-policies,omitempty"`

	// Guard is the name of the guard to use for this server (requires DIFC)
	Guard string `toml:"guard" json:"guard,omitempty"`
}

// GuardConfig represents a guard configuration for DIFC enforcement.
type GuardConfig struct {
	// Type is the guard type: "wasm", "noop", etc.
	Type string `toml:"type" json:"type"`

	// Path is the path to the guard implementation (e.g., WASM file)
	Path string `toml:"path" json:"path,omitempty"`

	// Config holds guard-specific configuration
	Config map[string]interface{} `toml:"config" json:"config,omitempty"`

	// Policy holds guard policy configuration for label_agent lifecycle initialization
	Policy *GuardPolicy `toml:"policy" json:"policy,omitempty"`
}

// applyGatewayDefaults applies default values to a GatewayConfig if they are not set.
// This helper ensures consistent default initialization across TOML and JSON config loading.
// It only applies defaults for zero values, preserving any explicitly set values.
func applyGatewayDefaults(cfg *GatewayConfig) {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.StartupTimeout == 0 {
		cfg.StartupTimeout = DefaultStartupTimeout
	}
	if cfg.ToolTimeout == 0 {
		cfg.ToolTimeout = DefaultToolTimeout
	}
}

// LoadFromFile loads configuration from a TOML file.
//
// This function uses the BurntSushi/toml v1.6.0+ parser with TOML 1.1 support,
// which enables modern syntax features like newlines in inline tables and
// improved duplicate key detection.
//
// Error Handling:
//   - Parse errors include both line AND column numbers (v1.5.0+ feature)
//   - Unknown fields generate warnings instead of hard errors (typo detection)
//   - Metadata tracks all decoded keys for validation purposes
//
// Example usage with TOML 1.1 multi-line arrays:
//
//	[servers.github]
//	command = "docker"
//	args = [
//	    "run", "--rm", "-i",
//	    "--name", "awmg-github-mcp"
//	]
func LoadFromFile(path string) (*Config, error) {
	logConfig.Printf("Loading configuration from file: %s", path)

	// Open file for streaming
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	// Use streaming decoder for better memory efficiency with large configs
	var cfg Config
	decoder := toml.NewDecoder(file)
	md, err := decoder.Decode(&cfg)
	if err != nil {
		// Extract position information from ParseError for better error messages
		// Note: We use Position.Line, Position.Col, and Message separately to provide
		// a consistent, precise error format. perr.Error() includes line info but not
		// column, so we construct our own message with both for better UX.
		// Try pointer type first (for compatibility)
		if perr, ok := err.(*toml.ParseError); ok {
			return nil, fmt.Errorf("failed to parse TOML at line %d, column %d: %s",
				perr.Position.Line, perr.Position.Col, perr.Message)
		}
		// Try value type (used by toml.Decode)
		if perr, ok := err.(toml.ParseError); ok {
			return nil, fmt.Errorf("failed to parse TOML at line %d, column %d: %s",
				perr.Position.Line, perr.Position.Col, perr.Message)
		}
		return nil, fmt.Errorf("failed to parse TOML: %w", err)
	}

	logConfig.Printf("Parsed TOML config with %d servers", len(cfg.Servers))

	// Detect and warn about unknown configuration keys (typos, deprecated options)
	// This uses MetaData.Undecoded() to identify keys present in TOML but not
	// in the Config struct. This provides a balance between strict validation
	// (hard errors) and user-friendliness (warnings allow config to load).
	//
	// Design decision: We use warnings rather than toml.Decoder.DisallowUnknownFields()
	// (which doesn't exist) or hard errors to maintain backward compatibility and
	// allow gradual config migration. Common typos like "prot" → "port" are caught
	// while still allowing the gateway to start.
	undecoded := md.Undecoded()
	if len(undecoded) > 0 {
		for _, key := range undecoded {
			// Log to both debug logger and file logger for visibility
			logConfig.Printf("WARNING: Unknown configuration key '%s' - check for typos or deprecated options", key)
			logger.LogWarn("config", "Unknown configuration key '%s' - check for typos or deprecated options", key)
		}
	}

	// Validate required fields
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("no servers defined in configuration")
	}

	// Validate TOML stdio servers use Docker for containerization (Spec Section 3.2.1)
	if err := validateTOMLStdioContainerization(cfg.Servers); err != nil {
		return nil, err
	}

	// Initialize gateway if not present
	if cfg.Gateway == nil {
		cfg.Gateway = &GatewayConfig{}
	}

	// Apply core gateway defaults
	applyGatewayDefaults(cfg.Gateway)

	// Apply feature-specific defaults
	applyDefaults(&cfg)

	if err := validateGuardPolicies(&cfg); err != nil {
		return nil, err
	}

	logConfig.Printf("Successfully loaded %d servers from TOML file", len(cfg.Servers))
	return &cfg, nil
}

// logger for config package
var logConfig = log.New(io.Discard, "[CONFIG] ", log.LstdFlags)

// SetDebug enables debug logging for config package
func SetDebug(enabled bool) {
	if enabled {
		logConfig = log.New(os.Stderr, "[CONFIG] ", log.LstdFlags)
	} else {
		logConfig = log.New(io.Discard, "[CONFIG] ", log.LstdFlags)
	}
}
