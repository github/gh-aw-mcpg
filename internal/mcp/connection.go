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
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
	"github.com/github/gh-aw-mcpg/internal/oidc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

var logConn = logger.New("mcp:connection")

// ContextKey for session ID
type ContextKey string

// SessionIDContextKey is used to store MCP session ID in context
// This is the same key used in the server package to avoid circular dependencies
const SessionIDContextKey ContextKey = "awmg-session-id"

// AgentTagsSnapshotContextKey stores a per-request snapshot of agent DIFC tags for enriched logging.
const AgentTagsSnapshotContextKey ContextKey = "awmg-agent-tags-snapshot"

// AgentTagsSnapshot contains agent secrecy/integrity tag snapshots for log enrichment.
type AgentTagsSnapshot struct {
	Secrecy   []string
	Integrity []string
}

// GetAgentTagsSnapshotFromContext extracts the agent DIFC tag snapshot from the request context.
// Used by guards (e.g., write-sink) that need the agent's current labels to mirror onto resources.
func GetAgentTagsSnapshotFromContext(ctx context.Context) (*AgentTagsSnapshot, bool) {
	if ctx == nil {
		return nil, false
	}

	raw := ctx.Value(AgentTagsSnapshotContextKey)
	snapshot, ok := raw.(*AgentTagsSnapshot)
	if !ok || snapshot == nil {
		return nil, false
	}

	return snapshot, true
}

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
	keepAliveInterval time.Duration     // Keepalive interval for SDK transports (0 = disabled)
	// sessionMu protects the mutable session fields: httpSessionID, session, and client.
	// Always use getHTTPSessionID() or getSDKSession() to read these fields; the
	// reconnect functions (reconnectPlainJSON, reconnectSDKTransport) hold the full Lock.
	sessionMu sync.RWMutex
}

// getSDKSession returns a snapshot of the current SDK session under a read lock.
// Returns nil if no session is available (e.g. plain JSON-RPC transport).
func (c *Connection) getSDKSession() *sdk.ClientSession {
	c.sessionMu.RLock()
	s := c.session
	c.sessionMu.RUnlock()
	return s
}

// getHTTPSessionID returns a snapshot of the current HTTP session ID under a read lock.
func (c *Connection) getHTTPSessionID() string {
	c.sessionMu.RLock()
	id := c.httpSessionID
	c.sessionMu.RUnlock()
	return id
}

// NewConnection creates a new MCP connection using the official SDK
func NewConnection(ctx context.Context, serverID, command string, args []string, env map[string]string) (*Connection, error) {
	logger.LogInfo("backend", "Creating new MCP backend connection, command=%s, args=%v", command, sanitize.SanitizeArgs(args))
	ctx, cancel := context.WithCancel(ctx)

	// Create MCP client with logger (no keepalive for stdio – the process lifespan manages the session)
	client := newMCPClient(logConn, 0)

	// Expand Docker -e flags that reference environment variables
	// Docker's `-e VAR_NAME` expects VAR_NAME to be in the environment
	expandedArgs := envutil.ExpandEnvArgs(args)
	logConn.Printf("Expanded args for Docker env: %v", sanitize.SanitizeArgs(expandedArgs))

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
			sanitizedLine := sanitize.SanitizeString(line)
			logger.LogInfoWithServer(serverID, "backend", "[stderr] %s", sanitizedLine)
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

		stderrOutput := strings.TrimSpace(stderrBuf.String())
		LogConnectionError(ConnectionErrorContext{
			ServerID:     serverID,
			Command:      command,
			Args:         expandedArgs,
			StderrOutput: stderrOutput,
		}, err)

		logConn.Printf("Connection failed: command=%s, error=%v", command, err)
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	logger.LogInfoMd("backend", "Successfully connected to MCP backend server, command=%s", command)

	conn := &Connection{
		client:   client,
		session:  session,
		ctx:      ctx,
		cancel:   cancel,
		serverID: serverID,
		isHTTP:   false,
	}

	log.Printf("Started MCP server: %s %v", command, sanitize.SanitizeArgs(args))
	return conn, nil
}

