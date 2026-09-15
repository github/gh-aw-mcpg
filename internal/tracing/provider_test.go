package tracing_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/github/gh-aw-mcpg/internal/config"
	"github.com/github/gh-aw-mcpg/internal/tracing"
)

// TestInitProvider_IsEnabled_Noop verifies that IsEnabled returns false for a noop provider.
func TestInitProvider_IsEnabled_Noop(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GH_AW_OTLP_ENDPOINTS", "")
	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)
	assert.False(t, provider.IsEnabled(), "noop provider should report IsEnabled=false")
}

// TestInitProvider_IsEnabled_SDK verifies that IsEnabled returns true when a real exporter is active.
func TestInitProvider_IsEnabled_SDK(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GH_AW_OTLP_ENDPOINTS", "")
	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:14318",
		ServiceName: "test",
	}
	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		_ = provider.Shutdown(shutdownCtx)
	}()
	assert.True(t, provider.IsEnabled(), "SDK provider should report IsEnabled=true")
}

func TestInitProvider_HonorsCompressionOptOut(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GH_AW_OTLP_ENDPOINTS", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "none")

	received := make(chan http.Header, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	provider, err := tracing.InitProvider(ctx, &config.TracingConfig{Endpoint: ts.URL})
	require.NoError(t, err)

	_, span := provider.Tracer().Start(ctx, "compression-opt-out-test-span")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	require.NoError(t, provider.Shutdown(shutdownCtx))

	select {
	case headers := <-received:
		assert.Empty(t, headers.Get("Content-Encoding"))
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for OTLP export request")
	}
}

// TestInitProvider_FanOut_GHAWOTLPEndpoints verifies that when GH_AW_OTLP_ENDPOINTS
// is set, spans are delivered to every listed endpoint.
func TestInitProvider_FanOut_GHAWOTLPEndpoints(t *testing.T) {
	ctx := context.Background()

	// Start two test OTLP collectors.
	received1 := make(chan struct{}, 10)
	ts1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received1 <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts1.Close()

	received2 := make(chan struct{}, 10)
	ts2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received2 <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts2.Close()

	t.Setenv("GH_AW_OTLP_ENDPOINTS", ts1.URL+","+ts2.URL)

	// Config with no primary endpoint; GH_AW_OTLP_ENDPOINTS provides them all.
	provider, err := tracing.InitProvider(ctx, &config.TracingConfig{ServiceName: "test-fanout"})
	require.NoError(t, err)
	require.NotNil(t, provider)
	assert.True(t, provider.IsEnabled(), "provider should be enabled when GH_AW_OTLP_ENDPOINTS is set")

	// Emit a span to trigger export.
	_, span := provider.Tracer().Start(ctx, "fanout-test-span")
	span.End()

	// Flush by shutting down.
	shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)

	// Both collectors must have received at least one export request.
	select {
	case <-received1:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for export to first endpoint")
	}
	select {
	case <-received2:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for export to second endpoint")
	}
}

// TestInitProvider_FanOut_TakesPrecedenceOverSingleEndpoint verifies that when
// GH_AW_OTLP_ENDPOINTS is set and a primary endpoint is also configured, the
// fan-out list is used (not the primary endpoint separately).
func TestInitProvider_FanOut_TakesPrecedenceOverSingleEndpoint(t *testing.T) {
	ctx := context.Background()

	fanoutReceived := make(chan struct{}, 10)
	tsFanout := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case fanoutReceived <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer tsFanout.Close()

	t.Setenv("GH_AW_OTLP_ENDPOINTS", tsFanout.URL)

	// Also configure a primary endpoint — this should be ignored in favour of
	// GH_AW_OTLP_ENDPOINTS.
	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:19999", // unreachable
		ServiceName: "test-precedence",
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	assert.True(t, provider.IsEnabled())

	_, span := provider.Tracer().Start(ctx, "precedence-test-span")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)

	select {
	case <-fanoutReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for export to fan-out endpoint")
	}
}

