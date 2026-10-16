// Package config provides configuration loading and parsing.
// This file defines stdin (JSON) configuration types.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var logStdin = logger.ForFile()

// StdinConfig represents the JSON configuration format read from stdin.
type StdinConfig struct {
	// MCPServers maps server names to their configurations
	MCPServers map[string]*StdinServerConfig `json:"mcpServers"`

	// Gateway holds global gateway settings
	Gateway *StdinGatewayConfig `json:"gateway,omitempty"`

	// Guards holds guard configurations for DIFC enforcement
	Guards map[string]*StdinGuardConfig `json:"guards,omitempty"`

	// CustomSchemas defines custom server types
	CustomSchemas map[string]interface{} `json:"customSchemas,omitempty"`
}

// StdinGatewayConfig represents gateway configuration in stdin JSON format.
// Uses pointers for optional fields to distinguish between unset and zero values.
type StdinGatewayConfig struct {
	Port     *int     `json:"port,omitempty"`
	AgentID  string   `json:"agentId,omitempty"`
	AgentIDs []string `json:"agentIds,omitempty"`
	// APIKey is a deprecated alias for AgentID, retained for internal callers that
	// unmarshal or construct StdinGatewayConfig without schema validation. Raw
	// JSON stdin payloads containing "apiKey" are rejected by schema validation
	// (additionalProperties: false) before this field is consulted.
	APIKey                      string                    `json:"apiKey,omitempty"`
	Domain                      string                    `json:"domain,omitempty"`
	StartupTimeout              *int                      `json:"startupTimeout,omitempty"`
	ToolTimeout                 *int                      `json:"toolTimeout,omitempty"`
	Dockerless                  bool                      `json:"dockerless,omitempty"`
	ContainerRuntime            string                    `json:"containerRuntime,omitempty"`
	ContainerRuntimeCommand     string                    `json:"containerRuntimeCommand,omitempty"`
	ContainerRuntimeArgs        []string                  `json:"containerRuntimeArgs,omitempty"`
	KeepaliveInterval           *int                      `json:"keepaliveInterval,omitempty"`
	PayloadDir                  string                    `json:"payloadDir,omitempty"`
	PayloadPathPrefix           *string                   `json:"payloadPathPrefix,omitempty"`
	PayloadSizeThreshold        *int                      `json:"payloadSizeThreshold,omitempty"`
	TrustedBots                 []string                  `json:"trustedBots,omitempty"`
	ForcePublicRepos            *bool                     `json:"forcePublicRepos,omitempty"`
	SinkVisibilityExemptServers []string                  `json:"sinkVisibilityExemptServers,omitempty"`
	AgentPolicies               map[string]*AgentPolicy   `json:"agentPolicies,omitempty"`
	OpenTelemetry               *StdinOpenTelemetryConfig `json:"opentelemetry,omitempty"`

	agentIDSet      bool `json:"-"`
	agentIDsSet     bool `json:"-"`
	legacyAPIKeySet bool `json:"-"`
}

// UnmarshalJSON enables backward-compatible parsing for gateway.apiKey and
// tracks deprecated field usage for warning emission.
func (g *StdinGatewayConfig) UnmarshalJSON(data []byte) error {
	type Alias StdinGatewayConfig
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(g),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawFields); err != nil {
		return err
	}
	_, g.agentIDSet = rawFields["agentId"]
	_, g.agentIDsSet = rawFields["agentIds"]
	_, g.legacyAPIKeySet = rawFields["apiKey"]
	return nil
}

// StdinOpenTelemetryConfig represents the OpenTelemetry configuration in stdin JSON format (spec §4.1.3.6).
type StdinOpenTelemetryConfig struct {
	// Endpoint is the OTLP/HTTP collector URL. MUST be HTTPS. Supports ${VAR} expansion.
	Endpoint string `json:"endpoint"`

	// TraceID is the parent trace ID (32-char lowercase hex, W3C format). Supports ${VAR}.
	TraceID string `json:"traceId,omitempty"`

	// SpanID is the parent span ID (16-char lowercase hex, W3C format). Ignored without TraceID. Supports ${VAR}.
	SpanID string `json:"spanId,omitempty"`

	// ServiceName is the service.name resource attribute. Default: "mcp-gateway".
	ServiceName string `json:"serviceName,omitempty"`
}

