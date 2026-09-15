// Package tracing provides OpenTelemetry OTLP trace export for the MCP Gateway.
//
// When an OTLP endpoint is configured (via config or OTEL_EXPORTER_OTLP_ENDPOINT),
// this package initializes a real tracer provider that exports spans over HTTP.
// When no endpoint is configured, a noop tracer provider is used, adding zero overhead.
//
// Usage:
//
//	tp, err := tracing.InitProvider(ctx, cfg.Gateway.Tracing)
//	if err != nil {
//	    return err
//	}
//	defer tp.Shutdown(ctx)
//
// Once initialized, obtain a tracer with:
//
//	tracer := otel.Tracer("github.com/github/gh-aw-mcpg")
package tracing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
	"github.com/github/gh-aw-mcpg/internal/version"
)

const instrumentationName = "github.com/github/gh-aw-mcpg"

var logTracing = logger.ForFile()

// Provider wraps an OpenTelemetry TracerProvider and provides a Shutdown method.
type Provider struct {
	tp       trace.TracerProvider
	sdk      *sdktrace.TracerProvider // non-nil only when OTLP is configured
	tracer   trace.Tracer
	resource *resource.Resource // resource used when building the SDK provider
}

// Tracer returns the tracer for the MCP gateway instrumentation scope.
func (p *Provider) Tracer() trace.Tracer {
	return p.tracer
}

// IsEnabled reports whether the provider has a real SDK (non-noop) exporter active.
func (p *Provider) IsEnabled() bool {
	return p.sdk != nil
}

// Resource returns the OTel resource associated with this provider, or nil if
// the provider is a noop (no OTLP endpoint configured).
func (p *Provider) Resource() *resource.Resource {
	return p.resource
}

// Shutdown flushes and shuts down the tracer provider.
// For noop providers this is a no-op. Must be called on application exit.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.sdk != nil {
		logTracing.Print("Flushing and shutting down OTLP tracer provider")
		return p.sdk.Shutdown(ctx)
	}
	return nil
}

// generateRandomSpanID creates a cryptographically random 8-byte span ID.
func generateRandomSpanID() (trace.SpanID, error) {
	var id trace.SpanID
	b, err := util.RandomBytes(len(id))
	if err != nil {
		return id, fmt.Errorf("failed to generate random span ID: %w", err)
	}
	copy(id[:], b)
	return id, nil
}

// registerPropagator installs the global W3C TraceContext + Baggage propagator.
// This enables incoming traceparent/tracestate headers to be extracted so that
// agent-initiated traces are continued rather than fragmented.
func registerPropagator() {
	logTracing.Print("Registering global W3C TraceContext+Baggage text map propagator")
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
}

