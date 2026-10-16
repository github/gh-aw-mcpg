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
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/github/gh-aw-mcpg/internal/logger"
)

// Core constants for configuration defaults
const (
	DefaultPort              = 3000
	DefaultStartupTimeout    = 30 // seconds (per spec §4.1.3)
	DefaultToolTimeout       = 60 // seconds (per spec §4.1.3)
	DefaultContainerRuntime  = "docker"
	DefaultKeepaliveInterval = 1500 // seconds (25 minutes) — keeps HTTP backend sessions alive
	DefaultConnectTimeout    = 30   // seconds — per-transport timeout for HTTP backend connect
	// DefaultPayloadDir is the default directory for storing large payloads.
	DefaultPayloadDir = "/tmp/jq-payloads"
	// DefaultLogDir is the default directory for storing log files.
	DefaultLogDir = "/tmp/gh-aw/mcp-logs"
	// DefaultWasmCacheDirName is the default subdirectory name for persisted wazero compilation artifacts.
	DefaultWasmCacheDirName = "wazero-cache"
	// DefaultPayloadSizeThreshold is the default threshold (in bytes) for storing payloads on disk.
	DefaultPayloadSizeThreshold = 524288
)

func init() {
	// Register default setter for payload-related gateway settings.
	RegisterDefaults(func(cfg *Config) {
		if cfg.Gateway != nil {
			if cfg.Gateway.PayloadDir == "" {
				cfg.Gateway.PayloadDir = DefaultPayloadDir
			}
			if cfg.Gateway.PayloadSizeThreshold == 0 {
				cfg.Gateway.PayloadSizeThreshold = DefaultPayloadSizeThreshold
			}
		}
	})
}

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

	// Delegation optionally enables github-repository-delegation-v1 runtime
	// control/data-plane authorization. It is resolved from environment
	// activation inputs, never from user configuration.
	Delegation *delegation.RuntimeConfig `toml:"-" json:"-"`
}