// StdinGuardConfig represents a guard configuration in stdin JSON format.
type StdinGuardConfig struct {
	// Type is the guard type: "wasm", "noop", etc.
	Type string `json:"type"`

	// Path is the path to the guard implementation (e.g., WASM file)
	Path string `json:"path,omitempty"`

	// Config holds guard-specific configuration
	Config map[string]interface{} `json:"config,omitempty"`

	// Policy holds guard policy configuration for label_agent lifecycle initialization
	Policy *GuardPolicy `json:"policy,omitempty"`
}

// StdinServerConfig represents a single server configuration in stdin JSON format.
// Note: unlike TOML ServerConfig, this struct intentionally has no Command field;
// stdio servers must use Container instead.
type StdinServerConfig struct {
	// Type is the server type: "stdio", "local", or "http"
	Type string `json:"type"`

	// Container is the Docker image for stdio servers
	Container string `json:"container,omitempty"`

	// Entrypoint overrides the container entrypoint
	Entrypoint string `json:"entrypoint,omitempty"`

	// EntrypointArgs are additional arguments to the entrypoint
	EntrypointArgs []string `json:"entrypointArgs,omitempty"`

	// Args are additional Docker runtime arguments (passed before container image)
	Args []string `json:"args,omitempty"`

	// Mounts are volume mounts for the container
	Mounts []string `json:"mounts,omitempty"`

	// Env holds environment variables
	Env map[string]string `json:"env,omitempty"`

	// URL is the HTTP endpoint (for http servers)
	URL string `json:"url,omitempty"`

	// Headers are HTTP headers to send (for http servers)
	Headers map[string]string `json:"headers,omitempty"`

	// CACertFile is a PEM file containing CA certificates trusted only for this HTTP server.
	CACertFile string `json:"caCertFile,omitempty"`

	// Tools is an optional list of tools to filter/expose
	Tools []string `json:"tools,omitempty"`

	// Prompts is an optional list of prompts to filter/expose
	Prompts []string `json:"prompts,omitempty"`

	// ToolResponseFilters configures per-tool jq expressions that transform tool
	// response data before it is returned to the agent.
	ToolResponseFilters map[string]string `json:"tool_response_filters,omitempty"`

	// Registry is the URI to the installation location in an MCP registry (informational)
	Registry string `json:"registry,omitempty"`

	// GuardPolicies holds guard policies for access control at the MCP gateway level.
	// The structure is server-specific. For GitHub MCP server, see the GitHub guard policy schema.
	GuardPolicies map[string]interface{} `json:"guard-policies,omitempty"`

	// Guard is the name of the guard to use for this server (requires DIFC)
	Guard string `json:"guard,omitempty"`

	// Auth configures upstream authentication for HTTP MCP servers.
	Auth *AuthConfig `json:"auth,omitempty"`

	// ConnectTimeout is the per-transport timeout (in seconds) for connecting to HTTP backends.
	// Only applies to HTTP server types. Default: 30 seconds.
	ConnectTimeout *int `json:"connectTimeout,omitempty"`

	// ToolTimeout is the per-server maximum time (seconds) to wait for a single tool invocation.
	// When set to a positive value, this overrides the global gateway.toolTimeout for calls to
	// this server only. Minimum: 10. Omit the field (or set to 0) to fall back to the global
	// gateway.toolTimeout (or MCP_GATEWAY_TOOL_TIMEOUT env fallback).
	ToolTimeout *int `json:"toolTimeout,omitempty"`

	// AdditionalProperties stores any extra fields for custom server types
	// This allows custom schemas to define their own fields beyond the standard ones
	AdditionalProperties map[string]interface{} `json:"-"`

	toolTimeoutFieldName string `json:"-"`
}