// TestInitProvider_FanOut_EmptyEnvVar falls back to single-endpoint mode.
func TestInitProvider_FanOut_EmptyEnvVar(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GH_AW_OTLP_ENDPOINTS", "")

	cfg := &config.TracingConfig{Endpoint: "http://localhost:14318"}
	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	assert.True(t, provider.IsEnabled(), "single-endpoint mode should still be active")

	shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)
}

func ptrFloat64(v float64) *float64 { return &v }

func TestInitProvider_NoEndpoint_ReturnsNoopProvider(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GH_AW_OTLP_ENDPOINTS", "") // prevent ambient CI env from overriding noop path

	// With nil config (no endpoint), should return a noop provider
	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Noop provider must shut down cleanly
	require.NoError(t, provider.Shutdown(ctx))

	// The global provider should be a noop provider
	tp := otel.GetTracerProvider()
	assert.IsType(t, noop.NewTracerProvider(), tp)
}

// TestInitProvider_AllExportersFail verifies that InitProvider returns an error when
// every OTLP exporter fails to initialize (e.g. cancelled context).
func TestInitProvider_AllExportersFail(t *testing.T) {
	// A pre-cancelled context causes otlptracehttp.New to return an error
	// immediately, exercising the "len(exporters) == 0" error path.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Setenv("GH_AW_OTLP_ENDPOINTS", "")

	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:14318",
		ServiceName: "test-service",
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.Error(t, err, "InitProvider should return an error when all exporters fail")
	assert.Nil(t, provider)
	assert.Contains(t, err.Error(), "failed to create any OTLP trace exporters")
}

func TestInitProvider_EmptyEndpoint_ReturnsNoopProvider(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GH_AW_OTLP_ENDPOINTS", "") // prevent ambient CI env from overriding noop path

	cfg := &config.TracingConfig{
		Endpoint:    "", // explicitly empty
		ServiceName: "test-service",
		SampleRate:  ptrFloat64(1.0),
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	assert.NoError(t, provider.Shutdown(ctx))
}

func TestInitProvider_WithEndpoint_ReturnsSdkProvider(t *testing.T) {
	ctx := context.Background()

	// Point at a non-existent endpoint; exporter creation should still succeed
	// (connection is lazy) and the provider should be initialized.
	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:14318", // non-existent, but valid URL
		ServiceName: "test-service",
		SampleRate:  ptrFloat64(1.0),
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Tracer should be non-nil
	assert.NotNil(t, provider.Tracer())

	// Shutdown with a short context so test doesn't hang waiting to flush
	shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	// Shutdown may fail if it tries to flush to the non-existent endpoint,
	// but the provider itself should handle it gracefully (no panic)
	_ = provider.Shutdown(shutdownCtx)
}

// TestInitProvider_ResourceContainsServiceName verifies that the OTel resource
// built by InitProvider always includes a non-empty service.name attribute.
// This guards against the semconv schema-URL conflict that previously caused
// resource.New to return an error and the old fallback path to use resource.Empty(),
// stripping all identity attributes.
func TestInitProvider_ResourceContainsServiceName(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GH_AW_OTLP_ENDPOINTS", "")

	const wantServiceName = "mcp-gateway-test"
	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:14318", // non-existent, but valid URL
		ServiceName: wantServiceName,
		SampleRate:  ptrFloat64(1.0),
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		_ = provider.Shutdown(shutdownCtx)
	}()

	res := provider.Resource()
	require.NotNil(t, res, "SDK provider must have a non-nil resource")

	// Find service.name in the resource attributes.
	var gotServiceName string
	for _, attr := range res.Attributes() {
		if attr.Key == tracing.ServiceNameKey {
			gotServiceName = attr.Value.AsString()
			break
		}
	}
	assert.Equalf(t, wantServiceName, gotServiceName,
		"resource must contain service.name=%q; got %q", wantServiceName, gotServiceName)
}

func TestTracer_ReturnsNonNil(t *testing.T) {
	// Reset to noop global provider
	ctx := context.Background()
	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	tr := tracing.Tracer()
	assert.NotNil(t, tr)
}

func TestGetCachedOrGlobal_WithCachedTracer_ReturnsCached(t *testing.T) {
	cached := noop.NewTracerProvider().Tracer("cached")
	assert.Equal(t, cached, tracing.GetCachedOrGlobal(cached))
}

