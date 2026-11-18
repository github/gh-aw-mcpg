package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcp"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logSDK = logger.New("server:sdk-frontend")

// WithSDKLogging wraps an SDK StreamableHTTPHandler to log JSON-RPC translation results.
// This captures the request/response at the HTTP boundary to understand what the SDK
// sees and what it returns, particularly for debugging protocol state issues.
//
// us is used only to resolve whether the request belongs to an enclave-scoped session.
// This boundary sees complete request and response bodies, so it must reach the same
// redaction decision as the inner sinks; a nil us (tests, non-gateway callers) simply
// falls back to the process-wide redaction mode.
func WithSDKLogging(handler http.Handler, mode string, us *UnifiedServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startTime := time.Now()

		// Extract session info for logging context
		sessionID := extractSessionIDFromRequest(r)
		mcpSessionID := r.Header.Get("Mcp-Session-Id")
		redactPayload := sanitize.ShouldRedactPayload(us.isEnclaveSession(sessionID))

		// Log incoming request
		logSDK.Printf(">>> SDK Request [%s] session=%s mcp-session=%s method=%s path=%s",
			mode, util.HashIdentifierForLog(sessionID), util.HashIdentifierForLog(mcpSessionID), r.Method, r.URL.Path)

		// Capture and log request body for POST requests
		var requestBody []byte
		var err error
		if r.Method == http.MethodPost {
			requestBody, err = readAndRestoreRequestBody(r)
		}
		var jsonrpcReq mcp.Request
		if err == nil && len(requestBody) > 0 {
			// Parse JSON-RPC request
			if err := json.Unmarshal(requestBody, &jsonrpcReq); err == nil {
				logSDK.Printf("    JSON-RPC Request: method=%s id=%v", jsonrpcReq.Method, jsonrpcReq.ID)
				logger.LogDebug("sdk-frontend", "JSON-RPC request parsed: mode=%s, method=%s, id=%v, session=%s",
					mode, jsonrpcReq.Method, jsonrpcReq.ID, util.HashIdentifierForLog(sessionID))
			} else {
				payloadText := sanitize.PayloadTextForLog(requestBody, redactPayload)
				if redactPayload {
					logSDK.Printf("    Failed to parse JSON-RPC request: %s", sanitize.RedactErrorForLog(err))
					logSDK.Printf("    Raw body: %s", payloadText)
				} else {
					logSDK.Printf("    Failed to parse JSON-RPC request: %v", err)
					logSDK.Printf("    Raw body (sanitized): %.500s", payloadText)
				}
			}
		}

		// Wrap response writer to capture output
		lw := newResponseWriter(w)

		// Call the actual SDK handler
		handler.ServeHTTP(lw, r)

		duration := time.Since(startTime)
		// The inner handler mutates *r in place when it injects the session context, so an
		// enclave marker attached during session establishment is visible here.
		redactPayload = redactPayload || sanitize.ShouldRedactPayload(mcp.IsEnclaveSession(r.Context()))

		// Parse and log response
		responseBody := lw.Body()
		if len(responseBody) > 0 {
			// Try to parse as JSON-RPC response
			var jsonrpcResp mcp.Response
			if err := json.Unmarshal(responseBody, &jsonrpcResp); err == nil {
				if jsonrpcResp.Error != nil {
					// Error response - this is what we're particularly interested in
					errorCode := jsonrpcResp.Error.Code
					errorMsg := jsonrpcResp.Error.Message
					// Backend error messages routinely quote the failing request or response
					// content, so enclave traffic logs only a correlatable token.
					loggedErrorMsg := sanitize.RedactMessageForLog(errorMsg, redactPayload)

					logSDK.Printf("<<< SDK Response [%s] ERROR status=%d duration=%v",
						mode, lw.StatusCode, duration)
					logSDK.Printf("    JSON-RPC Error: code=%d message=%q",
						errorCode, loggedErrorMsg)

					// Check for specific error types

					// Log tool not found errors specifically for better monitoring
					// Error code -32602 (Invalid params) is used by the SDK for unknown tools
					// Error code -32601 (Method not found) could also indicate tool issues
					// We check the method to ensure this is a tools/call request
					if (errorCode == -32602 || errorCode == -32601) && jsonrpcReq.Method == "tools/call" {
						logSDK.Printf("    ⚠️  TOOL NOT FOUND ERROR")
						logger.LogWarn("client",
							"Tool not found: mode=%s, method=%s, session=%s, code=%d, message=%q",
							mode, jsonrpcReq.Method, util.HashIdentifierForLog(sessionID), errorCode, loggedErrorMsg)
					}

					// Log detailed error info for protocol state issues
					if strings.Contains(errorMsg, "session initialization") ||
						strings.Contains(errorMsg, "invalid during") {
						logSDK.Printf("    ⚠️  PROTOCOL STATE ERROR DETECTED")
						logSDK.Printf("    Request method was: %s", jsonrpcReq.Method)
						logSDK.Printf("    Session ID: %s", util.HashIdentifierForLog(sessionID))
						logSDK.Printf("    MCP-Session-Id header: %s", util.HashIdentifierForLog(mcpSessionID))
						logSDK.Printf("    This error indicates SDK's StreamableHTTPHandler created fresh protocol state")

						logger.LogWarn("sdk-frontend",
							"Protocol state error: mode=%s, method=%s, session=%s, mcp_session=%s, error=%q",
							mode, jsonrpcReq.Method, util.HashIdentifierForLog(sessionID),
							util.HashIdentifierForLog(mcpSessionID), loggedErrorMsg)
					} else if (errorCode != -32602 && errorCode != -32601) || jsonrpcReq.Method != "tools/call" {
						// Only log as general error if not already logged above
						logger.LogError("sdk-frontend",
							"JSON-RPC error: mode=%s, method=%s, code=%d, message=%q",
							mode, jsonrpcReq.Method, errorCode, loggedErrorMsg)
					}
				} else {
					// Success response
					logSDK.Printf("<<< SDK Response [%s] SUCCESS status=%d duration=%v",
						mode, lw.StatusCode, duration)
					logSDK.Printf("    JSON-RPC Response id=%v has result=%v",
						jsonrpcResp.ID, jsonrpcResp.Result != nil)

					logger.LogDebug("sdk-frontend",
						"JSON-RPC success: mode=%s, method=%s, id=%v, duration=%v",
						mode, jsonrpcReq.Method, jsonrpcResp.ID, duration)
				}
			} else {
				// Could be SSE stream or other format
				logSDK.Printf("<<< SDK Response [%s] status=%d duration=%v (non-JSON or stream)",
					mode, lw.StatusCode, duration)
				payloadText := sanitize.PayloadTextForLog(responseBody, redactPayload)
				if redactPayload {
					logSDK.Printf("    Raw response: %s", payloadText)
				} else {
					if len(payloadText) < 500 {
						logSDK.Printf("    Raw response (sanitized): %s", payloadText)
					} else {
						logSDK.Printf("    Raw response (sanitized, truncated): %.500s...", payloadText)
					}
				}
			}
		} else {
			logSDK.Printf("<<< SDK Response [%s] status=%d duration=%v (empty body)",
				mode, lw.StatusCode, duration)
		}
	})
}

// withResponseLogging wraps an http.Handler to log response bodies
func withResponseLogging(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lw := newResponseWriter(w)
		handler.ServeHTTP(lw, r)
		if len(lw.Body()) > 0 {
			sanitizedBody := sanitize.SanitizeString(string(lw.Body()))
			logServerHelpers.Printf("[%s] %s %s - Status: %d, Response: %s", r.RemoteAddr, r.Method, r.URL.Path, lw.StatusCode, sanitizedBody)
		}
	})
}
