package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/guard"
	"github.com/github/gh-aw-mcpg/internal/httputil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/strutil"
	"github.com/github/gh-aw-mcpg/internal/tracing"
)

var logHandler = logger.New("proxy:handler")

// toResultOrWriteEmpty calls ToResult on the labeled data. On success it returns
// (result, true). On error it logs the failure, writes an empty-shaped response,
// and returns (nil, false) so the caller can return early.
func (h *proxyHandler) toResultOrWriteEmpty(w http.ResponseWriter, resp *http.Response, responseData interface{}, labeled difc.LabeledData) (interface{}, bool) {
	result, err := labeled.ToResult()
	if err != nil {
		logHandler.Printf("[DIFC] Phase 5 ToResult failed: %v", err)
		h.writeEmptyResponse(w, resp, responseData)
		return nil, false
	}
	return result, true
}

// writeDIFCForbidden writes a 403 JSON response for DIFC policy violations.
// Uses the shared WriteErrorResponse helper so that the response shape is consistent
// with all other error responses in the gateway ({"error": ..., "message": ...}).
func writeDIFCForbidden(w http.ResponseWriter, message string) {
	httputil.WriteErrorResponse(w, http.StatusForbidden, "difc_forbidden", message)
}

// proxyHandler implements http.Handler and runs the DIFC pipeline on proxied requests.
type proxyHandler struct {
	server *Server
	tracing.CachedTracer
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip the /api/v3 prefix that GH_HOST adds
	rawPath := StripGHHostPrefix(r.URL.Path)
	// Preserve query string for upstream forwarding
	fullPath := rawPath
	if r.URL.RawQuery != "" {
		fullPath = rawPath + "?" + r.URL.RawQuery
	}

	logHandler.Printf("incoming %s %s", r.Method, rawPath)

	// Health check endpoint
	if rawPath == "/health" || rawPath == "/healthz" {
		httputil.WriteSimpleHealthResponse(w)
		return
	}

	// Reflect endpoint exposes a live DIFC label snapshot.
	if r.Method == http.MethodGet && rawPath == "/reflect" {
		httputil.WriteReflectResponse(w, h.server.DIFCComponents)
		return
	}

	// Safe metadata endpoints carry no user/repo-scoped data and can be passed
	// through without DIFC labeling.
	if r.Method == http.MethodGet && isMetadataPassthroughPath(rawPath) {
		h.passthrough(w, r, fullPath)
		return
	}

	// Only filter read operations (GET + GraphQL POST to /graphql)
	isGraphQL := IsGraphQLPath(rawPath)
	isRead := r.Method == http.MethodGet || (r.Method == http.MethodPost && isGraphQL)
	if !isRead {
		// Pass through write operations unmodified
		h.passthrough(w, r, fullPath)
		return
	}

	// Route the request to a guard tool name
	var toolName string
	var args map[string]interface{}
	var graphQLBody []byte

	if isGraphQL {
		// Read and parse the GraphQL body
		var err error
		graphQLBody, err = io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			httputil.WriteErrorResponse(w, http.StatusBadRequest, "bad_request", "failed to read request body")
			return
		}

		match := MatchGraphQL(graphQLBody)
		if match == nil {
			// Unknown GraphQL query — fail closed: deny rather than risk leaking unfiltered data
			logHandler.Printf("unknown GraphQL query, blocking request: %s", strutil.Truncate(string(graphQLBody), 500))
			httputil.WriteJSONResponse(w, http.StatusForbidden, map[string]interface{}{
				"errors": []map[string]string{{"message": "access denied: unrecognized GraphQL operation"}},
				"data":   nil,
			})
			return
		}
		// Schema introspection (__type, __schema) is safe metadata — passthrough without DIFC
		if match.ToolName == "graphql_introspection" {
			logHandler.Printf("GraphQL introspection query, passing through")
			clientAuth := r.Header.Get("Authorization")
			resp, respBody := h.forwardAndReadBody(w, r.Context(), http.MethodPost, fullPath, bytes.NewReader(graphQLBody), "application/json", clientAuth)
			if resp == nil {
				return
			}
			h.writeResponse(w, resp, respBody)
			return
		}
		toolName = match.ToolName
		args = match.Args

		// Inject guard-required fields (author{login}, authorAssociation) into
		// the GraphQL query so the guard can label items without enrichment.
		graphQLBody = InjectGuardFields(graphQLBody, toolName)
	} else {
		match := MatchRoute(rawPath)
		if match == nil {
			h.handleUnrecognizedPassthrough(w, r, rawPath, fullPath)
			return
		}
		toolName = match.ToolName
		args = match.Args

		// Pass search query parameter so the guard can scope integrity labels
		if q := r.URL.Query().Get("q"); q != "" {
			args["query"] = q
		}
	}

	// Run the DIFC pipeline
	h.handleWithDIFC(w, r, fullPath, toolName, args, graphQLBody)
}

