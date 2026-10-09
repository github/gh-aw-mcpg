package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/github/gh-aw-mcpg/internal/jqutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/mcpresult"
	"github.com/github/gh-aw-mcpg/internal/urlutil"
	"github.com/github/gh-aw-mcpg/internal/util"
	"github.com/itchyny/gojq"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logMiddleware = logger.ForFile()

// DefaultJqTimeout is the default timeout for jq query execution (5 seconds)
// This prevents malformed queries or large payloads from causing hangs
const DefaultJqTimeout = 5 * time.Second

// PayloadPreviewSize is the maximum number of characters to include in the payload preview
// This controls how much of the original payload is returned inline when a payload is stored to disk
const PayloadPreviewSize = 500

// PayloadTruncatedInstructions is the message returned to clients when a payload
// has been truncated and saved to the filesystem
const PayloadTruncatedInstructions = "The payload was too large for an MCP response. The complete original response data is saved as a JSON file at payloadPath. The file contains valid JSON that can be parsed directly. The payloadSchema shows the structure and types of fields in the full response, but not the actual values. To access the full data with all values, read and parse the JSON file at payloadPath."

// fastPathOverheadBound is the conservative upper bound (in bytes) for the JSON
// envelope that wraps the first text content item when data is serialised.
// The outer structure is roughly: {"content":[{"type":"text","text":"..."}],"isError":false}
// which adds ~52 bytes, plus up to ~1 KiB for any appended DIFC notice items.
// Using 4 KiB as the margin ensures we never skip disk-storage for a payload that
// actually exceeds the threshold due to envelope overhead.
const fastPathOverheadBound = 4 * 1024

// PayloadMetadata represents the metadata response returned when a payload is too large
// and has been saved to the filesystem
type PayloadMetadata struct {
	AgentInstructions string `json:"agentInstructions"`
	PayloadPath       string `json:"payloadPath"`
	PayloadPreview    string `json:"payloadPreview"`
	PayloadSchema     any    `json:"payloadSchema"`
	OriginalSize      int    `json:"originalSize"`
	QueryID           string `json:"-"` // Internal use only, not serialized to clients
}

// jqSchemaFilter is the jq entry point that invokes the native Go walk_schema function.
// The recursive schema-walk logic is implemented in inferSchema (see below) and registered
// via gojq.WithFunction, so the filter itself is a single call.
//
// walk_schema intentionally uses WithFunction (not WithIterFunction): it has a strict
// single-output contract and always returns exactly one schema value.
//
// The transformation replaces every leaf value with its jq type name:
//
//	Input:  {"name": "test", "count": 42, "items": [{"id": 1}]}
//	Output: {"name": "string", "count": "number", "items": [{"id": "number"}]}
//
// For arrays, only the first element's schema is retained to represent the array structure.
// Empty arrays are preserved as [].
const jqSchemaFilter = `walk_schema`

// Pre-compiled jq query code for performance.
// This is compiled once at package initialization and reused for all requests.
var (
	jqSchemaCode       *gojq.Code
	jqSchemaCompileErr error
)

// filterCodeCache caches compiled tool-response filter code.
//
// Key shapes:
//   - string (raw filter expression) for CompileToolResponseFilter
//   - toolResponseFilterVarsCacheKey for CompileToolResponseFilterWithVars
//
// Entries are stored on first call and reused on subsequent calls with the same
// key, avoiding redundant parse+compile work when multiple tools share identical
// filter definitions.
//
// The cache is unbounded and grows with the number of unique filter expressions.
// In practice this is bounded by the number of distinct filter strings in the
// gateway configuration, which is typically small (one per tool).
//
// TODO: if config hot-reload is added, replace sync.Map with a bounded LRU cache.
var filterCodeCache sync.Map

type toolResponseFilterVarsCacheKey struct {
	filter      string
	varNamesKey string
}

// secureCompileOpts delegates to jqutil.SecureCompileOpts so that all packages
// share a single authoritative definition of the $ENV-disabled gojq compile options.
var secureCompileOpts = jqutil.SecureCompileOpts