func TestGetCachedOrGlobal_WithNilTracer_ReturnsGlobal(t *testing.T) {
	ctx := context.Background()
	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	assert.NotNil(t, tracing.GetCachedOrGlobal(nil))
}

func TestInitProvider_SampleRateZero_UsesNeverSampler(t *testing.T) {
	ctx := context.Background()

	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:14318",
		ServiceName: "test-service",
		SampleRate:  ptrFloat64(0.0), // never sample
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Verify NeverSample behavior: spans should not be sampled
	tr := provider.Tracer()
	_, span := tr.Start(ctx, "test-span")
	assert.False(t, span.SpanContext().IsSampled(), "span should NOT be sampled with rate 0.0")
	assert.False(t, span.IsRecording(), "span should NOT be recording with rate 0.0")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)
}

func TestInitProvider_SampleRatePartial_UsesRatioSampler(t *testing.T) {
	ctx := context.Background()

	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:14318",
		ServiceName: "test-service",
		SampleRate:  ptrFloat64(0.5), // 50% sampling
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)
}

func TestInitProvider_SampleRateOne_UsesAlwaysSampler(t *testing.T) {
	ctx := context.Background()

	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:14318",
		ServiceName: "test-service",
		SampleRate:  ptrFloat64(1.0), // always sample
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Verify AlwaysSample behavior: spans should be sampled
	tr := provider.Tracer()
	_, span := tr.Start(ctx, "test-span")
	assert.True(t, span.SpanContext().IsSampled(), "span should be sampled with rate 1.0")
	assert.True(t, span.IsRecording(), "span should be recording with rate 1.0")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)
}

func TestInitProvider_SampleRateNil_DefaultsToAlwaysSample(t *testing.T) {
	ctx := context.Background()

	cfg := &config.TracingConfig{
		Endpoint:    "http://localhost:14318",
		ServiceName: "test-service",
		// SampleRate is nil (unset) — should default to 1.0
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	// Verify default AlwaysSample behavior
	tr := provider.Tracer()
	_, span := tr.Start(ctx, "test-span")
	assert.True(t, span.SpanContext().IsSampled(), "span should be sampled with default rate")
	assert.True(t, span.IsRecording(), "span should be recording with default rate")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)
}

