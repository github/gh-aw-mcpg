package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/util"
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

const sessionNotFoundMessage = "session not found"

// MCPProtocolVersion is the MCP protocol version used only by the plain JSON-RPC
// fallback path in this package. Streamable and SSE transports are SDK-managed
// and negotiate protocol versions internally.
const MCPProtocolVersion = "2025-11-25"

// streamableMaxRetries disables SDK-level automatic reconnect retries on
// StreamableClientTransport. The SDK interprets 0 as "use the default of 5
// retries"; a negative value means 0 retries (give up immediately on stream
// close). The gateway handles reconnection itself, so SDK-level retries would
// cause double-retry behaviour.
// This sentinel behaviour was verified against go-sdk v1.7.0; see
// TestMaxRetriesSentinelCanary for an automated guard against SDK changes.
const streamableMaxRetries = -1

// sseMaxEventSize bounds the number of bytes buffered while reading a single
// server-sent event on SSE-capable transports (StreamableClientTransport's
// standalone SSE stream and SSEClientTransport). The gateway sets this
// explicitly to the SDK's own default (sdk.DefaultMaxEventSize) rather than
// relying on the zero-value default, so an SDK change to that default does
// not silently alter the gateway's memory-exhaustion protection against
// oversized events from backend servers.
const sseMaxEventSize = 16 << 20 // 16 MiB

// requestIDCounter is used to generate unique request IDs for HTTP requests
var requestIDCounter uint64

var logHTTP = logger.ForFile()

// httpRequestResult contains the result of an HTTP request execution
type httpRequestResult struct {
	StatusCode   int
	ResponseBody []byte
	Header       http.Header
}

// transportConnector is a function that creates an SDK transport for a given URL and HTTP client.
// The returned transport is owned by the SDK client session after Connect() succeeds;
// callers must not close it directly — it is cleaned up when the session is closed.
type transportConnector func(url string, httpClient *http.Client) sdk.Transport

// newStreamableTransport constructs the SDK streamable HTTP transport with the
// gateway's required reconnect settings. Keep this aligned with
// reconnectSDKTransport and the SDK upgrade canaries:
// TestMaxRetriesSentinelCanary and server.TestArgumentValidationBypassCanary
// must both continue to pass after go-sdk upgrades.
func newStreamableTransport(url string, httpClient *http.Client) *sdk.StreamableClientTransport {
	return &sdk.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: httpClient,
		// See streamableMaxRetries for rationale. We fall through to SSE or plain
		// JSON-RPC on failure.
		MaxRetries: streamableMaxRetries,
		// DisableStandaloneSSE prevents the SDK from issuing a standalone GET
		// request for a server-sent events stream immediately after initialization.
		// The gateway operates in request-response mode only and does not need
		// server-initiated messages, so this stream is unnecessary.
		// See StreamableClientTransport.DisableStandaloneSSE in the SDK docs and
		// TestDisableStandaloneSSECanary for an automated guard against SDK changes.
		DisableStandaloneSSE: true,
		// See sseMaxEventSize for rationale.
		MaxEventSize: sseMaxEventSize,
	}
}

// newSSETransport constructs the SDK SSE transport with the gateway's
// required settings. Keep this aligned with reconnectSDKTransport.
func newSSETransport(url string, httpClient *http.Client) *sdk.SSEClientTransport {
	return &sdk.SSEClientTransport{
		Endpoint:   url,
		HTTPClient: httpClient,
		// See sseMaxEventSize for rationale.
		MaxEventSize: sseMaxEventSize,
	}
}

// isHTTPConnectionError checks if an error is a network connection error.
// It uses errors.As to inspect the underlying *net.OpError for dial operations,
// which covers connection refused, no such host, and network unreachable errors.
func isHTTPConnectionError(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		logHTTP.Printf("Network operation error detected: op=%s, isDial=%v", opErr.Op, opErr.Op == "dial")
		return opErr.Op == "dial"
	}
	return false
}

