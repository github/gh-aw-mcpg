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
// Error Reporting: Wraps ParseError with %w to preserve structured type and surface full source context
// Unknown Fields: Uses MetaData.Undecoded() to reject configurations with unrecognized fields (spec §4.3.1)
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
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Core constants for configuration defaults
const (
	DefaultPort              = 3000
	DefaultStartupTimeout    = 30   // seconds (per spec §4.1.3)
	DefaultToolTimeout       = 60   // seconds (per spec §4.1.3)
	DefaultKeepaliveInterval = 1500 // seconds (25 minutes) — keeps HTTP backend sessions alive
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

	// KeepaliveInterval is the interval (seconds) for sending keepalive pings to HTTP
	// backends. This prevents long-running sessions from being expired by the remote
	// server's idle timeout (typically 30 minutes). Set to -1 to disable keepalive
	// pings entirely (useful when higher-level timeouts manage session lifecycle).
	// Default: 1500 (25 minutes)
	KeepaliveInterval int `toml:"keepalive_interval" json:"keepalive_interval,omitempty"`

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

	// TrustedBots is an optional list of additional bot usernames that should be treated
	// as trusted. Objects authored by these bots receive "approved" integrity regardless
	// of their author_association. This list is merged with the guard's built-in trusted
	// bot list and is purely additive (it cannot remove built-in trusted bots).
	// Example values: "copilot-swe-agent[bot]", "my-org-bot[bot]"
	TrustedBots []string `toml:"trusted_bots" json:"trusted_bots,omitempty"`

	// Tracing holds OpenTelemetry OTLP tracing configuration (legacy TOML key).
	// New configurations should use the opentelemetry key (spec §4.1.3.6).
	// When Endpoint is set, traces are exported to the specified OTLP endpoint.
	// When omitted or Endpoint is empty, a noop tracer is used (zero overhead).
	Tracing *TracingConfig `toml:"tracing" json:"tracing,omitempty"`

	// Opentelemetry holds OpenTelemetry OTLP tracing configuration per spec §4.1.3.6.
	// This key takes precedence over the legacy tracing key when both are present.
	// MUST use an HTTPS endpoint when configured.
	Opentelemetry *TracingConfig `toml:"opentelemetry" json:"opentelemetry,omitempty"`
}

// HTTPKeepaliveInterval returns the keepalive interval as a time.Duration.
// A negative KeepaliveInterval disables keepalive (returns 0).
func (g *GatewayConfig) HTTPKeepaliveInterval() time.Duration {
	if g == nil {
		return time.Duration(DefaultKeepaliveInterval) * time.Second
	}
	if g.KeepaliveInterval < 0 {
		return 0
	}
	return time.Duration(g.KeepaliveInterval) * time.Second
}

// GetAPIKey returns the gateway API key, handling a nil Gateway safely.
func (c *Config) GetAPIKey() string {
	if c.Gateway == nil {
		return ""
	}
	return c.Gateway.APIKey
}

// AuthConfig configures upstream authentication for HTTP MCP servers.
type AuthConfig struct {
	// Type is the authentication type. Currently only "github-oidc" is supported.
	Type string `toml:"type" json:"type"`

	// Audience is the intended audience for the OIDC token.
	// If empty, defaults to the server URL.
	Audience string `toml:"audience" json:"audience,omitempty"`
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

	// Auth configures upstream authentication for HTTP MCP servers.
	Auth *AuthConfig `toml:"auth" json:"auth,omitempty"`

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
	if cfg.KeepaliveInterval == 0 {
		cfg.KeepaliveInterval = DefaultKeepaliveInterval
	}
}

// EnsureGatewayDefaults guarantees that cfg.Gateway is non-nil and that all
// gateway-level fields have sensible defaults applied. This matches the
// invariants enforced by the standard loaders (LoadFromFile, LoadFromStdin),
// and can be used by callers that construct Config values manually (e.g. in
// tests) to avoid nil-pointer panics and ensure consistent defaults.
func (cfg *Config) EnsureGatewayDefaults() {
	if cfg.Gateway == nil {
		cfg.Gateway = &GatewayConfig{}
	}
	applyGatewayDefaults(cfg.Gateway)
	applyDefaults(cfg)
}

// isDynamicTOMLPath reports whether the TOML key path falls under a known
// map[string]interface{} field in the config struct. Such fields accept
// arbitrary nested keys by design and must be excluded from the unknown-field check.
//
// toml.Key is a []string of path components, e.g.:
//
//	["servers", "github", "guard_policies", "mypolicy", "repos"]
//	 [0]        [1]       [2]               [3]          [4]
//
// Dynamic sections:
//   - servers[0].<name>[1].guard_policies[2].<policy>[3].<key>[4+]  (len ≥ 5)
//   - guards[0].<name>[1].config[2].<key>[3+]                       (len ≥ 4)
func isDynamicTOMLPath(key toml.Key) bool {
	// servers.<name>.guard_policies.<policy>.<key> → indices [0]="servers" [2]="guard_policies", len ≥ 5
	if len(key) >= 5 && key[0] == "servers" && key[2] == "guard_policies" {
		return true
	}
	// guards.<name>.config.<key> → indices [0]="guards" [2]="config", len ≥ 4
	if len(key) >= 4 && key[0] == "guards" && key[2] == "config" {
		return true
	}
	return false
}