// TestInitProvider_GlobalPropagatorRegistration verifies that InitProvider registers the
// W3C TraceContext and Baggage propagators globally, so that incoming propagation
// headers are respected by downstream HTTP middleware.
func TestInitProvider_GlobalPropagatorRegistration(t *testing.T) {
	ctx := context.Background()

	// Use a nil config (noop path) — propagator should still be registered.
	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	prop := otel.GetTextMapPropagator()
	require.NotNil(t, prop)

	member, err := baggage.NewMember("test-key", "test-value")
	require.NoError(t, err)
	bag, err := baggage.New(member)
	require.NoError(t, err)
	ctx = baggage.ContextWithBaggage(ctx, bag)

	// Round-trip: inject a known span context and baggage, then extract them.
	exporter := tracetest.NewInMemoryExporter()
	sp := sdktrace.NewSimpleSpanProcessor(exporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	tr := tp.Tracer("test")

	// Create a parent span and inject its context into HTTP headers.
	_, parentSpan := tr.Start(ctx, "parent")
	parentSpanCtx := parentSpan.SpanContext()
	parentSpan.End()

	carrier := propagation.MapCarrier{}
	prop.Inject(trace.ContextWithSpanContext(ctx, parentSpanCtx), carrier)

	// Simulate an incoming HTTP request carrying the traceparent header.
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	for k, v := range carrier {
		req.Header.Set(k, v)
	}

	// Extract should recover the parent span context from the request headers.
	extractedCtx := otel.GetTextMapPropagator().Extract(req.Context(), propagation.HeaderCarrier(req.Header))
	extractedSpanCtx := trace.SpanFromContext(extractedCtx).SpanContext()

	assert.Equal(t, parentSpanCtx.TraceID(), extractedSpanCtx.TraceID(),
		"extracted trace ID must match the injected parent trace ID")
	assert.Equal(t, "test-value", baggage.FromContext(extractedCtx).Member("test-key").Value(),
		"extracted baggage must match the injected baggage")
}

// TestWrapHTTPHandler_ContinuesRemoteTrace verifies that WrapHTTPHandler extracts an
// incoming traceparent header and makes the span a child of the remote parent.
func TestWrapHTTPHandler_ContinuesRemoteTrace(t *testing.T) {
	ctx := context.Background()

	// Initialise provider so the W3C propagator is registered globally.
	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	// Build an in-memory SDK provider so we can capture spans.
	exporter := tracetest.NewInMemoryExporter()
	sp := sdktrace.NewSimpleSpanProcessor(exporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(noop.NewTracerProvider())

	// Create a parent span and build a request with its traceparent header.
	_, parentSpan := tp.Tracer("test").Start(ctx, "agent-span")
	parentTraceID := parentSpan.SpanContext().TraceID()
	parentSpan.End()

	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(
		trace.ContextWithSpanContext(ctx, parentSpan.SpanContext()),
		carrier,
	)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	for k, v := range carrier {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()

	// Capture the span context seen inside the handler.
	var capturedSpanCtx trace.SpanContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSpanCtx = trace.SpanFromContext(r.Context()).SpanContext()
	})

	handler := tracing.WrapHTTPHandler(inner, "test.request")
	handler.ServeHTTP(rr, req)

	assert.Equal(t, parentTraceID, capturedSpanCtx.TraceID(),
		"handler span should share the parent's trace ID when traceparent is present")
}

// TestWrapHTTPHandler_GeneratesRootSpan verifies that when no
// traceparent header is present a fresh root span (new trace ID) is generated.
func TestWrapHTTPHandler_GeneratesRootSpan(t *testing.T) {
	ctx := context.Background()

	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	exporter := tracetest.NewInMemoryExporter()
	sp := sdktrace.NewSimpleSpanProcessor(exporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	defer otel.SetTracerProvider(noop.NewTracerProvider())

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil) // no traceparent
	rr := httptest.NewRecorder()

	var capturedSpanCtx trace.SpanContext
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedSpanCtx = trace.SpanFromContext(r.Context()).SpanContext()
	})

	handler := tracing.WrapHTTPHandler(inner, "test.request")
	handler.ServeHTTP(rr, req)

	assert.True(t, capturedSpanCtx.IsValid(), "should have a valid span context even without traceparent")
	assert.False(t, capturedSpanCtx.IsRemote(), "span should not be marked remote — it is a local root span")
}

// TestWrapHTTPHandler_UsesHTTPRouteWhenPatternAvailable verifies that handler
// patterns are recorded as http.route instead of high-cardinality concrete paths.
func TestWrapHTTPHandler_UsesHTTPRouteWhenPatternAvailable(t *testing.T) {
	ctx := context.Background()

	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	_, exporter, cleanup := newInMemoryProvider(t)
	defer cleanup()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux := http.NewServeMux()
	mux.Handle("GET /mcp/{serverID}", tracing.WrapHTTPHandler(inner, "test.route"))

	req := httptest.NewRequest(http.MethodGet, "/mcp/github", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "expected exactly one span")

	var foundRoute, foundPath bool
	for _, attr := range spans[0].Attributes {
		if attr.Key == tracing.HTTPRouteKey {
			assert.Equal(t, "/mcp/{serverID}", attr.Value.AsString())
			foundRoute = true
		}
		if attr.Key == tracing.URLPathKey {
			assert.Equal(t, "/mcp/github", attr.Value.AsString())
			foundPath = true
		}
	}
	assert.True(t, foundRoute, "http.route attribute must be present")
	assert.True(t, foundPath, "url.path attribute must be present")
}

