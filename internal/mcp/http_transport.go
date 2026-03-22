package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/strutil"
	"github.com/github/gh-aw-mcpg/internal/version"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPTransportType represents the type of HTTP transport being used
type HTTPTransportType string

const (
	// HTTPTransportStreamable uses the streamable HTTP transport (2025-03-26 spec)
	HTTPTransportStreamable HTTPTransportType = "streamable"
	// HTTPTransportSSE uses the SSE transport (2024-11-05 spec)
	HTTPTransportSSE HTTPTransportType = "sse"
	// HTTPTransportPlainJSON uses plain JSON-RPC 2.0 over HTTP POST (non-standard)
	HTTPTransportPlainJSON HTTPTransportType = "plain-json"
)

// MCPProtocolVersion is the MCP protocol version used in initialization requests.
const MCPProtocolVersion = "2025-11-25"

// requestIDCounter is used to generate unique request IDs for HTTP requests
var requestIDCounter uint64

// httpRequestResult contains the result of an HTTP request execution
type httpRequestResult struct {
	StatusCode   int
	ResponseBody []byte
	Header       http.Header
}

// transportConnector is a function that creates an SDK transport for a given URL and HTTP client
type transportConnector func(url string, httpClient *http.Client) sdk.Transport

// isHTTPConnectionError checks if an error is a network connection error.
// It uses errors.As to inspect the underlying *net.OpError for dial operations,
// which covers connection refused, no such host, and network unreachable errors.
func isHTTPConnectionError(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return opErr.Op == "dial"
	}
	return false
}

// parseSSEResponse extracts JSON data from SSE-formatted response
// SSE format: "event: message\ndata: {json}\n\n"
func parseSSEResponse(body []byte) ([]byte, error) {
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")
			return []byte(jsonData), nil
		}
	}
	return nil, fmt.Errorf("no data field found in SSE response")
}

// parseJSONRPCResponseWithSSE parses a JSON-RPC response that might be in SSE format.
// This helper consolidates duplicate response parsing logic that appears in multiple places.
//
// The function tries to parse the body as JSON first. If that fails, it attempts to extract
// JSON from SSE format (event: message\ndata: {...}). This handles backends that return
// responses in Server-Sent Events format.
//
// Parameters:
//   - body: Raw response body bytes
//   - statusCode: HTTP status code (used for enhanced error messages)
//   - contextDesc: Description of the calling context (e.g., "initialize response", "JSON-RPC response")
//
// Returns:
//   - *Response: Parsed JSON-RPC response on success
//   - error: Detailed parsing error with body preview on failure
func parseJSONRPCResponseWithSSE(body []byte, statusCode int, contextDesc string) (*Response, error) {
	var rpcResponse Response
	httpErrorResponse := func() *Response {
		return &Response{
			JSONRPC: "2.0",
			Error: &ResponseError{
				Code:    -32603, // Internal error
				Message: fmt.Sprintf("HTTP %d: %s", statusCode, http.StatusText(statusCode)),
				Data:    json.RawMessage(body),
			},
		}
	}

	// Try parsing as standard JSON first
	if err := json.Unmarshal(body, &rpcResponse); err != nil {
		// Try parsing as SSE format
		logConn.Printf("Initial JSON parse failed, attempting SSE format parsing")
		sseData, sseErr := parseSSEResponse(body)
		if sseErr != nil {
			// If we have a non-OK HTTP status and can't parse the response,
			// construct a JSON-RPC error response with HTTP error details
			if statusCode != http.StatusOK {
				logConn.Printf("HTTP error status=%d, body cannot be parsed as JSON-RPC", statusCode)
				return httpErrorResponse(), nil
			}
			// Include the response body to help debug what the server actually returned
			bodyPreview := strutil.TruncateWithSuffix(string(body), 500, "... (truncated)")
			return nil, fmt.Errorf("failed to parse %s (received non-JSON or malformed response): %w\nResponse body: %s", contextDesc, sseErr, bodyPreview)
		}

		// Successfully extracted JSON from SSE, now parse it
		if err := json.Unmarshal(sseData, &rpcResponse); err != nil {
			// If we have a non-OK HTTP status and can't parse the SSE data,
			// construct a JSON-RPC error response with HTTP error details
			if statusCode != http.StatusOK {
				logConn.Printf("HTTP error status=%d, SSE data cannot be parsed as JSON-RPC", statusCode)
				return httpErrorResponse(), nil
			}
			return nil, fmt.Errorf("failed to parse JSON data extracted from SSE response: %w\nJSON data: %s", err, string(sseData))
		}
		logConn.Printf("Successfully parsed SSE-formatted response")
	}

	if statusCode != http.StatusOK {
		logConn.Printf("HTTP error status=%d, returning synthetic JSON-RPC error response", statusCode)
		return httpErrorResponse(), nil
	}

	return &rpcResponse, nil
}

