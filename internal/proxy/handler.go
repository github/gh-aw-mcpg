package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/strutil"
)

var logHandler = logger.New("proxy:handler")

// proxyHandler implements http.Handler and runs the DIFC pipeline on proxied requests.
type proxyHandler struct {
	server *Server
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
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
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
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}

		match := MatchGraphQL(graphQLBody)
		if match == nil {
			// Unknown GraphQL query — fail closed: deny rather than risk leaking unfiltered data
			logHandler.Printf("unknown GraphQL query, blocking request: %s", strutil.Truncate(string(graphQLBody), 500))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"errors": []map[string]string{{"message": "access denied: unrecognized GraphQL operation"}},
				"data":   nil,
			})
			return
		}
		// Schema introspection (__type, __schema) is safe metadata — passthrough without DIFC
		if match.ToolName == "graphql_introspection" {
			logHandler.Printf("GraphQL introspection query, passing through")
			clientAuth := r.Header.Get("Authorization")
			resp, err := h.server.forwardToGitHub(r.Context(), http.MethodPost, "/graphql", bytes.NewReader(graphQLBody), "application/json", clientAuth)
			if err != nil {
				http.Error(w, "upstream request failed", http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			respBody, _ := io.ReadAll(resp.Body)
			h.writeResponse(w, resp, respBody)
			return
		}
		toolName = match.ToolName
		args = match.Args
	} else {
		match := MatchRoute(rawPath)
		if match == nil {
			// Unknown REST endpoint — fail closed: deny rather than risk leaking unfiltered data
			logHandler.Printf("unknown REST endpoint %s, blocking request", rawPath)
			http.Error(w, "access denied: unrecognized endpoint", http.StatusForbidden)
			return
		}
		toolName = match.ToolName
		args = match.Args
	}

	// Run the DIFC pipeline
	h.handleWithDIFC(w, r, fullPath, toolName, args, graphQLBody)
}