// TestWrapHTTPHandler_UsesURLPathWhenPatternUnavailable verifies url.path is
// always captured and http.route is omitted when no route template is available.
func TestWrapHTTPHandler_UsesURLPathWhenPatternUnavailable(t *testing.T) {
	ctx := context.Background()

	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	_, exporter, cleanup := newInMemoryProvider(t)
	defer cleanup()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/unmatched/path", nil)
	rr := httptest.NewRecorder()
	tracing.WrapHTTPHandler(inner, "test.route.fallback").ServeHTTP(rr, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "expected exactly one span")

	var foundRoute, foundPath bool
	for _, attr := range spans[0].Attributes {
		if attr.Key == tracing.HTTPRouteKey {
			foundRoute = true
		}
		if attr.Key == tracing.URLPathKey {
			assert.Equal(t, "/unmatched/path", attr.Value.AsString())
			foundPath = true
		}
	}
	assert.False(t, foundRoute, "http.route attribute should be omitted when route template is unavailable")
	assert.True(t, foundPath, "url.path attribute must be present")
}

// TestInitProvider_WithHeaders verifies that OTLP export headers are forwarded
// to the collector. Table-driven sub-tests cover single headers, multiple
// headers with whitespace trimming, and malformed/empty-key cases that must be
// skipped. A channel synchronises with the test HTTP server so assertions are
// deterministic rather than timing-dependent.
func TestInitProvider_WithHeaders(t *testing.T) {
	ctx := context.Background()
	// Prevent GH_AW_OTLP_ENDPOINTS set in CI from overriding cfg.Endpoint and routing
	// spans away from the per-subtest httptest.Server to external garbage URLs.
	t.Setenv("GH_AW_OTLP_ENDPOINTS", "")

	tests := []struct {
		name           string
		headers        string
		expectedValues map[string]string // canonical HTTP header name → expected value
		notExpectedSet []string          // canonical HTTP header names that must NOT be present
	}{
		{
			name:    "single well-formed header",
			headers: "Authorization=Bearer test-token",
			expectedValues: map[string]string{
				"Authorization": "Bearer test-token",
			},
		},
		{
			name:    "multiple headers with whitespace trimmed",
			headers: " Authorization = Bearer test-token , X-Request-Id = req-123 ",
			expectedValues: map[string]string{
				"Authorization": "Bearer test-token",
				"X-Request-Id":  "req-123",
			},
		},
		{
			name:    "malformed and empty-key headers are skipped",
			headers: "Authorization=Bearer test-token, malformed, =empty-key, X-Trace-Id=trace-123",
			expectedValues: map[string]string{
				"Authorization": "Bearer test-token",
				"X-Trace-Id":    "trace-123",
			},
			notExpectedSet: []string{"Malformed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Channel signals when the test server receives an export request.
			received := make(chan http.Header, 1)
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case received <- r.Header.Clone():
				default:
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer ts.Close()

			cfg := &config.TracingConfig{
				Endpoint: ts.URL,
				Headers:  tt.headers,
			}

			provider, err := tracing.InitProvider(ctx, cfg)
			require.NoError(t, err)
			require.NotNil(t, provider)

			// Create and end a span to trigger an export attempt.
			tr := provider.Tracer()
			_, span := tr.Start(ctx, "header-test-span")
			span.End()

			// Shutdown flushes the batch processor, ensuring the export is sent.
			shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			_ = provider.Shutdown(shutdownCtx)

			// Wait for the export request with a timeout.
			select {
			case headers := <-received:
				for key, expectedValue := range tt.expectedValues {
					assert.Equalf(t, expectedValue, headers.Get(key), "%s header must be forwarded to the OTLP collector", key)
				}
				for _, key := range tt.notExpectedSet {
					assert.Emptyf(t, headers.Get(key), "%s header must not be sent when pair is malformed", key)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("timed out waiting for OTLP export request — headers test is non-deterministic")
			}
		})
	}
}

func TestInitProvider_WithJSONExtraEndpointHeaders(t *testing.T) {
	ctx := context.Background()

	received := make(chan http.Header, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case received <- r.Header.Clone():
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	payload, err := json.Marshal([]map[string]string{
		{
			"url":     ts.URL,
			"headers": "Authorization=******,X-Trace-Id=trace-123",
		},
	})
	require.NoError(t, err)
	t.Setenv("GH_AW_OTLP_ENDPOINTS", string(payload))

	provider, err := tracing.InitProvider(ctx, &config.TracingConfig{ServiceName: "test-json-headers"})
	require.NoError(t, err)
	require.NotNil(t, provider)

	_, span := provider.Tracer().Start(ctx, "json-header-test-span")
	span.End()

	shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)

	select {
	case headers := <-received:
		assert.Equal(t, "******", headers.Get("Authorization"))
		assert.Equal(t, "trace-123", headers.Get("X-Trace-Id"))
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for OTLP export request for JSON extra endpoint headers")
	}
}

// TestParentContext_WithValidTraceIDAndSpanID verifies that ParentContext builds a valid
// remote span context when both traceId and spanId are provided.
func TestParentContext_WithValidTraceIDAndSpanID(t *testing.T) {
	ctx := context.Background()

	// Initialize noop provider to set up the W3C propagator globally
	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	cfg := &config.TracingConfig{
		Endpoint: "https://example.com",
		TraceID:  "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:   "00f067aa0ba902b7",
	}

	parentCtx := tracing.ParentContext(ctx, cfg)

	// The context must be enriched (different from background context)
	assert.NotEqual(t, ctx, parentCtx, "ParentContext must return an enriched context")

	// Verify the remote span context contains the correct traceId and spanId
	// by extracting it from the context and checking via propagation round-trip.
	prop := otel.GetTextMapPropagator()
	carrier := propagation.MapCarrier{}
	prop.Inject(parentCtx, carrier)
	traceparent := carrier["traceparent"]
	require.NotEmpty(t, traceparent, "W3C traceparent must be present after injection")

	// traceparent format: 00-{traceId}-{spanId}-{flags}
	assert.Contains(t, traceparent, "4bf92f3577b34da6a3ce929d0e0e4736",
		"traceparent must contain the configured traceId")
	assert.Contains(t, traceparent, "00f067aa0ba902b7",
		"traceparent must contain the configured spanId")
}

// TestParentContext_WithTraceIDOnly verifies that ParentContext works when only traceId is provided.
func TestParentContext_WithTraceIDOnly(t *testing.T) {
	ctx := context.Background()

	cfg := &config.TracingConfig{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
		// SpanID intentionally absent
	}

	parentCtx := tracing.ParentContext(ctx, cfg)
	// Should return an enriched context
	assert.NotEqual(t, ctx, parentCtx, "ParentContext with traceId only must return an enriched context")
}

// TestParentContext_NoConfig verifies that ParentContext is a no-op when config is nil.
func TestParentContext_NoConfig(t *testing.T) {
	ctx := context.Background()
	parentCtx := tracing.ParentContext(ctx, nil)
	assert.Equal(t, ctx, parentCtx, "ParentContext with nil config must return the original context unchanged")
}

// TestParentContext_EmptyTraceID verifies that ParentContext is a no-op when traceId is empty.
func TestParentContext_EmptyTraceID(t *testing.T) {
	ctx := context.Background()
	cfg := &config.TracingConfig{
		SpanID: "00f067aa0ba902b7", // spanId without traceId
	}
	parentCtx := tracing.ParentContext(ctx, cfg)
	assert.Equal(t, ctx, parentCtx, "ParentContext without traceId must return the original context unchanged")
}

// TestParentContext_InvalidTraceID verifies that ParentContext handles malformed traceIds gracefully.
func TestParentContext_InvalidTraceID(t *testing.T) {
	ctx := context.Background()
	cfg := &config.TracingConfig{
		TraceID: "not-valid-hex",
	}
	parentCtx := tracing.ParentContext(ctx, cfg)
	assert.Equal(t, ctx, parentCtx, "ParentContext with invalid traceId must return original context")
}

// TestInitProvider_WithTraceIDAndSpanID verifies that InitProvider succeeds with traceId/spanId config.
func TestInitProvider_WithTraceIDAndSpanID(t *testing.T) {
	ctx := context.Background()

	cfg := &config.TracingConfig{
		Endpoint: fmt.Sprintf("http://localhost:%d", 14320),
		TraceID:  "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:   "00f067aa0ba902b7",
	}

	provider, err := tracing.InitProvider(ctx, cfg)
	require.NoError(t, err)
	require.NotNil(t, provider)

	shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_ = provider.Shutdown(shutdownCtx)
}

// TestInitProvider_InvalidSampleRate verifies that a sample rate outside [0.0, 1.0]
// falls back to the default (1.0 = AlwaysSample) and still produces a working provider.
// This exercises the warning path in resolveSampleRate.
func TestInitProvider_InvalidSampleRate(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		rate float64
	}{
		{name: "rate above 1.0", rate: 1.5},
		{name: "negative rate", rate: -0.5},
		{name: "large positive rate", rate: 100.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rate := tt.rate
			cfg := &config.TracingConfig{
				Endpoint:   "http://localhost:14318",
				SampleRate: &rate,
			}

			provider, err := tracing.InitProvider(ctx, cfg)
			require.NoError(t, err, "InitProvider should succeed even with an invalid sample rate")
			require.NotNil(t, provider)

			// The invalid rate should fall back to the default (1.0 = AlwaysSample).
			// Verify by checking that a span is sampled.
			tr := provider.Tracer()
			_, span := tr.Start(ctx, "invalid-rate-test-span")
			assert.True(t, span.SpanContext().IsSampled(), "span should be sampled when rate falls back to default 1.0")
			span.End()

			shutdownCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			_ = provider.Shutdown(shutdownCtx)
		})
	}
}

