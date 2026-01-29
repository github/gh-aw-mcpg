package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/githubnext/gh-aw-mcpg/internal/config"
	"github.com/githubnext/gh-aw-mcpg/internal/logger"
	"github.com/githubnext/gh-aw-mcpg/internal/server"
	"github.com/spf13/cobra"
)

// Default values for command-line flags.
const (
	defaultConfigFile  = "" // No default config file - user must explicitly specify --config or --config-stdin
	defaultConfigStdin = false
	// DefaultListenIPv4 is the default interface used by the HTTP server.
	DefaultListenIPv4 = "127.0.0.1"
	// DefaultListenPort is the default port used by the HTTP server.
	DefaultListenPort       = "3000"
	defaultListenAddr       = DefaultListenIPv4 + ":" + DefaultListenPort
	defaultRoutedMode       = false
	defaultUnifiedMode      = false
	defaultEnvFile          = ""
	defaultEnableDIFC       = false
	defaultLogDir           = "/tmp/gh-aw/mcp-logs"
	defaultPayloadDir       = "/tmp/jq-payloads"
	defaultSequentialLaunch = false
)

var (
	configFile       string
	configStdin      bool
	listenAddr       string
	routedMode       bool
	unifiedMode      bool
	envFile          string
	enableDIFC       bool
	difcFilter       bool
	logDir           string
	payloadDir       string
	validateEnv      bool
	sequentialLaunch bool
	verbosity        int // Verbosity level: 0 (default), 1 (-v info), 2 (-vv debug), 3 (-vvv trace)
	debugLog         = logger.New("cmd:root")
	version          = "dev" // Default version, overridden by SetVersion
)

var rootCmd = &cobra.Command{
	Use:     "awmg",
	Short:   "MCPG MCP proxy server",
	Version: version,
	Long: `MCPG is a proxy server for Model Context Protocol (MCP) servers.
It provides routing, aggregation, and management of multiple MCP backend servers.`,
	SilenceUsage:      true, // Don't show help on runtime errors
	PersistentPreRunE: preRun,
	RunE:              run,
}

func init() {
	// Set custom error prefix for better branding
	rootCmd.SetErrPrefix("MCPG Error:")

	rootCmd.Flags().StringVarP(&configFile, "config", "c", defaultConfigFile, "Path to config file")
	rootCmd.Flags().BoolVar(&configStdin, "config-stdin", defaultConfigStdin, "Read MCP server configuration from stdin (JSON format). When enabled, overrides --config")
	rootCmd.Flags().StringVarP(&listenAddr, "listen", "l", defaultListenAddr, "HTTP server listen address")
	rootCmd.Flags().BoolVar(&routedMode, "routed", defaultRoutedMode, "Run in routed mode (each backend at /mcp/<server>)")
	rootCmd.Flags().BoolVar(&unifiedMode, "unified", defaultUnifiedMode, "Run in unified mode (all backends at /mcp)")
	rootCmd.Flags().StringVar(&envFile, "env", defaultEnvFile, "Path to .env file to load environment variables")
	rootCmd.Flags().BoolVar(&enableDIFC, "enable-difc", getDefaultEnableDIFC(), "Enable DIFC enforcement (sessions are auto-created from Authorization header)")
	rootCmd.Flags().BoolVar(&difcFilter, "difc-filter", getDefaultDIFCFilter(), "Enable DIFC response filtering based on path labels (requires --enable-difc)")
	rootCmd.Flags().StringVar(&logDir, "log-dir", getDefaultLogDir(), "Directory for log files (falls back to stdout if directory cannot be created)")
	rootCmd.Flags().StringVar(&payloadDir, "payload-dir", getDefaultPayloadDir(), "Directory for storing large payload files (segmented by session ID)")
	rootCmd.Flags().BoolVar(&validateEnv, "validate-env", false, "Validate execution environment (Docker, env vars) before starting")
	rootCmd.Flags().BoolVar(&sequentialLaunch, "sequential-launch", defaultSequentialLaunch, "Launch MCP servers sequentially during startup (parallel launch is default)")
	rootCmd.Flags().CountVarP(&verbosity, "verbose", "v", "Increase verbosity level (use -v for info, -vv for debug, -vvv for trace)")

	// Mark mutually exclusive flags
	rootCmd.MarkFlagsMutuallyExclusive("routed", "unified")

	// Register custom flag completions
	registerFlagCompletions(rootCmd)

	// Add completion command
	rootCmd.AddCommand(newCompletionCmd())
}