// isSessionNotFoundError checks if an error indicates a backend MCP session has expired
// or is not found. This is used to detect when automatic reconnection to the backend is needed.
// It uses errors.Is to check for sdk.ErrSessionMissing (the typed sentinel introduced in v1.5.0),
// and also falls back to string-matching for non-SDK transports that return plain error messages.
func isSessionNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sdk.ErrSessionMissing) {
		logHTTP.Print("Session-not-found detected via sdk.ErrSessionMissing")
		return true
	}
	// Plain JSON-RPC fallback requests bypass SDK session types, so they cannot
	// return sdk.ErrSessionMissing and are matched by backend error text instead.
	// TODO(tech-debt): remove this string-matching fallback once the plain JSON-RPC
	// transport (HTTPTransportPlainJSON) is retired. Retirement criteria:
	// confirm no active backends still require the pre-2024-11-05 compatibility
	// path, then delete the fallback together with the plain JSON-RPC transport.
	matched := strings.Contains(strings.ToLower(err.Error()), sessionNotFoundMessage)
	if matched {
		logHTTP.Print("Session-not-found detected via error message fallback")
	}
	return matched
}

// isSessionNotFoundHTTPResponse checks if an HTTP response indicates the backend session was not found.
// MCP backends return HTTP 404 with a "session not found" body when a session has expired.
func isSessionNotFoundHTTPResponse(statusCode int, body []byte) bool {
	if statusCode != http.StatusNotFound {
		return false
	}
	matched := strings.Contains(strings.ToLower(string(body)), sessionNotFoundMessage)
	if matched {
		logHTTP.Printf("Session-not-found detected in HTTP 404 response: body_len=%d", len(body))
	}
	return matched
}

// parseSSEResponse extracts JSON data from SSE-formatted response
// SSE format: "event: message\ndata: {json}\n\n"
func parseSSEResponse(body []byte) ([]byte, error) {
	logHTTP.Printf("Parsing SSE response: body_len=%d", len(body))
	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			jsonData := strings.TrimPrefix(line, "data: ")
			logHTTP.Printf("Extracted SSE data field: data_len=%d", len(jsonData))
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
			bodyPreview := util.TruncateWithSuffix(string(body), 500, "... (truncated)")
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

// assembleHTTPConnection assembles a *Connection from low-level HTTP transport parts.
// It is the internal assembly step called by trySDKTransport after a transport has
// been successfully established; it is distinct from NewHTTPConnection (connection.go),
// which is the high-level public constructor that drives transport selection and
// creates the HTTP client.
// keepAlive is passed through to store on the connection so that reconnectSDKTransport
// can re-create the SDK client with the same keepalive setting.
func assembleHTTPConnection(ctx context.Context, cancel context.CancelFunc, client *sdk.Client, session *sdk.ClientSession, url string, headers map[string]string, httpClient *http.Client, transportType HTTPTransportType, serverID string, keepAlive time.Duration, connectTimeout time.Duration) *Connection {
	// Extract session ID from SDK session if available
	var sessionID string
	if session != nil {
		sessionID = session.ID()
	}
	logHTTP.Printf("Creating HTTP connection: serverID=%s, url=%s, transport=%s, headers=%d, keepAlive=%v, connectTimeout=%v", serverID, sanitize.RedactURL(url), transportType, len(headers), keepAlive, connectTimeout)
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
		keepAliveInterval: keepAlive,
		connectTimeout:    connectTimeout,
	}
}

// createJSONRPCRequest creates a JSON-RPC 2.0 request map
func createJSONRPCRequest(requestID uint64, method string, params any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
	}
}

// ensureToolCallArguments ensures that the arguments field exists in tools/call params
// The MCP protocol requires the arguments field to always be present, even if empty
func ensureToolCallArguments(params any) any {
	// Convert params to map if it isn't already
	paramsMap, ok := params.(map[string]any)
	if !ok {
		// If params isn't a map, return as-is (this shouldn't happen for tools/call)
		return params
	}

	// Check if arguments field exists
	if _, hasArgs := paramsMap["arguments"]; !hasArgs {
		// Add empty arguments map if missing
		logHTTP.Print("tools/call params missing 'arguments' field, adding empty map")
		paramsMap["arguments"] = make(map[string]any)
	} else if paramsMap["arguments"] == nil {
		// Replace nil with empty map
		logHTTP.Print("tools/call params has nil 'arguments' field, replacing with empty map")
		paramsMap["arguments"] = make(map[string]any)
	}

	return paramsMap
}