// UnmarshalJSON implements custom JSON unmarshaling to capture additional properties
func (s *StdinServerConfig) UnmarshalJSON(data []byte) error {
	// Define an auxiliary type to avoid infinite recursion
	type Alias StdinServerConfig
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(s),
	}

	// Unmarshal into the auxiliary struct first
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(data, &rawFields); err != nil {
		return err
	}

	s.toolTimeoutFieldName = "toolTimeout"
	if _, ok := rawFields["toolTimeout"]; !ok {
		if _, legacyOK := rawFields["tool_timeout"]; legacyOK {
			s.toolTimeoutFieldName = "tool_timeout"
		}
	}

	if err := assignLegacyIntAlias(rawFields, "connect_timeout", &s.ConnectTimeout); err != nil {
		return err
	}
	if err := assignLegacyIntAlias(rawFields, "tool_timeout", &s.ToolTimeout); err != nil {
		return err
	}

	// Now unmarshal into a map to capture all fields.
	// Use jsonschema.UnmarshalJSON (which calls decoder.UseNumber()) so that
	// numbers are stored as json.Number rather than float64.  This preserves
	// precision for large integers such as 9007199254740993 that cannot be
	// represented exactly as float64.
	allFieldsObj, parseErr := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if parseErr != nil {
		return parseErr
	}
	allFields, ok := allFieldsObj.(map[string]interface{})
	if !ok {
		return fmt.Errorf("expected JSON object for server config, got %T", allFieldsObj)
	}

	// Known fields in the struct
	knownFields := map[string]bool{
		"type":                  true,
		"container":             true,
		"entrypoint":            true,
		"entrypointArgs":        true,
		"args":                  true,
		"mounts":                true,
		"env":                   true,
		"url":                   true,
		"headers":               true,
		"caCertFile":            true,
		"tools":                 true,
		"prompts":               true,
		"tool_response_filters": true,
		"registry":              true,
		"guard-policies":        true,
		"guard":                 true,
		"auth":                  true,
		"connectTimeout":        true,
		"connect_timeout":       true,
		"toolTimeout":           true,
		"tool_timeout":          true,
	}

	// Store additional properties (fields not in the struct)
	s.AdditionalProperties = make(map[string]interface{})
	for key, value := range allFields {
		if !knownFields[key] {
			s.AdditionalProperties[key] = value
		}
	}

	return nil
}

func (s *StdinServerConfig) toolTimeoutField() string {
	if s.toolTimeoutFieldName != "" {
		return s.toolTimeoutFieldName
	}
	return "toolTimeout"
}

func assignLegacyIntAlias(rawFields map[string]json.RawMessage, alias string, target **int) error {
	if *target != nil {
		return nil
	}
	raw, ok := rawFields[alias]
	if !ok {
		return nil
	}

	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("invalid %s value: %w", alias, err)
	}
	logStdin.Printf("Applying legacy alias %q: value=%d (prefer camelCase equivalent)", alias, value)
	*target = &value
	return nil
}

// intPtrOrDefault returns the value of the int pointer if not nil, otherwise returns the default value.
// This helper reduces code duplication when handling optional integer fields with defaults.
func intPtrOrDefault(ptr *int, defaultValue int) int {
	if ptr != nil {
		return *ptr
	}
	return defaultValue
}

// stripExtensionFieldsForValidation returns a copy of the raw JSON with known gateway
// extension fields removed, so the copy can be validated against the upstream MCP Gateway
// schema. These fields are gateway-specific additions that are not part of the upstream
// schema definition, so they must be removed before schema validation to prevent spurious
// "additional properties" errors.
//
// Fields stripped:
//   - Top-level "guards": gateway-specific guard configuration
//   - Per-server "guard": reference to a named guard
//   - Per-server "auth": upstream authentication configuration (OIDC etc.)
//   - Per-server "tool_response_filters": gateway-side jq response shaping config
//
// Note: "guard-policies" and "registry" are already injected into the upstream schema
// by fetchAndFixSchema, so they do not need to be stripped here.
func stripExtensionFieldsForValidation(data []byte) ([]byte, error) {
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// Strip top-level "guards" extension field
	delete(config, "guards")

	// Strip per-server "guard" and "auth" extension fields
	serverCount := 0
	if servers, ok := config["mcpServers"].(map[string]interface{}); ok {
		serverCount = len(servers)
		for _, server := range servers {
			if serverMap, ok := server.(map[string]interface{}); ok {
				delete(serverMap, "guard")
				delete(serverMap, "auth")
				delete(serverMap, "tool_response_filters")
			}
		}
	}

	logStdin.Printf("Stripped gateway extension fields for schema validation: %d servers processed", serverCount)
	return json.Marshal(config)
}

