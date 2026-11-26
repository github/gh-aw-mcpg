package tracing

import (
	"context"
	"crypto/rand"
	"errors"
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"

	"github.com/github/gh-aw-mcpg/internal/config"
)

func TestMergeOTLPHeaders(t *testing.T) {
	tests := []struct {
		name     string
		shared   map[string]string
		specific map[string]string
		want     map[string]string
	}{
		{
			name: "both nil returns nil",
			want: nil,
		},
		{
			name:     "both empty returns nil",
			shared:   map[string]string{},
			specific: map[string]string{},
			want:     nil,
		},
		{
			name:   "only shared headers",
			shared: map[string]string{"Authorization": "shared"},
			want:   map[string]string{"Authorization": "shared"},
		},
		{
			name:     "only specific headers",
			specific: map[string]string{"X-Trace-Id": "trace-123"},
			want:     map[string]string{"X-Trace-Id": "trace-123"},
		},
		{
			name:     "non-overlapping maps are combined",
			shared:   map[string]string{"Authorization": "shared"},
			specific: map[string]string{"X-Trace-Id": "trace-123"},
			want: map[string]string{
				"Authorization": "shared",
				"X-Trace-Id":    "trace-123",
			},
		},
		{
			name:     "specific overrides shared",
			shared:   map[string]string{"Authorization": "shared"},
			specific: map[string]string{"Authorization": "specific"},
			want:     map[string]string{"Authorization": "specific"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sharedBefore := maps.Clone(tt.shared)
			specificBefore := maps.Clone(tt.specific)
			merged := mergeOTLPHeaders(tt.shared, tt.specific)
			assert.Equal(t, tt.want, merged)
			if merged != nil {
				merged["Authorization"] = "changed"
			}
			assert.Equal(t, sharedBefore, tt.shared)
			assert.Equal(t, specificBefore, tt.specific)
		})
	}
}

// TestGenerateRandomSpanID verifies that generateRandomSpanID produces a valid,
// non-zero 8-byte span ID and that successive calls produce distinct values.
func TestGenerateRandomSpanID(t *testing.T) {
	t.Parallel()

	id, err := generateRandomSpanID()
	require.NoError(t, err, "generateRandomSpanID should not fail with a healthy rand.Reader")
	assert.NotEqual(t, trace.SpanID{}, id, "generated span ID should be non-zero")

	id2, err := generateRandomSpanID()
	require.NoError(t, err)
	assert.NotEqual(t, id, id2, "successive calls should produce distinct span IDs")
}

// errorReader is an io.Reader that always returns the configured error.
type errorReader struct{ err error }

func (r *errorReader) Read(_ []byte) (int, error) { return 0, r.err }

// TestGenerateRandomSpanID_Error verifies that generateRandomSpanID propagates
// errors from the underlying random source and returns a zero span ID.
// Must NOT run in parallel: temporarily replaces the global crypto/rand.Reader.
func TestGenerateRandomSpanID_Error(t *testing.T) {
	syntheticErr := errors.New("synthetic entropy failure")

	origReader := rand.Reader
	rand.Reader = &errorReader{err: syntheticErr}
	defer func() { rand.Reader = origReader }()

	id, err := generateRandomSpanID()

	assert.Equal(t, trace.SpanID{}, id, "span ID should be zero on error")
	require.Error(t, err, "should return an error when the random source fails")
	require.ErrorIs(t, err, syntheticErr, "error should wrap the underlying source error")
	assert.Contains(t, err.Error(), "failed to generate random span ID")
}

// TestParentContext_RandomSpanIDFailure verifies that ParentContext returns the
// original context unchanged when generateRandomSpanID fails due to a broken
// random source. This covers the genErr != nil branch (lines 322–325 of provider.go).
// Must NOT run in parallel: temporarily replaces the global crypto/rand.Reader.
func TestParentContext_RandomSpanIDFailure(t *testing.T) {
	ctx := context.Background()
	cfg := &config.TracingConfig{
		TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", // valid 32-char hex (non-zero)
		// SpanID intentionally absent so ParentContext must generate one
	}

	origReader := rand.Reader
	rand.Reader = &errorReader{err: errors.New("entropy unavailable")}
	defer func() { rand.Reader = origReader }()

	parentCtx := ParentContext(ctx, cfg)

	assert.Equal(t, ctx, parentCtx, "ParentContext must return the original context when random span ID generation fails")
}