// setupHTTPRequest creates and configures an HTTP request with standard headers
func setupHTTPRequest(ctx context.Context, url string, requestBody []byte, headers map[string]string) (*http.Request, error) {
	logHTTP.Printf("Setting up HTTP request: url=%s, bodyLen=%d, customHeaders=%d", sanitize.RedactURL(url), len(requestBody), len(headers))
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
func (c *Connection) executeHTTPRequest(ctx context.Context, method string, params any, requestID uint64, headerModifier func(*http.Request)) (*httpRequestResult, error) {
	logHTTP.Printf("Executing HTTP request: method=%s, url=%s, id=%d", method, sanitize.RedactURL(c.httpURL), requestID)

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

	logHTTP.Printf("HTTP response received: method=%s, status=%d, body_len=%d", method, httpResp.StatusCode, len(responseBody))
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
	keepAlive time.Duration,
	connectTimeout time.Duration,
) (*Connection, error) {
	// Create MCP client with logger and optional keepalive.
	// When keepAlive > 0, periodic pings prevent session expiry on backends
	// (e.g. safeoutputs) that have an idle timeout.
	client := newMCPClient(logConn, keepAlive)

	// Create transport using the provided connector
	transport := createTransport(url, httpClient)

	// Try to connect with the configured timeout
	connectCtx, connectCancel := context.WithTimeout(ctx, connectTimeout)
	defer connectCancel()

	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("%s transport connect failed: %w", transportName, err)
	}

	conn := assembleHTTPConnection(ctx, cancel, client, session, url, headers, httpClient, transportType, serverID, keepAlive, connectTimeout)

	logger.LogInfo("backend", "%s transport connected successfully", transportName)
	logConn.Printf("Connected with %s transport", transportName)
	return conn, nil
}

// tryStreamableHTTPTransport attempts to connect using the streamable HTTP transport (2025-03-26 spec)
func tryStreamableHTTPTransport(ctx context.Context, cancel context.CancelFunc, serverID, url string, headers map[string]string, httpClient *http.Client, keepAlive time.Duration, connectTimeout time.Duration) (*Connection, error) {
	logHTTP.Printf("Attempting streamable HTTP transport: serverID=%s, url=%s, connectTimeout=%v", serverID, sanitize.RedactURL(url), connectTimeout)
	return trySDKTransport(
		ctx, cancel, serverID, url, headers, httpClient,
		HTTPTransportStreamable,
		"streamable HTTP",
		func(url string, httpClient *http.Client) sdk.Transport {
			return newStreamableTransport(url, httpClient)
		},
		keepAlive,
		connectTimeout,
	)
}

// trySSETransport attempts to connect using the SSE transport (2024-11-05 spec)
func trySSETransport(ctx context.Context, cancel context.CancelFunc, serverID, url string, headers map[string]string, httpClient *http.Client, keepAlive time.Duration, connectTimeout time.Duration) (*Connection, error) {
	logHTTP.Printf("Attempting SSE transport: serverID=%s, url=%s, connectTimeout=%v", serverID, sanitize.RedactURL(url), connectTimeout)
	return trySDKTransport(
		ctx, cancel, serverID, url, headers, httpClient,
		HTTPTransportSSE,
		"SSE",
		func(url string, httpClient *http.Client) sdk.Transport {
			return newSSETransport(url, httpClient)
		},
		keepAlive,
		connectTimeout,
	)
}