func (h *proxyHandler) handleUnrecognizedPassthrough(w http.ResponseWriter, r *http.Request, rawPath, fullPath string) {
	logger.LogUnrecognizedEndpointPassthrough(r.Method, rawPath)
	logHandler.Printf("unrecognized REST endpoint %s, forwarding with empty labels", rawPath)

	resp, respBody := h.forwardAndReadBody(w, r.Context(), r.Method, fullPath, nil, "", r.Header.Get("Authorization"))
	if resp == nil {
		return
	}

	pre := &guard.PipelinePreResult{
		AgentLabels: h.server.AgentRegistry.GetOrCreate(proxyAgentID),
		Resource:    difc.NewLabeledResource(fmt.Sprintf("unrecognized endpoint %s", rawPath)),
		Operation:   difc.OperationRead,
		EvalResult: &difc.EvaluationResult{
			Decision:        difc.AccessAllow,
			SecrecyToAdd:    []difc.Tag{},
			IntegrityToDrop: []difc.Tag{},
		},
	}
	guard.RunPipelinePhase6(pre, nil, h.server.Mode)

	h.writeResponse(w, resp, respBody)
}

// handleWithDIFC runs the 6-phase DIFC pipeline on a request.
func (h *proxyHandler) handleWithDIFC(w http.ResponseWriter, r *http.Request, path, toolName string, args map[string]interface{}, graphQLBody []byte) {
	ctx := r.Context()
	s := h.server
	backend := &restBackendCaller{server: s, clientAuth: r.Header.Get("Authorization")}

	// Start a DIFC pipeline span covering all phases for this request
	ctx, difcSpan := tracing.StartDIFCPipelineSpan(ctx, h.GetTracer(), toolName, r.URL.Path)
	defer difcSpan.End()

	if !s.guardInitialized {
		errMsg := "returning 503: proxy enforcement not configured (no --policy flag provided)"
		logHandler.Print(errMsg)
		logger.LogError("proxy", "%s", errMsg)
		httputil.WriteErrorResponse(w, http.StatusServiceUnavailable, "service_unavailable", "proxy enforcement not configured")
		return
	}

	// **Phases 0–2: Get agent labels, label resource, coarse access check**
	pipelineIn := guard.PipelineInput{
		AgentID:         proxyAgentID,
		ToolName:        toolName,
		Args:            args,
		Guard:           s.guard,
		Evaluator:       s.Evaluator,
		AgentRegistry:   s.AgentRegistry,
		Capabilities:    s.Capabilities,
		EnforcementMode: s.Mode,
		BackendCaller:   backend,
	}
	ctx, pre, err := guard.RunPipelinePrePhases(ctx, pipelineIn)
	if err != nil {
		if denied, ok := err.(*guard.PipelineAccessDenied); ok {
			logHandler.Printf("[DIFC] Phase 2: BLOCKED %s %s — %s", r.Method, path, denied.EvalResult.Reason)
			deniedErr := fmt.Errorf("DIFC policy violation: %s", denied.EvalResult.Reason)
			tracing.RecordSpanError(difcSpan, deniedErr, "access denied: "+denied.EvalResult.Reason)
			writeDIFCForbidden(w, deniedErr.Error())
			return
		}
		logHandler.Printf("[DIFC] Phase 1 failed: %v", err)
		httputil.WriteErrorResponse(w, http.StatusBadGateway, "bad_gateway", "resource labeling failed")
		return
	}

	// **Phase 3: Forward to upstream GitHub API**
	clientAuth := r.Header.Get("Authorization")
	var resp *http.Response
	var respBody []byte

	fwdCtx, fwdSpan := tracing.StartProxyForwardSpan(ctx, h.GetTracer(), toolName, r.URL.Path, h.server.upstreamHost())
	defer fwdSpan.End()
	if graphQLBody != nil {
		resp, respBody = h.forwardAndReadBody(w, fwdCtx, http.MethodPost, path, bytes.NewReader(graphQLBody), "application/json", clientAuth)
	} else {
		resp, respBody = h.forwardAndReadBody(w, fwdCtx, r.Method, path, nil, "", clientAuth)
	}
	if resp != nil {
		fwdSpan.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(resp.StatusCode))
	}
	if resp == nil {
		return
	}

	// For non-200 responses, pass through as-is
	if resp.StatusCode >= 300 {
		h.writeResponse(w, resp, respBody)
		return
	}

	// Parse the response as JSON for DIFC filtering
	var responseData interface{}
	if err := json.Unmarshal(respBody, &responseData); err != nil {
		// Non-JSON response — pass through
		logHandler.Printf("[DIFC] response is not JSON, passing through")
		h.writeResponse(w, resp, respBody)
		return
	}

	// **Phase 4: Guard labels the response**
	labeledData, err := guard.RunPipelinePhase4(ctx, pipelineIn, pre, responseData)
	if err != nil {
		logHandler.Printf("[DIFC] Phase 4 failed: %v", err)
		// On labeling failure, fall back to coarse-grained result
		if pre.EvalResult.IsAllowed() {
			h.writeResponse(w, resp, respBody)
		} else {
			h.writeEmptyResponse(w, resp, responseData)
		}
		return
	}

	// **Phase 5: Fine-grained filtering**
	var finalData interface{}
	var useOriginalBody bool // GraphQL responses need original format preserved
	if labeledData != nil {
		if collection, ok := labeledData.(*difc.CollectionLabeledData); ok {
			filtered := s.Evaluator.FilterCollection(
				pre.AgentLabels.Secrecy, pre.AgentLabels.Integrity, collection, pre.Operation)

			logHandler.Printf("[DIFC] Phase 5: %d/%d items accessible",
				filtered.GetAccessibleCount(), filtered.TotalCount)

			// Log filtered items
			if filtered.GetFilteredCount() > 0 {
				logHandler.Printf("[DIFC] Filtered %d items", filtered.GetFilteredCount())
				logger.LogInfo("proxy", "DIFC filtered %d/%d items for %s %s (tool=%s)",
					filtered.GetFilteredCount(), filtered.TotalCount, r.Method, path, toolName)
			}

			// Strict mode: block entire response if any item filtered
			if difc.ShouldBlockFilteredResponse(s.Mode, filtered.GetFilteredCount()) {
				logHandler.Printf("[DIFC] STRICT: blocking response — %d filtered items", filtered.GetFilteredCount())
				writeDIFCForbidden(w, fmt.Sprintf("DIFC policy violation: %d of %d items not accessible",
					filtered.GetFilteredCount(), filtered.TotalCount))
				return
			}

			// For GraphQL: if nothing was filtered, return original response body
			// to preserve the exact response format (ToResult transforms the structure)
			if graphQLBody != nil && filtered.GetFilteredCount() == 0 {
				useOriginalBody = true
			} else if graphQLBody != nil {
				// GraphQL with filtered items: reconstruct the response with only accessible items
				logHandler.Printf("[DIFC] GraphQL response: %d/%d items filtered, reconstructing response",
					filtered.GetFilteredCount(), filtered.TotalCount)
				finalData = rebuildGraphQLResponse(responseData, filtered)
			} else {
				var ok bool
				finalData, ok = h.toResultOrWriteEmpty(w, resp, responseData, filtered)
				if !ok {
					return
				}
				// Re-wrap search responses to preserve the envelope
				finalData = rewrapSearchResponse(responseData, finalData)
				// Unwrap single-object responses (e.g., get_file_contents)
				finalData = unwrapSingleObject(responseData, finalData)
			}
		} else {
			// Simple labeled data — already passed coarse check
			if graphQLBody != nil {
				useOriginalBody = true
			} else {
				var ok bool
				finalData, ok = h.toResultOrWriteEmpty(w, resp, responseData, labeledData)
				if !ok {
					return
				}
			}
		}
	} else {
		// No fine-grained labels — use coarse result
		if pre.EvalResult.IsAllowed() {
			finalData = responseData
		} else {
			h.writeEmptyResponse(w, resp, responseData)
			return
		}
	}

	// **Phase 6: Label accumulation (propagate mode)**
	guard.RunPipelinePhase6(pre, labeledData, s.Mode)

	// Write the filtered response
	if useOriginalBody {
		// GraphQL: return original upstream response to preserve exact format
		logHandler.Printf("[DIFC] returning original response body (GraphQL, no items filtered)")
		h.writeResponse(w, resp, respBody)
	} else {
		filteredJSON, err := json.Marshal(finalData)
		if err != nil {
			httputil.WriteErrorResponse(w, http.StatusInternalServerError, "internal_error", "failed to serialize filtered response")
			return
		}
		copyResponseHeaders(w, resp)
		httputil.WriteJSONResponse(w, resp.StatusCode, json.RawMessage(filteredJSON))
	}
}

