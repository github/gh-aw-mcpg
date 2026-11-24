package cmd

// Tracing-related flags and helpers for OpenTelemetry OTLP trace export.

import (
	"context"
	"time"

	"github.com/spf13/cobra"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/tracing"
)

// Tracing flag variables
var (
	otlpEndpoint    string
	otlpServiceName string
	otlpSampleRate  float64
)

func init() {
	RegisterFlag(func(cmd *cobra.Command) {
		registerTracingFlags(cmd, &otlpEndpoint, &otlpServiceName, &otlpSampleRate,
			"OTLP HTTP endpoint for trace export (e.g. http://localhost:4318). Defaults from OTEL_EXPORTER_OTLP_ENDPOINT when set. Tracing is disabled when empty.",
			"Service name reported in traces. Defaults from OTEL_SERVICE_NAME when set.",
			"Fraction of new root traces to sample and export (0.0–1.0). Default 1.0 samples all roots. Child spans inherit parent sampling decisions.")
	})
}

func registerTracingFlags(cmd *cobra.Command, endpoint *string, serviceName *string, sampleRate *float64, endpointUsage string, serviceUsage string, sampleUsage string) {
	flags := cmd.Flags()
	flags.StringVar(endpoint, "otlp-endpoint", envutil.GetEnvString("OTEL_EXPORTER_OTLP_ENDPOINT", ""),
		endpointUsage)
	flags.StringVar(serviceName, "otlp-service-name", envutil.GetEnvString("OTEL_SERVICE_NAME", config.DefaultTracingServiceName),
		serviceUsage)
	flags.Float64Var(sampleRate, "otlp-sample-rate", config.DefaultTracingSampleRate,
		sampleUsage)
}

// ensureTracingConfig returns cfg.Gateway.Tracing, initializing it if nil.
func ensureTracingConfig(cfg *config.Config) *config.TracingConfig {
	if cfg.Gateway.Tracing == nil {
		debugLog.Print("Gateway tracing config was nil, initializing empty TracingConfig")
		cfg.Gateway.Tracing = &config.TracingConfig{}
	}
	return cfg.Gateway.Tracing
}

// applyTracingOverrides merges CLI flag values and non-default env var values into
// cfg.Gateway.Tracing. It is called after config load so that explicit CLI flags and
// env var overrides take precedence over whatever was in the config file.
// applyFlagOrEnv applies a value when the flag was explicitly set, or when the current
// value differs from its built-in default (i.e. an env var has overridden the default).
func applyTracingOverrides(cmd *cobra.Command, cfg *config.Config) {
	shouldInitTracingConfig := (cfg.Gateway != nil && cfg.Gateway.Tracing != nil) ||
		cmd.Flags().Changed("otlp-endpoint") || otlpEndpoint != "" ||
		cmd.Flags().Changed("otlp-service-name") || otlpServiceName != config.DefaultTracingServiceName ||
		cmd.Flags().Changed("otlp-sample-rate")
	debugLog.Printf("Applying tracing overrides: shouldInitTracingConfig=%v", shouldInitTracingConfig)
	if shouldInitTracingConfig {
		tc := ensureTracingConfig(cfg)
		applyFlagOrEnv(cmd, "otlp-endpoint", &tc.Endpoint, otlpEndpoint, "")
		applyFlagOrEnv(cmd, "otlp-service-name", &tc.ServiceName, otlpServiceName, config.DefaultTracingServiceName)
		if cmd.Flags().Changed("otlp-sample-rate") {
			debugLog.Printf("Tracing sample rate explicitly set via flag: %v", otlpSampleRate)
			tc.SampleRate = &otlpSampleRate
		}
		debugLog.Printf("Tracing overrides applied: endpointConfigured=%v, serviceName=%q", tc.Endpoint != "", tc.ServiceName)
	} else {
		debugLog.Print("No tracing overrides needed, skipping tracing config initialization")
	}
}

func initTracingProviderWithFallback(
	ctx context.Context,
	tracingCfg *config.TracingConfig,
	initWarningFormat string,
	warnf func(format string, args ...any),
) *tracing.Provider {
	debugLog.Print("Initializing tracing provider")
	tracingProvider, err := tracing.InitProvider(ctx, tracingCfg)
	if err != nil {
		debugLog.Printf("Tracing provider init failed, falling back to no-op provider: %v", err)
		warnf(initWarningFormat, err)
		tracingProvider, _ = tracing.InitProvider(ctx, nil)
	} else {
		debugLog.Print("Tracing provider initialized successfully")
	}

	return tracingProvider
}

func shutdownTracingProviderWithTimeout(tracingProvider *tracing.Provider, warnf func(format string, args ...any)) {
	debugLog.Print("Shutting down tracing provider")
	shutdownCtxTracing, cancelTracing := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelTracing()

	if err := tracingProvider.Shutdown(shutdownCtxTracing); err != nil {
		debugLog.Printf("Tracing provider shutdown error: %v", err)
		warnf("tracing provider shutdown error: %v", err)
	} else {
		debugLog.Print("Tracing provider shut down successfully")
	}
}

func setupCommandTracing(
	ctx context.Context,
	tracingCfg *config.TracingConfig,
	initWarningFormat string,
	initWarnf func(format string, args ...any),
	shutdownWarnf func(format string, args ...any),
) (*tracing.Provider, func()) {
	tracingProvider := initTracingProviderWithFallback(ctx, tracingCfg, initWarningFormat, initWarnf)
	return tracingProvider, func() {
		shutdownTracingProviderWithTimeout(tracingProvider, shutdownWarnf)
	}
}