// NewHTTPConnection creates a new HTTP-based MCP connection with transport fallback
// For HTTP servers that are already running, we connect and initialize a session
//
// This function implements a fallback strategy for HTTP transports:
//  1. Try standard transports in order:
//     a. Streamable HTTP (2025-03-26 spec) using SDK's StreamableClientTransport
//     b. SSE (2024-11-05 spec) using SDK's SSEClientTransport
//     c. Plain JSON-RPC 2.0 over HTTP POST as final fallback
//
// Custom headers (e.g. Authorization) are injected into every outgoing request via a
// custom http.RoundTripper, so the SDK transports are used even when authentication
// headers are configured.
//
// When oidcProvider is non-nil, a GitHub Actions OIDC token is dynamically acquired
// and injected as Authorization: Bearer on every request, overriding any static
// Authorization header from the headers map.
//
// This ensures compatibility with all types of HTTP MCP servers.
func NewHTTPConnection(ctx context.Context, serverID, url string, headers map[string]string, oidcProvider *oidc.Provider, oidcAudience string, keepAlive time.Duration) (*Connection, error) {
	logger.LogInfo("backend", "Creating HTTP MCP connection with transport fallback, url=%s", url)
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

	// Build the transport layer in the correct order so that OIDC takes precedence
	// over any static Authorization header:
	//
	//   headerInjectingRoundTripper (outer — sets static headers first)
	//     └─ oidcRoundTripper        (inner — overrides Authorization with OIDC token)
	//          └─ http.DefaultTransport
	//
	// By placing the OIDC layer inside, it runs last and its Authorization: Bearer
	// header is the one that reaches the server, overwriting any static Authorization
	// from the headers map. Other static headers (e.g. X-Custom-Header) pass through.
	baseClient := httpClient
	if oidcProvider != nil {
		baseClient = buildHTTPClientWithOIDC(httpClient, oidcProvider, oidcAudience)
		logger.LogInfo("backend", "OIDC authentication enabled for HTTP MCP connection: url=%s, audience=%s", url, oidcAudience)
	}

	// Wrap with static header injection on top. When no headers are configured the
	// original client is returned unchanged.
	headerClient := buildHTTPClientWithHeaders(baseClient, headers)

	// Try standard transports in order: streamable HTTP → SSE → plain JSON-RPC

	// Try 1: Streamable HTTP (2025-03-26 spec)
	logConn.Printf("Attempting streamable HTTP transport for %s", url)
	conn, err := tryStreamableHTTPTransport(ctx, cancel, serverID, url, headers, headerClient, keepAlive)
	if err == nil {
		logger.LogInfo("backend", "Successfully connected using streamable HTTP transport, url=%s", url)
		log.Printf("Configured HTTP MCP server with streamable transport: %s", url)
		return conn, nil
	}
	logConn.Printf("Streamable HTTP failed: %v", err)

	// Try 2: SSE (2024-11-05 spec)
	logConn.Printf("Attempting SSE transport for %s", url)
	conn, err = trySSETransport(ctx, cancel, serverID, url, headers, headerClient, keepAlive)
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
	conn, err = tryPlainJSONTransport(ctx, cancel, serverID, url, headers, headerClient)
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

// ServerInfo returns the backend's name and version from the MCP initialize handshake.
// Returns ("", "") when no SDK session is available (plain JSON-RPC transport).
func (c *Connection) ServerInfo() (name, version string) {
	sess := c.getSDKSession()
	if sess == nil {
		return "", ""
	}
	initResult := sess.InitializeResult()
	if initResult == nil || initResult.ServerInfo == nil {
		return "", ""
	}
	return initResult.ServerInfo.Name, initResult.ServerInfo.Version
}

// reconnectPlainJSON re-initialises the plain JSON-RPC session with the HTTP backend.
// It is safe for concurrent callers: only one reconnect runs at a time, and the updated
// session ID is available to all callers once the lock is released.
func (c *Connection) reconnectPlainJSON() error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	logConn.Printf("Session expired, reconnecting plain JSON-RPC for serverID=%s", c.serverID)
	logger.LogWarn("backend", "MCP session expired for %s, attempting to reconnect...", c.serverID)

	sessionID, err := c.initializeHTTPSession()
	if err != nil {
		logger.LogError("backend", "Session reconnect failed for %s: %v", c.serverID, err)
		return fmt.Errorf("session reconnect failed: %w", err)
	}

	c.httpSessionID = sessionID
	logConn.Printf("Reconnected plain JSON-RPC session for serverID=%s, new sessionID=%s", c.serverID, sessionID)
	logger.LogInfo("backend", "Session successfully reconnected for %s", c.serverID)
	return nil
}

