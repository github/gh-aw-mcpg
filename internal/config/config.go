package config

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/BurntSushi/toml"
	"github.com/githubnext/gh-aw-mcpg/internal/logger"
)

var logConfig = logger.New("config:config")

const (
	// DefaultPort is the default port for the gateway HTTP server
	DefaultPort = 3000
	// DefaultStartupTimeout is the default timeout for backend server startup (seconds)
	DefaultStartupTimeout = 60
	// DefaultToolTimeout is the default timeout for tool execution (seconds)
	DefaultToolTimeout = 120
	// DefaultPayloadDir is the default directory for storing large payload files
	DefaultPayloadDir = "/tmp/jq-payloads"
)

// Config represents the MCPG configuration
type Config struct {
	Servers          map[string]*ServerConfig `toml:"servers"`
	Guards           map[string]*GuardConfig  `toml:"guards"`            // Guard configurations (optional, experimental)
	EnableDIFC       bool                     `toml:"enable_difc"`       // When true, enables DIFC enforcement and requires sys___init call before tool access. Default is false for standard MCP client compatibility.
	SequentialLaunch bool                     `toml:"sequential_launch"` // When true, launches MCP servers sequentially during startup. Default is false (parallel launch).
	Gateway          *GatewayConfig           `toml:"gateway"`           // Gateway configuration (port, API key, etc.)
}

// GatewayConfig represents gateway-level configuration
type GatewayConfig struct {
	Port           int    `toml:"port"`
	APIKey         string `toml:"api_key"`
	Domain         string `toml:"domain"`
	StartupTimeout int    `toml:"startup_timeout"` // Seconds
	ToolTimeout    int    `toml:"tool_timeout"`    // Seconds
	PayloadDir     string `toml:"payload_dir"`     // Directory for storing large payload files
}

// ServerConfig represents a single MCP server configuration
type ServerConfig struct {
	Type             string            `toml:"type"` // "stdio" | "http"
	Command          string            `toml:"command"`
	Args             []string          `toml:"args"`
	Env              map[string]string `toml:"env"`
	WorkingDirectory string            `toml:"working_directory"`
	// HTTP-specific fields
	URL     string            `toml:"url"`     // HTTP endpoint URL
	Headers map[string]string `toml:"headers"` // HTTP headers for authentication
	// Tool filtering (applies to both stdio and http servers)
	Tools []string `toml:"tools"` // Tool filter: ["*"] for all tools, or list of specific tool names
	// Guard binding (optional, experimental)
	Guard string `toml:"guard"` // Guard ID to use for this server (references a guard in the guards section)
}

// GuardConfig represents a DIFC guard configuration (experimental)
type GuardConfig struct {
	Type     string `toml:"type"`      // "wasm" for WebAssembly guards
	Path     string `toml:"path"`      // Path to WASM file (mutually exclusive with URL)
	URL      string `toml:"url"`       // URL to download WASM file from (mutually exclusive with Path)
	SHA256   string `toml:"sha256"`    // SHA256 checksum for URL downloads (required when URL is set)
	CacheDir string `toml:"cache_dir"` // Directory to cache downloaded WASM files (optional, defaults to system temp)
}

// StdinConfig represents JSON configuration from stdin
type StdinConfig struct {
	MCPServers    map[string]*StdinServerConfig `json:"mcpServers"`
	Guards        map[string]*StdinGuardConfig  `json:"guards,omitempty"` // Guard configurations (optional, experimental)
	Gateway       *StdinGatewayConfig           `json:"gateway,omitempty"`
	CustomSchemas map[string]string             `json:"customSchemas,omitempty"` // Map of custom server type names to JSON Schema URLs
}