// TestParentContext_InvalidSpanID verifies that ParentContext falls through to
// generating a random span ID when the configured spanId is not valid hex, and still
// returns an enriched context (per spec T-OTEL-008).
func TestParentContext_InvalidSpanID(t *testing.T) {
	ctx := context.Background()

	// Initialize noop provider to ensure the W3C propagator is registered globally.
	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	tests := []struct {
		name   string
		spanID string
	}{
		{name: "non-hex span ID", spanID: "not-valid-hex"},
		{name: "too-short hex span ID", spanID: "aabb"},
		{name: "too-long hex span ID", spanID: "00f067aa0ba902b700f067aa0ba902b7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.TracingConfig{
				TraceID: "4bf92f3577b34da6a3ce929d0e0e4736",
				SpanID:  tt.spanID,
			}

			parentCtx := tracing.ParentContext(ctx, cfg)

			// Should still return an enriched context because a random span ID is generated.
			assert.NotEqual(t, ctx, parentCtx,
				"ParentContext must return an enriched context even when spanId is invalid")

			// The context must carry a valid remote span context.
			prop := otel.GetTextMapPropagator()
			carrier := propagation.MapCarrier{}
			prop.Inject(parentCtx, carrier)
			traceparent := carrier["traceparent"]
			require.NotEmpty(t, traceparent, "traceparent must be present in W3C propagation carrier")
			assert.Contains(t, traceparent, "4bf92f3577b34da6a3ce929d0e0e4736",
				"traceparent must carry the configured traceId")
		})
	}
}

