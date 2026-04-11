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
	"time"

	"github.com/github/gh-aw-mcpg/internal/auth"
	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/server"
	"github.com/github/gh-aw-mcpg/internal/tracing"
	"github.com/github/gh-aw-mcpg/internal/version"
	"github.com/spf13/cobra"
)

// Exported constants for use by other packages
const (
	// DefaultListenIPv4 is the default interface used by the HTTP server.
	DefaultListenIPv4 = "127.0.0.1"
	// DefaultListenPort is the default port used by the HTTP server.
	DefaultListenPort = "3000"
)

// Package-level variables that don't belong to a specific feature
var (
	debugLog = logger.New("cmd:root")
	// cliVersion stores the version string for Cobra's CLI version display.
	// This is kept separate from version.Get() because rootCmd.Version must be
	// set at initialization time (before SetVersion is called). We sync both
	// values in SetVersion() to maintain a single source of truth.
	cliVersion = "dev" // Default version, overridden by SetVersion
)

var rootCmd = &cobra.Command{
	Use:     "awmg",
	Short:   "MCPG MCP proxy server",
	Version: cliVersion,
	Long: `MCPG is a proxy server for Model Context Protocol (MCP) servers.
It provides routing, aggregation, and management of multiple MCP backend servers.`,
	SilenceUsage:      true, // Don't show help on runtime errors
	SilenceErrors:     true, // Prevent cobra from printing errors — Execute() caller handles display
	PersistentPreRunE: preRun,
	RunE:              run,
	PersistentPostRun: postRun,
}

func init() {
	// Set custom error prefix for better branding
	rootCmd.SetErrPrefix("MCPG Error:")

	// Set custom version template with enhanced formatting
	rootCmd.SetVersionTemplate(`MCPG Gateway {{.Version}}
`)

	// Register all flags from feature modules (flags_*.go files)
	registerAllFlags(rootCmd)

	// Register custom flag completions
	registerFlagCompletions(rootCmd)

	// Add completion command
	rootCmd.AddCommand(newCompletionCmd())
}

const (
	// Debug log patterns for different verbosity levels
	debugMainPackages = "cmd:*,server:*,launcher:*"
	debugAllPackages  = "*"
)

// preRun performs validation before command execution
func preRun(cmd *cobra.Command, args []string) error {
	// Apply verbosity level to logging (if DEBUG is not already set)
	// -v (1): info level, -vv (2): debug level, -vvv (3): trace level
	debugEnv := os.Getenv(logger.EnvDebug)
	if verbosity > 0 && debugEnv == "" {
		// Set DEBUG env var based on verbosity level
		// Level 1: basic info (no special DEBUG setting needed, handled by logger)
		// Level 2: enable debug logs for cmd and server packages
		// Level 3: enable all debug logs
		switch verbosity {
		case 1:
			// Info level - no special DEBUG setting (standard log output)
			log.Printf("Logging level: info (-v)")
		case 2:
			// Debug level - enable debug logs for main packages
			os.Setenv("DEBUG", debugMainPackages)
			log.Printf("Logging level: debug (-vv), DEBUG=%s", debugMainPackages)
		default:
			// Trace level (3+) - enable all debug logs
			os.Setenv("DEBUG", debugAllPackages)
			log.Printf("Logging level: trace (-vvv), DEBUG=%s", debugAllPackages)
		}
	} else if debugEnv != "" {
		log.Printf("Logging level: DEBUG=%s (from environment)", debugEnv)
	}

	return nil
}

// postRun performs cleanup after command execution
func postRun(cmd *cobra.Command, args []string) {
	// Close all loggers
	logger.CloseMarkdownLogger()
	logger.CloseJSONLLogger()
	logger.CloseServerFileLogger()
	logger.CloseToolsLogger()
	logger.CloseGlobalLogger()
}