// init compiles the jq schema filter at startup as a canary check.
//
// applyJqSchema no longer runs the gojq interpreter at request time — it calls
// inferSchema directly for lower latency. This init() serves two purposes:
//  1. Startup validation: panics early if the jqSchemaFilter expression is invalid.
//  2. Parity canary: TestInferSchema_MatchesJqOutput exercises both paths to detect
//     any future divergence between inferSchema and the jq reference implementation.
func init() {
	query, err := gojq.Parse(jqSchemaFilter)
	if err != nil {
		panic(fmt.Sprintf("built-in jq schema filter failed to parse: %v", err))
	}

	jqSchemaCode, jqSchemaCompileErr = gojq.Compile(query,
		append(secureCompileOpts,
			gojq.WithFunction("walk_schema", 0, 0, func(v any, _ []any) any {
				return inferSchema(v)
			}),
		)...,
	)
	if jqSchemaCompileErr != nil {
		panic(fmt.Sprintf("built-in jq schema filter failed to compile: %v", jqSchemaCompileErr))
	}

	logger.LogInfo("startup", "jq schema filter compiled successfully - native Go walk_schema (direct-call fast path active), array limit: 2^29 elements")
}

// queryIDBytes is the number of random bytes used to generate a query ID.
// The resulting hex string has length 2*queryIDBytes (32 characters).
const queryIDBytes = 16

// applyJqSchema applies the jq schema transformation to JSON data.
//
// The schema walk is implemented as a native Go recursive function (inferSchema),
// which is called directly here instead of going through the gojq interpreter.
// This eliminates the per-call overhead of gojq iterator setup, context-with-timeout
// allocation, and interpreter dispatch — significant savings on the large-payload path
// where this function is called for every tool response that exceeds the size threshold.
//
// Context cancellation is respected: if ctx is already done on entry, an error is returned
// immediately. This preserves the observable behaviour of the previous gojq-based
// implementation for callers that supply a pre-cancelled context.
//
// Returns the schema as an any object (not a JSON string).
func applyJqSchema(ctx context.Context, jsonData any) (any, error) {
	// Check context before doing any work, mirroring the behaviour of the former
	// gojq-based path which checked ctx before running the iterator.
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("schema inference failed: %w", err)
	}

	logMiddleware.Printf("applyJqSchema: starting schema inference, dataType=%T", jsonData)

	v := inferSchema(jsonData)

	logMiddleware.Printf("applyJqSchema: schema inference completed, resultType=%T", v)
	return v, nil
}

type runJqCodeOptions struct {
	// ExecutionPrefix overrides the prefix used for context cancellation/timeout errors.
	// When empty, errPrefix is reused.
	ExecutionPrefix string
	// LogDefaultTimeout emits the standard timeout log when this helper applies DefaultJqTimeout.
	LogDefaultTimeout bool
	// CheckMultipleResults enforces a single-output contract and returns an error when the
	// iterator produces additional values.
	CheckMultipleResults bool
}

// runJqCode executes precompiled gojq code against jsonData and applies the standard
// middleware timeout/error handling behavior shared by jq schema and tool response filters.
func runJqCode(
	ctx context.Context,
	code *gojq.Code,
	jsonData any,
	errPrefix string,
	opts runJqCodeOptions,
) (any, error) {
	executionPrefix := opts.ExecutionPrefix
	if executionPrefix == "" {
		executionPrefix = errPrefix
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultJqTimeout)
		defer cancel()
		if opts.LogDefaultTimeout {
			logMiddleware.Printf("Applied default timeout of %v to jq query execution", DefaultJqTimeout)
		}
	}

	iter := code.RunWithContext(ctx, jsonData)
	v, ok := iter.Next()
	if !ok {
		logMiddleware.Printf("runJqCode: %s returned no results", errPrefix)
		return nil, fmt.Errorf("%s returned no results", errPrefix)
	}

	if err, ok := v.(error); ok {
		if ctx.Err() != nil {
			logMiddleware.Printf("runJqCode: %s aborted by context: ctxErr=%v", executionPrefix, ctx.Err())
			return nil, fmt.Errorf("%s execution failed: %w", executionPrefix, errors.Join(err, ctx.Err()))
		}
		var haltErr *gojq.HaltError
		if errors.As(err, &haltErr) {
			logMiddleware.Printf("runJqCode: %s halted: exitCode=%d, hasValue=%v", errPrefix, haltErr.ExitCode(), haltErr.Value() != nil)
			if haltErr.Value() == nil {
				return nil, fmt.Errorf("%s halted cleanly with no output", errPrefix)
			}
			return nil, fmt.Errorf("%s halted with error (exit code %d): %w", errPrefix, haltErr.ExitCode(), err)
		}
		// gojq.ValueError is implemented by errors that carry a value (e.g., thrown by jq's
		// error/1 builtin). These are intentional and their message is already descriptive.
		var valErr gojq.ValueError
		if errors.As(err, &valErr) {
			return nil, fmt.Errorf("%s error: %w", errPrefix, err)
		}
		// All remaining errors are runtime type errors (e.g., "length cannot be applied to:
		// null"). Include a hint to help authors write null-safe filters.
		return nil, fmt.Errorf("%s type error: %w (check filter handles null/missing fields)", errPrefix, err)
	}

	if opts.CheckMultipleResults {
		if extra, ok := iter.Next(); ok {
			logMiddleware.Printf("runJqCode: %s returned multiple results: firstType=%T, extraType=%T", errPrefix, v, extra)
			return nil, fmt.Errorf("%s returned multiple results — use array form ([.a, .b]) to combine outputs into a single value; first=%T extra=%T", errPrefix, v, extra)
		}
	}

	return v, nil
}