// getDefaultLogDir returns the default log directory, checking MCP_GATEWAY_LOG_DIR
// environment variable first, then falling back to the hardcoded default
func getDefaultLogDir() string {
	if envLogDir := os.Getenv("MCP_GATEWAY_LOG_DIR"); envLogDir != "" {
		return envLogDir
	}
	return defaultLogDir
}

// getDefaultPayloadDir returns the default payload directory, checking MCP_GATEWAY_PAYLOAD_DIR
// environment variable first, then falling back to the hardcoded default
func getDefaultPayloadDir() string {
	if envPayloadDir := os.Getenv("MCP_GATEWAY_PAYLOAD_DIR"); envPayloadDir != "" {
		return envPayloadDir
	}
	return defaultPayloadDir
}

// getDefaultEnableDIFC returns the default DIFC setting, checking MCP_GATEWAY_ENABLE_DIFC
// environment variable first, then falling back to the hardcoded default (false)
func getDefaultEnableDIFC() bool {
	if envDIFC := os.Getenv("MCP_GATEWAY_ENABLE_DIFC"); envDIFC != "" {
		// Accept common truthy values: "1", "true", "yes", "on"
		switch strings.ToLower(envDIFC) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return defaultEnableDIFC
}

// getDefaultDIFCFilter returns the default DIFC filter setting, checking MCP_GATEWAY_DIFC_FILTER
// environment variable first, then falling back to the hardcoded default (false)
func getDefaultDIFCFilter() bool {
	if envDIFCFilter := os.Getenv("MCP_GATEWAY_DIFC_FILTER"); envDIFCFilter != "" {
		// Accept common truthy values: "1", "true", "yes", "on"
		switch strings.ToLower(envDIFCFilter) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

const (
	// Debug log patterns for different verbosity levels
	debugMainPackages = "cmd:*,server:*,launcher:*"
	debugAllPackages  = "*"
)

// registerFlagCompletions registers custom completion functions for flags
func registerFlagCompletions(cmd *cobra.Command) {
	// Custom completion for --config flag (complete with .toml files)
	cmd.RegisterFlagCompletionFunc("config", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"toml"}, cobra.ShellCompDirectiveFilterFileExt
	})

	// Custom completion for --log-dir flag (complete with directories)
	cmd.RegisterFlagCompletionFunc("log-dir", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	})

	// Custom completion for --payload-dir flag (complete with directories)
	cmd.RegisterFlagCompletionFunc("payload-dir", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveFilterDirs
	})

	// Custom completion for --env flag (complete with .env files)
	cmd.RegisterFlagCompletionFunc("env", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return []string{"env"}, cobra.ShellCompDirectiveFilterFileExt
	})
}

// preRun performs validation before command execution
func preRun(cmd *cobra.Command, args []string) error {
	// Validate that either --config or --config-stdin is provided
	if !configStdin && configFile == "" {
		return fmt.Errorf("configuration source required: specify either --config <file> or --config-stdin")
	}

	// Apply verbosity level to logging (if DEBUG is not already set)
	// -v (1): info level, -vv (2): debug level, -vvv (3): trace level
	if verbosity > 0 && os.Getenv("DEBUG") == "" {
		// Set DEBUG env var based on verbosity level
		// Level 1: basic info (no special DEBUG setting needed, handled by logger)
		// Level 2: enable debug logs for cmd and server packages
		// Level 3: enable all debug logs
		switch verbosity {
		case 1:
			// Info level - no special DEBUG setting (standard log output)
			debugLog.Printf("Verbosity level: info")
		case 2:
			// Debug level - enable debug logs for main packages
			os.Setenv("DEBUG", debugMainPackages)
			debugLog.Printf("Verbosity level: debug (DEBUG=%s)", debugMainPackages)
		default:
			// Trace level (3+) - enable all debug logs
			os.Setenv("DEBUG", debugAllPackages)
			debugLog.Printf("Verbosity level: trace (DEBUG=%s)", debugAllPackages)
		}
	}

	return nil
}