// passthrough forwards a request to the upstream GitHub API without DIFC filtering.
func (h *proxyHandler) passthrough(w http.ResponseWriter, r *http.Request, path string) {
	logHandler.Printf("passthrough %s %s", r.Method, path)

	var body io.Reader
	if r.Body != nil {
		body = r.Body
		defer r.Body.Close()
	}

	resp, respBody := h.forwardAndReadBody(w, r.Context(), r.Method, path, body, r.Header.Get("Content-Type"), r.Header.Get("Authorization"))
	if resp == nil {
		return
	}

	h.writeResponse(w, resp, respBody)
}

// writeResponse writes an upstream response to the client.
// When the upstream signals rate-limiting (HTTP 429 or X-RateLimit-Remaining == 0),
// it injects a Retry-After header and logs the event at ERROR level.
func (h *proxyHandler) writeResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	copyResponseHeaders(w, resp)
	injectRetryAfterIfRateLimited(w, resp)
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// writeEmptyResponse writes an empty JSON response matching the shape of the original data.
// originalData should be the parsed upstream response; nil or unrecognized types fall back to "[]".
// For JSON arrays it writes "[]", for GraphQL objects with a "data" key it writes {"data":null},
// and for other JSON objects it writes "{}".
func (h *proxyHandler) writeEmptyResponse(w http.ResponseWriter, resp *http.Response, originalData interface{}) {
	copyResponseHeaders(w, resp)

	var empty string
	switch obj := originalData.(type) {
	case []interface{}:
		empty = "[]"
	case map[string]interface{}:
		// GraphQL responses wrap their payload in a "data" key
		if _, ok := obj["data"]; ok {
			empty = `{"data":null}`
		} else {
			empty = "{}"
		}
	default:
		empty = "[]" // safe default for nil or unknown types
	}
	logHandler.Printf("writeEmptyResponse: shape=%s, status=%d", empty, resp.StatusCode)
	httputil.WriteJSONResponse(w, resp.StatusCode, json.RawMessage(empty))
}