// CompileToolResponseFilter parses and compiles a jq expression used to transform
// tool response payloads. Compiled code is cached by filter expression string so that
// identical filters configured on multiple tools are only compiled once.
//
// For parameterized filters that need to incorporate per-call values such as server IDs,
// session metadata, or user-controlled data, use CompileToolResponseFilterWithVars instead.
func CompileToolResponseFilter(filter string) (*gojq.Code, error) {
	return compileToolResponseFilterInternal(
		filter,
		filter,
		secureCompileOpts,
		"CompileToolResponseFilter",
		"",
	)
}

// CompileToolResponseFilterWithVars parses and compiles a jq expression that references
// the named variables. Variable names must be prefixed with "$" (e.g. "$serverID").
//
// Values are bound at run time by passing them in the same order to code.RunWithContext:
//
//	code, _ := CompileToolResponseFilterWithVars(expr, []string{"$serverID", "$sessionID"})
//	iter := code.RunWithContext(ctx, data, serverID, sessionID)
//
// Binding values at the Go level is injection-safe: they are never interpolated into
// the jq expression string.
//
// Compiled code is cached by the composite key (filter + variable names). The same
// (filter, varNames) tuple recurs on every tool invocation, so caching here eliminates
// repeated gojq.Parse + gojq.Compile calls on the parameterized-filter hot path.
func CompileToolResponseFilterWithVars(filter string, varNames []string) (*gojq.Code, error) {
	cacheKey := toolResponseFilterVarsCacheKey{
		filter:      filter,
		varNamesKey: buildVarNamesCacheKey(varNames),
	}
	return compileToolResponseFilterInternal(
		cacheKey,
		filter,
		jqutil.CompileOptsWithVariables(varNames),
		"CompileToolResponseFilterWithVars",
		fmt.Sprintf(", vars=%v", varNames),
	)
}

func compileToolResponseFilterInternal[K comparable](
	cacheKey K,
	filter string,
	compileOpts []gojq.CompilerOption,
	logFunctionName string,
	logSuffix string,
) (*gojq.Code, error) {
	if cached, ok := filterCodeCache.Load(cacheKey); ok {
		code, ok := cached.(*gojq.Code)
		if !ok {
			// Should never happen; the cache only stores *gojq.Code values.
			return nil, fmt.Errorf("internal error: unexpected cached value type %T for filter (len=%d)", cached, len(filter))
		}
		logMiddleware.Printf("%s: cache hit, len=%d%s", logFunctionName, len(filter), logSuffix)
		return code, nil
	}

	logMiddleware.Printf("%s: parsing jq filter expression, len=%d%s", logFunctionName, len(filter), logSuffix)
	query, err := gojq.Parse(filter)
	if err != nil {
		return nil, fmt.Errorf("failed to parse tool response filter%s: %w", jqutil.ParseErrorDetails(err), err)
	}

	code, err := gojq.Compile(query, compileOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to compile tool response filter: %w", err)
	}

	filterCodeCache.Store(cacheKey, code)
	logMiddleware.Printf("%s: filter compiled and cached successfully", logFunctionName)
	return code, nil
}

func buildVarNamesCacheKey(varNames []string) string {
	if len(varNames) == 0 {
		return ""
	}
	const estimatedBytesPerVar = 12 // ~len-digits + ":" + typical var name + ";"
	var b strings.Builder
	b.Grow(len(varNames) * estimatedBytesPerVar)
	for _, varName := range varNames {
		b.WriteString(strconv.Itoa(len(varName)))
		b.WriteByte(':')
		b.WriteString(varName)
		b.WriteByte(';')
	}
	return b.String()
}

