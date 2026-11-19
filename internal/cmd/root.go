package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/envutil"
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
	debugLog = logger.ForFile()
	// Keep cleanup injectable so postRun can exercise its warning path.
	closeAllLoggers = logger.CloseAllLoggers
	// cliVersion stores the version string for Cobra's CLI version display.
	// This is kept separate from version.Get() because rootCmd.Version must be
	// set at initialization time (before SetVersion is called). We sync both
	// values in SetVersion() to maintain a single source of truth.
	cliVersion = "dev" // Default version, overridden by SetVersion
)

var rootCmd = &cobra.Command{
	Use:   "awmg",
	Short: "MCPG MCP proxy server",
	Long: `MCPG is a proxy server for Model Context Protocol (MCP) servers.
It provides routing, aggregation, and management of multiple MCP backend servers.`,
	Example: `  # Start in routed mode with a config file
  awmg --config config.toml --routed

  # Start in unified mode reading config from stdin
  cat config.json | awmg --config-stdin --unified --listen 0.0.0.0:3000

  # Run with debug logging
  DEBUG=* awmg --config config.toml`,
	Version: cliVersion,
	Args:    cobra.NoArgs,
	// SilenceUsage: cobra checks this on the specific subcommand to suppress usage on runtime errors.
	SilenceUsage: true,
	// SilenceErrors: cobra checks this on the root command for ALL subcommands; the Execute() caller
	// in main handles display so we suppress cobra's own error printing here.
	SilenceErrors:     true,
	PersistentPreRunE: preRun,
	RunE:              run,
	PersistentPostRun: postRun,
}