func run(cmd *cobra.Command, args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize file logger early
	if err := logger.InitFileLogger(logDir, "mcp-gateway.log"); err != nil {
		log.Printf("Warning: Failed to initialize file logger: %v", err)
	}
	defer logger.CloseGlobalLogger()

	// Initialize markdown logger for GitHub workflow preview
	if err := logger.InitMarkdownLogger(logDir, "gateway.md"); err != nil {
		log.Printf("Warning: Failed to initialize markdown logger: %v", err)
	}
	defer logger.CloseMarkdownLogger()

	// Initialize JSONL logger for RPC message logging
	if err := logger.InitJSONLLogger(logDir, "rpc-messages.jsonl"); err != nil {
		log.Printf("Warning: Failed to initialize JSONL logger: %v", err)
	}
	defer logger.CloseJSONLLogger()

	logger.LogInfoMd("startup", "MCPG Gateway version: %s", version)

	// Log config source based on what was provided
	configSource := configFile
	if configStdin {
		configSource = "stdin"
	}
	logger.LogInfoMd("startup", "Starting MCPG with config: %s, listen: %s, log-dir: %s", configSource, listenAddr, logDir)
	debugLog.Printf("Starting MCPG with config: %s, listen: %s", configSource, listenAddr)

	// Load .env file if specified
	if envFile != "" {
		debugLog.Printf("Loading environment from file: %s", envFile)
		if err := loadEnvFile(envFile); err != nil {
			return fmt.Errorf("failed to load .env file: %w", err)
		}
	}

	// Validate execution environment if requested
	if validateEnv {
		debugLog.Printf("Validating execution environment...")
		result := config.ValidateExecutionEnvironment()
		if !result.IsValid() {
			logger.LogErrorMd("startup", "Environment validation failed: %s", result.Error())
			return fmt.Errorf("environment validation failed: %s", result.Error())
		}
		logger.LogInfoMd("startup", "Environment validation passed")
		log.Println("Environment validation passed")
	}

	// Load configuration
	var cfg *config.Config
	var err error

	if configStdin {
		log.Println("Reading configuration from stdin...")
		cfg, err = config.LoadFromStdin()
	} else {
		log.Printf("Reading configuration from %s...", configFile)
		cfg, err = config.LoadFromFile(configFile)
	}

	if err != nil {
		// Log configuration validation errors to markdown logger
		logger.LogErrorMd("startup", "Configuration validation failed:\n%s", err.Error())
		return fmt.Errorf("failed to load config: %w", err)
	}

	debugLog.Printf("Configuration loaded with %d servers", len(cfg.Servers))
	log.Printf("Loaded %d MCP server(s)", len(cfg.Servers))

	// Log server names to markdown
	serverNames := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		serverNames = append(serverNames, name)
	}
	if len(serverNames) > 0 {
		logger.LogInfoMd("startup", "Loaded %d MCP server(s): %v", len(cfg.Servers), serverNames)
	} else {
		logger.LogInfoMd("startup", "Loaded %d MCP server(s)", len(cfg.Servers))
	}

	// Apply command-line flags to config
	cfg.EnableDIFC = enableDIFC
	cfg.SequentialLaunch = sequentialLaunch

	if enableDIFC {
		log.Println("DIFC enforcement and session requirement enabled")
		if difcFilter {
			log.Println("DIFC response filtering enabled")
		}
	} else {
		log.Println("DIFC enforcement disabled (sessions auto-created for standard MCP client compatibility)")
		if difcFilter {
			log.Println("Warning: --difc-filter has no effect without --enable-difc")
		}
	}

	if sequentialLaunch {
		log.Println("Sequential server launching enabled")
	} else {
		log.Println("Parallel server launching enabled (default)")
	}

	// Determine mode (default to routed if neither flag is set)
	mode := "routed"
	if unifiedMode {
		mode = "unified"
	}

	debugLog.Printf("Server mode: %s, DIFC enabled: %v", mode, cfg.EnableDIFC)

	// Set gateway version for health endpoint reporting
	server.SetGatewayVersion(version)

	// Create unified MCP server (backend for both modes)
	unifiedServer, err := server.NewUnified(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create unified server: %w", err)
	}
	defer unifiedServer.Close()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.LogInfoMd("shutdown", "Shutting down gateway...")
		log.Println("Shutting down...")
		cancel()
		unifiedServer.Close()
		logger.CloseMarkdownLogger()
		logger.CloseGlobalLogger()
		os.Exit(0)
	}()

	// Create HTTP server based on mode
	var httpServer *http.Server
	if mode == "routed" {
		log.Printf("Starting MCPG in ROUTED mode on %s", listenAddr)
		log.Printf("Routes: /mcp/<server> where <server> is one of: %v", unifiedServer.GetServerIDs())
		logger.LogInfoMd("startup", "Starting in ROUTED mode on %s", listenAddr)
		logger.LogInfoMd("startup", "Routes: /mcp/<server> for servers: %v", unifiedServer.GetServerIDs())

		// Extract API key from gateway config (spec 7.1)
		apiKey := ""
		if cfg.Gateway != nil {
			apiKey = cfg.Gateway.APIKey
		}

		httpServer = server.CreateHTTPServerForRoutedMode(listenAddr, unifiedServer, apiKey)
	} else {
		log.Printf("Starting MCPG in UNIFIED mode on %s", listenAddr)
		log.Printf("Endpoint: /mcp")
		logger.LogInfoMd("startup", "Starting in UNIFIED mode on %s", listenAddr)
		logger.LogInfoMd("startup", "Endpoint: /mcp")

		// Extract API key from gateway config (spec 7.1)
		apiKey := ""
		if cfg.Gateway != nil {
			apiKey = cfg.Gateway.APIKey
		}

		httpServer = server.CreateHTTPServerForMCP(listenAddr, unifiedServer, apiKey)
	}
	// Start HTTP server in background
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
			cancel()
		}
	}()

	// Write gateway configuration to stdout per spec section 5.4
	if err := writeGatewayConfigToStdout(cfg, listenAddr, mode); err != nil {
		log.Printf("Warning: failed to write gateway configuration to stdout: %v", err)
	}

	// Wait for shutdown signal
	<-ctx.Done()
	return nil
}