// GatewayConfig holds global gateway settings.
// Feature-specific fields are added in their respective config_*.go files.
type GatewayConfig struct {
	// Port is the HTTP port to listen on
	Port int `toml:"port" json:"port,omitempty"`

	// AgentID is the gateway agent/session identifier.
	AgentID string `toml:"agent_id" json:"agent_id,omitempty"`

	// AgentIDs are the gateway agent/session identifiers.
	AgentIDs []string `toml:"agent_ids" json:"agent_ids,omitempty"`

	// APIKey is a deprecated alias for AgentID.
	APIKey string `toml:"api_key" json:"api_key,omitempty"`

	// Domain is the gateway domain for external access
	Domain string `toml:"domain" json:"domain,omitempty"`

	// StartupTimeout is the maximum time (seconds) to wait for server startup
	StartupTimeout int `toml:"startup_timeout" json:"startup_timeout,omitempty"`

	// ToolTimeout is the maximum time (seconds) to wait for tool execution
	ToolTimeout int `toml:"tool_timeout" json:"tool_timeout,omitempty"`

	// Dockerless forces Podman for stdin JSON container launches.
	// It is intentionally unavailable in TOML configuration.
	Dockerless bool `toml:"-" json:"dockerless,omitempty"`

	// ContainerRuntime selects the container runtime used for stdio MCP server launches
	// in stdin JSON configurations that specify a container image.
	// Supported values: "docker" (default), "podman".
	ContainerRuntime string `toml:"container_runtime" json:"containerRuntime,omitempty"`

	// ContainerRuntimeCommand optionally overrides the runtime executable/binary path.
	// Example: "/usr/bin/podman", "nerdctl".
	ContainerRuntimeCommand string `toml:"container_runtime_command" json:"containerRuntimeCommand,omitempty"`

	// ContainerRuntimeArgs are optional runtime-level arguments inserted before "run".
	// Example: ["--events-backend=file"].
	ContainerRuntimeArgs []string `toml:"container_runtime_args" json:"containerRuntimeArgs,omitempty"`

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

	// URLDomainAudit enables URL domain observation in middleware/guard logging.
	// This is currently toggled via CLI/environment and not loaded from config files.
	URLDomainAudit bool `toml:"-" json:"-"`

	// TrustedBots is an optional list of additional bot usernames that should be treated
	// as trusted. Objects authored by these bots receive "approved" integrity regardless
	// of their author_association. This list is merged with the guard's built-in trusted
	// bot list and is purely additive (it cannot remove built-in trusted bots).
	// Example values: "copilot-swe-agent[bot]", "my-org-bot[bot]"
	TrustedBots []string `toml:"trusted_bots" json:"trusted_bots,omitempty"`

	// ForcePublicRepos controls whether the gateway automatically overrides the
	// allow-only policy to repos="public" when the GITHUB_REPOSITORY repo is public.
	// nil / omitted → enabled by default (auto-force when repo is public)
	// true  → explicitly enabled
	// false → disabled (opt-out via private-to-public-flows: allow)
	// Corresponds to env var MCP_GATEWAY_FORCE_PUBLIC_REPOS.
	ForcePublicRepos *bool `toml:"force_public_repos" json:"forcePublicRepos,omitempty"`

	// SinkVisibilityExemptServers lists server IDs that are exempt from the
	// default sink-visibility="public" enforcement. By default, all non-safe-outputs
	// write-sink servers are assigned sink-visibility="public" (security-by-default).
	// Servers in this list retain their configured (or omitted) sink-visibility as-is.
	// Use ["*"] to exempt all servers (equivalent to disabling the default).
	// Set by the compiler when private-to-public-flows is configured in frontmatter.
	SinkVisibilityExemptServers []string `toml:"sink_visibility_exempt_servers" json:"sinkVisibilityExemptServers,omitempty"`

	// AgentPolicies holds optional per-agent access policies keyed by agent ID.
	// Each key must match a configured agent ID (agent_id/agent_ids). When present,
	// per-agent enforcement is fail-closed: an agent may only access the servers and
	// tools granted by its policy, and (in multi-agent mode) every configured agent
	// ID must have a policy. Omit entirely to grant all agents full access.
	AgentPolicies map[string]*AgentPolicy `toml:"agent_policies" json:"agentPolicies,omitempty"`

	// Tracing holds OpenTelemetry OTLP tracing configuration (legacy TOML key).
	// New configurations should use the opentelemetry key (spec §4.1.3.6).
	// When Endpoint is set, traces are exported to the specified OTLP endpoint.
	// When omitted or Endpoint is empty, a noop tracer is used (zero overhead).
	Tracing *TracingConfig `toml:"tracing" json:"tracing,omitempty"`

	// Opentelemetry holds OpenTelemetry OTLP tracing configuration per spec §4.1.3.6.
	// This key takes precedence over the legacy tracing key when both are present.
	// MUST use an HTTPS endpoint when configured.
	Opentelemetry *TracingConfig `toml:"opentelemetry" json:"opentelemetry,omitempty"`

	// agentIDExplicit tracks whether agent_id/agentId was explicitly provided
	// (as opposed to being derived from deprecated api_key/apiKey aliases).
	agentIDExplicit bool `toml:"-" json:"-"`
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

// GetAgentID returns the gateway agent identifier, handling a nil Gateway safely.
func (c *Config) GetAgentID() string {
	if c.Gateway == nil {
		return ""
	}
	return c.Gateway.effectiveAgentID()
}

// GetAgentIDs returns every configured gateway identity that should authenticate
// successfully. When gateway.agentIds (plural) is configured, each entry is a
// valid credential, enabling concurrent primary/enclave sessions that each
// authenticate with their own identifier. Otherwise, it falls back to the
// singular effective agent ID (which may be empty when none is configured).
func (c *Config) GetAgentIDs() []string {
	if c.Gateway == nil {
		return nil
	}
	if len(c.Gateway.AgentIDs) > 0 {
		logConfig.Printf("GetAgentIDs: using gateway.agentIds (plural), count=%d", len(c.Gateway.AgentIDs))
		return append([]string{}, c.Gateway.AgentIDs...)
	}
	if id := c.Gateway.effectiveAgentID(); id != "" {
		logConfig.Print("GetAgentIDs: gateway.agentIds not set, falling back to singular effective agent ID")
		return []string{id}
	}
	logConfig.Print("GetAgentIDs: no agent IDs configured, authentication disabled")
	return nil
}

func (g *GatewayConfig) effectiveAgentID() string {
	if g == nil {
		return ""
	}
	if g.agentIDExplicit {
		return g.AgentID
	}
	if g.AgentID != "" {
		return g.AgentID
	}
	return g.APIKey
}

func (g *GatewayConfig) normalizeAgentID(agentIDDefined, legacyAPIKeyDefined bool, source string) {
	if g == nil {
		return
	}

	if legacyAPIKeyDefined {
		logConfig.Printf("DEPRECATION: gateway.api_key is deprecated in %s config; use gateway.agent_id instead", source)
	}
	if legacyAPIKeyDefined && agentIDDefined && g.AgentID != g.APIKey {
		logConfig.Printf("Both gateway.agent_id and deprecated gateway.api_key are set in %s config; using gateway.agent_id", source)
	}

	g.agentIDExplicit = agentIDDefined
	if !agentIDDefined && legacyAPIKeyDefined {
		g.AgentID = g.APIKey
	}
	g.APIKey = g.effectiveAgentID()
}

// HTTPConnectTimeout returns the per-transport connect timeout as a Duration.
// Returns DefaultConnectTimeout when the field is zero or negative.
func (s *ServerConfig) HTTPConnectTimeout() time.Duration {
	if s == nil || s.ConnectTimeout <= 0 {
		return time.Duration(DefaultConnectTimeout) * time.Second
	}
	return time.Duration(s.ConnectTimeout) * time.Second
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

	// Containerized indicates this stdio server entry is explicitly containerized.
	// This is internal-only metadata used by the launcher for diagnostics.
	Containerized bool `toml:"-" json:"-"`

	// ContainerRuntimeArgs contains only the arguments before the container image.
	// It is internal-only metadata used to distinguish runtime options from
	// arguments passed to the container process.
	ContainerRuntimeArgs []string `toml:"-" json:"-"`

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

	// CACertFile is a PEM file containing CA certificates trusted only for this HTTP server.
	CACertFile string `toml:"ca_cert_file" json:"caCertFile,omitempty"`

	// Auth configures upstream authentication for HTTP MCP servers.
	Auth *AuthConfig `toml:"auth" json:"auth,omitempty"`

	// Tools is an optional list of tools to filter/expose
	Tools []string `toml:"tools" json:"tools,omitempty"`

	// Prompts is an optional list of prompts to filter/expose.
	Prompts []string `toml:"prompts" json:"prompts,omitempty"`

	// ToolResponseFilters configures per-tool jq expressions that transform tool
	// response data before it is returned to the agent and before large-payload
	// preview/schema processing runs.
	ToolResponseFilters map[string]string `toml:"tool_response_filters" json:"tool_response_filters,omitempty"`

	// Registry is the URI to the installation location in an MCP registry (informational)
	Registry string `toml:"registry" json:"registry,omitempty"`

	// GuardPolicies holds guard policies for access control at the MCP gateway level.
	// The structure is server-specific. For GitHub MCP server, see the GitHub guard policy schema.
	GuardPolicies map[string]interface{} `toml:"guard_policies" json:"guard-policies,omitempty"`

	// Guard is the name of the guard to use for this server (requires DIFC)
	Guard string `toml:"guard" json:"guard,omitempty"`

	// ConnectTimeout is the timeout (in seconds) used for SDK-managed HTTP transport connect attempts.
	// The gateway tries multiple transports in sequence (streamable HTTP → SSE → plain JSON-RPC).
	// This timeout applies to the streamable HTTP and SSE connection attempts; the plain JSON-RPC
	// fallback uses the HTTP client's request timeout instead. Increase this for backends that are
	// slow to initialize. Only applies to HTTP server types. Default: 30 seconds.
	ConnectTimeout int `toml:"connect_timeout" json:"connect_timeout,omitempty"`

	// ToolTimeout is the per-server maximum time (seconds) to wait for a single tool invocation.
	// When set, this overrides the global gateway.tool_timeout for calls to this server only.
	// Minimum: 10. When 0 (unset), the global gateway.tool_timeout is used.
	ToolTimeout int `toml:"tool_timeout" json:"tool_timeout,omitempty"`

	// RateLimitThreshold is the number of consecutive rate-limit errors from this backend
	// that will trip the circuit breaker (transition CLOSED → OPEN). When OPEN, requests
	// are immediately rejected until the cooldown period elapses. Default: 3.
	// Supported in TOML config only; the JSON stdin config does not currently accept this field.
	RateLimitThreshold int `toml:"rate_limit_threshold" json:"-"`

	// RateLimitCooldown is the number of seconds the circuit breaker stays OPEN before
	// allowing a single probe request (transition OPEN → HALF-OPEN). If the probe
	// succeeds the circuit closes; if rate-limited again it re-opens. Default: 60.
	// Supported in TOML config only; the JSON stdin config does not currently accept this field.
	RateLimitCooldown int `toml:"rate_limit_cooldown" json:"-"`
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
	logConfig.Printf("Resolved gateway config after defaulting: port=%d, startupTimeout=%d, toolTimeout=%d, keepaliveInterval=%d",
		cfg.Port, cfg.StartupTimeout, cfg.ToolTimeout, cfg.KeepaliveInterval)
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
//
// This manual walk over MetaData.Undecoded() exists because
// toml.Decoder.SetStrict(true) enforces strict mode for the entire document
// and cannot be scoped to only the statically-typed fields, so it would
// reject the dynamic map[string]interface{} sections above. If a future
// BurntSushi/toml release adds a selective/per-field strict mode (or an
// allow-list hook), this function can likely be removed in favor of it. See
// https://github.com/BurntSushi/toml for release notes.
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

// LoadFromFile parses a TOML configuration file at path and returns the
// validated Config, or an error if the file cannot be opened, parsed,
// contains unknown fields, or fails schema/field-level validation (e.g. no
// servers defined, containerization, auth, tracing, or trusted_bots checks).
//
// This function uses the BurntSushi/toml v1.6.0+ parser with TOML 1.1 support,
// which enables modern syntax features like newlines in inline tables and
// improved duplicate key detection.
//
// Error Handling:
//   - Parse errors include both line AND column numbers (v1.5.0+ feature)
//   - Unknown fields are rejected with an error per spec §4.3.1
//   - Metadata tracks all decoded keys for validation purposes
//
// Callers that need structured parse-error details (line, column, source
// snippet) can extract the original toml.ParseError via errors.As:
//
//	var perr toml.ParseError
//	if errors.As(err, &perr) {
//		fmt.Println(perr.Position())
//	}
//
// Example usage with TOML 1.1 multi-line arrays:
//
//	[servers.github]
//	command = "docker"
//	args = [
//		"run", "--rm", "-i",
//		"--name", "awmg-github-mcp"
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
		// decoder.Decode returns ParseError as a value type. Wrap with %w to
		// preserve the structured error for callers while surfacing the full
		// source context (line snippet + column pointer) via ParseError.Error().
		var perr toml.ParseError
		if errors.As(err, &perr) {
			logConfig.Printf("TOML parse error at line %d, column %d", perr.Position.Line, perr.Position.Col)
		}
		return nil, fmt.Errorf("failed to parse TOML: %w", err)
	}

	logConfig.Printf("Parsed TOML config with %d servers", len(cfg.Servers))

	// Detect and reject unknown configuration keys (typos, unrecognized fields).
	// This uses MetaData.Undecoded() to identify keys present in TOML but not
	// in the Config struct. Per spec §4.3.1, the gateway MUST reject configurations
	// containing unrecognized fields with an informative error message.
	//
	// Design note: Decoder.SetStrict(true) (available since v1.6.0) would
	// automatically reject unknown fields, but cannot be used here because the
	// config contains map[string]interface{} sections (guard_policies,
	// guards.*.config) that accept arbitrary nested keys by design.
	// SetStrict(true) would treat those dynamic keys as unknown fields and
	// reject valid configurations. The manual Undecoded() + isDynamicTOMLPath
	// approach enforces strictness for typed struct fields while exempting the
	// flexible map sections.
	undecoded := md.Undecoded()
	logConfig.Printf("Checking %d undecoded TOML keys against allowed fields", len(undecoded))
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

	// Validate TOML stdio servers use configured container runtime command (Spec Section 3.2.1)
	stdioServerCount := 0
	for _, serverCfg := range cfg.Servers {
		if serverCfg.Type == "stdio" {
			stdioServerCount++
		}
	}
	logConfig.Printf("Validating stdio server containerization requirements for %d stdio servers", stdioServerCount)
	if err := validateTOMLStdioContainerization(cfg.Servers, cfg.Gateway); err != nil {
		return nil, err
	}
	for _, serverCfg := range cfg.Servers {
		if IsStdioServerType(serverCfg.Type) {
			serverCfg.Containerized = true
			if runtimeArgs, ok := containerRuntimeArgs(serverCfg.Args); ok {
				serverCfg.ContainerRuntimeArgs = runtimeArgs
			}
		}
	}

	logConfig.Printf("Validating shared server configuration for %d servers", len(cfg.Servers))
	for name, serverCfg := range cfg.Servers {
		jsonPath := fmt.Sprintf("servers.%s", name)
		if serverCfg.CACertFile != "" && serverCfg.Type != "http" {
			return nil, fmt.Errorf("%s.ca_cert_file is only supported for HTTP servers", jsonPath)
		}
		if err := validateCommonServerFields(name, serverCfg.Type, serverCfg.Auth, serverCfg.ToolResponseFilters, jsonPath); err != nil {
			return nil, err
		}
	}

	// Initialize gateway if not present
	if cfg.Gateway == nil {
		cfg.Gateway = &GatewayConfig{}
	}
	cfg.Gateway.normalizeAgentID(md.IsDefined("gateway", "agent_id"), md.IsDefined("gateway", "api_key"), "TOML")
	agentIDsDefined := md.IsDefined("gateway", "agent_ids")
	if md.IsDefined("gateway", "agent_id") && strings.TrimSpace(cfg.Gateway.AgentID) == "" {
		return nil, fmt.Errorf("gateway.agent_id must be a non-empty string when provided")
	}
	if md.IsDefined("gateway", "api_key") && strings.TrimSpace(cfg.Gateway.APIKey) == "" {
		return nil, fmt.Errorf("gateway.api_key must be a non-empty string when provided")
	}
	if !md.IsDefined("gateway", "agent_id") && !md.IsDefined("gateway", "api_key") && !agentIDsDefined && cfg.Gateway.AgentID == "" && cfg.Gateway.APIKey == "" && len(cfg.Gateway.AgentIDs) == 0 {
		return nil, fmt.Errorf("gateway.agent_id or gateway.agent_ids must be configured; exactly one selection is required")
	}
	if err := validateAgentIDs(cfg.Gateway.AgentIDs, agentIDsDefined, "agent_ids"); err != nil {
		return nil, err
	}
	if agentIDsDefined && (md.IsDefined("gateway", "agent_id") || md.IsDefined("gateway", "api_key")) {
		return nil, fmt.Errorf("gateway.agent_ids cannot be combined with gateway.agent_id or gateway.api_key")
	}
	if err := validateContainerRuntimeValue(cfg.Gateway.ContainerRuntime, "gateway.container_runtime"); err != nil {
		return nil, err
	}
	if err := validateContainerRuntimeCommandNotBlank(
		cfg.Gateway.ContainerRuntimeCommand,
		"container_runtime_command",
		"gateway.container_runtime_command",
	); err != nil {
		return nil, err
	}

	// Validate trusted_bots per spec §4.1.3.4: must be non-empty array when present
	if err := validateTrustedBots(cfg.Gateway.TrustedBots); err != nil {
		return nil, err
	}

	// Validate gateway.port when explicitly set in TOML so documented port bounds
	// are enforced consistently across TOML and JSON config paths.
	if md.IsDefined("gateway", "port") {
		if err := PortRange(cfg.Gateway.Port, "gateway.port"); err != nil {
			return nil, err
		}
	}

	// Validate payload_size_threshold per spec §4.1.3.3 when explicitly set in TOML.
	if md.IsDefined("gateway", "payload_size_threshold") {
		if err := validateGatewayPayloadSizeThreshold(cfg.Gateway.PayloadSizeThreshold, "payload_size_threshold", "gateway.payload_size_threshold"); err != nil {
			return nil, err
		}
	}

	// Merge opentelemetry key into tracing when present (spec §4.1.3.6).
	// opentelemetry takes precedence over the legacy tracing key.
	if cfg.Gateway.Opentelemetry != nil {
		if cfg.Gateway.Tracing != nil {
			logConfig.Print("Warning: both [gateway.tracing] and [gateway.opentelemetry] are set; [gateway.opentelemetry] takes precedence")
		}
		logConfig.Printf("opentelemetry section found: merging into tracing config (endpoint_set=%t)", cfg.Gateway.Opentelemetry.Endpoint != "")
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

	if err := validateGuardPolicies(&cfg); err != nil {
		return nil, err
	}

	if err := validateAgentPolicies(&cfg); err != nil {
		return nil, err
	}

	logConfig.Printf("Successfully loaded %d servers from TOML file", len(cfg.Servers))
	return &cfg, nil
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

// logConfig is the debug logger for the config package.
// Enable with DEBUG=config:* or DEBUG=*.
var logConfig = logger.New("config:config")