func init() {
	// Chain PersistentPreRunE/PostRun hooks through parent→child automatically.
	// Without this, a child's PersistentPreRunE replaces the parent's entirely.
	cobra.EnableTraverseRunHooks = true

	// Preserve the intentional command registration order within each group
	// (cobra sorts alphabetically by default, overriding AddGroup ordering).
	cobra.EnableCommandSorting = false

	// Set custom error prefix for better branding
	rootCmd.SetErrPrefix("MCPG Error:")

	// Provide user-friendly flag parse error messages that include the usage hint
	rootCmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w\nSee '%s --help' for usage", err, cmd.CommandPath())
	})

	// Set custom version template with enhanced formatting
	rootCmd.SetVersionTemplate(`MCPG Gateway {{.Version}}
`)

	// Disable cobra's auto-generated "completion" command since we provide a
	// custom one via newCompletionCmd().
	rootCmd.CompletionOptions.DisableDefaultCmd = true

	// Register all flags from feature modules (flags_*.go files)
	registerAllFlags(rootCmd)

	// Preserve flag registration order in help output (cobra sorts alphabetically by default)
	rootCmd.Flags().SortFlags = false
	rootCmd.PersistentFlags().SortFlags = false

	// Register custom flag completions
	registerFlagCompletions(rootCmd)

	// Group subcommands for organized help output
	rootCmd.AddGroup(
		&cobra.Group{ID: "modes", Title: "Operation Modes:"},
		&cobra.Group{ID: "utils", Title: "Utilities:"},
	)

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
	debugEnv := envutil.GetEnvString(logger.EnvDebug, "")
	if verbosity > 0 && debugEnv == "" {
		// Set DEBUG env var based on verbosity level
		// Level 1: basic info (no special DEBUG setting needed, handled by logger)
		// Level 2: enable debug logs for cmd and server packages
		// Level 3: enable all debug logs
		switch verbosity {
		case 1:
			// Info level - no special DEBUG setting.
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
	if err := closeAllLoggers(); err != nil {
		log.Printf("Warning: error closing loggers: %v", err)
	}
}

func run(cmd *cobra.Command, args []string) error {
	// Use signal.NotifyContext for proper cancellation on SIGINT/SIGTERM
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Load .env file if specified before resolving env-backed startup settings.
	if envFile != "" {
		debugLog.Printf("Loading environment from file: %s", envFile)
		if err := envutil.LoadEnvFile(envFile); err != nil {
			return fmt.Errorf("failed to load .env file: %w", err)
		}
	}

	logger.InitGatewayLoggers(logDir)

	logger.LogInfoToMarkdown("startup", "MCPG Gateway version: %s", cliVersion)

	// Log config source based on what was provided
	configSource := configFile
	if configStdin {
		configSource = "stdin"
	}
	logger.LogInfoToMarkdown("startup", "Starting MCPG with config: %s, listen: %s, log-dir: %s", configSource, listenAddr, logDir)
	debugLog.Printf("Starting MCPG with config: %s, listen: %s", configSource, listenAddr)

	resolvedWasmCacheDir, cleanupWasmCache, cacheErr := setupWasmCompilationCache(ctx, cmd.Flags().Changed("wasm-cache-dir"), wasmCacheDir, logDir)
	if cacheErr != nil {
		return cacheErr
	}
	defer cleanupWasmCache()
	logger.StartupInfo("WASM compilation cache directory: %s", resolvedWasmCacheDir)

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
		logger.LogErrorToMarkdown("startup", "Configuration validation failed:\n%s", config.FormatConfigError(err))
		return fmt.Errorf("failed to load config: %w", err)
	}

	debugLog.Printf("Configuration loaded with %d servers", len(cfg.Servers))
	log.Printf("Loaded %d MCP server(s)", len(cfg.Servers))

	// Validate after loading configuration so dockerless mode can select Podman.
	if validateEnv {
		runtimeCommand := config.EffectiveContainerRuntimeCommand(cfg.Gateway)
		debugLog.Printf("Validating execution environment with container runtime %s...", runtimeCommand)
		result := config.ValidateExecutionEnvironmentForRuntime(runtimeCommand)
		if result.IsContainerized && result.ContainerID != "" {
			result = config.ValidateContainerizedEnvironmentForRuntime(result.ContainerID, runtimeCommand)
		}
		if !result.IsValid() {
			logger.LogErrorToMarkdown("startup", "Environment validation failed: %s", result.Error())
			return fmt.Errorf("environment validation failed: %s", result.Error())
		}
		logger.StartupInfo("Environment validation passed")
	}

	// Log server names to markdown
	serverNames := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		serverNames = append(serverNames, name)
	}
	if len(serverNames) > 0 {
		logger.LogInfoToMarkdown("startup", "Loaded %d MCP server(s): %v", len(cfg.Servers), serverNames)
	} else {
		logger.LogInfoToMarkdown("startup", "Loaded %d MCP server(s)", len(cfg.Servers))
	}

	if err := applyLaunchAndGuardsOverrides(cmd, cfg); err != nil {
		return err
	}

	// Override gateway config with command-line flags
	if cfg.Gateway == nil {
		cfg.Gateway = &config.GatewayConfig{}
	}

	policyOverride, policySource, err := resolveGuardPolicyFromFlags(cmd)
	if err != nil {
		return fmt.Errorf("invalid guard policy configuration: %w", err)
	}
	debugLog.Printf("Guard policy resolved: hasOverride=%v, source=%s", policyOverride != nil, policySource)
	if policyOverride != nil {
		cfg.GuardPolicy = policyOverride
		cfg.GuardPolicySource = policySource
		logger.StartupInfo("Guard policy override configured (source=%s)", policySource)
	}

	if envSinkServerIDs, exists := envutil.LookupEnv("MCP_GATEWAY_GUARDS_SINK_SERVER_IDS"); exists {
		logger.StartupInfo("MCP_GATEWAY_GUARDS_SINK_SERVER_IDS=%q", envSinkServerIDs)
	}

	resolvedSinkServerIDs, err := difc.ParseSinkServerIDs(difcSinkServerIDs)
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

	// Apply payload flags: CLI flag takes priority; otherwise apply if env var overrides the default.
	applyFlagOrEnv(cmd, "payload-dir", &cfg.Gateway.PayloadDir, payloadDir, config.DefaultPayloadDir)
	applyFlagOrEnv(cmd, "payload-path-prefix", &cfg.Gateway.PayloadPathPrefix, payloadPathPrefix, "")
	applyFlagOrEnv(cmd, "payload-size-threshold", &cfg.Gateway.PayloadSizeThreshold, payloadSizeThreshold, config.DefaultPayloadSizeThreshold)
	applyFlagOrEnv(cmd, "url-domain-audit", &cfg.Gateway.URLDomainAudit, urlDomainAudit, false)
	logger.SetURLDomainAuditEnabled(cfg.Gateway.URLDomainAudit)

	if cfg.SequentialLaunch {
		log.Println("Sequential server launching enabled")
	} else {
		log.Println("Parallel server launching enabled (default)")
	}

	// Determine mode (default to routed if neither flag is set)
	mode := resolveServerMode(routedMode, unifiedMode)

	debugLog.Printf("Server mode: %s, guards mode: %s", mode, cfg.DIFCMode)

	delegationConfig, delegationStatePath, err := resolveDelegationProxyConfig()
	if err != nil {
		return err
	}
	cfg.Delegation = delegationConfig

	// Apply tracing flags: CLI flags and env var overrides take precedence over config values.
	applyTracingOverrides(cmd, cfg)

	// Initialize OpenTelemetry tracer provider.
	// When no endpoint is configured, a noop provider is used (zero overhead).
	var tracingCfg *config.TracingConfig
	if cfg.Gateway != nil {
		tracingCfg = cfg.Gateway.Tracing
	}
	tracingProvider, cleanupTracing := setupCommandTracing(
		ctx,
		tracingCfg,
		"Failed to initialize tracing provider: %v",
		logger.StartupWarn,
		logger.ShutdownWarn,
	)
	defer cleanupTracing()

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
		if tracingProvider.IsEnabled() {
			// When GH_AW_OTLP_ENDPOINTS is set, InitProvider uses fan-out mode and
			// it takes precedence over the primary endpoint; use the env var as the
			// display value to accurately reflect what is actually receiving spans.
			displayEndpoint := envutil.GetEnvString("GH_AW_OTLP_ENDPOINTS", "")
			if displayEndpoint == "" {
				displayEndpoint = endpoint
			}
			logger.StartupInfo("OpenTelemetry tracing enabled: endpoint=%s, service=%s, sampleRate=%.2f",
				displayEndpoint, serviceName, sampleRate)
		} else {
			logger.StartupInfo("OpenTelemetry tracing disabled (no OTLP endpoint configured)")
		}
	}

	// Create unified MCP server (backend for both modes)
	debugLog.Printf("Creating unified MCP server: mode=%s, servers=%d", mode, len(cfg.Servers))
	unifiedServer, err := server.NewUnified(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create unified server: %w", err)
	}
	debugLog.Printf("Unified MCP server created successfully")
	defer unifiedServer.Close()

	delegationControlServer, delegationControlErrCh, err := startUnifiedDelegationControl(ctx, cancel, unifiedServer, delegationConfig)
	if err != nil {
		return err
	}
	if delegationControlServer != nil {
		defer func() {
			_ = delegationControlServer.Shutdown(context.Background())
		}()
	}

	// Handle graceful shutdown via context cancellation
	go func() {
		<-ctx.Done()
		logger.LogInfoToMarkdown("shutdown", "Shutting down gateway...")
		log.Println("Shutting down...")
		unifiedServer.Close()
	}()

	// Create HTTP server based on mode, wire BaseContext and shutdown hooks.
	httpServer := buildHTTPServer(ctx, mode, listenAddr, unifiedServer, cfg.GetAgentIDs(), hmacSecret, cancel)

	// Build net.Listener — optionally wrapping with TLS (ASI-07 Phase 1).
	// Plain HTTP is still used when no TLS certificate is configured (backward compatible).
	// CLI-flag co-requirement is enforced by MarkFlagsRequiredTogether("tls-cert","tls-key")
	// in flags_tls.go; the checks below catch env-var defaults that bypass cobra's flag
	// parsing (MarkFlagsRequiredTogether only fires when flags are explicitly changed on
	// the command line).
	listener, tlsEnabled, err := setupTLSListener(listenAddr, tlsCertPath, tlsKeyPath, tlsCAPath)
	if err != nil {
		return err
	}
	if hmacSecret != "" {
		logger.StartupInfo("HMAC request signing enabled (ASI-07)")
	}

	// Write gateway configuration to stdout per spec section 5.4
	if err := writeGatewayConfigToStdout(cmd, cfg, listenAddr, mode, tlsEnabled); err != nil {
		log.Printf("Warning: failed to write gateway configuration to stdout: %v", err)
	}

	err = serveAndWait(
		ctx,
		cancel,
		httpServer,
		shutdownTimeout,
		func() {
			debugLog.Print("Shutdown signal received, initiating graceful shutdown")
		},
		func() error {
			return httpServer.Serve(listener)
		},
	)
	err = selectDelegationControlError(err, delegationControlErrCh)
	if err != nil {
		debugLog.Printf("Server exited with error: %v", err)
		return err
	}
	if err := persistUnifiedDelegationState(delegationConfig, delegationStatePath); err != nil {
		return err
	}

	return nil
}

func resolveServerMode(_ bool, unified bool) string {
	if unified {
		return "unified"
	}
	return "routed"
}

func applyLaunchAndGuardsOverrides(cmd *cobra.Command, cfg *config.Config) error {
	guardsModeFlagChanged := cmd.Flags().Changed("guards-mode")
	envMode, guardsModeEnvSet := difc.EnforcementModeFromEnv()
	if guardsModeFlagChanged {
		if _, err := difc.ParseEnforcementMode(difcMode); err != nil {
			return fmt.Errorf("invalid --guards-mode flag: %w", err)
		}
		cfg.DIFCMode = difcMode
	} else if guardsModeEnvSet {
		cfg.DIFCMode = envMode
	}

	if cmd.Flags().Changed("sequential-launch") {
		cfg.SequentialLaunch = sequentialLaunch
	}

	return nil
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(rootCmd.ErrOrStderr(), err)
		os.Exit(1)
	}
}

// SetVersion sets the version string for the CLI
func SetVersion(v string) {
	cliVersion = v
	rootCmd.Version = v
	version.Set(v)
}