// writeGatewayConfigToStdout writes the rewritten gateway configuration to stdout
// per MCP Gateway Specification Section 5.4
func writeGatewayConfigToStdout(cfg *config.Config, listenAddr, mode string) error {
	return writeGatewayConfig(cfg, listenAddr, mode, os.Stdout)
}

func writeGatewayConfig(cfg *config.Config, listenAddr, mode string, w io.Writer) error {
	// Parse listen address to extract host and port
	// Use net.SplitHostPort which properly handles both IPv4 and IPv6 addresses
	host, port := DefaultListenIPv4, DefaultListenPort
	if h, p, err := net.SplitHostPort(listenAddr); err == nil {
		if h != "" {
			host = h
		}
		if p != "" {
			port = p
		}
	}

	// Determine domain (use host from listen address)
	domain := host

	// Extract API key from gateway config (per spec section 7.1)
	apiKey := ""
	if cfg.Gateway != nil {
		apiKey = cfg.Gateway.APIKey
	}

	// Build output configuration
	outputConfig := map[string]interface{}{
		"mcpServers": make(map[string]interface{}),
	}

	servers := outputConfig["mcpServers"].(map[string]interface{})

	for name, server := range cfg.Servers {
		serverConfig := map[string]interface{}{
			"type": "http",
		}

		if mode == "routed" {
			serverConfig["url"] = fmt.Sprintf("http://%s:%s/mcp/%s", domain, port, name)
		} else {
			// Unified mode - all servers use /mcp endpoint
			serverConfig["url"] = fmt.Sprintf("http://%s:%s/mcp", domain, port)
		}

		// Add auth headers per MCP Gateway Specification Section 5.4
		// Authorization header contains API key directly (not Bearer scheme per spec 7.1)
		if apiKey != "" {
			serverConfig["headers"] = map[string]string{
				"Authorization": apiKey,
			}
		}

		// Include tools field from original configuration per MCP Gateway Specification v1.5.0 Section 5.4
		// This preserves tool filtering from the input configuration
		if len(server.Tools) > 0 {
			serverConfig["tools"] = server.Tools
		}

		servers[name] = serverConfig
	}

	// Write to output as single JSON document
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(outputConfig); err != nil {
		return fmt.Errorf("failed to encode configuration: %w", err)
	}

	// Flush stdout buffer if it's a regular file
	// Note: Sync() fails on pipes and character devices like /dev/stdout,
	// which is expected behavior. We only sync regular files.
	if f, ok := w.(*os.File); ok {
		if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
			if err := f.Sync(); err != nil {
				// Log warning but don't fail - sync is best-effort
				debugLog.Printf("Warning: failed to sync file: %v", err)
			}
		}
	}

	return nil
}

// loadEnvFile reads a .env file and sets environment variables
func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	log.Printf("Loading environment from %s...", path)
	scanner := bufio.NewScanner(file)
	loadedVars := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Expand $VAR references in value
		value = os.ExpandEnv(value)

		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("failed to set %s: %w", key, err)
		}

		// Log loaded variable (hide sensitive values)
		displayValue := value
		if len(value) > 0 {
			displayValue = value[:min(10, len(value))] + "..."
		}
		log.Printf("  Loaded: %s=%s", key, displayValue)
		loadedVars++
	}

	log.Printf("Loaded %d environment variables from %s", loadedVars, path)

	return scanner.Err()
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// SetVersion sets the version string for the CLI
func SetVersion(v string) {
	version = v
	rootCmd.Version = v
	config.SetGatewayVersion(v)
}
