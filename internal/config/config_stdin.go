// Package config provides configuration loading and parsing.
// This file defines stdin (JSON) configuration types.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

var logStdin = logger.New("config:config_stdin")

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
	Port           *int   `json:"port,omitempty"`
	APIKey         string `json:"apiKey,omitempty"`
	Domain         string `json:"domain,omitempty"`
	StartupTimeout *int   `json:"startupTimeout,omitempty"`
	ToolTimeout    *int   `json:"toolTimeout,omitempty"`
	PayloadDir     string `json:"payloadDir,omitempty"`
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

	// Tools is an optional list of tools to filter/expose
	Tools []string `json:"tools,omitempty"`

	// Registry is the URI to the installation location in an MCP registry (informational)
	Registry string `json:"registry,omitempty"`

	// GuardPolicies holds guard policies for access control at the MCP gateway level.
	// The structure is server-specific. For GitHub MCP server, see the GitHub guard policy schema.
	GuardPolicies map[string]interface{} `json:"guard-policies,omitempty"`

	// Guard is the name of the guard to use for this server (requires DIFC)
	Guard string `json:"guard,omitempty"`

	// AdditionalProperties stores any extra fields for custom server types
	// This allows custom schemas to define their own fields beyond the standard ones
	AdditionalProperties map[string]interface{} `json:"-"`
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

	// Now unmarshal into a map to capture all fields
	var allFields map[string]interface{}
	if err := json.Unmarshal(data, &allFields); err != nil {
		return err
	}

	// Known fields in the struct
	knownFields := map[string]bool{
		"type":           true,
		"container":      true,
		"entrypoint":     true,
		"entrypointArgs": true,
		"args":           true,
		"mounts":         true,
		"env":            true,
		"url":            true,
		"headers":        true,
		"tools":          true,
		"registry":       true,
		"guard-policies": true,
		"guard":          true,
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

// intPtrOrDefault returns the value of the int pointer if not nil, otherwise returns the default value.
// This helper reduces code duplication when handling optional integer fields with defaults.
func intPtrOrDefault(ptr *int, defaultValue int) int {
	if ptr != nil {
		return *ptr
	}
	return defaultValue
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

	// Validate against JSON schema (fail-fast, spec-compliant)
	// Schema validation is skipped because extension fields like "guards", "guard",
	// and "guard-policies" are not in the official schema yet.
	logConfig.Print("Skipping strict JSON schema validation (extension fields may be present)")

	var stdinCfg StdinConfig
	if err := json.Unmarshal(data, &stdinCfg); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	logConfig.Printf("Parsed stdin config with %d servers", len(stdinCfg.MCPServers))

	// Validate string patterns from schema (regex constraints)
	if err := validateStringPatterns(&stdinCfg); err != nil {
		return nil, err
	}

	// Validate customSchemas field (reserved type names check)
	if err := validateCustomSchemas(stdinCfg.CustomSchemas); err != nil {
		return nil, err
	}

	// Validate gateway configuration (additional checks)
	if err := validateGatewayConfig(stdinCfg.Gateway); err != nil {
		return nil, err
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
			Port:           intPtrOrDefault(stdinCfg.Gateway.Port, DefaultPort),
			APIKey:         stdinCfg.Gateway.APIKey,
			Domain:         stdinCfg.Gateway.Domain,
			StartupTimeout: intPtrOrDefault(stdinCfg.Gateway.StartupTimeout, DefaultStartupTimeout),
			ToolTimeout:    intPtrOrDefault(stdinCfg.Gateway.ToolTimeout, DefaultToolTimeout),
		}
		if stdinCfg.Gateway.PayloadDir != "" {
			cfg.Gateway.PayloadDir = stdinCfg.Gateway.PayloadDir
		}
	} else {
		logStdin.Print("No gateway config in stdin, applying defaults")
		cfg.Gateway = &GatewayConfig{}
		applyGatewayDefaults(cfg.Gateway)
	}

	// Apply feature-specific defaults
	applyDefaults(cfg)

	// Convert servers
	for name, server := range stdinCfg.MCPServers {
		serverCfg, err := convertStdinServerConfig(name, server, stdinCfg.CustomSchemas)
		if err != nil {
			return nil, err
		}
		cfg.Servers[name] = serverCfg
	}

	// Convert guards
	if len(stdinCfg.Guards) > 0 {
		cfg.Guards = make(map[string]*GuardConfig)
		for name, guard := range stdinCfg.Guards {
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

	return cfg, nil
}

// convertStdinServerConfig converts a single StdinServerConfig to ServerConfig.
func convertStdinServerConfig(name string, server *StdinServerConfig, customSchemas map[string]interface{}) (*ServerConfig, error) {
	// Validate server configuration (fail-fast) with custom schemas support
	if err := validateServerConfigWithCustomSchemas(name, server, customSchemas); err != nil {
		return nil, err
	}

	// Expand variable expressions in env vars (fail-fast on undefined vars)
	if len(server.Env) > 0 {
		expandedEnv, err := expandEnvVariables(server.Env, name)
		if err != nil {
			return nil, err
		}
		server.Env = expandedEnv
	}

	// Expand variable expressions in HTTP headers (fail-fast on undefined vars)
	if len(server.Headers) > 0 {
		expandedHeaders, err := expandEnvVariables(server.Headers, name)
		if err != nil {
			return nil, err
		}
		server.Headers = expandedHeaders
	}

	// Normalize type: "local" is an alias for "stdio" (backward compatibility)
	serverType := server.Type
	if serverType == "" {
		serverType = "stdio"
	}
	if serverType == "local" {
		serverType = "stdio"
	}

	logStdin.Printf("Converting server %q: type=%s", name, serverType)

	// Handle HTTP servers
	if serverType == "http" {
		logConfig.Printf("Configured HTTP MCP server: name=%s, url=%s", name, server.URL)
		log.Printf("[CONFIG] Configured HTTP MCP server: %s -> %s", name, server.URL)
		return &ServerConfig{
			Type:          "http",
			URL:           server.URL,
			Headers:       server.Headers,
			Tools:         server.Tools,
			Registry:      server.Registry,
			GuardPolicies: server.GuardPolicies,
			Guard:         server.Guard,
		}, nil
	}

	// stdio/local servers only from this point
	// All stdio servers use Docker containers
	return buildStdioServerConfig(name, server), nil
}

// buildStdioServerConfig builds a ServerConfig for a stdio server.
func buildStdioServerConfig(name string, server *StdinServerConfig) *ServerConfig {
	args := []string{
		"run",
		"--rm",
		"-i",
		// Standard environment variables for better Docker compatibility
		"-e", "NO_COLOR=1",
		"-e", "TERM=dumb",
		"-e", "PYTHONUNBUFFERED=1",
	}

	// Add entrypoint override if specified
	if server.Entrypoint != "" {
		logStdin.Printf("Server %q: using custom entrypoint %q", name, server.Entrypoint)
		args = append(args, "--entrypoint", server.Entrypoint)
	}

	// Add volume mounts if specified
	for _, mount := range server.Mounts {
		args = append(args, "-v", mount)
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
	args = append(args, server.Args...)

	// Add container name
	args = append(args, server.Container)

	// Add entrypoint args
	args = append(args, server.EntrypointArgs...)

	logConfig.Printf("Configured stdio MCP server: name=%s, container=%s", name, server.Container)

	return &ServerConfig{
		Type:          "stdio",
		Command:       "docker",
		Args:          args,
		Env:           make(map[string]string),
		Tools:         server.Tools,
		Registry:      server.Registry,
		GuardPolicies: server.GuardPolicies,
		Guard:         server.Guard,
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