// forwardAndReadBody forwards a request to the upstream GitHub API and reads the
// entire response body. On success it returns the response and body bytes. It writes
// a 502 error to w and returns nil, nil on failure.
func (h *proxyHandler) forwardAndReadBody(
	w http.ResponseWriter, ctx context.Context,
	method, path string, body io.Reader, contentType, clientAuth string,
) (*http.Response, []byte) {
	logHandler.Printf("forwardAndReadBody: %s %s", method, path)
	resp, err := h.server.forwardToGitHub(ctx, method, path, body, contentType, clientAuth)
	if err != nil {
		logHandler.Printf("forwardAndReadBody: upstream request failed: method=%s path=%s err=%v", method, path, err)
		httputil.WriteErrorResponse(w, http.StatusBadGateway, "bad_gateway", "upstream request failed")
		return nil, nil
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		logHandler.Printf("forwardAndReadBody: body read failed: method=%s path=%s status=%d err=%v", method, path, resp.StatusCode, err)
		httputil.WriteErrorResponse(w, http.StatusBadGateway, "bad_gateway", "failed to read upstream response")
		return nil, nil
	}
	logHandler.Printf("forwardAndReadBody: %s %s -> status=%d bodyLen=%d", method, path, resp.StatusCode, len(respBody))
	return resp, respBody
}

// copyResponseHeaders copies relevant headers from upstream to the client response.
func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	for _, h := range []string{
		"Content-Type",
		"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset",
		"X-RateLimit-Resource", "X-RateLimit-Used",
		"Link", // pagination
		"X-GitHub-Request-Id",
	} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
}

// injectRetryAfterIfRateLimited inspects the upstream response for rate-limit signals
// (HTTP 429 or X-Ratelimit-Remaining == 0). When detected it:
//  1. Injects a Retry-After header so the client knows when to retry.
//  2. Logs the event at ERROR level so operators can monitor rate-limit incidents.
func injectRetryAfterIfRateLimited(w http.ResponseWriter, resp *http.Response) {
	is429 := resp.StatusCode == http.StatusTooManyRequests
	// Use Go's canonical header key form (textproto.CanonicalMIMEHeaderKey produces
	// "X-Ratelimit-Remaining", matching GitHub's actual response headers).
	remaining := resp.Header.Get("X-Ratelimit-Remaining")
	resetHeader := resp.Header.Get("X-Ratelimit-Reset")

	isRateLimited := is429 || remaining == "0"
	if !isRateLimited {
		return
	}

	resetAt := httputil.ParseRateLimitResetHeader(resetHeader)
	retryAfter := httputil.ComputeRetryAfter(resetAt)

	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))

	logger.LogError("client",
		"upstream rate limit hit: status=%d X-Ratelimit-Remaining=%s X-Ratelimit-Reset=%s retry-after=%ds",
		resp.StatusCode, remaining, resetHeader, retryAfter)
}

var metadataPassthrough = map[string]bool{
	"/meta":       true,
	"/rate_limit": true,
	"/octocat":    true,
	"/zen":        true,
	"/versions":   true,
}

func isMetadataPassthroughPath(path string) bool {
	return metadataPassthrough[path]
}