// reconnectSDKTransport re-establishes the SDK session for streamable or SSE transports.
// It is safe for concurrent callers: only one reconnect runs at a time.
func (c *Connection) reconnectSDKTransport() error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()

	logConn.Printf("Session expired, reconnecting SDK transport for serverID=%s, type=%s", c.serverID, c.httpTransportType)
	logger.LogWarn("backend", "MCP session expired for %s, attempting to reconnect...", c.serverID)

	// Close the existing session gracefully (ignore error – it's already dead).
	if c.session != nil {
		_ = c.session.Close()
	}

	// Rebuild the header-injecting client so custom auth headers are preserved on reconnect.
	headerClient := buildHTTPClientWithHeaders(c.httpClient, c.headers)

	// Build the appropriate transport.
	// Re-use the same keepAliveInterval so the reconnected session also sends periodic pings.
	client := newMCPClient(logConn, c.keepAliveInterval)
	var transport sdk.Transport
	switch c.httpTransportType {
	case HTTPTransportStreamable:
		transport = &sdk.StreamableClientTransport{
			Endpoint:   c.httpURL,
			HTTPClient: headerClient,
			MaxRetries: 0,
		}
	case HTTPTransportSSE:
		transport = &sdk.SSEClientTransport{
			Endpoint:   c.httpURL,
			HTTPClient: headerClient,
		}
	default:
		return fmt.Errorf("cannot reconnect: unsupported transport type %s", c.httpTransportType)
	}

	connectCtx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()

	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		logger.LogError("backend", "Session reconnect failed for %s: %v", c.serverID, err)
		return fmt.Errorf("session reconnect failed: %w", err)
	}

	c.client = client
	c.session = session

	logConn.Printf("Reconnected SDK session for serverID=%s", c.serverID)
	logger.LogInfo("backend", "Session successfully reconnected for %s", c.serverID)
	return nil
}

// callSDKMethodWithReconnect calls the SDK method and, if the session has expired,
// reconnects and retries exactly once before propagating the error.
func (c *Connection) callSDKMethodWithReconnect(method string, params interface{}) (*Response, error) {
	result, err := c.callSDKMethod(method, params)
	if err != nil && isSessionNotFoundError(err) {
		logConn.Printf("Session not found error from SDK (serverID=%s), attempting reconnect", c.serverID)
		if reconnErr := c.reconnectSDKTransport(); reconnErr != nil {
			logConn.Printf("SDK session reconnect failed for serverID=%s: %v; returning original error", c.serverID, reconnErr)
			logger.LogError("backend", "SDK session reconnect failed for %s: %v", c.serverID, reconnErr)
			// Return the original session-not-found error so the caller sees a meaningful message.
			return result, err
		}
		result, err = c.callSDKMethod(method, params)
	}
	return result, err
}

// SendRequest sends a JSON-RPC request and waits for the response
// The serverID parameter is used for logging to associate the request with a backend server
func (c *Connection) SendRequest(method string, params interface{}) (*Response, error) {
	return c.SendRequestWithServerID(context.Background(), method, params, "unknown")
}

// SendRequestWithServerID sends a JSON-RPC request with server ID for logging
// The ctx parameter is used to extract session ID for HTTP MCP servers
func (c *Connection) SendRequestWithServerID(ctx context.Context, method string, params interface{}, serverID string) (*Response, error) {
	snapshot, hasSnapshot := GetAgentTagsSnapshotFromContext(ctx)
	shouldAttachAgentTags := hasSnapshot && difc.IsSinkServerID(serverID)

	// Log the outbound request to backend server
	requestPayload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if shouldAttachAgentTags {
		logger.LogRPCRequestWithAgentSnapshot(logger.RPCDirectionOutbound, serverID, method, requestPayload, snapshot.Secrecy, snapshot.Integrity)
	} else {
		logger.LogRPCRequest(logger.RPCDirectionOutbound, serverID, method, requestPayload)
	}

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
			if shouldAttachAgentTags {
				logger.LogRPCResponseWithAgentSnapshot(logger.RPCDirectionInbound, serverID, responsePayload, err, snapshot.Secrecy, snapshot.Integrity)
			} else {
				logger.LogRPCResponse(logger.RPCDirectionInbound, serverID, responsePayload, err)
			}
			return result, err
		}

		// For streamable and SSE transports, use SDK session methods
		result, err = c.callSDKMethodWithReconnect(method, params)
		// Log the response from backend server
		var responsePayload []byte
		if result != nil {
			responsePayload, _ = json.Marshal(result)
		}
		if shouldAttachAgentTags {
			logger.LogRPCResponseWithAgentSnapshot(logger.RPCDirectionInbound, serverID, responsePayload, err, snapshot.Secrecy, snapshot.Integrity)
		} else {
			logger.LogRPCResponse(logger.RPCDirectionInbound, serverID, responsePayload, err)
		}
		return result, err
	}

	// Handle stdio connections using SDK client
	result, err = c.callSDKMethod(method, params)

	// Log the response from backend server
	var responsePayload []byte
	if result != nil {
		responsePayload, _ = json.Marshal(result)
	}
	if shouldAttachAgentTags {
		logger.LogRPCResponseWithAgentSnapshot(logger.RPCDirectionInbound, serverID, responsePayload, err, snapshot.Secrecy, snapshot.Integrity)
	} else {
		logger.LogRPCResponse(logger.RPCDirectionInbound, serverID, responsePayload, err)
	}

	return result, err
}