// TestParentContext_AllZerosTraceID verifies that ParentContext returns the
// original context unchanged when the traceId is all zeros (produces an invalid SpanContext).
func TestParentContext_AllZerosTraceID(t *testing.T) {
	ctx := context.Background()

	cfg := &config.TracingConfig{
		TraceID: "00000000000000000000000000000000", // all-zeros: invalid TraceID per W3C spec
	}

	parentCtx := tracing.ParentContext(ctx, cfg)

	// An all-zeros traceId yields an invalid SpanContext; the function must return ctx unchanged.
	assert.Equal(t, ctx, parentCtx,
		"ParentContext with an all-zeros traceId must return the original context unchanged")
}

// newInMemoryProvider creates a fresh in-process SDK tracer provider with an in-memory
// exporter and registers it globally. The returned cleanup func restores the noop provider.
func newInMemoryProvider(t *testing.T) (*sdktrace.TracerProvider, *tracetest.InMemoryExporter, func()) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	sp := sdktrace.NewSimpleSpanProcessor(exporter)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	otel.SetTracerProvider(tp)
	return tp, exporter, func() { otel.SetTracerProvider(noop.NewTracerProvider()) }
}

// TestWrapHTTPHandler_RecordsExplicitStatusCode verifies that an explicit WriteHeader call
// is reflected in the http.response.status_code span attribute.
func TestWrapHTTPHandler_RecordsExplicitStatusCode(t *testing.T) {
	ctx := context.Background()

	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	_, exporter, cleanup := newInMemoryProvider(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound) // explicit 404
	})

	tracing.WrapHTTPHandler(inner, "test.status").ServeHTTP(rr, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "expected exactly one span")
	span := spans[0]

	// Span must carry http.response.status_code = 404
	var found bool
	for _, attr := range span.Attributes {
		if attr.Key == tracing.HTTPResponseStatusCodeKey {
			assert.Equal(t, int64(http.StatusNotFound), attr.Value.AsInt64(), "status code attribute must be 404")
			found = true
		}
	}
	assert.True(t, found, "http.response.status_code attribute must be present on the span")

	// 4xx must NOT set span status to Error
	assert.Equal(t, codes.Unset, span.Status.Code, "4xx responses must not set span status to Error")
}