// handleWithDIFC runs the 6-phase DIFC pipeline on a request.
func (h *proxyHandler) handleWithDIFC(w http.ResponseWriter, r *http.Request, path, toolName string, args map[string]interface{}, graphQLBody []byte) {
	ctx := r.Context()
	s := h.server
	backend := &stubBackendCaller{}

	if !s.guardInitialized {
		log.Printf("[proxy] WARNING: guard not initialized, blocking request")
		http.Error(w, "proxy enforcement not configured", http.StatusServiceUnavailable)
		return
	}

	// **Phase 0: Get agent labels**
	agentLabels := s.agentRegistry.GetOrCreate("proxy")
	logHandler.Printf("[DIFC] Phase 0: agent secrecy=%v integrity=%v",
		agentLabels.GetSecrecyTags(), agentLabels.GetIntegrityTags())

	// **Phase 1: Guard labels the resource**
	resource, operation, err := s.guard.LabelResource(ctx, toolName, args, backend, s.capabilities)
	if err != nil {
		logHandler.Printf("[DIFC] Phase 1 failed: %v", err)
		// On labeling failure, fail closed to prevent enforcement bypass
		http.Error(w, "resource labeling failed", http.StatusBadGateway)
		return
	}

	logHandler.Printf("[DIFC] Phase 1: resource=%s op=%s secrecy=%v integrity=%v",
		resource.Description, operation,
		resource.Secrecy.Label.GetTags(), resource.Integrity.Label.GetTags())

	// **Phase 2: Coarse-grained access check**
	evalResult := s.evaluator.Evaluate(agentLabels.Secrecy, agentLabels.Integrity, resource, operation)

	if !evalResult.IsAllowed() {
		if operation == difc.OperationRead {
			// Read in filter mode: skip coarse block, proceed to fine-grained filtering
			logHandler.Printf("[DIFC] Phase 2: coarse check failed for read, proceeding to Phase 3")
		} else {
			// Write blocked
			logHandler.Printf("[DIFC] Phase 2: BLOCKED %s %s — %s", r.Method, path, evalResult.Reason)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{
				"message": fmt.Sprintf("DIFC policy violation: %s", evalResult.Reason),
			})
			return
		}
	}

	// **Phase 3: Forward to upstream GitHub API**
	clientAuth := r.Header.Get("Authorization")
	var resp *http.Response
	if graphQLBody != nil {
		resp, err = s.forwardToGitHub(ctx, http.MethodPost, "/graphql", bytes.NewReader(graphQLBody), "application/json", clientAuth)
	} else {
		resp, err = s.forwardToGitHub(ctx, r.Method, path, nil, "", clientAuth)
	}
	if err != nil {
		logHandler.Printf("[DIFC] Phase 3 failed: %v", err)
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Read the response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read upstream response", http.StatusBadGateway)
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
	labeledData, err := s.guard.LabelResponse(ctx, toolName, responseData, backend, s.capabilities)
	if err != nil {
		logHandler.Printf("[DIFC] Phase 4 failed: %v", err)
		// On labeling failure, use coarse-grained result
		if evalResult.IsAllowed() {
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
			filtered := s.evaluator.FilterCollection(
				agentLabels.Secrecy, agentLabels.Integrity, collection, operation)

			logHandler.Printf("[DIFC] Phase 5: %d/%d items accessible",
				filtered.GetAccessibleCount(), filtered.TotalCount)

			// Log filtered items
			if filtered.GetFilteredCount() > 0 {
				logHandler.Printf("[DIFC] Filtered %d items", filtered.GetFilteredCount())
				logger.LogInfo("proxy", "DIFC filtered %d/%d items for %s %s (tool=%s)",
					filtered.GetFilteredCount(), filtered.TotalCount, r.Method, path, toolName)
			}

			// Strict mode: block entire response if any item filtered
			if s.enforcementMode == difc.EnforcementStrict && filtered.GetFilteredCount() > 0 {
				logHandler.Printf("[DIFC] STRICT: blocking response — %d filtered items", filtered.GetFilteredCount())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{
					"message": fmt.Sprintf("DIFC policy violation: %d of %d items not accessible",
						filtered.GetFilteredCount(), filtered.TotalCount),
				})
				return
			}

			// For GraphQL: if nothing was filtered, return original response body
			// to preserve the exact response format (ToResult transforms the structure)
			if graphQLBody != nil && filtered.GetFilteredCount() == 0 {
				useOriginalBody = true
			} else if graphQLBody != nil {
				// GraphQL with filtered items: return valid empty GraphQL response
				// (ToResult returns an array which breaks gh CLI's GraphQL parser)
				logHandler.Printf("[DIFC] GraphQL response: %d/%d items filtered, returning empty GraphQL response",
					filtered.GetFilteredCount(), filtered.TotalCount)
				h.writeEmptyResponse(w, resp, responseData)
				return
			} else {
				finalData, err = filtered.ToResult()
				if err != nil {
					logHandler.Printf("[DIFC] Phase 5 ToResult failed: %v", err)
					h.writeEmptyResponse(w, resp, responseData)
					return
				}
			}
		} else {
			// Simple labeled data — already passed coarse check
			if graphQLBody != nil {
				useOriginalBody = true
			} else {
				finalData, err = labeledData.ToResult()
				if err != nil {
					logHandler.Printf("[DIFC] Phase 5 ToResult failed: %v", err)
					h.writeEmptyResponse(w, resp, responseData)
					return
				}
			}
		}
	} else {
		// No fine-grained labels — use coarse result
		if evalResult.IsAllowed() {
			finalData = responseData
		} else {
			h.writeEmptyResponse(w, resp, responseData)
			return
		}
	}

	// **Phase 6: Label accumulation (propagate mode)**
	if s.enforcementMode == difc.EnforcementPropagate && labeledData != nil {
		overall := labeledData.Overall()
		agentLabels.AccumulateFromRead(overall)
		logHandler.Printf("[DIFC] Phase 6: accumulated labels")
	}

	// Write the filtered response
	if useOriginalBody {
		// GraphQL: return original upstream response to preserve exact format
		logHandler.Printf("[DIFC] returning original response body (GraphQL, no items filtered)")
		h.writeResponse(w, resp, respBody)
	} else {
		filteredJSON, err := json.Marshal(finalData)
		if err != nil {
			http.Error(w, "failed to serialize filtered response", http.StatusInternalServerError)
			return
		}
		copyResponseHeaders(w, resp)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(filteredJSON)
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

	resp, err := h.server.forwardToGitHub(r.Context(), r.Method, path, body, r.Header.Get("Content-Type"), r.Header.Get("Authorization"))
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "failed to read upstream response", http.StatusBadGateway)
		return
	}

	h.writeResponse(w, resp, respBody)
}

// writeResponse writes an upstream response to the client.
func (h *proxyHandler) writeResponse(w http.ResponseWriter, resp *http.Response, body []byte) {
	copyResponseHeaders(w, resp)
	w.WriteHeader(resp.StatusCode)
	w.Write(body)
}

// writeEmptyResponse writes an empty JSON response matching the shape of the original data.
// originalData should be the parsed upstream response; nil or unrecognized types fall back to "[]".
// For JSON arrays it writes "[]", for GraphQL objects with a "data" key it writes {"data":null},
// and for other JSON objects it writes "{}".
func (h *proxyHandler) writeEmptyResponse(w http.ResponseWriter, resp *http.Response, originalData interface{}) {
	copyResponseHeaders(w, resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)

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
	w.Write([]byte(empty))
}

// copyResponseHeaders copies relevant headers from upstream to the client response.
func copyResponseHeaders(w http.ResponseWriter, resp *http.Response) {
	// Copy rate limit headers
	for _, h := range []string{
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