// LoadFromStdin loads configuration from stdin JSON.
func LoadFromStdin() (*Config, error) {
	logConfig.Print("Loading configuration from stdin JSON")
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("failed to read stdin: %w", err)
	}

	logConfig.Printf("Read %d bytes from stdin", len(data))

	// Pre-process: normalize "local" type to "stdio" for backward compatibility
	// This must happen before schema validation since schema only accepts "stdio" or "http"
	data, err = normalizeLocalType(data)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize configuration: %w", err)
	}

	// Pre-process: expand ${VAR} expressions before schema validation
	// This ensures the schema validates expanded values, not variable syntax
	data, err = ExpandRawJSONVariables(data)
	if err != nil {
		return nil, err
	}

	// Validate against JSON schema (fail-fast, spec-compliant).
	// Extension fields "guard" (per-server) and "guards" (top-level) are stripped from
	// a copy of the data before validation because they are not in the upstream schema.
	// "guard-policies" and "registry" are already injected into the schema by fetchAndFixSchema.
	validationData, err := stripExtensionFieldsForValidation(data)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare data for schema validation: %w", err)
	}
	if err := validateJSONSchema(validationData); err != nil {
		return nil, err
	}

	var stdinCfg StdinConfig
	if err := json.Unmarshal(data, &stdinCfg); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	logConfig.Printf("Parsed stdin config with %d servers", len(stdinCfg.MCPServers))

	// Validate additional rule-based constraints that are not fully covered by
	// schema validation alone.
	if err := validateRuleBasedPatterns(&stdinCfg); err != nil {
		return nil, err
	}

	// Validate customSchemas field (reserved type names check)
	if err := validateCustomSchemas(stdinCfg.CustomSchemas); err != nil {
		return nil, err
	}
	if stdinCfg.Gateway != nil {
		if err := validateGatewayConfig(stdinCfg.Gateway); err != nil {
			return nil, err
		}
	}

	// Convert stdin config to internal format
	cfg, err := convertStdinConfig(&stdinCfg)
	if err != nil {
		return nil, err
	}

	logConfig.Printf("Converted stdin config to internal format with %d servers", len(cfg.Servers))
	return cfg, nil
}