// StdinServerConfig represents a single server from stdin JSON
type StdinServerConfig struct {
	Type           string            `json:"type"` // "stdio" | "http" ("local" supported for backward compatibility)
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Container      string            `json:"container,omitempty"`
	Entrypoint     string            `json:"entrypoint,omitempty"`
	EntrypointArgs []string          `json:"entrypointArgs,omitempty"`
	Mounts         []string          `json:"mounts,omitempty"`
	URL            string            `json:"url,omitempty"`     // For HTTP-based MCP servers
	Headers        map[string]string `json:"headers,omitempty"` // HTTP headers for authentication
	Tools          []string          `json:"tools,omitempty"`   // Tool filter: ["*"] for all tools, or list of specific tool names
	Guard          string            `json:"guard,omitempty"`   // Guard ID to use for this server (references a guard in the guards section)
}

// StdinGuardConfig represents a DIFC guard configuration from stdin JSON (experimental)
type StdinGuardConfig struct {
	Type     string `json:"type"`               // "wasm" for WebAssembly guards
	Path     string `json:"path,omitempty"`     // Path to WASM file (mutually exclusive with URL)
	URL      string `json:"url,omitempty"`      // URL to download WASM file from (mutually exclusive with Path)
	SHA256   string `json:"sha256,omitempty"`   // SHA256 checksum for URL downloads (required when URL is set)
	CacheDir string `json:"cacheDir,omitempty"` // Directory to cache downloaded WASM files (optional)
}

// StdinGatewayConfig represents gateway configuration from stdin JSON
type StdinGatewayConfig struct {
	Port           *int   `json:"port,omitempty"`
	APIKey         string `json:"apiKey,omitempty"`
	Domain         string `json:"domain,omitempty"`
	StartupTimeout *int   `json:"startupTimeout,omitempty"` // Seconds to wait for backend startup
	ToolTimeout    *int   `json:"toolTimeout,omitempty"`    // Seconds to wait for tool execution
	PayloadDir     string `json:"payloadDir,omitempty"`     // Directory for storing large payload files
}

// LoadFromFile loads configuration from a TOML file
func LoadFromFile(path string) (*Config, error) {
	logConfig.Printf("Loading configuration from file: path=%s", path)
	var cfg Config
	meta, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		// Check if it's a ParseError to provide line numbers
		if pErr, ok := err.(toml.ParseError); ok {
			return nil, fmt.Errorf("TOML parse error at line %d: %w", pErr.Position.Line, err)
		}
		return nil, fmt.Errorf("failed to decode TOML: %w", err)
	}

	// Check for unknown/undecoded keys (typo detection)
	if len(meta.Undecoded()) > 0 {
		logConfig.Printf("Warning: unknown configuration keys detected: %v", meta.Undecoded())
		// For now, just warn - we could make this strict with a flag later
		// return nil, fmt.Errorf("unknown configuration keys: %v", meta.Undecoded())
	}

	// Set default gateway config values if gateway section exists but fields are unset
	if cfg.Gateway != nil {
		if cfg.Gateway.StartupTimeout == 0 {
			cfg.Gateway.StartupTimeout = DefaultStartupTimeout
		}
		if cfg.Gateway.ToolTimeout == 0 {
			cfg.Gateway.ToolTimeout = DefaultToolTimeout
		}
		if cfg.Gateway.Port == 0 {
			cfg.Gateway.Port = DefaultPort
		}
		if cfg.Gateway.PayloadDir == "" {
			cfg.Gateway.PayloadDir = DefaultPayloadDir
		}
	}

	logConfig.Printf("Successfully loaded %d servers from TOML file", len(cfg.Servers))
	return &cfg, nil
}