// tryPlainJSONTransport attempts to connect using plain JSON-RPC 2.0 over HTTP POST (non-standard)
// This is used for compatibility with servers like safeinputs that don't implement standard MCP HTTP transports
func tryPlainJSONTransport(ctx context.Context, cancel context.CancelFunc, serverID, url string, headers map[string]string, httpClient *http.Client, connectTimeout time.Duration) (*Connection, error) {
	logHTTP.Printf("Attempting plain JSON-RPC transport: serverID=%s, url=%s", serverID, sanitize.RedactURL(url))
	conn := &Connection{
		ctx:               ctx,
		cancel:            cancel,
		serverID:          serverID,
		isHTTP:            true,
		httpURL:           url,
		headers:           headers,
		httpClient:        httpClient,
		httpTransportType: HTTPTransportPlainJSON,
		connectTimeout:    connectTimeout,
	}

	// Send initialize request to establish a session with the HTTP backend
	// This is critical for backends that require session management
	logConn.Printf("Sending initialize request via plain JSON-RPC to: %s", url)
	sessionID, err := conn.initializeHTTPSession()
	if err != nil {
		return nil, fmt.Errorf("plain JSON-RPC initialize failed: %w", err)
	}

	conn.httpSessionID = sessionID
	logger.LogInfo("backend", "Plain JSON-RPC transport connected successfully with session=%s", util.FormatSessionIDForLog(sessionID))
	logConn.Printf("Connected with plain JSON-RPC transport, session=%s", util.FormatSessionIDForLog(sessionID))
	return conn, nil
}

// initializeHTTPSession sends an initialize request to the HTTP backend and captures the session ID
func (c *Connection) initializeHTTPSession() (string, error) {
	connectCtx, cancel := context.WithTimeout(c.ctx, normalizeConnectTimeout(c.connectTimeout))
	defer cancel()
	return c.initializeHTTPSessionContext(connectCtx)
}

func (c *Connection) initializeHTTPSessionContext(ctx context.Context) (string, error) {
	// Generate unique request ID
	requestID := atomic.AddUint64(&requestIDCounter, 1)

	// Create initialize request with MCP protocol parameters
	initParams := map[string]any{
		"protocolVersion": MCPProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "awmg",
			"version": version.Get(),
		},
	}

	logConn.Printf("Sending initialize request")

	// Execute HTTP request without a Mcp-Session-Id header: the MCP spec does not
	// define a session ID on the initialize request — the server assigns it in the
	// response.  Sending a synthetic ID could cause some backends to misinterpret
	// the request as resuming an existing (and unknown) session.
	result, err := c.executeHTTPRequest(ctx, "initialize", initParams, requestID, nil)
	if err != nil {
		logConn.Printf("Initialize request failed: %v", err)
		return "", err
	}

	// Capture the Mcp-Session-Id from response headers
	sessionID := result.Header.Get("Mcp-Session-Id")
	if sessionID != "" {
		logConn.Printf("Captured Mcp-Session-Id from response: %s", util.FormatSessionIDForLog(sessionID))
	} else {
		logConn.Printf("No Mcp-Session-Id in initialize response; backend does not use session management")
	}

	logConn.Printf("Initialize response: status=%d, body_len=%d, session=%s", result.StatusCode, len(result.ResponseBody), util.FormatSessionIDForLog(sessionID))

	// Check for HTTP errors
	if result.StatusCode != http.StatusOK {
		logConn.Printf("Initialize returned non-OK status: %d", result.StatusCode)
		return "", fmt.Errorf("initialize failed: status=%d, body=%s", result.StatusCode, string(result.ResponseBody))
	}

	// Parse JSON-RPC response to check for errors
	// The response might be in SSE format (event: message\ndata: {...})
	rpcResponse, err := parseJSONRPCResponseWithSSE(result.ResponseBody, result.StatusCode, "initialize response")
	if err != nil {
		return "", err
	}

	if rpcResponse.Error != nil {
		logConn.Printf("Initialize returned JSON-RPC error: code=%d", rpcResponse.Error.Code)
		return "", fmt.Errorf("initialize error: code=%d, message=%s", rpcResponse.Error.Code, rpcResponse.Error.Message)
	}

	return sessionID, nil
}