// applyToolResponseFilter applies a pre-compiled jq filter to tool response data.
// It is the internal implementation used by ApplyToolResponseFilter (which compiles
// the filter from a string) and by tryApplyToolResponseFilter (which applies it
// within the wrapToolHandler pipeline).
func applyToolResponseFilter(ctx context.Context, code *gojq.Code, jsonData any) (any, error) {
	return runJqCode(ctx, code, jsonData, "tool response filter", runJqCodeOptions{
		CheckMultipleResults: true,
	})
}

// ApplyToolResponseFilter compiles and applies a jq expression to tool response data.
func ApplyToolResponseFilter(ctx context.Context, filter string, jsonData any) (any, error) {
	code, err := CompileToolResponseFilter(filter)
	if err != nil {
		return nil, err
	}
	return applyToolResponseFilter(ctx, code, jsonData)
}

func tryApplyToolResponseFilter(
	ctx context.Context,
	filterCode *gojq.Code,
	result *sdk.CallToolResult,
	data any,
	toolName string,
	queryID string,
) (*sdk.CallToolResult, any) {
	logMiddleware.Printf("tryApplyToolResponseFilter: tool=%s, queryID=%s, hasFilter=%v", toolName, queryID, filterCode != nil)
	if filterCode == nil {
		return result, data
	}

	if result != nil && len(result.Content) > 0 {
		if textContent, ok := result.Content[0].(*sdk.TextContent); ok {
			var textPayload any
			if err := json.Unmarshal([]byte(textContent.Text), &textPayload); err == nil {
				filteredPayload, filterErr := applyToolResponseFilter(ctx, filterCode, textPayload)
				if filterErr != nil {
					logger.LogWarn("payload", "Failed to apply tool response filter to text payload, returning original response: tool=%s, queryID=%s, error=%v",
						toolName, queryID, filterErr)
					return result, data
				}

				filteredJSON, marshalErr := json.Marshal(filteredPayload)
				if marshalErr != nil {
					logger.LogWarn("payload", "Failed to marshal filtered text payload, returning original response: tool=%s, queryID=%s, error=%v",
						toolName, queryID, marshalErr)
					return result, data
				}

				return rewriteFilteredTextPayload(result, data, string(filteredJSON))
			}
		}
	}

	filteredData, filterErr := applyToolResponseFilter(ctx, filterCode, data)
	if filterErr != nil {
		logger.LogWarn("payload", "Failed to apply tool response filter, returning original response: tool=%s, queryID=%s, error=%v",
			toolName, queryID, filterErr)
		return result, data
	}

	filteredResult, convertErr := mcp.ConvertToCallToolResult(filteredData)
	if convertErr != nil {
		logger.LogWarn("payload", "Failed to rebuild filtered tool response, returning original response: tool=%s, queryID=%s, error=%v",
			toolName, queryID, convertErr)
		return result, data
	}

	// Filtering rewrites the primary serialized JSON payload from the first
	// text content item only. Preserve metadata fields unconditionally, and
	// preserve trailing content items (such as DIFC notices) when the leading
	// item is the rewritten JSON text payload.
	if len(result.Content) > 1 {
		if _, ok := result.Content[0].(*sdk.TextContent); ok {
			filteredResult.Content = append(filteredResult.Content, result.Content[1:]...)
		}
	}
	filteredResult.IsError = result.IsError
	filteredResult.Meta = result.Meta

	return filteredResult, filteredData
}

// WrapToolHandler wraps a tool handler with jqschema middleware
// This middleware:
// 1. Generates a random ID for the query
// 2. Extracts session ID from context (or uses "default")
// 3. If payload size > sizeThreshold: saves to {baseDir}/{sessionID}/{queryID}/payload.json and returns metadata
// 4. If payload size <= sizeThreshold: returns original response directly (no file storage)
// 5. For large payloads: returns first PayloadPreviewSize chars of payload + jq inferred schema
// 6. Uses pathPrefix to remap returned payloadPath for clients (if configured)
func WrapToolHandler(
	handler func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error),
	toolName string,
	baseDir string,
	pathPrefix string,
	sizeThreshold int,
	getSessionID func(context.Context) string,
) func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
	return wrapToolHandler(handler, toolName, baseDir, pathPrefix, sizeThreshold, getSessionID, "")
}