// newMCPClient creates a new MCP SDK client with standard implementation details
// Pass nil for logger parameter to disable SDK logging (for tests)
func newMCPClient(log *logger.Logger) *sdk.Client {
	var slogLogger *slog.Logger
	if log != nil {
		slogLogger = logger.NewSlogLoggerWithHandler(log)
	}
	return sdk.NewClient(&sdk.Implementation{
		Name:    "awmg",
		Version: version.Get(),
	}, &sdk.ClientOptions{
		Logger: slogLogger,
	})
}

// newHTTPConnection creates a new HTTP Connection struct with common fields
func newHTTPConnection(ctx context.Context, cancel context.CancelFunc, client *sdk.Client, session *sdk.ClientSession, url string, headers map[string]string, httpClient *http.Client, transportType HTTPTransportType, serverID string) *Connection {
	// Extract session ID from SDK session if available
	var sessionID string
	if session != nil {
		sessionID = session.ID()
	}
	return &Connection{
		client:            client,
		session:           session,
		ctx:               ctx,
		cancel:            cancel,
		serverID:          serverID,
		isHTTP:            true,
		httpURL:           url,
		headers:           headers,
		httpClient:        httpClient,
		httpTransportType: transportType,
		httpSessionID:     sessionID,
	}
}

// jsonrpcRequest is the JSON-RPC 2.0 request envelope.
// Using a typed struct instead of map[string]interface{} avoids a heap
// allocation for the hash table on every outbound RPC call.
type jsonrpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      uint64      `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// createJSONRPCRequest creates a JSON-RPC 2.0 request struct
func createJSONRPCRequest(requestID uint64, method string, params interface{}) jsonrpcRequest {
	return jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      requestID,
		Method:  method,
		Params:  params,
	}
}

// ensureToolCallArguments ensures that the arguments field exists in tools/call params
// The MCP protocol requires the arguments field to always be present, even if empty
func ensureToolCallArguments(params interface{}) interface{} {
	// Convert params to map if it isn't already
	paramsMap, ok := params.(map[string]interface{})
	if !ok {
		// If params isn't a map, return as-is (this shouldn't happen for tools/call)
		return params
	}

	// Check if arguments field exists
	if _, hasArgs := paramsMap["arguments"]; !hasArgs {
		// Add empty arguments map if missing
		paramsMap["arguments"] = make(map[string]interface{})
	} else if paramsMap["arguments"] == nil {
		// Replace nil with empty map
		paramsMap["arguments"] = make(map[string]interface{})
	}

	return paramsMap
}

// setupHTTPRequest creates and configures an HTTP request with standard headers
func setupHTTPRequest(ctx context.Context, url string, requestBody []byte, headers map[string]string) (*http.Request, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set standard headers
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	// Add configured headers (e.g., Authorization)
	for key, value := range headers {
		httpReq.Header.Set(key, value)
	}

	return httpReq, nil
}

// executeHTTPRequest executes an HTTP JSON-RPC request and returns the response details.
// This helper consolidates the common pattern of: create request → marshal → setup HTTP → execute → read response.
// It handles connection errors consistently and provides method-specific error messages.
// The headerModifier function allows callers to modify headers before the request is sent.
func (c *Connection) executeHTTPRequest(ctx context.Context, method string, params interface{}, requestID uint64, headerModifier func(*http.Request)) (*httpRequestResult, error) {
	// Create JSON-RPC request
	request := createJSONRPCRequest(requestID, method, params)

	// Marshal request body
	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal %s request: %w", method, err)
	}

	// Create HTTP request with standard headers
	httpReq, err := setupHTTPRequest(ctx, c.httpURL, requestBody, c.headers)
	if err != nil {
		return nil, err
	}

	// Allow caller to modify headers (e.g., add session ID)
	if headerModifier != nil {
		headerModifier(httpReq)
	}

	// Execute HTTP request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Check if it's a connection error (cannot connect at all)
		if isHTTPConnectionError(err) {
			return nil, fmt.Errorf("cannot connect to HTTP backend at %s: %w", c.httpURL, err)
		}
		return nil, fmt.Errorf("%s HTTP request failed: %w", method, err)
	}
	defer httpResp.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s response: %w", method, err)
	}

	return &httpRequestResult{
		StatusCode:   httpResp.StatusCode,
		ResponseBody: responseBody,
		Header:       httpResp.Header,
	}, nil
}

// trySDKTransport is a generic function to attempt connection with any SDK-based transport
// It handles the common logic of creating a client, connecting with timeout, and returning a connection
func trySDKTransport(
	ctx context.Context,
	cancel context.CancelFunc,
	serverID string,
	url string,
	headers map[string]string,
	httpClient *http.Client,
	transportType HTTPTransportType,
	transportName string,
	createTransport transportConnector,
) (*Connection, error) {
	// Create MCP client with logger
	client := newMCPClient(logConn)

	// Create transport using the provided connector
	transport := createTransport(url, httpClient)

	// Try to connect with a timeout - this will fail if the server doesn't support this transport
	// Use a short timeout to fail fast and try other transports
	connectCtx, connectCancel := context.WithTimeout(ctx, 5*time.Second)
	defer connectCancel()

	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("%s transport connect failed: %w", transportName, err)
	}

	conn := newHTTPConnection(ctx, cancel, client, session, url, headers, httpClient, transportType, serverID)

	logger.LogInfo("backend", "%s transport connected successfully", transportName)
	logConn.Printf("Connected with %s transport", transportName)
	return conn, nil
}

// tryStreamableHTTPTransport attempts to connect using the streamable HTTP transport (2025-03-26 spec)
func tryStreamableHTTPTransport(ctx context.Context, cancel context.CancelFunc, serverID, url string, headers map[string]string, httpClient *http.Client) (*Connection, error) {
	return trySDKTransport(
		ctx, cancel, serverID, url, headers, httpClient,
		HTTPTransportStreamable,
		"streamable HTTP",
		func(url string, httpClient *http.Client) sdk.Transport {
			return &sdk.StreamableClientTransport{
				Endpoint:   url,
				HTTPClient: httpClient,
				MaxRetries: 0, // Don't retry on failure - we'll try other transports
			}
		},
	)
}

// trySSETransport attempts to connect using the SSE transport (2024-11-05 spec)
func trySSETransport(ctx context.Context, cancel context.CancelFunc, serverID, url string, headers map[string]string, httpClient *http.Client) (*Connection, error) {
	return trySDKTransport(
		ctx, cancel, serverID, url, headers, httpClient,
		HTTPTransportSSE,
		"SSE",
		func(url string, httpClient *http.Client) sdk.Transport {
			return &sdk.SSEClientTransport{
				Endpoint:   url,
				HTTPClient: httpClient,
			}
		},
	)
}

// tryPlainJSONTransport attempts to connect using plain JSON-RPC 2.0 over HTTP POST (non-standard)
// This is used for compatibility with servers like safeinputs that don't implement standard MCP HTTP transports
func tryPlainJSONTransport(ctx context.Context, cancel context.CancelFunc, serverID, url string, headers map[string]string, httpClient *http.Client) (*Connection, error) {
	conn := &Connection{
		ctx:               ctx,
		cancel:            cancel,
		serverID:          serverID,
		isHTTP:            true,
		httpURL:           url,
		headers:           headers,
		httpClient:        httpClient,
		httpTransportType: HTTPTransportPlainJSON,
	}

	// Send initialize request to establish a session with the HTTP backend
	// This is critical for backends that require session management
	logConn.Printf("Sending initialize request via plain JSON-RPC to: %s", url)
	sessionID, err := conn.initializeHTTPSession()
	if err != nil {
		return nil, fmt.Errorf("plain JSON-RPC initialize failed: %w", err)
	}

	conn.httpSessionID = sessionID
	logger.LogInfo("backend", "Plain JSON-RPC transport connected successfully with session=%s", sessionID)
	logConn.Printf("Connected with plain JSON-RPC transport, session=%s", sessionID)
	return conn, nil
}

// initializeHTTPSession sends an initialize request to the HTTP backend and captures the session ID
func (c *Connection) initializeHTTPSession() (string, error) {
	// Generate unique request ID
	requestID := atomic.AddUint64(&requestIDCounter, 1)

	// Create initialize request with MCP protocol parameters
	initParams := map[string]interface{}{
		"protocolVersion": MCPProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "awmg",
			"version": version.Get(),
		},
	}

	logConn.Printf("Sending initialize request")

	// Generate a temporary session ID for the initialize request
	// Some backends may require this header even during initialization
	tempSessionID := fmt.Sprintf("awmg-init-%d", requestID)

	// Execute HTTP request with custom header modification
	result, err := c.executeHTTPRequest(context.Background(), "initialize", initParams, requestID, func(httpReq *http.Request) {
		httpReq.Header.Set("Mcp-Session-Id", tempSessionID)
		logConn.Printf("Sending initialize with temporary session ID: %s", tempSessionID)
	})
	if err != nil {
		return "", err
	}

	// Capture the Mcp-Session-Id from response headers
	sessionID := result.Header.Get("Mcp-Session-Id")
	if sessionID != "" {
		logConn.Printf("Captured Mcp-Session-Id from response: %s", sessionID)
	} else {
		// If no session ID in response, use the temporary one
		// This handles backends that don't return a session ID
		sessionID = tempSessionID
		logConn.Printf("No Mcp-Session-Id in response, using temporary session ID: %s", sessionID)
	}

	logConn.Printf("Initialize response: status=%d, body_len=%d, session=%s", result.StatusCode, len(result.ResponseBody), sessionID)

	// Check for HTTP errors
	if result.StatusCode != http.StatusOK {
		return "", fmt.Errorf("initialize failed: status=%d, body=%s", result.StatusCode, string(result.ResponseBody))
	}

	// Parse JSON-RPC response to check for errors
	// The response might be in SSE format (event: message\ndata: {...})
	rpcResponse, err := parseJSONRPCResponseWithSSE(result.ResponseBody, result.StatusCode, "initialize response")
	if err != nil {
		return "", err
	}

	if rpcResponse.Error != nil {
		return "", fmt.Errorf("initialize error: code=%d, message=%s", rpcResponse.Error.Code, rpcResponse.Error.Message)
	}

	return sessionID, nil
}

// sendHTTPRequest sends a JSON-RPC request to an HTTP MCP server
// The ctx parameter is used to extract session ID for the Mcp-Session-Id header
func (c *Connection) sendHTTPRequest(ctx context.Context, method string, params interface{}) (*Response, error) {
	// Generate unique request ID using atomic counter
	requestID := atomic.AddUint64(&requestIDCounter, 1)

	// For tools/call, ensure arguments field always exists (MCP protocol requirement)
	if method == "tools/call" {
		params = ensureToolCallArguments(params)
	}

	logConn.Printf("Sending HTTP request to %s: method=%s, id=%d", c.httpURL, method, requestID)

	// Execute HTTP request with custom header modification for session ID
	result, err := c.executeHTTPRequest(ctx, method, params, requestID, func(httpReq *http.Request) {
		// Add Mcp-Session-Id header with priority:
		// 1) Context session ID (if explicitly provided for this request)
		// 2) Stored httpSessionID from initialization
		var sessionID string
		if ctxSessionID, ok := ctx.Value(SessionIDContextKey).(string); ok && ctxSessionID != "" {
			sessionID = ctxSessionID
			logConn.Printf("Using session ID from context: %s", sessionID)
		} else if c.httpSessionID != "" {
			sessionID = c.httpSessionID
			logConn.Printf("Using stored session ID from initialization: %s", sessionID)
		}

		if sessionID != "" {
			httpReq.Header.Set("Mcp-Session-Id", sessionID)
		} else {
			logConn.Printf("No session ID available (backend may not require session management)")
		}
	})
	if err != nil {
		return nil, err
	}

	logConn.Printf("Received HTTP response: status=%d, body_len=%d", result.StatusCode, len(result.ResponseBody))

	// Parse JSON-RPC response
	// The response might be in SSE format (event: message\ndata: {...})
	rpcResponse, err := parseJSONRPCResponseWithSSE(result.ResponseBody, result.StatusCode, "JSON-RPC response")
	if err != nil {
		return nil, err
	}

	// Check for HTTP errors after parsing
	// If we have a non-OK status but successfully parsed a JSON-RPC response,
	// pass it through (it may already contain an error field)
	if result.StatusCode != http.StatusOK {
		logConn.Printf("HTTP error status=%d with valid JSON-RPC response, passing through", result.StatusCode)
		// If the response doesn't already have an error, construct one
		if rpcResponse.Error == nil {
			rpcResponse.Error = &ResponseError{
				Code:    -32603, // Internal error
				Message: fmt.Sprintf("HTTP %d: %s", result.StatusCode, http.StatusText(result.StatusCode)),
				Data:    result.ResponseBody,
			}
		}
	}

	return rpcResponse, nil
}
