package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
	"github.com/github/gh-aw-mcpg/internal/version"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logConn = logger.New("mcp:connection")

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

// isConnectionError checks if an error indicates a connection failure
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "network is unreachable")
}

// ContextKey for session ID
type ContextKey string

// SessionIDContextKey is used to store MCP session ID in context
// This is the same key used in the server package to avoid circular dependencies
const SessionIDContextKey ContextKey = "awmg-session-id"

// requestIDCounter is used to generate unique request IDs for HTTP requests
var requestIDCounter uint64

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

// Connection represents a connection to an MCP server using the official SDK
type Connection struct {
	client   *sdk.Client
	session  *sdk.ClientSession
	ctx      context.Context
	cancel   context.CancelFunc
	serverID string // Server ID from config for logging
	// HTTP-specific fields
	isHTTP            bool
	httpURL           string
	headers           map[string]string
	httpClient        *http.Client
	httpSessionID     string            // Session ID returned by the HTTP backend
	httpTransportType HTTPTransportType // Type of HTTP transport in use
}

// newMCPClient creates a new MCP SDK client with standard implementation details
func newMCPClient() *sdk.Client {
	return sdk.NewClient(&sdk.Implementation{
		Name:    "awmg",
		Version: version.Get(),
	}, nil)
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

// createJSONRPCRequest creates a JSON-RPC 2.0 request map
func createJSONRPCRequest(requestID uint64, method string, params interface{}) map[string]interface{} {
	return map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      requestID,
		"method":  method,
		"params":  params,
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

// NewConnection creates a new MCP connection using the official SDK
func NewConnection(ctx context.Context, serverID, command string, args []string, env map[string]string) (*Connection, error) {
	logger.LogInfo("backend", "Creating new MCP backend connection, command=%s, args=%v", command, sanitize.SanitizeArgs(args))
	logConn.Printf("Creating new MCP connection: command=%s, args=%v", command, sanitize.SanitizeArgs(args))
	ctx, cancel := context.WithCancel(ctx)

	// Create MCP client
	client := newMCPClient()

	// Expand Docker -e flags that reference environment variables
	// Docker's `-e VAR_NAME` expects VAR_NAME to be in the environment
	expandedArgs := expandDockerEnvArgs(args)
	logConn.Printf("Expanded args for Docker env: %v", expandedArgs)

	// Create command transport
	cmd := exec.CommandContext(ctx, command, expandedArgs...)

	// Start with parent's environment to inherit shell variables
	cmd.Env = append([]string{}, cmd.Environ()...)

	// Add/override with config-specified environment variables
	if len(env) > 0 {
		for k, v := range env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Capture and stream stderr to help diagnose container issues
	// The SDK's CommandTransport only uses stdin/stdout for MCP protocol,
	// so we can capture stderr separately for debugging
	// Use a TeeReader-style approach: write to both a buffer (for error reporting)
	// and to a pipe that streams to logs in real-time
	var stderrBuf bytes.Buffer
	stderrPipeReader, stderrPipeWriter := io.Pipe()
	cmd.Stderr = io.MultiWriter(&stderrBuf, stderrPipeWriter)

	// Stream stderr to logs in a goroutine
	go func() {
		defer stderrPipeReader.Close()
		scanner := bufio.NewScanner(stderrPipeReader)
		for scanner.Scan() {
			line := scanner.Text()
			logger.LogInfoWithServer(serverID, "backend", "[stderr] %s", line)
			logConn.Printf("[%s stderr] %s", serverID, line)
		}
	}()

	logger.LogInfo("backend", "Starting MCP backend server, command=%s, args=%v", command, sanitize.SanitizeArgs(expandedArgs))
	log.Printf("Starting MCP server command: %s %v", command, sanitize.SanitizeArgs(expandedArgs))
	transport := &sdk.CommandTransport{Command: cmd}

	// Connect to the server (this handles the initialization handshake automatically)
	log.Printf("Connecting to MCP server...")
	logConn.Print("Initiating MCP server connection and handshake")
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cancel()
		stderrPipeWriter.Close() // Close pipe to stop the stderr streaming goroutine

		// Enhanced error context for debugging
		logger.LogErrorMd("backend", "MCP backend connection failed, command=%s, args=%v, error=%v", command, sanitize.SanitizeArgs(expandedArgs), err)
		log.Printf("❌ MCP Connection Failed:")
		log.Printf("   Command: %s", command)
		log.Printf("   Args: %v", sanitize.SanitizeArgs(expandedArgs))
		log.Printf("   Error: %v", err)

		// Log captured stderr output from the container/process
		stderrOutput := strings.TrimSpace(stderrBuf.String())
		if stderrOutput != "" {
			logger.LogErrorMd("backend", "MCP backend stderr output:\n%s", stderrOutput)
			log.Printf("   📋 Container/Process stderr output:")
			for _, line := range strings.Split(stderrOutput, "\n") {
				log.Printf("      %s", line)
			}
		}

		// Check if it's a command not found error
		if strings.Contains(err.Error(), "executable file not found") ||
			strings.Contains(err.Error(), "no such file or directory") {
			logger.LogErrorMd("backend", "MCP backend command not found, command=%s", command)
			log.Printf("   ⚠️  Command '%s' not found in PATH", command)
			log.Printf("   ⚠️  Verify the command is installed and executable")
		}

		// Check if it's a connection/protocol error
		if strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "broken pipe") {
			logger.LogErrorMd("backend", "MCP backend connection/protocol error, command=%s", command)
			log.Printf("   ⚠️  Process started but terminated unexpectedly")
			log.Printf("   ⚠️  Check if the command supports MCP protocol over stdio")
		}

		logConn.Printf("Connection failed: command=%s, error=%v", command, err)
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	logger.LogInfoMd("backend", "Successfully connected to MCP backend server, command=%s", command)
	logConn.Printf("Successfully connected to MCP server: command=%s", command)

	conn := &Connection{
		client:   client,
		session:  session,
		ctx:      ctx,
		cancel:   cancel,
		serverID: serverID,
		isHTTP:   false,
	}

	log.Printf("Started MCP server: %s %v", command, args)
	return conn, nil
}

// NewHTTPConnection creates a new HTTP-based MCP connection with transport fallback
// For HTTP servers that are already running, we connect and initialize a session
//
// This function implements a fallback strategy for HTTP transports:
//  1. If custom headers are provided, skip SDK transports (they don't support custom headers)
//     and use plain JSON-RPC 2.0 over HTTP POST (for safeinputs compatibility)
//  2. Otherwise, try standard transports:
//     a. Streamable HTTP (2025-03-26 spec) using SDK's StreamableClientTransport
//     b. SSE (2024-11-05 spec) using SDK's SSEClientTransport
//     c. Plain JSON-RPC 2.0 over HTTP POST as final fallback
//
// This ensures compatibility with all types of HTTP MCP servers.
func NewHTTPConnection(ctx context.Context, serverID, url string, headers map[string]string) (*Connection, error) {
	logger.LogInfo("backend", "Creating HTTP MCP connection with transport fallback, url=%s", url)
	logConn.Printf("Creating HTTP MCP connection: url=%s", url)
	ctx, cancel := context.WithCancel(ctx)

	// Create an HTTP client with appropriate timeouts
	httpClient := &http.Client{
		Timeout: 120 * time.Second, // Overall request timeout
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	// If custom headers are provided, skip SDK transports as they don't support headers
	// This is typical for backends like safeinputs that require authentication
	if len(headers) > 0 {
		logConn.Printf("Custom headers detected, using plain JSON-RPC transport for %s", url)
		conn, err := tryPlainJSONTransport(ctx, cancel, serverID, url, headers, httpClient)
		if err == nil {
			logger.LogInfo("backend", "Successfully connected using plain JSON-RPC transport, url=%s", url)
			log.Printf("Configured HTTP MCP server with plain JSON-RPC transport: %s", url)
			return conn, nil
		}
		cancel()
		logger.LogError("backend", "Plain JSON-RPC transport failed for url=%s, error=%v", url, err)
		return nil, fmt.Errorf("failed to connect with plain JSON-RPC transport: %w", err)
	}

	// Try standard transports in order: streamable HTTP → SSE → plain JSON-RPC

	// Try 1: Streamable HTTP (2025-03-26 spec)
	logConn.Printf("Attempting streamable HTTP transport for %s", url)
	conn, err := tryStreamableHTTPTransport(ctx, cancel, serverID, url, headers, httpClient)
	if err == nil {
		logger.LogInfo("backend", "Successfully connected using streamable HTTP transport, url=%s", url)
		log.Printf("Configured HTTP MCP server with streamable transport: %s", url)
		return conn, nil
	}
	logConn.Printf("Streamable HTTP failed: %v", err)

	// Try 2: SSE (2024-11-05 spec)
	logConn.Printf("Attempting SSE transport for %s", url)
	conn, err = trySSETransport(ctx, cancel, serverID, url, headers, httpClient)
	if err == nil {
		logger.LogWarn("backend", "⚠️  MCP over SSE has been deprecated. Connected using SSE transport for url=%s. Please migrate to streamable HTTP transport (2025-03-26 spec).", url)
		log.Printf("⚠️  WARNING: MCP over SSE (2024-11-05 spec) has been DEPRECATED")
		log.Printf("⚠️  The server at %s is using the deprecated SSE transport", url)
		log.Printf("⚠️  Please migrate to streamable HTTP transport (2025-03-26 spec)")
		log.Printf("Configured HTTP MCP server with SSE transport: %s", url)
		return conn, nil
	}
	logConn.Printf("SSE transport failed: %v", err)

	// Try 3: Plain JSON-RPC over HTTP (non-standard, for fallback)
	logConn.Printf("Attempting plain JSON-RPC transport for %s", url)
	conn, err = tryPlainJSONTransport(ctx, cancel, serverID, url, headers, httpClient)
	if err == nil {
		logger.LogInfo("backend", "Successfully connected using plain JSON-RPC transport, url=%s", url)
		log.Printf("Configured HTTP MCP server with plain JSON-RPC transport: %s", url)
		return conn, nil
	}
	logConn.Printf("Plain JSON-RPC transport failed: %v", err)

	// All transports failed
	cancel()
	logger.LogError("backend", "All HTTP transports failed for url=%s", url)
	return nil, fmt.Errorf("failed to connect using any HTTP transport (tried streamable, SSE, and plain JSON-RPC): last error: %w", err)
}

// transportConnector is a function that creates an SDK transport for a given URL and HTTP client
type transportConnector func(url string, httpClient *http.Client) sdk.Transport

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
	// Create MCP client
	client := newMCPClient()

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

// IsHTTP returns true if this is an HTTP connection
func (c *Connection) IsHTTP() bool {
	return c.isHTTP
}

// GetHTTPURL returns the HTTP URL for this connection
func (c *Connection) GetHTTPURL() string {
	return c.httpURL
}

// GetHTTPHeaders returns the HTTP headers for this connection
func (c *Connection) GetHTTPHeaders() map[string]string {
	return c.headers
}

// SendRequest sends a JSON-RPC request and waits for the response
// The serverID parameter is used for logging to associate the request with a backend server
func (c *Connection) SendRequest(method string, params interface{}) (*Response, error) {
	return c.SendRequestWithServerID(context.Background(), method, params, "unknown")
}

// SendRequestWithServerID sends a JSON-RPC request with server ID for logging
// The ctx parameter is used to extract session ID for HTTP MCP servers
func (c *Connection) SendRequestWithServerID(ctx context.Context, method string, params interface{}, serverID string) (*Response, error) {
	// Log the outbound request to backend server
	requestPayload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	logger.LogRPCRequest(logger.RPCDirectionOutbound, serverID, method, requestPayload)

	var result *Response
	var err error

	// Handle HTTP connections
	if c.isHTTP {
		// For plain JSON-RPC transport, use manual HTTP requests
		if c.httpTransportType == HTTPTransportPlainJSON {
			result, err = c.sendHTTPRequest(ctx, method, params)
			// Log the response from backend server
			var responsePayload []byte
			if result != nil {
				responsePayload, _ = json.Marshal(result)
			}
			logger.LogRPCResponse(logger.RPCDirectionInbound, serverID, responsePayload, err)
			return result, err
		}

		// For streamable and SSE transports, use SDK session methods
		result, err = c.callSDKMethod(method, params)
		// Log the response from backend server
		var responsePayload []byte
		if result != nil {
			responsePayload, _ = json.Marshal(result)
		}
		logger.LogRPCResponse(logger.RPCDirectionInbound, serverID, responsePayload, err)
		return result, err
	}

	// Handle stdio connections using SDK client
	result, err = c.callSDKMethod(method, params)

	// Log the response from backend server
	var responsePayload []byte
	if result != nil {
		responsePayload, _ = json.Marshal(result)
	}
	logger.LogRPCResponse(logger.RPCDirectionInbound, serverID, responsePayload, err)

	return result, err
}

// callSDKMethod calls the appropriate SDK method based on the method name
// This centralizes the method dispatch logic used by both HTTP SDK transports and stdio
func (c *Connection) callSDKMethod(method string, params interface{}) (*Response, error) {
	switch method {
	case "tools/list":
		return c.listTools()
	case "tools/call":
		return c.callTool(params)
	case "resources/list":
		return c.listResources()
	case "resources/read":
		return c.readResource(params)
	case "prompts/list":
		return c.listPrompts()
	case "prompts/get":
		return c.getPrompt(params)
	default:
		return nil, fmt.Errorf("unsupported method: %s", method)
	}
}

// initializeHTTPSession sends an initialize request to the HTTP backend and captures the session ID
func (c *Connection) initializeHTTPSession() (string, error) {
	// Generate unique request ID
	requestID := atomic.AddUint64(&requestIDCounter, 1)

	// Create initialize request with MCP protocol parameters
	initParams := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    "awmg",
			"version": "1.0.0",
		},
	}

	request := createJSONRPCRequest(requestID, "initialize", initParams)

	requestBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal initialize request: %w", err)
	}

	logConn.Printf("Sending initialize request: %s", string(requestBody))

	// Create HTTP request with standard headers
	httpReq, err := setupHTTPRequest(context.Background(), c.httpURL, requestBody, c.headers)
	if err != nil {
		return "", err
	}

	// Generate a temporary session ID for the initialize request
	// Some backends may require this header even during initialization
	tempSessionID := fmt.Sprintf("awmg-init-%d", requestID)
	httpReq.Header.Set("Mcp-Session-Id", tempSessionID)
	logConn.Printf("Sending initialize with temporary session ID: %s", tempSessionID)

	logConn.Printf("Sending initialize to %s", c.httpURL)

	// Send request
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Check if it's a connection error (cannot connect at all)
		if isConnectionError(err) {
			return "", fmt.Errorf("cannot connect to HTTP backend at %s: %w", c.httpURL, err)
		}
		return "", fmt.Errorf("failed to send initialize request to %s: %w", c.httpURL, err)
	}
	defer httpResp.Body.Close()

	// Capture the Mcp-Session-Id from response headers
	sessionID := httpResp.Header.Get("Mcp-Session-Id")
	if sessionID != "" {
		logConn.Printf("Captured Mcp-Session-Id from response: %s", sessionID)
	} else {
		// If no session ID in response, use the temporary one
		// This handles backends that don't return a session ID
		sessionID = tempSessionID
		logConn.Printf("No Mcp-Session-Id in response, using temporary session ID: %s", sessionID)
	}

	// Read response body
	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read initialize response: %w", err)
	}

	logConn.Printf("Initialize response: status=%d, body_len=%d, session=%s", httpResp.StatusCode, len(responseBody), sessionID)

	// Check for HTTP errors
	if httpResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("initialize failed: status=%d, body=%s", httpResp.StatusCode, string(responseBody))
	}

	// Parse JSON-RPC response to check for errors
	// The response might be in SSE format (event: message\ndata: {...})
	// Try to parse as JSON first, if that fails, try SSE format
	var rpcResponse Response

	if err := json.Unmarshal(responseBody, &rpcResponse); err != nil {
		// Try parsing as SSE format
		logConn.Printf("Initial JSON parse failed, attempting SSE format parsing")
		sseData, sseErr := parseSSEResponse(responseBody)
		if sseErr != nil {
			// Include the response body to help debug what the server actually returned
			bodyPreview := string(responseBody)
			if len(bodyPreview) > 500 {
				bodyPreview = bodyPreview[:500] + "... (truncated)"
			}
			return "", fmt.Errorf("failed to parse initialize response (received non-JSON or malformed response): %w\nResponse body: %s", err, bodyPreview)
		}

		// Successfully extracted JSON from SSE, now parse it
		if err := json.Unmarshal(sseData, &rpcResponse); err != nil {
			return "", fmt.Errorf("failed to parse JSON data extracted from SSE response: %w\nJSON data: %s", err, string(sseData))
		}
		logConn.Printf("Successfully parsed SSE-formatted response")
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

	// Create JSON-RPC request
	request := createJSONRPCRequest(requestID, method, params)

	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request with standard headers
	httpReq, err := setupHTTPRequest(ctx, c.httpURL, requestBody, c.headers)
	if err != nil {
		return nil, err
	}

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

	logConn.Printf("Sending HTTP request to %s: method=%s, id=%d", c.httpURL, method, requestID)

	// Send request using the reusable HTTP client
	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Check if it's a connection error (cannot connect at all)
		if isConnectionError(err) {
			return nil, fmt.Errorf("cannot connect to HTTP backend at %s: %w", c.httpURL, err)
		}
		return nil, fmt.Errorf("failed to send HTTP request to %s: %w", c.httpURL, err)
	}
	defer httpResp.Body.Close()

	// Read response
	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read HTTP response: %w", err)
	}

	logConn.Printf("Received HTTP response: status=%d, body_len=%d", httpResp.StatusCode, len(responseBody))

	// Parse JSON-RPC response
	// The response might be in SSE format (event: message\ndata: {...})
	// Try to parse as JSON first, if that fails, try SSE format
	var rpcResponse Response

	if err := json.Unmarshal(responseBody, &rpcResponse); err != nil {
		// Try parsing as SSE format
		logConn.Printf("Initial JSON parse failed, attempting SSE format parsing")
		sseData, sseErr := parseSSEResponse(responseBody)
		if sseErr != nil {
			// If we have a non-OK HTTP status and can't parse the response,
			// construct a JSON-RPC error response with HTTP error details
			if httpResp.StatusCode != http.StatusOK {
				logConn.Printf("HTTP error status=%d, body cannot be parsed as JSON-RPC", httpResp.StatusCode)
				return &Response{
					JSONRPC: "2.0",
					Error: &ResponseError{
						Code:    -32603, // Internal error
						Message: fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, http.StatusText(httpResp.StatusCode)),
						Data:    json.RawMessage(responseBody),
					},
				}, nil
			}
			// Include the response body to help debug what the server actually returned
			bodyPreview := string(responseBody)
			if len(bodyPreview) > 500 {
				bodyPreview = bodyPreview[:500] + "... (truncated)"
			}
			return nil, fmt.Errorf("failed to parse JSON-RPC response (received non-JSON or malformed response): %w\nResponse body: %s", err, bodyPreview)
		}

		// Successfully extracted JSON from SSE, now parse it
		if err := json.Unmarshal(sseData, &rpcResponse); err != nil {
			// If we have a non-OK HTTP status and can't parse the SSE data,
			// construct a JSON-RPC error response with HTTP error details
			if httpResp.StatusCode != http.StatusOK {
				logConn.Printf("HTTP error status=%d, SSE data cannot be parsed as JSON-RPC", httpResp.StatusCode)
				return &Response{
					JSONRPC: "2.0",
					Error: &ResponseError{
						Code:    -32603, // Internal error
						Message: fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, http.StatusText(httpResp.StatusCode)),
						Data:    json.RawMessage(responseBody),
					},
				}, nil
			}
			return nil, fmt.Errorf("failed to parse JSON data extracted from SSE response: %w\nJSON data: %s", err, string(sseData))
		}
		logConn.Printf("Successfully parsed SSE-formatted response")
	}

	// Check for HTTP errors after parsing
	// If we have a non-OK status but successfully parsed a JSON-RPC response,
	// pass it through (it may already contain an error field)
	if httpResp.StatusCode != http.StatusOK {
		logConn.Printf("HTTP error status=%d with valid JSON-RPC response, passing through", httpResp.StatusCode)
		// If the response doesn't already have an error, construct one
		if rpcResponse.Error == nil {
			rpcResponse.Error = &ResponseError{
				Code:    -32603, // Internal error
				Message: fmt.Sprintf("HTTP %d: %s", httpResp.StatusCode, http.StatusText(httpResp.StatusCode)),
				Data:    responseBody,
			}
		}
	}

	return &rpcResponse, nil
}

