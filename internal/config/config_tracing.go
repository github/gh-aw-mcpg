// Package config provides configuration loading and parsing.
// This file defines the tracing configuration for OpenTelemetry OTLP export.
package config

import "github.com/github/gh-aw-mcpg/internal/logger"

var logTracingCfg = logger.ForFile()

// DefaultTracingSampleRate is the default sample rate for tracing (100% sampling).
const DefaultTracingSampleRate = 1.0

// DefaultTracingServiceName is the default service name for tracing.
const DefaultTracingServiceName = "mcp-gateway"

// DefaultTracingExporterTimeoutSec is the default OTLP HTTP exporter timeout in seconds.
const DefaultTracingExporterTimeoutSec = 10

// TracingConfig holds OpenTelemetry tracing configuration.
// Tracing is disabled when Endpoint is empty.
//
// Configuration can also be provided via standard OTEL environment variables:
//   - OTEL_EXPORTER_OTLP_ENDPOINT — overrides Endpoint
//   - OTEL_SERVICE_NAME — overrides ServiceName
//
// Example TOML (spec §4.1.3.7, using the opentelemetry section):
//
//	[gateway.opentelemetry]
//	endpoint = "https://otel-collector.example.com"
//	service_name = "mcp-gateway"
//	trace_id = "4bf92f3577b34da6a3ce929d0e0e4736"
//	span_id = "00f067aa0ba902b7"
//	headers = "Authorization=Bearer ${OTEL_TOKEN}"
type TracingConfig struct {
	// Endpoint is the OTLP HTTP endpoint to export traces to.
	// When using the opentelemetry section (spec §4.1.3.7), this MUST be an HTTP or HTTPS URL.
	// If empty, tracing is disabled and a noop tracer is used.
	Endpoint string `toml:"endpoint" json:"endpoint,omitempty"`

	// Headers is a comma-separated list of key=value HTTP headers sent with every OTLP
	// export request (e.g. "Authorization=Bearer ${OTEL_TOKEN},X-Custom=value").
	// Supports ${VAR} variable expansion (expanded at config load time).
	Headers string `toml:"headers" json:"headers,omitempty"`

	// TraceID is an optional W3C trace ID (32-char lowercase hex) used to construct the
	// parent traceparent header, linking gateway spans into a pre-existing trace.
	// Supports ${VAR} variable expansion (expanded at config load time).
	// Must be 32 lowercase hex characters and must not be all zeros.
	TraceID string `toml:"trace_id" json:"traceId,omitempty"`

	// SpanID is an optional W3C span ID (16-char lowercase hex) paired with TraceID
	// to construct the parent traceparent header. Ignored when TraceID is absent.
	// Supports ${VAR} variable expansion (expanded at config load time).
	// Must be 16 lowercase hex characters and must not be all zeros.
	SpanID string `toml:"span_id" json:"spanId,omitempty"`

	// ServiceName is the service name reported in traces.
	// Defaults to "mcp-gateway".
	ServiceName string `toml:"service_name" json:"serviceName,omitempty"`

	// SampleRate controls the fraction of traces that are sampled and exported.
	// Valid range: 0.0 (no sampling) to 1.0 (sample everything).
	// Defaults to 1.0 (100% sampling).
	// Uses a pointer so that 0.0 can be distinguished from "unset".
	// Note: SampleRate is a gateway extension field not present in spec §4.1.3.7.
	SampleRate *float64 `toml:"sample_rate" json:"sampleRate,omitempty"`

	// SignalPath is the OTLP signal path appended to the base endpoint URL.
	// Defaults to "/v1/traces" per the OpenTelemetry specification.
	// Override this only if your collector uses a non-standard ingest path.
	SignalPath string `toml:"signal_path" json:"signalPath,omitempty"`

	// ExporterTimeoutSec is the per-request timeout in seconds for the OTLP HTTP exporter.
	// This controls how long the exporter waits for each export RPC to the collector.
	// Defaults to 10 seconds when unset or zero.
	ExporterTimeoutSec int `toml:"exporter_timeout" json:"exporterTimeout,omitempty"`
}

// GetSampleRate returns the configured sample rate, defaulting to 1.0 if unset.
func (c *TracingConfig) GetSampleRate() float64 {
	if c == nil || c.SampleRate == nil {
		logTracingCfg.Printf("Sample rate unset, using default: %v", DefaultTracingSampleRate)
		return DefaultTracingSampleRate
	}
	return *c.SampleRate
}

func init() {
	// Register default setter for Tracing config
	RegisterDefaults(func(cfg *Config) {
		if cfg.Gateway != nil && cfg.Gateway.Tracing != nil {
			if cfg.Gateway.Tracing.ServiceName == "" {
				logTracingCfg.Printf("Applying default tracing service name: %s", DefaultTracingServiceName)
				cfg.Gateway.Tracing.ServiceName = DefaultTracingServiceName
			}
		}
	})

	// Register stdin converter for the opentelemetry gateway config field (spec §4.1.3.6).
	RegisterStdinConverter(func(cfg *Config, stdinCfg *StdinConfig) {
		if stdinCfg.Gateway == nil || stdinCfg.Gateway.OpenTelemetry == nil {
			logTracingCfg.Print("No OpenTelemetry config in stdin, skipping tracing conversion")
			return
		}
		otel := stdinCfg.Gateway.OpenTelemetry
		logTracingCfg.Printf("Converting OpenTelemetry config from stdin: hasEndpoint=%v, hasServiceName=%v, hasTraceID=%v",
			otel.Endpoint != "", otel.ServiceName != "", otel.TraceID != "")
		if cfg.Gateway == nil {
			logTracingCfg.Print("Gateway config missing, creating empty gateway config for tracing")
			cfg.Gateway = &GatewayConfig{}
		}
		cfg.Gateway.Tracing = &TracingConfig{
			Endpoint:    otel.Endpoint,
			TraceID:     otel.TraceID,
			SpanID:      otel.SpanID,
			ServiceName: otel.ServiceName,
		}
		if cfg.Gateway.Tracing.ServiceName == "" {
			logTracingCfg.Printf("OpenTelemetry service name not configured, applying default: %s", DefaultTracingServiceName)
			cfg.Gateway.Tracing.ServiceName = DefaultTracingServiceName
		}
	})
}