// LoadFromStdin loads configuration from stdin JSON
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

	// Validate against JSON schema first (fail-fast, spec-compliant)
	if err := validateJSONSchema(data); err != nil {
		return nil, err
	}

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
	cfg := &Config{
		Servers: make(map[string]*ServerConfig),
	}

	// Store gateway config with defaults
	if stdinCfg.Gateway != nil {
		cfg.Gateway = &GatewayConfig{
			Port:           DefaultPort,
			APIKey:         stdinCfg.Gateway.APIKey,
			Domain:         stdinCfg.Gateway.Domain,
			StartupTimeout: DefaultStartupTimeout,
			ToolTimeout:    DefaultToolTimeout,
			PayloadDir:     DefaultPayloadDir,
		}
		if stdinCfg.Gateway.Port != nil {
			cfg.Gateway.Port = *stdinCfg.Gateway.Port
		}
		if stdinCfg.Gateway.StartupTimeout != nil {
			cfg.Gateway.StartupTimeout = *stdinCfg.Gateway.StartupTimeout
		}
		if stdinCfg.Gateway.ToolTimeout != nil {
			cfg.Gateway.ToolTimeout = *stdinCfg.Gateway.ToolTimeout
		}
		if stdinCfg.Gateway.PayloadDir != "" {
			cfg.Gateway.PayloadDir = stdinCfg.Gateway.PayloadDir
		}
	}

	for name, server := range stdinCfg.MCPServers {
		// Validate server configuration (fail-fast) with custom schemas support
		if err := validateServerConfigWithCustomSchemas(name, server, stdinCfg.CustomSchemas); err != nil {
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

		// Handle HTTP servers
		if serverType == "http" {
			cfg.Servers[name] = &ServerConfig{
				Type:    "http",
				URL:     server.URL,
				Headers: server.Headers,
				Tools:   server.Tools,
			}
			logConfig.Printf("Configured HTTP MCP server: name=%s, url=%s", name, server.URL)
			log.Printf("[CONFIG] Configured HTTP MCP server: %s -> %s", name, server.URL)
			continue
		}

		// stdio/local servers only from this point
		// All stdio servers use Docker containers

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

		// Add container name
		args = append(args, server.Container)

		// Add entrypoint args
		args = append(args, server.EntrypointArgs...)

		cfg.Servers[name] = &ServerConfig{
			Type:    "stdio",
			Command: "docker",
			Args:    args,
			Env:     make(map[string]string),
			Tools:   server.Tools,
			Guard:   server.Guard, // Bind guard to server
		}
	}

	// Convert guards configuration
	if len(stdinCfg.Guards) > 0 {
		cfg.Guards = make(map[string]*GuardConfig)
		for name, guard := range stdinCfg.Guards {
			logConfig.Printf("Processing guard: name=%s, type=%s", name, guard.Type)

			// Validate guard type
			if guard.Type != "wasm" {
				return nil, fmt.Errorf("guard '%s': unsupported type '%s' (only 'wasm' is supported)", name, guard.Type)
			}

			// Validate path/URL - must have exactly one
			hasPath := guard.Path != ""
			hasURL := guard.URL != ""

			if !hasPath && !hasURL {
				return nil, fmt.Errorf("guard '%s': either path or url is required for wasm guards", name)
			}
			if hasPath && hasURL {
				return nil, fmt.Errorf("guard '%s': path and url are mutually exclusive", name)
			}

			// Validate SHA256 is required for URL
			if hasURL && guard.SHA256 == "" {
				return nil, fmt.Errorf("guard '%s': sha256 is required when using url", name)
			}

			// Build guard config
			guardCfg := &GuardConfig{
				Type:     guard.Type,
				SHA256:   guard.SHA256,
				CacheDir: os.ExpandEnv(guard.CacheDir),
			}

			if hasPath {
				// Expand path (support ${VAR} syntax)
				guardCfg.Path = os.ExpandEnv(guard.Path)
				logConfig.Printf("Configured WASM guard: name=%s, path=%s", name, guardCfg.Path)
			} else {
				// Expand URL (support ${VAR} syntax for tokens, etc.)
				guardCfg.URL = os.ExpandEnv(guard.URL)
				logConfig.Printf("Configured WASM guard: name=%s, url=%s, sha256=%s", name, guardCfg.URL, guardCfg.SHA256)
			}

			cfg.Guards[name] = guardCfg
		}
	}

	logConfig.Printf("Converted stdin config to internal format with %d servers and %d guards", len(cfg.Servers), len(cfg.Guards))
	return cfg, nil
}

// normalizeLocalType normalizes "local" type to "stdio" for backward compatibility
// This allows the configuration to pass schema validation which only accepts "stdio" or "http"
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
		return json.Marshal(rawConfig)
	}

	return data, nil
}
