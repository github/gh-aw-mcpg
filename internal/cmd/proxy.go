package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/proxy"
	"github.com/github/gh-aw-mcpg/internal/tracing"
	"github.com/spf13/cobra"
)

var logProxyCmd = logger.New("cmd:proxy")

// Proxy subcommand flag variables
var (
	proxyGuardWasm      string
	proxyPolicy         string
	proxyToken          string
	proxyListen         string
	proxyLogDir         string
	proxyDIFCMode       string
	proxyAPIURL         string
	proxyTLS            bool
	proxyTLSDir         string
	proxyTrustedBots    []string
	proxyTrustedUsers   []string
	proxyOTLPEndpoint   string
	proxyOTLPService    string
	proxyOTLPSampleRate float64
)

func init() {
	rootCmd.AddCommand(newProxyCmd())
}

// containerGuardWasmPath is the baked-in guard path in the container image.
const containerGuardWasmPath = "/guards/github/00-github-guard.wasm"

// detectGuardWasm returns the baked-in container guard path if it exists,
// or empty string if not found (requiring the user to specify --guard-wasm).
func detectGuardWasm() string {
	logProxyCmd.Printf("Checking for baked-in guard at %s", containerGuardWasmPath)
	if _, err := os.Stat(containerGuardWasmPath); err == nil {
		logProxyCmd.Printf("Auto-detected baked-in guard: %s", containerGuardWasmPath)
		return containerGuardWasmPath
	}
	logProxyCmd.Print("Baked-in guard not found, --guard-wasm flag required")
	return ""
}

func newProxyCmd() *cobra.Command {
	defaultGuard := detectGuardWasm()

	cmd := &cobra.Command{
		Use:   "proxy",
		Short: "Run as a GitHub API filtering proxy",
		Long: `Run the gateway in proxy mode — an HTTP(S) forward proxy that intercepts
gh CLI requests and applies DIFC filtering using the same guard WASM module.

Container usage (uses baked-in guard automatically):

  docker run --rm -p 8443:8443 \
    -e GITHUB_TOKEN \
    -v /tmp/proxy-logs:/tmp/gh-aw/mcp-logs \
    ghcr.io/github/gh-aw-mcpg:latest proxy \
    --policy '{"allow-only":{"repos":["org/repo"],"min-integrity":"approved"}}' \
    --listen 0.0.0.0:8443 \
    --tls

  # Trust the CA cert from the mounted volume
  export GH_HOST=localhost:8443
  export NODE_EXTRA_CA_CERTS=/tmp/proxy-logs/proxy-tls/ca.crt
  gh issue list -R org/repo

Local usage:

  awmg proxy \
    --guard-wasm guards/github-guard/github_guard.wasm \
    --policy '{"allow-only":{"repos":["org/repo"],"min-integrity":"approved"}}' \
    --listen localhost:8443 --tls`,
		SilenceUsage: true,
		RunE:         runProxy,
	}

	guardHelp := "Path to the guard WASM module"
	if defaultGuard != "" {
		guardHelp += " (auto-detected: " + defaultGuard + ")"
	} else {
		guardHelp += " (required)"
	}
	cmd.Flags().StringVar(&proxyGuardWasm, "guard-wasm", defaultGuard, guardHelp)
	cmd.Flags().StringVar(&proxyPolicy, "policy", os.Getenv("MCP_GATEWAY_GUARD_POLICY_JSON"), "Guard policy JSON")
	cmd.Flags().StringVar(&proxyToken, "github-token", "", "Fallback GitHub API token (default: forwards client Authorization header)")
	cmd.Flags().StringVarP(&proxyListen, "listen", "l", "127.0.0.1:8080", "Proxy listen address")
	cmd.Flags().StringVar(&proxyLogDir, "log-dir", getDefaultLogDir(), "Log file directory")
	cmd.Flags().StringVar(&proxyDIFCMode, "guards-mode", "filter", "DIFC enforcement mode: strict, filter, propagate")
	cmd.Flags().StringVar(&proxyAPIURL, "github-api-url", "", "Upstream GitHub API URL (default: auto-derived from GITHUB_API_URL or GITHUB_SERVER_URL, falls back to https://api.github.com)")
	cmd.Flags().BoolVar(&proxyTLS, "tls", false, "Enable HTTPS with auto-generated self-signed certificates")
	cmd.Flags().StringVar(&proxyTLSDir, "tls-dir", "", "Directory for TLS certificates (default: <log-dir>/proxy-tls)")
	cmd.Flags().StringSliceVar(&proxyTrustedBots, "trusted-bots", nil, "Additional trusted bot usernames (comma-separated, extends built-in list)")
	cmd.Flags().StringSliceVar(&proxyTrustedUsers, "trusted-users", nil, "User logins that receive approved integrity (comma-separated)")
	cmd.Flags().StringVar(&proxyOTLPEndpoint, "otlp-endpoint", getDefaultOTLPEndpoint(),
		"OTLP HTTP endpoint for trace export (e.g. http://localhost:4318). Tracing is disabled when empty.")
	cmd.Flags().StringVar(&proxyOTLPService, "otlp-service-name", getDefaultOTLPServiceName(),
		"Service name reported in traces.")
	cmd.Flags().Float64Var(&proxyOTLPSampleRate, "otlp-sample-rate", config.DefaultTracingSampleRate,
		"Fraction of traces to sample and export (0.0–1.0).")

	// Only require --guard-wasm when no baked-in guard is available
	if defaultGuard == "" {
		cmd.MarkFlagRequired("guard-wasm")
	}

	// Enum completions for proxy DIFC flag
	cmd.RegisterFlagCompletionFunc("guards-mode", cobra.FixedCompletions(
		[]string{"strict", "filter", "propagate"}, cobra.ShellCompDirectiveNoFileComp))

	return cmd
}