// callSDKMethod calls the appropriate SDK method based on the method name
// This centralizes the method dispatch logic used by both HTTP SDK transports and stdio
func (c *Connection) callSDKMethod(method string, params interface{}) (*Response, error) {
	logConn.Printf("Dispatching SDK method: %s, serverID=%s", method, c.serverID)
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
		logConn.Printf("Unsupported method: %s", method)
		return nil, fmt.Errorf("unsupported method: %s", method)
	}
}

// marshalToResponse marshals an SDK result into a Response object.
// This helper reduces code duplication across all MCP method wrappers.
//
// The ID field is set to a static placeholder (1) because this Response is only
// constructed after the SDK's session.XXX() call has already resolved the
// request–response correlation internally. The gateway never uses this ID for
// matching; it is present solely to satisfy the JSON-RPC 2.0 structure.
func marshalToResponse(result interface{}) (*Response, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      1, // Placeholder – see function comment for safety rationale
		Result:  resultJSON,
	}, nil
}

// requireSession validates that a session is available for SDK operations.
// This helper centralizes session validation logic across all MCP method wrappers.
// Returns an error if the session is nil (e.g., for plain JSON-RPC transport).
func (c *Connection) requireSession() error {
	if c.getSDKSession() == nil {
		return fmt.Errorf("SDK session not available for plain JSON-RPC transport")
	}
	return nil
}

// unmarshalParams converts generic interface{} params to a specific struct type.
// This helper reduces code duplication across MCP method wrappers and ensures
// consistent error handling for parameter conversion. It uses marshal/unmarshal
// to maintain JSON schema validation benefits.
func unmarshalParams(params interface{}, target interface{}) error {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("failed to marshal params: %w", err)
	}
	if err := json.Unmarshal(paramsJSON, target); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

// callParamMethod is a generic helper for SDK operations that require typed parameters.
// It handles the common pattern of: requireSession → unmarshalParams → fn(params) → marshalToResponse.
// P is the type of the parameter struct to unmarshal into.
func callParamMethod[P any](c *Connection, rawParams interface{}, fn func(P) (interface{}, error)) (*Response, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	var params P
	if err := unmarshalParams(rawParams, &params); err != nil {
		return nil, err
	}
	result, err := fn(params)
	if err != nil {
		return nil, err
	}
	return marshalToResponse(result)
}

// paginatedPage holds a single page of results from a paginated SDK list call.
type paginatedPage[T any] struct {
	Items      []T
	NextCursor string
}

// paginateAllMaxPages is the maximum number of pages that paginateAll will fetch.
// This guards against misbehaving or adversarial backends that return an unbounded
// sequence of pages, which would otherwise consume unbounded memory and time.
const paginateAllMaxPages = 100

// paginateAll collects all items across paginated SDK list calls.
// It returns an error if the backend returns more than paginateAllMaxPages pages,
// protecting against runaway backends.
func paginateAll[T any](
	serverID string,
	itemKind string,
	fetch func(cursor string) (paginatedPage[T], error),
) ([]T, error) {
	first, err := fetch("")
	if err != nil {
		return nil, err
	}
	all := make([]T, len(first.Items), max(len(first.Items), 1))
	copy(all, first.Items)
	logConn.Printf("list%s: received page of %d %s from serverID=%s", itemKind, len(first.Items), itemKind, serverID)

	cursor := first.NextCursor
	for pageCount := 1; cursor != ""; pageCount++ {
		if pageCount >= paginateAllMaxPages {
			return nil, fmt.Errorf("list%s: backend serverID=%s returned more than %d pages; aborting to prevent unbounded memory growth", itemKind, serverID, paginateAllMaxPages)
		}
		page, err := fetch(cursor)
		if err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		logConn.Printf("list%s: received page of %d %s (total so far: %d) from serverID=%s", itemKind, len(page.Items), itemKind, len(all), serverID)
		cursor = page.NextCursor
	}
	logConn.Printf("list%s: received %d %s total from serverID=%s", itemKind, len(all), itemKind, serverID)
	return all, nil
}