// This function uses the BurntSushi/toml v1.6.0+ parser with TOML 1.1 support,
// which enables modern syntax features like newlines in inline tables and
// improved duplicate key detection.
//
// Error Handling:
//   - Parse errors include both line AND column numbers (v1.5.0+ feature)
//   - Unknown fields are rejected with an error per spec §4.3.1
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
		// toml.Decode returns ParseError as a value type. Wrap with %w to preserve
		// the structured error for callers while surfacing the full source context
		// (line snippet + column pointer) via ParseError.Error().
		if perr, ok := err.(toml.ParseError); ok {
			return nil, fmt.Errorf("failed to parse TOML: %w", perr)
		}
		return nil, fmt.Errorf("failed to parse TOML: %w", err)
	}

	logConfig.Printf("Parsed TOML config with %d servers", len(cfg.Servers))

	// Detect and reject unknown configuration keys (typos, unrecognized fields).
	// This uses MetaData.Undecoded() to identify keys present in TOML but not
	// in the Config struct. Per spec §4.3.1, the gateway MUST reject configurations
	// containing unrecognized fields with an informative error message.
	//
	// Note: map[string]interface{} fields (guard_policies, guards.*.config) are
	// intentionally flexible and their nested keys are exempt from this check.
	undecoded := md.Undecoded()
	var unknownKeys []toml.Key
	for _, key := range undecoded {
		if !isDynamicTOMLPath(key) {
			unknownKeys = append(unknownKeys, key)
		}
	}
	if len(unknownKeys) > 0 {
		keyStrs := make([]string, len(unknownKeys))
		for i, k := range unknownKeys {
			keyStrs[i] = k.String()
		}
		return nil, fmt.Errorf("configuration contains unrecognized field(s): %s — check the MCP Gateway Specification for supported fields", strings.Join(keyStrs, ", "))
	}

	// Validate required fields
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("no servers defined in configuration")
	}

	// Validate TOML stdio servers use Docker for containerization (Spec Section 3.2.1)
	if err := validateTOMLStdioContainerization(cfg.Servers); err != nil {
		return nil, err
	}

	// Validate auth configs (e.g. fail-fast for missing OIDC env vars).
	// This ensures parity with the JSON stdin path which calls validateAuthConfig
	// via convertStdinServerConfig → validateServerConfigWithCustomSchemas.
	for name, serverCfg := range cfg.Servers {
		if serverCfg.Auth != nil {
			// Auth is only supported on HTTP servers, matching validateStandardServerConfig behavior.
			if serverCfg.Type != "http" {
				return nil, fmt.Errorf("server '%s': auth is only supported for HTTP servers (type: \"http\")", name)
			}
			jsonPath := fmt.Sprintf("servers.%s", name)
			if err := validateAuthConfig(serverCfg.Auth, name, jsonPath); err != nil {
				return nil, err
			}
		}
	}

	// Initialize gateway if not present
	if cfg.Gateway == nil {
		cfg.Gateway = &GatewayConfig{}
	}

	// Validate trusted_bots per spec §4.1.3.4: must be non-empty array when present
	if err := validateTrustedBots(cfg.Gateway.TrustedBots); err != nil {
		return nil, err
	}

	// Merge opentelemetry key into tracing when present (spec §4.1.3.6).
	// opentelemetry takes precedence over the legacy tracing key.
	if cfg.Gateway.Opentelemetry != nil {
		cfg.Gateway.Tracing = cfg.Gateway.Opentelemetry
		cfg.Gateway.Opentelemetry = nil
		// Expand ${VAR} expressions in tracing fields before validation.
		if err := expandTracingVariables(cfg.Gateway.Tracing); err != nil {
			return nil, err
		}
		// Validate HTTPS endpoint requirement for the opentelemetry section
		if err := validateOpenTelemetryConfig(cfg.Gateway.Tracing, true); err != nil {
			return nil, err
		}
	}

	// Apply core gateway defaults
	applyGatewayDefaults(cfg.Gateway)

	// Apply feature-specific defaults
	applyDefaults(&cfg)

	// Validate payload_size_threshold per spec §4.1.3.3: must be positive integer.
	// applyDefaults replaces 0 with the default, so only negative values remain to catch.
	if cfg.Gateway.PayloadSizeThreshold < 0 {
		return nil, fmt.Errorf("gateway.payload_size_threshold must be a positive integer, got %d (spec §4.1.3.3)", cfg.Gateway.PayloadSizeThreshold)
	}

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