func run(cmd *cobra.Command, args []string) error {
	// Use signal.NotifyContext for proper cancellation on SIGINT/SIGTERM
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logger.InitGatewayLoggers(logDir)

	logger.LogInfoMd("startup", "MCPG Gateway version: %s", cliVersion)

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
		logger.StartupInfo("Environment validation passed")
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

	// Validate guards mode before applying
	if _, err := difc.ParseEnforcementMode(difcMode); err != nil {
		return fmt.Errorf("invalid --guards-mode flag: %w", err)
	}

	// Apply command-line flags to config
	cfg.DIFCMode = difcMode
	cfg.SequentialLaunch = sequentialLaunch

	// Override gateway config with command-line flags
	if cfg.Gateway == nil {
		cfg.Gateway = &config.GatewayConfig{}
	}

	policyOverride, policySource, err := resolveGuardPolicyOverride(cmd)
	if err != nil {
		return fmt.Errorf("invalid guard policy configuration: %w", err)
	}
	if policyOverride != nil {
		cfg.GuardPolicy = policyOverride
		cfg.GuardPolicySource = policySource
		logger.StartupInfo("Guard policy override configured (source=%s)", policySource)
	}

	if envSinkServerIDs, exists := os.LookupEnv("MCP_GATEWAY_GUARDS_SINK_SERVER_IDS"); exists {
		logger.StartupInfo("MCP_GATEWAY_GUARDS_SINK_SERVER_IDS=%q", envSinkServerIDs)
	}

	resolvedSinkServerIDs, err := parseDIFCSinkServerIDs(difcSinkServerIDs)
	if err != nil {
		return fmt.Errorf("invalid --guards-sink-server-ids value: %w", err)
	}
	difc.SetSinkServerIDs(resolvedSinkServerIDs)
	if len(resolvedSinkServerIDs) == 0 {
		logger.StartupInfo("Guards sink server ID logging enrichment disabled (no sink server IDs configured)")
	} else {
		logger.StartupInfo("Guards sink server IDs configured for JSONL tag enrichment: %v", resolvedSinkServerIDs)
		for _, sinkServerID := range resolvedSinkServerIDs {
			if _, exists := cfg.Servers[sinkServerID]; !exists {
				logger.StartupWarn("Guards sink server ID '%s' is not configured in mcpServers", sinkServerID)
			}
		}
	}

	// Apply payload directory flag (if different from default, it was explicitly set)
	if cmd.Flags().Changed("payload-dir") {
		cfg.Gateway.PayloadDir = payloadDir
	} else if payloadDir != "" && payloadDir != config.DefaultPayloadDir {
		// Environment variable was set
		cfg.Gateway.PayloadDir = payloadDir
	}

	// Apply payload path prefix flag (if different from default, it was explicitly set)
	if cmd.Flags().Changed("payload-path-prefix") {
		cfg.Gateway.PayloadPathPrefix = payloadPathPrefix
	} else if payloadPathPrefix != "" && payloadPathPrefix != defaultPayloadPathPrefix {
		// Environment variable was set
		cfg.Gateway.PayloadPathPrefix = payloadPathPrefix
	}

	// Apply payload size threshold flag (if different from default, it was explicitly set)
	if cmd.Flags().Changed("payload-size-threshold") {
		cfg.Gateway.PayloadSizeThreshold = payloadSizeThreshold
	} else if payloadSizeThreshold != config.DefaultPayloadSizeThreshold {
		// Environment variable was set
		cfg.Gateway.PayloadSizeThreshold = payloadSizeThreshold
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

	debugLog.Printf("Server mode: %s, guards mode: %s", mode, cfg.DIFCMode)

	// Per spec §7.3: generate a random API key on startup if none is configured.
	// The generated key is set in the config so it propagates to both the HTTP
	// server authentication and the stdout configuration output (spec §5.4).
	if cfg.GetAPIKey() == "" {
		randomKey, err := auth.GenerateRandomAPIKey()
		if err != nil {
			return fmt.Errorf("failed to generate random API key: %w", err)
		}
		cfg.Gateway.APIKey = randomKey
		logger.StartupInfo("No API key configured — generated temporary random API key (spec §7.3)")
	}

	// Apply tracing flags: CLI flags override config values.
	// Merge CLI/env tracing settings into gateway config.
	if otlpEndpoint != "" || cmd.Flags().Changed("otlp-endpoint") {
		if cfg.Gateway.Tracing == nil {
			cfg.Gateway.Tracing = &config.TracingConfig{}
		}
		cfg.Gateway.Tracing.Endpoint = otlpEndpoint
	}
	if cmd.Flags().Changed("otlp-service-name") {
		if cfg.Gateway.Tracing == nil {
			cfg.Gateway.Tracing = &config.TracingConfig{}
		}
		cfg.Gateway.Tracing.ServiceName = otlpServiceName
	}
	if cmd.Flags().Changed("otlp-sample-rate") {
		if cfg.Gateway.Tracing == nil {
			cfg.Gateway.Tracing = &config.TracingConfig{}
		}
		cfg.Gateway.Tracing.SampleRate = &otlpSampleRate
	}

	// Initialize OpenTelemetry tracer provider.
	// When no endpoint is configured, a noop provider is used (zero overhead).
	var tracingCfg *config.TracingConfig
	if cfg.Gateway != nil {
		tracingCfg = cfg.Gateway.Tracing
	}
	tracingProvider, err := tracing.InitProvider(ctx, tracingCfg)
	if err != nil {
		logger.StartupWarn("Failed to initialize tracing provider: %v", err)
		// Non-fatal: continue without tracing
		tracingProvider, _ = tracing.InitProvider(ctx, nil)
	}
	defer func() {
		shutdownCtxTracing, cancelTracing := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelTracing()
		if err := tracingProvider.Shutdown(shutdownCtxTracing); err != nil {
			log.Printf("Warning: tracing provider shutdown error: %v", err)
		}
	}()

	// Apply W3C parent context from configured traceId/spanId (spec §4.1.3.6).
	// This links the gateway process lifetime span into a pre-existing trace when provided.
	ctx = tracing.ParentContext(ctx, tracingCfg)

	if tracingProvider.Tracer() != nil {
		// Log what InitProvider actually resolved (config already has env var defaults merged via CLI flags)
		endpoint := ""
		sampleRate := config.DefaultTracingSampleRate
		serviceName := config.DefaultTracingServiceName
		if tracingCfg != nil {
			endpoint = tracingCfg.Endpoint
			sampleRate = tracingCfg.GetSampleRate()
			serviceName = tracingCfg.ServiceName
		}
		if endpoint != "" {
			logger.StartupInfo("OpenTelemetry tracing enabled: endpoint=%s, service=%s, sampleRate=%.2f",
				endpoint, serviceName, sampleRate)
		} else {
			logger.StartupInfo("OpenTelemetry tracing disabled (no OTLP endpoint configured)")
		}
	}

	// Create unified MCP server (backend for both modes)
	unifiedServer, err := server.NewUnified(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create unified server: %w", err)
	}
	defer unifiedServer.Close()

	// Handle graceful shutdown via context cancellation
	go func() {
		<-ctx.Done()
		logger.LogInfoMd("shutdown", "Shutting down gateway...")
		log.Println("Shutting down...")
		unifiedServer.Close()
	}()

	// Create HTTP server based on mode
	var httpServer *http.Server
	if mode == "routed" {
		logger.StartupInfo("Starting MCPG in ROUTED mode on %s", listenAddr)
		logger.StartupInfo("Routes: /mcp/<server> where <server> is one of: %v", unifiedServer.GetServerIDs())

		// Extract API key from gateway config (spec 7.1)
		apiKey := cfg.GetAPIKey()

		httpServer = server.CreateHTTPServerForRoutedMode(listenAddr, unifiedServer, apiKey)
	} else {
		logger.StartupInfo("Starting MCPG in UNIFIED mode on %s", listenAddr)
		logger.StartupInfo("Endpoint: /mcp")

		// Extract API key from gateway config (spec 7.1)
		apiKey := cfg.GetAPIKey()

		httpServer = server.CreateHTTPServerForMCP(listenAddr, unifiedServer, apiKey)
	}
	// Register the HTTP server shutdown function so the /close handler can drain
	// in-flight requests before exiting (spec 5.1.3)
	unifiedServer.SetHTTPShutdown(httpServer.Shutdown)
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

	// Gracefully shutdown HTTP server with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	return nil
}

func resolveGuardPolicyOverride(cmd *cobra.Command) (*config.GuardPolicy, string, error) {
	cliChanged := cmd.Flags().Changed("guard-policy-json") ||
		cmd.Flags().Changed("allowonly-scope-public") ||
		cmd.Flags().Changed("allowonly-scope-owner") ||
		cmd.Flags().Changed("allowonly-scope-repo") ||
		cmd.Flags().Changed("allowonly-min-integrity")

	if cliChanged {
		if strings.TrimSpace(guardPolicyJSON) != "" {
			policy, err := config.ParseGuardPolicyJSON(guardPolicyJSON)
			if err != nil {
				return nil, "", err
			}
			return policy, "cli", nil
		}

		policy, err := config.BuildAllowOnlyPolicy(allowOnlyPublic, allowOnlyOwner, allowOnlyRepo, allowOnlyMinInt)
		if err != nil {
			return nil, "", err
		}
		return policy, "cli", nil
	}

	if envPolicyJSON := strings.TrimSpace(os.Getenv("MCP_GATEWAY_GUARD_POLICY_JSON")); envPolicyJSON != "" {
		policy, err := config.ParseGuardPolicyJSON(envPolicyJSON)
		if err != nil {
			return nil, "", err
		}
		return policy, "env", nil
	}

	_, hasScopePublic := os.LookupEnv("MCP_GATEWAY_ALLOWONLY_SCOPE_PUBLIC")
	_, hasScopeOwner := os.LookupEnv("MCP_GATEWAY_ALLOWONLY_SCOPE_OWNER")
	_, hasScopeRepo := os.LookupEnv("MCP_GATEWAY_ALLOWONLY_SCOPE_REPO")
	_, hasMinIntegrity := os.LookupEnv("MCP_GATEWAY_ALLOWONLY_MIN_INTEGRITY")

	if hasScopePublic || hasScopeOwner || hasScopeRepo || hasMinIntegrity {
		policy, err := config.BuildAllowOnlyPolicy(
			getDefaultAllowOnlyScopePublic(),
			os.Getenv("MCP_GATEWAY_ALLOWONLY_SCOPE_OWNER"),
			os.Getenv("MCP_GATEWAY_ALLOWONLY_SCOPE_REPO"),
			os.Getenv("MCP_GATEWAY_ALLOWONLY_MIN_INTEGRITY"),
		)
		if err != nil {
			return nil, "", err
		}
		return policy, "env", nil
	}

	return nil, "", nil
}

// writeGatewayConfigToStdout writes the rewritten gateway configuration to stdout
// per MCP Gateway Specification Section 5.4
func writeGatewayConfigToStdout(cfg *config.Config, listenAddr, mode string) error {
	return writeGatewayConfig(cfg, listenAddr, mode, os.Stdout)
}

func writeGatewayConfig(cfg *config.Config, listenAddr, mode string, w io.Writer) error {
	debugLog.Printf("Writing gateway config: listenAddr=%s, mode=%s, serverCount=%d", listenAddr, mode, len(cfg.Servers))

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
	debugLog.Printf("Parsed listen address: host=%s, port=%s", host, port)

	// Determine domain for gateway output URLs.
	// Use the listen host, but map wildcard bind addresses (0.0.0.0, ::) to
	// 127.0.0.1 since clients cannot connect to wildcard addresses.
	// Note: cfg.Gateway.Domain is NOT used here because the gateway output is
	// consumed by host-side tools (health checks, connectivity checks) that
	// need localhost-reachable URLs. The domain field (e.g., "host.docker.internal")
	// is applied by the downstream converter when generating agent configs for
	// container-side access.
	domain := host
	if domain == "0.0.0.0" || domain == "::" || domain == "[::]" {
		domain = "127.0.0.1"
	}

	debugLog.Printf("Resolved gateway address: host=%s, port=%s", host, port)

	// Extract API key from gateway config (per spec section 7.1)
	apiKey := cfg.GetAPIKey()
	debugLog.Printf("Gateway config: auth_enabled=%v", apiKey != "")

	debugLog.Printf("Gateway auth: apiKeyConfigured=%v", apiKey != "")

	// Build output configuration
	outputConfig := map[string]interface{}{
		"mcpServers": make(map[string]interface{}),
	}

	servers := outputConfig["mcpServers"].(map[string]interface{})

	for name, server := range cfg.Servers {
		serverConfig := map[string]interface{}{
			"type": "http",
		}

		var serverURL string
		if mode == "routed" {
			serverURL = fmt.Sprintf("http://%s:%s/mcp/%s", domain, port, name)
		} else {
			// Unified mode - all servers use /mcp endpoint
			serverURL = fmt.Sprintf("http://%s:%s/mcp", domain, port)
		}
		serverConfig["url"] = serverURL
		debugLog.Printf("Writing server config: name=%s, url=%s, toolCount=%d", name, serverURL, len(server.Tools))

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

		debugLog.Printf("Wrote server config entry: name=%s, url=%v, toolCount=%d", name, serverConfig["url"], len(server.Tools))

		servers[name] = serverConfig
	}

	// Write to output as single JSON document
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(outputConfig); err != nil {
		return fmt.Errorf("failed to encode configuration: %w", err)
	}
	debugLog.Printf("Gateway config written successfully: serverCount=%d", len(servers))

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
	cliVersion = v
	rootCmd.Version = v
	version.Set(v)
}