// convertStdinConfig converts StdinConfig to internal Config format.
func convertStdinConfig(stdinCfg *StdinConfig) (*Config, error) {
	logStdin.Printf("Converting stdin config: %d servers", len(stdinCfg.MCPServers))
	cfg := &Config{
		Servers: make(map[string]*ServerConfig),
	}

	// Convert gateway config with defaults
	if stdinCfg.Gateway != nil {
		cfg.Gateway = &GatewayConfig{
			Port:                    intPtrOrDefault(stdinCfg.Gateway.Port, DefaultPort),
			AgentID:                 stdinCfg.Gateway.AgentID,
			AgentIDs:                append([]string{}, stdinCfg.Gateway.AgentIDs...),
			APIKey:                  stdinCfg.Gateway.APIKey,
			Domain:                  stdinCfg.Gateway.Domain,
			StartupTimeout:          intPtrOrDefault(stdinCfg.Gateway.StartupTimeout, DefaultStartupTimeout),
			Dockerless:              stdinCfg.Gateway.Dockerless,
			ContainerRuntime:        stdinCfg.Gateway.ContainerRuntime,
			ContainerRuntimeCommand: stdinCfg.Gateway.ContainerRuntimeCommand,
			ContainerRuntimeArgs:    append([]string{}, stdinCfg.Gateway.ContainerRuntimeArgs...),
			KeepaliveInterval:       intPtrOrDefault(stdinCfg.Gateway.KeepaliveInterval, DefaultKeepaliveInterval),
		}
		cfg.Gateway.normalizeAgentID(stdinCfg.Gateway.agentIDSet, stdinCfg.Gateway.legacyAPIKeySet, "stdin JSON")
		if stdinCfg.Gateway.ToolTimeout != nil {
			cfg.Gateway.ToolTimeout = *stdinCfg.Gateway.ToolTimeout
		} else {
			cfg.Gateway.ToolTimeout = toolTimeoutEnvOrDefault()
		}
		if stdinCfg.Gateway.PayloadDir != "" {
			cfg.Gateway.PayloadDir = stdinCfg.Gateway.PayloadDir
		}
		if stdinCfg.Gateway.PayloadPathPrefix != nil {
			cfg.Gateway.PayloadPathPrefix = *stdinCfg.Gateway.PayloadPathPrefix
		}
		if stdinCfg.Gateway.PayloadSizeThreshold != nil {
			cfg.Gateway.PayloadSizeThreshold = *stdinCfg.Gateway.PayloadSizeThreshold
		}
		if stdinCfg.Gateway.TrustedBots != nil {
			if err := validateTrustedBots(stdinCfg.Gateway.TrustedBots); err != nil {
				return nil, err
			}
			cfg.Gateway.TrustedBots = stdinCfg.Gateway.TrustedBots
		}
		if stdinCfg.Gateway.ForcePublicRepos != nil {
			cfg.Gateway.ForcePublicRepos = stdinCfg.Gateway.ForcePublicRepos
		}
		if len(stdinCfg.Gateway.SinkVisibilityExemptServers) > 0 {
			cfg.Gateway.SinkVisibilityExemptServers = stdinCfg.Gateway.SinkVisibilityExemptServers
		}
		if len(stdinCfg.Gateway.AgentPolicies) > 0 {
			cfg.Gateway.AgentPolicies = stdinCfg.Gateway.AgentPolicies
		}
	} else {
		logStdin.Print("No gateway config in stdin, applying defaults")
		cfg.Gateway = &GatewayConfig{}
		applyGatewayDefaults(cfg.Gateway)
		// Apply MCP_GATEWAY_TOOL_TIMEOUT env var if set. toolTimeoutEnvOrDefault() returns
		// the env var value when valid and present, otherwise DefaultToolTimeout (which
		// applyGatewayDefaults already wrote). This is a no-op when the env var is absent.
		cfg.Gateway.ToolTimeout = toolTimeoutEnvOrDefault()
	}

	logStdin.Printf("Gateway configured: port=%d, toolTimeout=%d, startupTimeout=%d",
		cfg.Gateway.Port, cfg.Gateway.ToolTimeout, cfg.Gateway.StartupTimeout)

	// Apply feature-specific defaults
	applyDefaults(cfg)

	// Convert servers
	stdioRuntimeCfg := stdioRuntimeConfig{
		Command: effectiveContainerRuntimeCommand(cfg.Gateway),
	}
	if cfg.Gateway != nil && len(cfg.Gateway.ContainerRuntimeArgs) > 0 {
		stdioRuntimeCfg.Args = append([]string{}, cfg.Gateway.ContainerRuntimeArgs...)
	}

	for name, server := range stdinCfg.MCPServers {
		serverCfg, err := convertStdinServerConfigWithRuntime(name, server, stdinCfg.CustomSchemas, stdioRuntimeCfg)
		if err != nil {
			return nil, err
		}
		cfg.Servers[name] = serverCfg
	}

	// Convert guards
	if len(stdinCfg.Guards) > 0 {
		logStdin.Printf("Converting %d guard configuration(s)", len(stdinCfg.Guards))
		cfg.Guards = make(map[string]*GuardConfig)
		for name, guard := range stdinCfg.Guards {
			logStdin.Printf("Registering guard: name=%s, type=%s", name, guard.Type)
			cfg.Guards[name] = &GuardConfig{
				Type:   guard.Type,
				Path:   guard.Path,
				Config: guard.Config,
				Policy: guard.Policy,
			}
		}
	}

	// Apply feature-specific stdin conversions
	applyStdinConverters(cfg, stdinCfg)

	if err := validateGuardPolicies(cfg); err != nil {
		return nil, err
	}

	if err := validateAgentPolicies(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// convertStdinServerConfig converts a single StdinServerConfig to ServerConfig.
func convertStdinServerConfig(name string, server *StdinServerConfig, customSchemas map[string]interface{}) (*ServerConfig, error) {
	return convertStdinServerConfigWithRuntime(name, server, customSchemas, stdioRuntimeConfig{
		Command: DefaultContainerRuntime,
	})
}

// stdioRuntimeConfig carries resolved container runtime settings used when
// converting JSON stdin `container` server entries into internal stdio commands.
type stdioRuntimeConfig struct {
	// Command is the runtime executable/binary to invoke (e.g., docker, podman).
	Command string
	// Args are runtime-level arguments inserted before the "run" subcommand.
	Args []string
}

func convertStdinServerConfigWithRuntime(name string, server *StdinServerConfig, customSchemas map[string]interface{}, runtimeCfg stdioRuntimeConfig) (*ServerConfig, error) {
	// Validate server configuration (fail-fast) with custom schemas support
	if err := validateServerConfigWithCustomSchemas(name, server, customSchemas); err != nil {
		return nil, err
	}

	// Expand variable expressions in map fields (fail-fast on undefined vars)
	if err := expandMapInPlace(&server.Env, name, "environment variable(s)"); err != nil {
		return nil, err
	}
	if err := expandMapInPlace(&server.Headers, name, "HTTP header(s)"); err != nil {
		return nil, err
	}

	// server.Type has been normalized (empty and "local" -> "stdio") by validateServerConfigWithCustomSchemas above.
	serverType := server.Type

	logStdin.Printf("Converting server %q: type=%s", name, serverType)

	// Handle HTTP servers
	if serverType == "http" {
		logConfig.Printf("Configured HTTP MCP server: name=%s, url=%s", name, server.URL)
		log.Printf("[CONFIG] Configured HTTP MCP server: %s -> %s", name, server.URL)
		serverCfg := &ServerConfig{
			Type:       "http",
			URL:        server.URL,
			Headers:    server.Headers,
			CACertFile: server.CACertFile,
		}
		applyCommonServerConfigFields(serverCfg, server)
		if server.ConnectTimeout != nil {
			serverCfg.ConnectTimeout = *server.ConnectTimeout
		}
		if server.ConnectTimeout != nil || server.ToolTimeout != nil {
			var connectTimeout any
			if server.ConnectTimeout != nil {
				connectTimeout = *server.ConnectTimeout
			}
			var toolTimeout any
			if server.ToolTimeout != nil {
				toolTimeout = *server.ToolTimeout
			}
			logStdin.Printf("HTTP server %q: custom timeouts configured: connectTimeout=%v, toolTimeout=%v", name, connectTimeout, toolTimeout)
		}
		if server.Auth != nil {
			serverCfg.Auth = &AuthConfig{
				Type:     server.Auth.Type,
				Audience: server.Auth.Audience,
			}
			// Default audience to server URL if not specified
			if serverCfg.Auth.Audience == "" {
				serverCfg.Auth.Audience = server.URL
			}
			logStdin.Printf("HTTP server %q: authentication configured: type=%s, audience=%s", name, serverCfg.Auth.Type, serverCfg.Auth.Audience)
		}
		return serverCfg, nil
	}

	// stdio/local servers only from this point
	return buildStdioServerConfigWithRuntime(name, server, runtimeCfg), nil
}

// buildStdioServerConfig builds a ServerConfig for a stdio server.
func buildStdioServerConfig(name string, server *StdinServerConfig) *ServerConfig {
	return buildStdioServerConfigWithRuntime(name, server, stdioRuntimeConfig{
		Command: DefaultContainerRuntime,
	})
}

func buildStdioServerConfigWithRuntime(name string, server *StdinServerConfig, runtimeCfg stdioRuntimeConfig) *ServerConfig {
	// Defensive fallback for unit-level callers that bypass convertStdinConfig's
	// runtime resolution path. Production conversion always provides a command.
	if runtimeCfg.Command == "" {
		panic("stdio runtime command must not be empty")
	}

	args := append([]string{}, runtimeCfg.Args...)
	args = append(args,
		"run",
		"--rm",
		"-i",
		// Standard environment variables for better Docker compatibility
		"-e", "NO_COLOR=1",
		"-e", "TERM=dumb",
		"-e", "PYTHONUNBUFFERED=1",
	)

	// Add entrypoint override if specified
	if server.Entrypoint != "" {
		logStdin.Printf("Server %q: using custom entrypoint %q", name, server.Entrypoint)
		args = append(args, "--entrypoint", server.Entrypoint)
	}

	// Add volume mounts if specified
	for _, mount := range server.Mounts {
		args = append(args, "-v", mount)
	}
	if len(server.Mounts) > 0 {
		logStdin.Printf("Server %q: added %d volume mount(s)", name, len(server.Mounts))
	}

	// Add user-specified environment variables
	// Empty string "" means passthrough from host (just -e KEY)
	// Non-empty string means explicit value (-e KEY=value)
	for k, v := range server.Env {
		args = append(args, "-e")
		if v == "" {
			// Passthrough from host environment
			args = append(args, k)
		} else {
			// Explicit value
			args = append(args, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Add additional Docker runtime arguments (passed before container image)
	// e.g., "--network", "host"
	if len(server.Args) > 0 {
		logStdin.Printf("Server %q: adding %d extra Docker runtime arg(s)", name, len(server.Args))
	}
	args = append(args, server.Args...)

	runtimeArgs := append([]string(nil), args...)

	// Add container name
	args = append(args, server.Container)

	// Add entrypoint args
	if len(server.EntrypointArgs) > 0 {
		logStdin.Printf("Server %q: adding %d entrypoint arg(s)", name, len(server.EntrypointArgs))
	}
	args = append(args, server.EntrypointArgs...)

	logStdin.Printf("Server %q: configured stdio container=%s, env_vars=%d, mounts=%d", name, server.Container, len(server.Env), len(server.Mounts))
	logConfig.Printf("Configured stdio MCP server: name=%s, container=%s", name, server.Container)

	serverCfg := &ServerConfig{
		Type:                 "stdio",
		Containerized:        true,
		ContainerRuntimeArgs: runtimeArgs,
		Command:              runtimeCfg.Command,
		Args:                 args,
		Env:                  make(map[string]string),
	}
	applyCommonServerConfigFields(serverCfg, server)
	return serverCfg
}

// applyCommonServerConfigFields sets the ServerConfig fields that are shared
// between HTTP and stdio server configurations.
func applyCommonServerConfigFields(cfg *ServerConfig, src *StdinServerConfig) {
	cfg.Tools = src.Tools
	cfg.Prompts = src.Prompts
	cfg.ToolResponseFilters = src.ToolResponseFilters
	cfg.Registry = src.Registry
	cfg.GuardPolicies = src.GuardPolicies
	cfg.Guard = src.Guard
	if src.ToolTimeout != nil {
		cfg.ToolTimeout = *src.ToolTimeout
	}
}

// normalizeLocalType normalizes "local" type to "stdio" for backward compatibility.
// This allows the configuration to pass schema validation which only accepts "stdio" or "http".
func normalizeLocalType(data []byte) ([]byte, error) {
	var rawConfig map[string]interface{}
	if err := json.Unmarshal(data, &rawConfig); err != nil {
		return nil, err
	}

	// Check if mcpServers exists
	mcpServers, ok := rawConfig["mcpServers"]
	if !ok {
		return data, nil // No mcpServers, return as is
	}

	servers, ok := mcpServers.(map[string]interface{})
	if !ok {
		return data, nil // mcpServers is not a map, return as is
	}

	logStdin.Printf("Checking %d server(s) for 'local' type normalization", len(servers))

	// Iterate through servers and normalize "local" to "stdio"
	modified := false
	for _, serverConfig := range servers {
		server, ok := serverConfig.(map[string]interface{})
		if !ok {
			continue
		}

		if typeVal, exists := server["type"]; exists {
			if typeStr, ok := typeVal.(string); ok && typeStr == "local" {
				server["type"] = "stdio"
				modified = true
			}
		}
	}

	// If we modified anything, re-marshal the data
	if modified {
		logStdin.Print("Normalized 'local' server type to 'stdio' for backward compatibility")
		return json.Marshal(rawConfig)
	}

	return data, nil
}