func (c *Connection) listTools() (*Response, error) {
	if c.session == nil {
		return nil, fmt.Errorf("SDK session not available for plain JSON-RPC transport")
	}
	result, err := c.session.ListTools(c.ctx, &sdk.ListToolsParams{})
	if err != nil {
		return nil, err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      1, // Placeholder ID
		Result:  resultJSON,
	}, nil
}

func (c *Connection) callTool(params interface{}) (*Response, error) {
	if c.session == nil {
		return nil, fmt.Errorf("SDK session not available for plain JSON-RPC transport")
	}
	var callParams CallToolParams
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal params: %w", err)
	}
	logConn.Printf("callTool: marshaled params=%s", string(paramsJSON))

	if err := json.Unmarshal(paramsJSON, &callParams); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	// Ensure arguments is never nil - default to empty map
	// This is required by the MCP protocol which expects arguments to always be present
	if callParams.Arguments == nil {
		callParams.Arguments = make(map[string]interface{})
	}

	logConn.Printf("callTool: parsed name=%s, arguments=%+v", callParams.Name, callParams.Arguments)

	result, err := c.session.CallTool(c.ctx, &sdk.CallToolParams{
		Name:      callParams.Name,
		Arguments: callParams.Arguments,
	})
	if err != nil {
		return nil, err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      1,
		Result:  resultJSON,
	}, nil
}