// WrapToolHandlerWithFilter wraps a tool handler with jq response filtering followed
// by the standard large-payload jqschema middleware behavior.
func WrapToolHandlerWithFilter(
	handler func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error),
	toolName string,
	baseDir string,
	pathPrefix string,
	sizeThreshold int,
	getSessionID func(context.Context) string,
	filter string,
) func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
	return wrapToolHandler(handler, toolName, baseDir, pathPrefix, sizeThreshold, getSessionID, filter)
}

// wrapToolHandler is the shared implementation for WrapToolHandler and
// WrapToolHandlerWithFilter. When filter is empty the jq response-filter step is
// skipped; WrapToolHandler always passes an empty filter while
// WrapToolHandlerWithFilter passes the caller-supplied expression.
func wrapToolHandler(
	handler func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error),
	toolName string,
	baseDir string,
	pathPrefix string,
	sizeThreshold int,
	getSessionID func(context.Context) string,
	filter string,
) func(context.Context, *sdk.CallToolRequest, any) (*sdk.CallToolResult, any, error) {
	logMiddleware.Printf("WrapToolHandler: wrapping tool=%s, sizeThreshold=%d bytes, baseDir=%s, pathPrefixSet=%v",
		toolName, sizeThreshold, baseDir, pathPrefix != "")

	var filterCode *gojq.Code
	if strings.TrimSpace(filter) != "" {
		compiled, err := CompileToolResponseFilter(filter)
		if err != nil {
			logger.LogWarn("payload", "Failed to compile tool response filter during handler setup: tool=%s, error=%v", toolName, err)
		} else {
			filterCode = compiled
		}
	}

	return func(ctx context.Context, req *sdk.CallToolRequest, args any) (*sdk.CallToolResult, any, error) {
		// Generate random query ID
		queryID := util.RandomHexWithFallback(queryIDBytes)

		// Get session ID from context
		sessionID := getSessionID(ctx)
		if sessionID == "" {
			sessionID = "default"
		}

		logger.LogDebug("payload", "Middleware processing tool call: tool=%s, queryID=%s, session=%s, baseDir=%s",
			toolName, queryID, util.FormatSessionIDForLog(sessionID), baseDir)

		// Call the original handler
		result, data, err := handler(ctx, req, args)
		if err != nil {
			logger.LogDebug("payload", "Tool call failed, skipping payload storage: tool=%s, queryID=%s, error=%v",
				toolName, queryID, err)
			return result, data, err
		}

		// Only process successful results with data
		if result == nil || result.IsError || data == nil {
			logger.LogDebug("payload", "Skipping payload storage: tool=%s, queryID=%s, reason=%s",
				toolName, queryID,
				func() string {
					if result == nil {
						return "result is nil"
					} else if result.IsError {
						return "result indicates error"
					} else {
						return "no data returned"
					}
				}())
			return result, data, err
		}

		result, data = tryApplyToolResponseFilter(ctx, filterCode, result, data, toolName, queryID)
		auditObservedURLDomains(toolName, data)

		// Fast path: when the handler (or filter) sets a text content item, its byte
		// length is a reliable lower bound for json.Marshal(data). The outer JSON
		// envelope (content array wrapper, isError flag, any DIFC notice items) adds
		// at most fastPathOverheadBound bytes of overhead. If the text is clearly
		// within the threshold, skip the expensive json.Marshal allocation and return
		// immediately.
		//
		// This optimisation is only applied when the threshold is large enough to
		// absorb the overhead margin (sizeThreshold > fastPathOverheadBound), so it
		// does not affect exact-byte-count test scenarios with small thresholds.
		if sizeThreshold > fastPathOverheadBound && len(result.Content) > 0 {
			if tc, ok := result.Content[0].(*sdk.TextContent); ok {
				if env, ok := data.(map[string]any); ok {
					// Only apply fast path when the backing payload is the simple one-text-item MCP envelope.
					if len(env) <= 2 {
						onlyKnownKeys := true
						for k := range env {
							if k != "content" && k != "isError" {
								onlyKnownKeys = false
								break
							}
						}
						if onlyKnownKeys {
							items, ok := mcpresult.NormalizeContentItems(env["content"])
							var first map[string]any
							switch c := env["content"].(type) {
							case []any:
								if len(c) == 1 && ok && len(items) == 1 {
									first = items[0]
								}
							case []map[string]any:
								if len(c) == 1 && ok && len(items) == 1 {
									first = items[0]
								}
							}
							if first != nil {
								if itemType, _ := first["type"].(string); itemType == "text" {
									if text, _ := first["text"].(string); text == tc.Text && len(text) <= sizeThreshold-fastPathOverheadBound {
										logMiddleware.Printf("fast-path: content_text_len=%d within threshold-%d=%d, skipping json.Marshal: tool=%s, queryID=%s",
											len(text), fastPathOverheadBound, sizeThreshold-fastPathOverheadBound, toolName, queryID)
										return result, data, nil
									}
								}
							}
						}
					}
				}
			}
		}

		// Marshal the response data to JSON
		payloadJSON, marshalErr := json.Marshal(data)
		if marshalErr != nil {
			logger.LogError("payload", "Failed to marshal response data to JSON: tool=%s, queryID=%s, error=%v",
				toolName, queryID, marshalErr)
			return result, data, err
		}

		payloadSize := len(payloadJSON)
		logger.LogInfo("payload", "Response data marshaled to JSON: tool=%s, queryID=%s, size=%d bytes (%.2f KB, %.2f MB), threshold=%d bytes",
			toolName, queryID, payloadSize, float64(payloadSize)/1024, float64(payloadSize)/(1024*1024), sizeThreshold)

		// Check if payload size is within threshold - if so, return original response directly
		if payloadSize <= sizeThreshold {
			logger.LogInfo("payload", "Payload size (%d bytes) is within threshold (%d bytes), returning inline without file storage: tool=%s, queryID=%s",
				payloadSize, sizeThreshold, toolName, queryID)
			// Return the original result without modification
			return result, data, err
		}

		// Payload is larger than threshold - save to filesystem
		logger.LogInfo("payload", "Payload size (%d bytes) exceeds threshold (%d bytes), saving to filesystem: tool=%s, queryID=%s, session=%s, baseDir=%s",
			payloadSize, sizeThreshold, toolName, queryID, util.FormatSessionIDForLog(sessionID), baseDir)

		filePath, saveErr := savePayload(baseDir, pathPrefix, sessionID, queryID, payloadJSON)
		if saveErr != nil {
			logger.LogError("payload", "Failed to save payload to filesystem: tool=%s, queryID=%s, session=%s, error=%v",
				toolName, queryID, util.FormatSessionIDForLog(sessionID), saveErr)
			// Continue even if save fails - don't break the tool call
		} else {
			logger.LogInfo("payload", "Payload storage completed successfully: tool=%s, queryID=%s, session=%s, size=%d bytes",
				toolName, queryID, util.FormatSessionIDForLog(sessionID), len(payloadJSON))
		}

		// Apply jq schema transformation
		logger.LogDebug("payload", "Applying jq schema transformation: tool=%s, queryID=%s", toolName, queryID)
		var schemaObj any
		if schemaErr := func() error {
			// Prepare data for jq processing. If data is already a native JSON-compatible
			// Go type, use it directly to avoid a redundant JSON round-trip.
			var jsonData any
			switch v := data.(type) {
			case map[string]any, []any, string, float64, bool:
				jsonData = data
			case nil:
				// nil data produces a nil schema; nothing meaningful to infer.
				return nil
			case json.Number:
				// json.Number is emitted by decoders using UseNumber(); convert to
				// float64 so jq sees a plain number value rather than an opaque type.
				f, convErr := v.Float64()
				if convErr != nil {
					return fmt.Errorf("failed to convert json.Number to float64: %w", convErr)
				}
				jsonData = f
			default:
				if err := json.Unmarshal(payloadJSON, &jsonData); err != nil {
					return fmt.Errorf("failed to unmarshal for schema: %w", err)
				}
			}

			schema, err := applyJqSchema(ctx, jsonData)
			if err != nil {
				return err
			}
			schemaObj = schema
			return nil
		}(); schemaErr != nil {
			logger.LogWarn("payload", "Failed to generate schema for payload: tool=%s, queryID=%s, error=%v",
				toolName, queryID, schemaErr)
			// Continue with original response if schema extraction fails
			return result, data, err
		}

		logger.LogDebug("payload", "Schema transformation completed: tool=%s, queryID=%s", toolName, queryID)

		// Build the transformed response: first PayloadPreviewSize bytes + schema.
		// Slice the bytes before converting to string to avoid allocating a full copy of the
		// (potentially multi-MB) payload when only a short preview is needed.
		//
		// json.Marshal emits raw UTF-8 for non-ASCII runes, so a naive byte slice could
		// split a multi-byte sequence. We adjust the cut point backward to the nearest
		// valid rune boundary to guarantee the preview is valid UTF-8.
		payloadLen := len(payloadJSON)
		var preview string
		truncated := payloadLen > PayloadPreviewSize
		if truncated {
			cutPoint := PayloadPreviewSize
			for cutPoint > 0 && !utf8.RuneStart(payloadJSON[cutPoint]) {
				cutPoint--
			}
			preview = string(payloadJSON[:cutPoint]) + "..."
			logger.LogInfo("payload", "Payload truncated for preview: tool=%s, queryID=%s, originalSize=%d bytes, previewSize=%d bytes",
				toolName, queryID, payloadLen, PayloadPreviewSize)
		} else {
			preview = string(payloadJSON)
			logger.LogDebug("payload", "Payload small enough for full preview: tool=%s, queryID=%s, size=%d bytes",
				toolName, queryID, payloadLen)
		}

		// Create rewritten response using the PayloadMetadata struct
		rewrittenResponse := PayloadMetadata{
			AgentInstructions: PayloadTruncatedInstructions,
			PayloadPath:       filePath,
			PayloadPreview:    preview,
			PayloadSchema:     schemaObj,
			OriginalSize:      payloadLen,
			QueryID:           queryID,
		}

		logger.LogInfo("payload", "Created metadata response for client: tool=%s, queryID=%s, session=%s, originalSize=%d bytes, truncated=%v",
			toolName, queryID, util.FormatSessionIDForLog(sessionID), payloadLen, truncated)

		// Marshal the rewritten response to JSON for the Content field
		rewrittenJSON, marshalErr := json.Marshal(rewrittenResponse)
		if marshalErr != nil {
			logger.LogError("payload", "Failed to marshal metadata response: tool=%s, queryID=%s, error=%v",
				toolName, queryID, marshalErr)
			// Fall back to original result if we can't marshal
			return result, rewrittenResponse, nil
		}

		logger.LogDebug("payload", "Metadata response marshaled: tool=%s, queryID=%s, metadataSize=%d bytes",
			toolName, queryID, len(rewrittenJSON))

		// Create a new CallToolResult with the transformed content
		// Replace the original content with our rewritten response
		transformedResult := &sdk.CallToolResult{
			Content: []sdk.Content{
				&sdk.TextContent{
					Text: string(rewrittenJSON),
				},
			},
			IsError: result.IsError,
			Meta:    result.Meta,
		}

		logger.LogInfo("payload", "Returning transformed response to client: tool=%s, queryID=%s, session=%s, clientReceivesMetadata=true",
			toolName, queryID, util.FormatSessionIDForLog(sessionID))

		return transformedResult, rewrittenResponse, nil
	}
}