// buildSessionHeaderModifier returns a header modifier function that adds the Mcp-Session-Id header.
// Priority: context session ID > stored connection session ID.
// Context session IDs are static for the lifetime of a single request and are captured once at
// construction time. Connection session IDs can change during a reconnect, so getHTTPSessionID()
// is called at request time to always pick up the current value.
func (c *Connection) buildSessionHeaderModifier(ctx context.Context) func(*http.Request) {
	// Capture any context-provided session ID once (it never changes for this request).
	ctxSessionID, _ := ctx.Value(SessionIDContextKey).(string)
	return func(httpReq *http.Request) {
		var sessionID string
		if ctxSessionID != "" {
			sessionID = ctxSessionID
			logConn.Printf("Using session ID from context: %s", util.FormatSessionIDForLog(sessionID))
		} else if id := c.getHTTPSessionID(); id != "" {
			sessionID = id
			logConn.Printf("Using stored session ID from initialization: %s", util.FormatSessionIDForLog(sessionID))
		}
		if sessionID != "" {
			httpReq.Header.Set("Mcp-Session-Id", sessionID)
		} else {
			logConn.Printf("No session ID available (backend may not require session management)")
		}
	}
}

// parseHTTPResult converts a raw httpRequestResult into a JSON-RPC Response, handling non-OK
// HTTP status codes by synthesising a JSON-RPC error when the server did not provide one.
func parseHTTPResult(result *httpRequestResult) (*Response, error) {
	// Parse JSON-RPC response.
	// The response might be in SSE format (event: message\ndata: {...}).
	rpcResponse, err := parseJSONRPCResponseWithSSE(result.ResponseBody, result.StatusCode, "JSON-RPC response")
	if err != nil {
		return nil, err
	}

	// Check for HTTP errors after parsing.
	// If we have a non-OK status but successfully parsed a JSON-RPC response,
	// pass it through (it may already contain an error field).
	if result.StatusCode != http.StatusOK {
		logConn.Printf("HTTP error status=%d with valid JSON-RPC response, passing through", result.StatusCode)
		// If the response doesn't already have an error, construct one.
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

// sendHTTPRequest sends a JSON-RPC request to an HTTP MCP server.
// The ctx parameter is used to extract session ID for the Mcp-Session-Id header.
// If the backend returns a "session not found" (HTTP 404) response, it attempts a one-time
// session reconnect and retries the request transparently.
func (c *Connection) sendHTTPRequest(ctx context.Context, method string, params any) (*Response, error) {
	// For tools/call, ensure arguments field always exists (MCP protocol requirement)
	if method == "tools/call" {
		params = ensureToolCallArguments(params)
	}

	headerModifier := c.buildSessionHeaderModifier(ctx)

	requestID := atomic.AddUint64(&requestIDCounter, 1)
	if !logHTTP.Enabled() {
		logConn.Printf("Sending HTTP request to %s: method=%s, id=%d", c.httpURL, method, requestID)
	}

	result, err := c.executeHTTPRequest(ctx, method, params, requestID, headerModifier)
	if err != nil {
		return nil, err
	}

	if !logHTTP.Enabled() {
		logConn.Printf("Received HTTP response: status=%d, body_len=%d", result.StatusCode, len(result.ResponseBody))
	}

	// If the backend reported that the session has expired, reconnect and retry once.
	if isSessionNotFoundHTTPResponse(result.StatusCode, result.ResponseBody) {
		logConn.Printf("Session not found from %s (serverID=%s), attempting reconnect", c.httpURL, c.serverID)
		if reconnErr := c.reconnectPlainJSON(); reconnErr == nil {
			requestID = atomic.AddUint64(&requestIDCounter, 1)
			logConn.Printf("Retrying HTTP request after reconnect: method=%s, id=%d", method, requestID)
			result, err = c.executeHTTPRequest(ctx, method, params, requestID, headerModifier)
			if err != nil {
				return nil, err
			}
			logConn.Printf("Retry HTTP response: status=%d, body_len=%d", result.StatusCode, len(result.ResponseBody))
		} else {
			logConn.Printf("Session reconnect failed (%v), returning original session-not-found error", reconnErr)
		}
	}

	return parseHTTPResult(result)
}