func (c *Connection) listResources() (*Response, error) {
	if c.session == nil {
		return nil, fmt.Errorf("SDK session not available for plain JSON-RPC transport")
	}
	result, err := c.session.ListResources(c.ctx, &sdk.ListResourcesParams{})
	if err != nil {
		return nil, err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      1,
		Result:  resultJSON,
	}, nil
}

func (c *Connection) readResource(params interface{}) (*Response, error) {
	if c.session == nil {
		return nil, fmt.Errorf("SDK session not available for plain JSON-RPC transport")
	}
	var readParams struct {
		URI string `json:"uri"`
	}
	paramsJSON, _ := json.Marshal(params)
	if err := json.Unmarshal(paramsJSON, &readParams); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	result, err := c.session.ReadResource(c.ctx, &sdk.ReadResourceParams{
		URI: readParams.URI,
	})
	if err != nil {
		return nil, err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      1,
		Result:  resultJSON,
	}, nil
}

func (c *Connection) listPrompts() (*Response, error) {
	if c.session == nil {
		return nil, fmt.Errorf("SDK session not available for plain JSON-RPC transport")
	}
	result, err := c.session.ListPrompts(c.ctx, &sdk.ListPromptsParams{})
	if err != nil {
		return nil, err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      1,
		Result:  resultJSON,
	}, nil
}