// listMCPItems is a generic helper for the list* family of MCP operations.
// It handles session validation, logging, pagination, and response marshalling,
// eliminating the boilerplate that was previously duplicated across listTools,
// listResources, and listPrompts.
func listMCPItems[Item any, Result any](
	c *Connection,
	kind string,
	fetchPage func(cursor string) (paginatedPage[Item], error),
	buildResult func([]Item) Result,
) (*Response, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	logConn.Printf("list%s: requesting %s list from backend serverID=%s", kind, kind, c.serverID)
	items, err := paginateAll(c.serverID, kind, fetchPage)
	if err != nil {
		return nil, err
	}
	return marshalToResponse(buildResult(items))
}

func (c *Connection) listTools() (*Response, error) {
	return listMCPItems(c, "tools",
		func(cursor string) (paginatedPage[*sdk.Tool], error) {
			result, err := c.getSDKSession().ListTools(c.ctx, &sdk.ListToolsParams{Cursor: cursor})
			if err != nil {
				return paginatedPage[*sdk.Tool]{}, err
			}
			return paginatedPage[*sdk.Tool]{Items: result.Tools, NextCursor: result.NextCursor}, nil
		},
		func(items []*sdk.Tool) *sdk.ListToolsResult {
			return &sdk.ListToolsResult{Tools: items}
		},
	)
}

func (c *Connection) callTool(params interface{}) (*Response, error) {
	return callParamMethod(c, params, func(p CallToolParams) (interface{}, error) {
		// Ensure arguments is never nil - default to empty map
		// This is required by the MCP protocol which expects arguments to always be present
		if p.Arguments == nil {
			p.Arguments = make(map[string]interface{})
		}
		logConn.Printf("callTool: parsed name=%s, arguments=%+v", p.Name, p.Arguments)
		return c.getSDKSession().CallTool(c.ctx, &sdk.CallToolParams{
			Name:      p.Name,
			Arguments: p.Arguments,
		})
	})
}

func (c *Connection) listResources() (*Response, error) {
	return listMCPItems(c, "resources",
		func(cursor string) (paginatedPage[*sdk.Resource], error) {
			result, err := c.getSDKSession().ListResources(c.ctx, &sdk.ListResourcesParams{Cursor: cursor})
			if err != nil {
				return paginatedPage[*sdk.Resource]{}, err
			}
			return paginatedPage[*sdk.Resource]{Items: result.Resources, NextCursor: result.NextCursor}, nil
		},
		func(items []*sdk.Resource) *sdk.ListResourcesResult {
			return &sdk.ListResourcesResult{Resources: items}
		},
	)
}

func (c *Connection) readResource(params interface{}) (*Response, error) {
	type readResourceParams struct {
		URI string `json:"uri"`
	}
	return callParamMethod(c, params, func(p readResourceParams) (interface{}, error) {
		logConn.Printf("readResource: reading resource uri=%s from serverID=%s", p.URI, c.serverID)
		return c.getSDKSession().ReadResource(c.ctx, &sdk.ReadResourceParams{
			URI: p.URI,
		})
	})
}

func (c *Connection) listPrompts() (*Response, error) {
	return listMCPItems(c, "prompts",
		func(cursor string) (paginatedPage[*sdk.Prompt], error) {
			result, err := c.getSDKSession().ListPrompts(c.ctx, &sdk.ListPromptsParams{Cursor: cursor})
			if err != nil {
				return paginatedPage[*sdk.Prompt]{}, err
			}
			return paginatedPage[*sdk.Prompt]{Items: result.Prompts, NextCursor: result.NextCursor}, nil
		},
		func(items []*sdk.Prompt) *sdk.ListPromptsResult {
			return &sdk.ListPromptsResult{Prompts: items}
		},
	)
}

func (c *Connection) getPrompt(params interface{}) (*Response, error) {
	type getPromptParams struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	return callParamMethod(c, params, func(p getPromptParams) (interface{}, error) {
		logConn.Printf("getPrompt: getting prompt name=%s from serverID=%s", p.Name, c.serverID)
		return c.getSDKSession().GetPrompt(c.ctx, &sdk.GetPromptParams{
			Name:      p.Name,
			Arguments: p.Arguments,
		})
	})
}

// Close closes the connection
func (c *Connection) Close() error {
	logConn.Printf("Closing connection: serverID=%s, isHTTP=%v", c.serverID, c.isHTTP)
	if c.cancel != nil {
		c.cancel()
	}
	if session := c.getSDKSession(); session != nil {
		return session.Close()
	}
	return nil
}