// InitProvider initializes the global OpenTelemetry tracer provider.
// When no endpoint is configured, a noop provider is installed (zero overhead).
// When one or more endpoints are configured, an OTLP/HTTP exporter is created
// for each endpoint and the SDK tracer provider is registered as the global
// provider. Multiple endpoints (from GH_AW_OTLP_ENDPOINTS) are supported via a
// fan-out exporter so that every backend receives complete gateway traces.
//
// In both cases a W3C TraceContext propagator is registered globally so that
// incoming traceparent/tracestate headers are honoured by all HTTP middleware.
//
// The returned Provider must be shut down on application exit to flush buffered spans.
func InitProvider(ctx context.Context, cfg *config.TracingConfig) (*Provider, error) {
	endpoint := resolveEndpoint(cfg)
	extraEndpoints := resolveExtraEndpointConfigs(cfg)
	serviceName := resolveServiceName(cfg)
	sampleRate := resolveSampleRate(cfg)

	// Always register the W3C propagator so that incoming traceparent headers
	// are extracted, even when tracing is disabled (noop spans are still
	// parented correctly if propagation is later enabled upstream).
	registerPropagator()

	// Determine the active set of endpoints:
	//   - GH_AW_OTLP_ENDPOINTS takes precedence (fan-out to all listed endpoints).
	//   - Falls back to the single endpoint from config/OTEL_EXPORTER_OTLP_ENDPOINT.
	var activeEndpoints []extraEndpointConfig
	if len(extraEndpoints) > 0 {
		activeEndpoints = extraEndpoints
	} else if endpoint != "" {
		activeEndpoints = []extraEndpointConfig{{URL: endpoint}}
	}

	if len(activeEndpoints) == 0 {
		logTracing.Printf("Tracing disabled: no OTLP endpoint configured")
		noopTP := noop.NewTracerProvider()
		otel.SetTracerProvider(noopTP)
		return &Provider{
			tp:     noopTP,
			tracer: noopTP.Tracer(instrumentationName),
		}, nil
	}

	if len(activeEndpoints) > 1 {
		logTracing.Printf("Initializing OTLP fan-out tracing: %d endpoints, service=%s, sampleRate=%.2f",
			len(activeEndpoints), serviceName, sampleRate)
	} else {
		logTracing.Printf("Initializing OTLP tracing: endpoint=%s, service=%s, sampleRate=%.2f",
			activeEndpoints[0].URL, serviceName, sampleRate)
	}

	// Resolve shared headers applied to all exporters.
	headers := resolveHeaders(cfg)
	if headers != nil {
		logTracing.Printf("Applying %d OTLP export header(s)", len(headers))
	}

	// Build one OTLP HTTP exporter per active endpoint.
	exporters := make([]sdktrace.SpanExporter, 0, len(activeEndpoints))
	var constructionErrs []error
	for _, ep := range activeEndpoints {
		exporterHeaders := mergeOTLPHeaders(headers, ep.Headers)
		opts := []otlptracehttp.Option{
			otlptracehttp.WithEndpointURL(ep.URL),
			otlptracehttp.WithTimeout(resolveExporterTimeout(cfg)),
			// Retain the SDK's recommended retry policy while making it reviewable here.
			otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
				InitialInterval: 5 * time.Second,
				MaxInterval:     30 * time.Second,
				MaxElapsedTime:  time.Minute,
			}),
		}
		if exporterHeaders != nil {
			opts = append(opts, otlptracehttp.WithHeaders(exporterHeaders))
		}
		exp, err := otlptracehttp.New(ctx, opts...)
		if err != nil {
			logTracing.Printf("Warning: failed to create OTLP exporter for endpoint %s: %v; skipping", ep.URL, err)
			constructionErrs = append(constructionErrs, fmt.Errorf("endpoint %s: %w", ep.URL, err))
			continue
		}
		exporters = append(exporters, exp)
	}
	if len(exporters) == 0 {
		return nil, fmt.Errorf("failed to create any OTLP trace exporters: %w", errors.Join(constructionErrs...))
	}
	logTracing.Printf("OTLP HTTP trace exporter(s) created: %d", len(exporters))

	// Wrap in a fan-out exporter (returns the single exporter directly when only one).
	exporter := newFanoutExporter(exporters)

	// Build resource with service name, version, and SDK metadata.
	// resource.WithTelemetrySDK(), resource.WithHost(), resource.WithProcessPID(), and
	// resource.WithContainer() are built-in detectors that use semconv/v1.43.0 internally.
	// Keep tracing/semconv.go in lockstep with the otel/sdk pin in go.mod so resource
	// detection doesn't hit the "conflicting Schema URL" error that drops attributes.
	res, err := resource.New(ctx,
		resource.WithTelemetrySDK(),
		resource.WithContainer(),
		resource.WithAttributes(
			ServiceName(serviceName),
			ServiceVersion(version.Get()),
		),
		resource.WithProcessPID(),
		resource.WithHost(),
	)
	if err != nil {
		logTracing.Printf("Warning: failed to create OTEL resource: %v", err)
		serviceResource := resource.NewWithAttributes(
			SchemaURL,
			ServiceName(serviceName),
			ServiceVersion(version.Get()),
		)
		// resource.New can return a best-effort resource alongside an error.
		// Preserve detected attributes and only ensure service identity exists.
		merged, mergeErr := resource.Merge(res, serviceResource)
		if mergeErr != nil {
			logTracing.Printf("Warning: failed to merge OTEL resource fallback: %v", mergeErr)
			res = serviceResource
		} else {
			res = merged
		}
	}

	// Select sampler based on configured rate
	var sampler sdktrace.Sampler
	switch {
	case sampleRate >= 1.0:
		sampler = sdktrace.AlwaysSample()
		logTracing.Print("Using AlwaysSample tracer sampler")
	case sampleRate <= 0.0:
		sampler = sdktrace.NeverSample()
		logTracing.Print("Using NeverSample tracer sampler")
	default:
		sampler = sdktrace.TraceIDRatioBased(sampleRate)
		logTracing.Printf("Using TraceIDRatioBased tracer sampler: rate=%.4f", sampleRate)
	}

	sdkTP := sdktrace.NewTracerProvider(
		// NOTE: panic recording (added in otel v1.44.0, disabled via
		// sdktrace.WithoutPanicRecording) is intentionally left enabled: the gateway has no
		// panic-recovery middleware that records equivalent span error attributes, so the
		// SDK's automatic exception events are the only panic telemetry available.
		sdktrace.WithBatcher(exporter,
			// Tune batch processor for gateway throughput: halve the default batch
			// size (512→256) and reduce the flush interval (5s→2s) to lower tail
			// latency for trace delivery without sacrificing export efficiency.
			sdktrace.WithMaxExportBatchSize(256),
			sdktrace.WithBatchTimeout(2*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Register as the global provider so instrumented libraries pick it up
	otel.SetTracerProvider(sdkTP)

	tracer := sdkTP.Tracer(instrumentationName)
	logTracing.Printf("OTLP tracing initialized successfully")

	return &Provider{
		tp:       sdkTP,
		sdk:      sdkTP,
		tracer:   tracer,
		resource: res,
	}, nil
}

// Tracer returns the global MCP gateway tracer.
// This is a convenience wrapper around otel.Tracer for packages that don't
// hold a reference to the Provider.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// GetCachedOrGlobal returns cached if non-nil, otherwise falls back to the global tracer.
func GetCachedOrGlobal(cached trace.Tracer) trace.Tracer {
	if cached != nil {
		return cached
	}
	return Tracer()
}

func mergeOTLPHeaders(shared, specific map[string]string) map[string]string {
	if len(shared) == 0 && len(specific) == 0 {
		return nil
	}

	merged := make(map[string]string, len(shared)+len(specific))
	for key, value := range shared {
		merged[key] = value
	}
	for key, value := range specific {
		merged[key] = value
	}
	return merged
}

// CachedTracer holds an optional pre-initialized tracer and falls back to the
// global tracer when the cached tracer is nil.
type CachedTracer struct {
	Tracer trace.Tracer
}

// GetTracer returns the cached tracer when set, otherwise the global tracer.
func (ct CachedTracer) GetTracer() trace.Tracer {
	return GetCachedOrGlobal(ct.Tracer)
}

// ParentContext returns a context carrying the W3C remote parent span context
// from the configured traceId and spanId (spec §4.1.3.6).
// Exported for use at startup to build the root span's parent context.
func ParentContext(ctx context.Context, cfg *config.TracingConfig) context.Context {
	if cfg == nil || cfg.TraceID == "" {
		return ctx
	}

	traceID, err := trace.TraceIDFromHex(cfg.TraceID)
	if err != nil {
		logTracing.Printf("Warning: invalid traceId '%s'; skipping W3C parent context", cfg.TraceID)
		return ctx
	}

	var spanID trace.SpanID
	if cfg.SpanID != "" {
		parsed, parseErr := trace.SpanIDFromHex(cfg.SpanID)
		if parseErr != nil {
			logTracing.Printf("Warning: invalid spanId '%s'; generating a random span ID", cfg.SpanID)
		} else {
			spanID = parsed
		}
	}

	if spanID == (trace.SpanID{}) {
		generatedID, genErr := generateRandomSpanID()
		if genErr != nil {
			logTracing.Printf("Warning: failed to generate random span ID: %v; skipping W3C parent context", genErr)
			return ctx
		}
		spanID = generatedID
		logTracing.Printf("Generated random spanId for W3C parent context")
	}

	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	if !sc.IsValid() {
		logTracing.Printf("Warning: constructed parent SpanContext is not valid; skipping W3C parent context")
		return ctx
	}
	logTracing.Printf("W3C parent context resolved: traceId=%s, spanId=%s", traceID, spanID)
	return trace.ContextWithRemoteSpanContext(ctx, sc)
}
