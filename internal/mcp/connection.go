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
	"time"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/logger/sanitize"
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

func getAgentTagsSnapshotFromContext(ctx context.Context) (*AgentTagsSnapshot, bool) {
	return GetAgentTagsSnapshotFromContext(ctx)
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
}

// NewConnection creates a new MCP connection using the official SDK.
// workingDir sets the working directory for the launched process; if empty, the
// current directory of the parent process is used.
func NewConnection(ctx context.Context, serverID, command string, args []string, env map[string]string, workingDir string) (*Connection, error) {
	logger.LogInfo("backend", "Creating new MCP backend connection, command=%s, args=%v", command, sanitize.SanitizeArgs(args))
	logConn.Printf("Creating new MCP connection: command=%s, args=%v", command, sanitize.SanitizeArgs(args))
	ctx, cancel := context.WithCancel(ctx)

	// Create MCP client with logger
	client := newMCPClient(logConn)

	// Expand Docker -e flags that reference environment variables
	// Docker's `-e VAR_NAME` expects VAR_NAME to be in the environment
	expandedArgs := ExpandEnvArgs(args)
	logConn.Printf("Expanded args for Docker env: %v", sanitize.SanitizeArgs(expandedArgs))

	// Create command transport
	cmd := exec.CommandContext(ctx, command, expandedArgs...)

	// Set working directory if configured
	if workingDir != "" {
		cmd.Dir = workingDir
		logConn.Printf("Working directory set: %s", workingDir)
	}

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
			logConn.Printf("[%s stderr] %s", serverID, sanitizedLine)
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
			sanitizedStderr := sanitize.SanitizeString(stderrOutput)
			logger.LogErrorMd("backend", "MCP backend stderr output:\n%s", sanitizedStderr)
			log.Printf("   📋 Container/Process stderr output:")
			for _, line := range strings.Split(sanitizedStderr, "\n") {
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

	log.Printf("Started MCP server: %s %v", command, sanitize.SanitizeArgs(args))
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
	snapshot, hasSnapshot := getAgentTagsSnapshotFromContext(ctx)
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
func marshalToResponse(result interface{}) (*Response, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &Response{
		JSONRPC: "2.0",
		ID:      1, // Placeholder ID
		Result:  resultJSON,
	}, nil
}

// requireSession validates that a session is available for SDK operations.
// This helper centralizes session validation logic across all MCP method wrappers.
// Returns an error if the session is nil (e.g., for plain JSON-RPC transport).
func (c *Connection) requireSession() error {
	if c.session == nil {
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

// callListMethod is a generic helper for SDK list operations with no additional parameters.
// It handles the common pattern of: requireSession → SDK call → marshalToResponse.
func (c *Connection) callListMethod(call func() (interface{}, error)) (*Response, error) {
	if err := c.requireSession(); err != nil {
		return nil, err
	}
	result, err := call()
	if err != nil {
		return nil, err
	}
	return marshalToResponse(result)
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

func (c *Connection) listTools() (*Response, error) {
	logConn.Printf("listTools: requesting tool list from backend serverID=%s", c.serverID)
	return c.callListMethod(func() (interface{}, error) {
		result, err := c.session.ListTools(c.ctx, &sdk.ListToolsParams{})
		if err == nil {
			logConn.Printf("listTools: received %d tools from serverID=%s", len(result.Tools), c.serverID)
		}
		return result, err
	})
}

func (c *Connection) callTool(params interface{}) (*Response, error) {
	return callParamMethod(c, params, func(p CallToolParams) (interface{}, error) {
		// Ensure arguments is never nil - default to empty map
		// This is required by the MCP protocol which expects arguments to always be present
		if p.Arguments == nil {
			p.Arguments = make(map[string]interface{})
		}
		logConn.Printf("callTool: parsed name=%s, arguments=%+v", p.Name, p.Arguments)
		return c.session.CallTool(c.ctx, &sdk.CallToolParams{
			Name:      p.Name,
			Arguments: p.Arguments,
		})
	})
}

func (c *Connection) listResources() (*Response, error) {
	logConn.Printf("listResources: requesting resource list from backend serverID=%s", c.serverID)
	return c.callListMethod(func() (interface{}, error) {
		result, err := c.session.ListResources(c.ctx, &sdk.ListResourcesParams{})
		if err == nil {
			logConn.Printf("listResources: received %d resources from serverID=%s", len(result.Resources), c.serverID)
		}
		return result, err
	})
}

func (c *Connection) readResource(params interface{}) (*Response, error) {
	type readResourceParams struct {
		URI string `json:"uri"`
	}
	return callParamMethod(c, params, func(p readResourceParams) (interface{}, error) {
		logConn.Printf("readResource: reading resource uri=%s from serverID=%s", p.URI, c.serverID)
		return c.session.ReadResource(c.ctx, &sdk.ReadResourceParams{
			URI: p.URI,
		})
	})
}

func (c *Connection) listPrompts() (*Response, error) {
	logConn.Printf("listPrompts: requesting prompt list from backend serverID=%s", c.serverID)
	return c.callListMethod(func() (interface{}, error) {
		result, err := c.session.ListPrompts(c.ctx, &sdk.ListPromptsParams{})
		if err == nil {
			logConn.Printf("listPrompts: received %d prompts from serverID=%s", len(result.Prompts), c.serverID)
		}
		return result, err
	})
}

func (c *Connection) getPrompt(params interface{}) (*Response, error) {
	type getPromptParams struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	return callParamMethod(c, params, func(p getPromptParams) (interface{}, error) {
		logConn.Printf("getPrompt: getting prompt name=%s from serverID=%s", p.Name, c.serverID)
		return c.session.GetPrompt(c.ctx, &sdk.GetPromptParams{
			Name:      p.Name,
			Arguments: p.Arguments,
		})
	})
}

// Close closes the connection
func (c *Connection) Close() error {
	logConn.Printf("Closing connection: serverID=%s, isHTTP=%v", c.serverID, c.isHTTP)
	c.cancel()
	if c.session != nil {
		return c.session.Close()
	}
	return nil
}