func (c *Connection) getPrompt(params interface{}) (*Response, error) {
	if c.session == nil {
		return nil, fmt.Errorf("SDK session not available for plain JSON-RPC transport")
	}
	var getParams struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	paramsJSON, _ := json.Marshal(params)
	if err := json.Unmarshal(paramsJSON, &getParams); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	result, err := c.session.GetPrompt(c.ctx, &sdk.GetPromptParams{
		Name:      getParams.Name,
		Arguments: getParams.Arguments,
	})
	if err != nil {
		return nil, err
	}

	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      1,
		Result:  resultJSON,
	}, nil
}

// expandDockerEnvArgs expands Docker -e flags that reference environment variables
// Converts "-e VAR_NAME" to "-e VAR_NAME=value" by reading from the process environment
func expandDockerEnvArgs(args []string) []string {
	result := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]

		// Check if this is a -e flag
		if arg == "-e" && i+1 < len(args) {
			nextArg := args[i+1]
			// If next arg doesn't contain '=', it's a variable reference
			if len(nextArg) > 0 && !containsEqual(nextArg) {
				// Look up the variable in the environment
				if value, exists := os.LookupEnv(nextArg); exists {
					result = append(result, "-e")
					result = append(result, fmt.Sprintf("%s=%s", nextArg, value))
					i++ // Skip the next arg since we processed it
					continue
				}
			}
		}
		result = append(result, arg)
	}
	return result
}

func containsEqual(s string) bool {
	for _, c := range s {
		if c == '=' {
			return true
		}
	}
	return false
}

// Close closes the connection
func (c *Connection) Close() error {
	c.cancel()
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}