// TestWrapHTTPHandler_RecordsImplicit200 verifies that a handler that calls Write
// without an explicit WriteHeader is recorded as status 200.
func TestWrapHTTPHandler_RecordsImplicit200(t *testing.T) {
	ctx := context.Background()

	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	_, exporter, cleanup := newInMemoryProvider(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok")) // implicit 200 — no WriteHeader call
	})

	tracing.WrapHTTPHandler(inner, "test.implicit200").ServeHTTP(rr, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "expected exactly one span")

	var found bool
	for _, attr := range spans[0].Attributes {
		if attr.Key == tracing.HTTPResponseStatusCodeKey {
			assert.Equal(t, int64(http.StatusOK), attr.Value.AsInt64(), "implicit Write must record status 200")
			found = true
		}
	}
	assert.True(t, found, "http.response.status_code attribute must be present on the span")
}

// TestWrapHTTPHandler_5xxSetsSpanStatusError verifies that a 5xx response sets span
// status to codes.Error and leaves a non-empty description.
func TestWrapHTTPHandler_5xxSetsSpanStatusError(t *testing.T) {
	ctx := context.Background()

	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	_, exporter, cleanup := newInMemoryProvider(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rr := httptest.NewRecorder()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError) // 500
	})

	tracing.WrapHTTPHandler(inner, "test.5xx").ServeHTTP(rr, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "expected exactly one span")
	span := spans[0]

	assert.Equal(t, codes.Error, span.Status.Code, "5xx must set span status to Error")
	assert.NotEmpty(t, span.Status.Description, "5xx must provide a non-empty status description")

	var foundException, foundStackTrace bool
	for _, event := range span.Events {
		if event.Name != "exception" {
			continue
		}
		foundException = true
		for _, attr := range event.Attributes {
			if attr.Key == "exception.stacktrace" {
				assert.NotEmpty(t, attr.Value.AsString(), "recorded exception should include stacktrace")
				foundStackTrace = true
			}
		}
	}
	assert.True(t, foundException, "5xx should record an exception event")
	assert.True(t, foundStackTrace, "5xx exception event should include stacktrace")

	var found bool
	for _, attr := range span.Attributes {
		if attr.Key == tracing.HTTPResponseStatusCodeKey {
			assert.Equal(t, int64(http.StatusInternalServerError), attr.Value.AsInt64())
			found = true
		}
	}
	assert.True(t, found, "http.response.status_code attribute must be present on the span")
}

func TestWrapHTTPHandler_Unknown5xxUsesFallbackStatusDescription(t *testing.T) {
	ctx := context.Background()

	provider, err := tracing.InitProvider(ctx, nil)
	require.NoError(t, err)
	defer provider.Shutdown(ctx)

	_, exporter, cleanup := newInMemoryProvider(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rr := httptest.NewRecorder()

	const unknown5xx = 599
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(unknown5xx)
	})

	tracing.WrapHTTPHandler(inner, "test.unknown5xx").ServeHTTP(rr, req)

	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "expected exactly one span")
	span := spans[0]

	assert.Equal(t, codes.Error, span.Status.Code, "unknown 5xx must set span status to Error")
	assert.Equal(t, "HTTP 599", span.Status.Description, "unknown 5xx should use HTTP <code> fallback description")
}
