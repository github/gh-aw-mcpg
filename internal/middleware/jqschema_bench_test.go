package middleware

import (
	"context"
	"strings"
	"testing"

	"github.com/itchyny/gojq"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// BenchmarkApplyJqSchema_CompiledCode benchmarks the current implementation
// that calls inferSchema directly (bypassing the gojq interpreter for schema walks)
func BenchmarkApplyJqSchema_CompiledCode(b *testing.B) {
	tests := []struct {
		name  string
		input interface{}
	}{
		{
			name:  "small object",
			input: map[string]interface{}{"name": "test", "count": 42, "active": true},
		},
		{
			name: "medium object",
			input: map[string]interface{}{
				"total_count": 1000,
				"items": []interface{}{
					map[string]interface{}{"id": 1, "name": "item1", "price": 10.5},
					map[string]interface{}{"id": 2, "name": "item2", "price": 20.5},
					map[string]interface{}{"id": 3, "name": "item3", "price": 30.5},
				},
			},
		},
		{
			name: "large nested object",
			input: map[string]interface{}{
				"user": map[string]interface{}{
					"id":       123,
					"login":    "testuser",
					"verified": true,
					"profile": map[string]interface{}{
						"bio":      "Test bio",
						"location": "Test location",
						"website":  "https://example.com",
					},
				},
				"repositories": []interface{}{
					map[string]interface{}{
						"id":          1,
						"name":        "repo1",
						"stars":       100,
						"description": "First repo",
						"owner": map[string]interface{}{
							"login": "owner1",
							"id":    999,
						},
					},
					map[string]interface{}{
						"id":          2,
						"name":        "repo2",
						"stars":       200,
						"description": "Second repo",
						"owner": map[string]interface{}{
							"login": "owner2",
							"id":    888,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := applyJqSchema(ctx, tt.input)
				require.NoError(b, err, "applyJqSchema failed")
			}
		})
	}
}

// BenchmarkApplyJqSchema_ParseEveryTime used to benchmark parsing the query on every
// invocation to quantify the compile-once speedup.
//
// NOTE: This benchmark is no longer valid. Since walk_schema is now a native Go
// function registered via gojq.WithFunction, running the parsed query without the
// corresponding function registration will fail at runtime with an undefined-function
// error. Skipping to avoid misleading benchmark output.
func BenchmarkApplyJqSchema_ParseEveryTime(b *testing.B) {
	b.Skip("invalid benchmark: walk_schema requires gojq.Compile with gojq.WithFunction registration; parse-only path no longer produces meaningful results")
}

// BenchmarkCompileVsParse compares the time to compile vs parse the jq query.
// The compile path uses walkSchemaCompileOpts so the walk_schema reference resolves
// exactly as it does in production.
func BenchmarkCompileVsParse(b *testing.B) {
	b.Run("parse_only", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := gojq.Parse(jqSchemaFilter)
			require.NoError(b, err, "Parse failed")
		}
	})

	b.Run("parse_and_compile", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			query, err := gojq.Parse(jqSchemaFilter)
			require.NoError(b, err, "Parse failed")
			_, err = gojq.Compile(query, walkSchemaCompileOpts()...)
			require.NoError(b, err, "Compile failed")
		}
	})
}

// BenchmarkWrapToolHandler_FastPath compares the fast-path (text content small enough
// to skip json.Marshal) against the normal marshal-and-size-check path.
//
// Run with: go test -bench=BenchmarkWrapToolHandler_FastPath -benchmem ./internal/middleware/
func BenchmarkWrapToolHandler_FastPath(b *testing.B) {
	baseDir := b.TempDir()

	sizes := []struct {
		name    string
		textLen int
	}{
		{"1KB", 1 * 1024},
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
	}

	for _, tt := range sizes {
		innerText := strings.Repeat("x", tt.textLen)
		innerData := map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "text", "text": innerText},
			},
			"isError": false,
		}

		makeHandler := func() func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
			return func(ctx context.Context, req *sdk.CallToolRequest, args interface{}) (*sdk.CallToolResult, interface{}, error) {
				return &sdk.CallToolResult{
					Content: []sdk.Content{&sdk.TextContent{Text: innerText}},
				}, innerData, nil
			}
		}

		// threshold >> text size: fast path always fires
		fastThreshold := tt.textLen + fastPathOverheadBound + 512*1024
		fastWrapped := WrapToolHandler(makeHandler(), "bench_tool", baseDir, "", fastThreshold, func(ctx context.Context) string { return "bench-session" })

		// Threshold tuned so fast path does not fire (textLen > threshold-fastPathOverheadBound),
		// but the payload remains under the threshold so we benchmark marshal overhead without disk I/O.
		slowThreshold := tt.textLen + fastPathOverheadBound - 1
		slowWrapped := WrapToolHandler(makeHandler(), "bench_tool", baseDir, "", slowThreshold, func(ctx context.Context) string { return "bench-session" })
		b.Run("fast-path/"+tt.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			ctx := context.Background()
			req := &sdk.CallToolRequest{}
			for i := 0; i < b.N; i++ {
				_, _, _ = fastWrapped(ctx, req, nil)
			}
		})

		b.Run("marshal-path/"+tt.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			ctx := context.Background()
			req := &sdk.CallToolRequest{}
			for i := 0; i < b.N; i++ {
				_, _, _ = slowWrapped(ctx, req, nil)
			}
		})
	}
}

// The optimized version slices the byte slice before converting to string, avoiding
// a full allocation of the entire (potentially multi-MB) payload as a string.
func BenchmarkPreviewCreation(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"10KB", 10 * 1024},
		{"100KB", 100 * 1024},
		{"1MB", 1 * 1024 * 1024},
	}

	for _, tt := range sizes {
		// Build a payload larger than PayloadPreviewSize
		payload := make([]byte, tt.size)
		for i := range payload {
			payload[i] = 'x'
		}

		b.Run("optimized/"+tt.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Optimized: slice bytes before converting to string
				_ = string(payload[:PayloadPreviewSize]) + "..."
			}
		})

		b.Run("original/"+tt.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// Original: convert full payload to string first
				s := string(payload)
				_ = s[:PayloadPreviewSize] + "..."
			}
		})
	}
}

// TestWalkSchemaCompileOpts_CompilesFilter guards the benchmark compile path: the
// filter must compile with the shared walk_schema options and produce the same
// output as the production compiled code.
func TestWalkSchemaCompileOpts_CompilesFilter(t *testing.T) {
	query, err := gojq.Parse(jqSchemaFilter)
	require.NoError(t, err)

	code, err := gojq.Compile(query, walkSchemaCompileOpts()...)
	require.NoError(t, err, "filter must compile with walk_schema registered")

	input := map[string]interface{}{"name": "test", "count": 42}
	got, ok := code.RunWithContext(context.Background(), input).Next()
	require.True(t, ok, "compiled filter should produce one output")
	assert.Equal(t, inferSchema(input), got)
}