func runProxy(cmd *cobra.Command, args []string) error {
	ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logProxyCmd.Printf("Starting proxy: listen=%s, guard=%s, mode=%s, tls=%v", proxyListen, proxyGuardWasm, proxyDIFCMode, proxyTLS)

	if _, err := difc.ParseEnforcementMode(proxyDIFCMode); err != nil {
		return fmt.Errorf("invalid --guards-mode flag: %w", err)
	}

	// Initialize loggers
	logger.InitProxyLoggers(proxyLogDir)

	logger.LogInfo("startup", "MCPG Proxy starting: listen=%s, guard=%s, mode=%s, tls=%v", proxyListen, proxyGuardWasm, proxyDIFCMode, proxyTLS)

	// Initialize OpenTelemetry tracer provider for the proxy server.
	// When no endpoint is configured, a noop provider is used (zero overhead).
	var tracingCfg *config.TracingConfig
	if proxyOTLPEndpoint != "" {
		tracingCfg = &config.TracingConfig{
			Endpoint:    proxyOTLPEndpoint,
			ServiceName: proxyOTLPService,
			SampleRate:  &proxyOTLPSampleRate,
		}
	}
	tracingProvider, err := tracing.InitProvider(ctx, tracingCfg)
	if err != nil {
		log.Printf("Warning: failed to initialize tracing provider: %v", err)
		tracingProvider, _ = tracing.InitProvider(ctx, nil)
	}
	defer func() {
		shutdownCtx, cancelTracing := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelTracing()
		if err := tracingProvider.Shutdown(shutdownCtx); err != nil {
			log.Printf("Warning: tracing provider shutdown error: %v", err)
		}
	}()
	if tracingCfg != nil {
		log.Printf("OpenTelemetry tracing enabled for proxy: endpoint=%s, service=%s", proxyOTLPEndpoint, proxyOTLPService)
		logger.LogInfo("startup", "OpenTelemetry tracing enabled for proxy: endpoint=%s, service=%s", proxyOTLPEndpoint, proxyOTLPService)
	} else {
		log.Printf("OpenTelemetry tracing disabled for proxy (no --otlp-endpoint configured)")
	}

	// Resolve GitHub token (optional — proxy forwards client auth by default)
	token := proxyToken
	if token == "" {
		token = envutil.LookupGitHubToken()
	}
	if token != "" {
		logger.LogInfo("startup", "Fallback GitHub token configured from flag/env")
	} else {
		logger.LogInfo("startup", "No fallback token — proxy will forward client Authorization headers")
	}

	// Resolve GitHub API URL: flag → env vars → default
	apiURL := proxyAPIURL
	if apiURL == "" {
		apiURL = proxy.DeriveGitHubAPIURL()
	}
	if apiURL == "" {
		apiURL = proxy.DefaultGitHubAPIBase
	}
	logger.LogInfo("startup", "Upstream GitHub API URL: %s", apiURL)
	logProxyCmd.Printf("Resolved GitHub API URL: %s, explicit flag=%v", apiURL, proxyAPIURL != "")

	// Create the proxy server
	proxySrv, err := proxy.New(ctx, proxy.Config{
		WasmPath:     proxyGuardWasm,
		Policy:       proxyPolicy,
		GitHubToken:  token,
		GitHubAPIURL: apiURL,
		DIFCMode:     proxyDIFCMode,
		TrustedBots:  proxyTrustedBots,
		TrustedUsers: proxyTrustedUsers,
	})
	if err != nil {
		return fmt.Errorf("failed to create proxy server: %w", err)
	}

	// Generate TLS certificates if requested
	var tlsCfg *proxy.TLSConfig
	if proxyTLS {
		tlsDir := proxyTLSDir
		if tlsDir == "" {
			tlsDir = filepath.Join(proxyLogDir, "proxy-tls")
		}
		logProxyCmd.Printf("Generating TLS certificates in: %s", tlsDir)
		tlsCfg, err = proxy.GenerateSelfSignedTLS(tlsDir)
		if err != nil {
			return fmt.Errorf("failed to generate TLS certificates: %w", err)
		}
		logger.LogInfo("startup", "TLS certificates generated: ca=%s", tlsCfg.CACertPath)
	}

	// Create the HTTP server
	httpServer := &http.Server{
		Addr:    proxyListen,
		Handler: proxySrv.Handler(),
	}
	if tlsCfg != nil {
		httpServer.TLSConfig = tlsCfg.Config
	}

	// Start server in background
	go func() {
		listener, err := net.Listen("tcp", proxyListen)
		if err != nil {
			log.Printf("Failed to listen on %s: %v", proxyListen, err)
			cancel()
			return
		}

		if tlsCfg != nil {
			listener = tls.NewListener(listener, tlsCfg.Config)
		}

		actualAddr := listener.Addr().String()
		scheme := "http"
		if tlsCfg != nil {
			scheme = "https"
		}

		log.Printf("MCPG Proxy listening on %s://%s", scheme, actualAddr)
		logger.LogInfo("startup", "Proxy listening on %s://%s", scheme, actualAddr)

		// Print connection info
		fmt.Fprintf(os.Stderr, "\nMCPG GitHub API Proxy\n")
		fmt.Fprintf(os.Stderr, "  Listening: %s://%s\n", scheme, actualAddr)
		fmt.Fprintf(os.Stderr, "  Upstream:  %s\n", apiURL)
		fmt.Fprintf(os.Stderr, "  Mode:      %s\n", proxyDIFCMode)
		fmt.Fprintf(os.Stderr, "  Guard:     %s\n", proxyGuardWasm)
		if tlsCfg != nil {
			fmt.Fprintf(os.Stderr, "  CA cert:   %s\n", tlsCfg.CACertPath)
			fmt.Fprintf(os.Stderr, "\nConnect with:\n")
			fmt.Fprintf(os.Stderr, "  export GH_HOST=%s\n", clientAddr(actualAddr))
			fmt.Fprintf(os.Stderr, "  export NODE_EXTRA_CA_CERTS=%s\n", tlsCfg.CACertPath)
			fmt.Fprintf(os.Stderr, "  gh issue list -R org/repo\n\n")
		} else {
			fmt.Fprintf(os.Stderr, "\nConnect with:\n")
			fmt.Fprintf(os.Stderr, "  curl http://%s/repos/org/repo/issues\n\n", actualAddr)
		}

		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
			cancel()
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	log.Println("Shutting down proxy...")
	logger.LogInfo("shutdown", "Proxy shutting down")

	return httpServer.Close()
}

// clientAddr returns a client-friendly address from a listener address.
// When the host is a wildcard (0.0.0.0, ::, or empty), it substitutes
// "localhost" so the printed GH_HOST value is usable from a client.
func clientAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return net.JoinHostPort("localhost", port)
	}
	return addr
}