// ShouldApplyMiddleware determines if the middleware should be applied to a tool
// Currently applies to all tools, but can be configured to filter specific tools
func ShouldApplyMiddleware(toolName string) bool {
	// Apply to all tools except sys tools
	applies := !strings.HasPrefix(toolName, "sys___")
	logMiddleware.Printf("ShouldApplyMiddleware: tool=%s, applies=%v", toolName, applies)
	return applies
}

func auditObservedURLDomains(toolName string, data any) {
	if !logger.URLDomainAuditEnabled() || data == nil {
		return
	}
	serverID := util.ParseServerIDFromToolName(toolName)
	domains := urlutil.ExtractURLDomainsFromValue(data)
	if len(domains) == 0 {
		return
	}
	logger.LogObservedURLDomains(serverID, domains)
}

// savePayload saves the payload to disk and returns the file path
// The file is saved to {baseDir}/{sessionID}/{queryID}/payload.json
// The returned path uses pathPrefix if provided, otherwise returns the actual filesystem path
func savePayload(baseDir, pathPrefix, sessionID, queryID string, payload []byte) (string, error) {
	// Create directory structure: {baseDir}/{sessionID}/{queryID}
	dir := filepath.Join(baseDir, sessionID, queryID)

	logger.LogDebug("payload", "Creating payload directory: baseDir=%s, session=%s, query=%s",
		baseDir, util.FormatSessionIDForLog(sessionID), queryID)

	if err := os.MkdirAll(dir, 0755); err != nil {
		sanitizedErr := strings.ReplaceAll(err.Error(), sessionID, util.FormatSessionIDForLog(sessionID))
		logger.LogError("payload", "Failed to create payload directory: session=%s, error=%s", util.FormatSessionIDForLog(sessionID), sanitizedErr)
		return "", fmt.Errorf("failed to create payload directory: %s", sanitizedErr)
	}

	logger.LogDebug("payload", "Successfully created payload directory: session=%s, query=%s, permissions=0755",
		util.FormatSessionIDForLog(sessionID), queryID)

	// Save payload to file with restrictive permissions (owner read/write only)
	filePath := filepath.Join(dir, "payload.json")
	payloadSize := len(payload)

	logger.LogInfo("payload", "Writing large payload to filesystem: session=%s, query=%s, size=%d bytes (%.2f KB, %.2f MB)",
		util.FormatSessionIDForLog(sessionID), queryID, payloadSize, float64(payloadSize)/1024, float64(payloadSize)/(1024*1024))

	if err := os.WriteFile(filePath, payload, 0600); err != nil {
		sanitizedErr := strings.ReplaceAll(err.Error(), sessionID, util.FormatSessionIDForLog(sessionID))
		logger.LogError("payload", "Failed to write payload file: session=%s, query=%s, size=%d bytes, error=%s",
			util.FormatSessionIDForLog(sessionID), queryID, payloadSize, sanitizedErr)
		return "", fmt.Errorf("failed to write payload file: %s", sanitizedErr)
	}

	// Enforce permissions even if the file already existed (WriteFile only sets mode on create)
	if err := os.Chmod(filePath, 0600); err != nil {
		sanitizedErr := strings.ReplaceAll(err.Error(), sessionID, util.FormatSessionIDForLog(sessionID))
		logger.LogError("payload", "Failed to enforce payload file permissions: session=%s, query=%s, size=%d bytes, error=%s",
			util.FormatSessionIDForLog(sessionID), queryID, payloadSize, sanitizedErr)
		return "", fmt.Errorf("failed to set payload file permissions: %s", sanitizedErr)
	}

	// Log with the requested chmod mode rather than re-stating as a guarantee (Chmod may be
	// a no-op on some platforms/filesystems while still returning nil).
	logger.LogInfo("payload", "Successfully saved large payload to filesystem: session=%s, query=%s, size=%d bytes, chmod=0600",
		util.FormatSessionIDForLog(sessionID), queryID, payloadSize)

	// If pathPrefix is provided, use it to remap the path for the client
	// This allows the gateway to save files at one path (e.g., /tmp/jq-payloads)
	// while returning a different path to clients (e.g., /workspace/payloads)
	returnPath := filePath
	if pathPrefix != "" {
		// Replace baseDir with pathPrefix in the file path
		relPath := filepath.Join(sessionID, queryID, "payload.json")
		returnPath = filepath.Join(pathPrefix, relPath)
		logger.LogInfo("payload", "Remapped payload path for client: session=%s, query=%s, pathPrefix=%s",
			util.FormatSessionIDForLog(sessionID), queryID, pathPrefix)
	}

	return returnPath, nil
}

// inferSchema recursively walks a JSON-compatible Go value and replaces every leaf
// with its jq type name ("null", "boolean", "number", "string"). Objects are
// traversed key-by-key; arrays are collapsed to a single representative element (or
// [] when empty). The output mirrors what the previous pure-jq walk_schema filter
// produced, but runs entirely in Go, bypassing jq interpreter overhead for recursion.
//
// Type mapping (matches jq's built-in type function):
//   - nil                                          → "null"
//   - bool                                         → "boolean"
//   - any integer or floating-point numeric type   → "number"
//     (float32/64, int/8/16/32/64, uint/8/16/32/64, json.Number)
//   - string                                       → "string"
//   - map[string]any                               → recursed object
//   - []any                                        → recursed array (first element only)
func inferSchema(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, child := range val {
			result[k] = inferSchema(child)
		}
		return result
	case []any:
		if len(val) == 0 {
			return []any{}
		}
		return []any{inferSchema(val[0])}
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		json.Number:
		return "number"
	case string:
		return "string"
	default:
		// Defensive fallback: classify any remaining numeric reflect.Kind as "number"
		// and everything else as "string" to keep the schema output valid.
		switch reflect.TypeOf(v).Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return "number"
		default:
			return "string"
		}
	}
}
