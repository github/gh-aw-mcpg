package mcp

import (
	"context"
	"fmt"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// withReconnectLock acquires the session write lock, logs the reconnect attempt, runs
// reconnect, logs the result, and wraps any error with a consistent message.
// transportName is included in the debug log to identify which transport is reconnecting
// (e.g. "plain JSON-RPC" or "SDK transport (type=streamable)").
func (c *Connection) withReconnectLock(transportName string, reconnect func() error) error {
	c.sessionMu.Lock()
	defer c.sessionMu.Unlock()
	logConn.Printf("Session expired, reconnecting %s for serverID=%s", transportName, c.serverID)
	c.logReconnectStart()
	err := reconnect()
	c.logReconnectResult(err)
	if err != nil {
		return fmt.Errorf("session reconnect failed: %w", err)
	}
	return nil
}

// reconnectPlainJSON re-initialises the plain JSON-RPC session with the HTTP backend.
// It is safe for concurrent callers: only one reconnect runs at a time, and the updated
// session ID is available to all callers once the lock is released.
func (c *Connection) reconnectPlainJSON() error {
	return c.withReconnectLock("plain JSON-RPC", func() error {
		sessionID, err := c.initializeHTTPSession()
		if err != nil {
			return err
		}
		c.httpSessionID = sessionID
		logConn.Printf("Reconnected plain JSON-RPC session for serverID=%s, new sessionID=%s", c.serverID, util.HashIdentifierForLog(sessionID))
		return nil
	})
}

// reconnectSDKTransport re-establishes the SDK session for streamable or SSE transports.
// It is safe for concurrent callers: only one reconnect runs at a time.
func (c *Connection) reconnectSDKTransport() error {
	return c.withReconnectLock(fmt.Sprintf("SDK transport (type=%s)", c.httpTransportType), func() error {
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
			transport = newStreamableTransport(c.httpURL, headerClient)
		case HTTPTransportSSE:
			transport = newSSETransport(c.httpURL, headerClient)
		default:
			return fmt.Errorf("cannot reconnect: unsupported transport type %s", c.httpTransportType)
		}

		timeout := normalizeConnectTimeout(c.connectTimeout)
		connectCtx, cancel := context.WithTimeout(c.ctx, timeout)
		defer cancel()

		session, err := client.Connect(connectCtx, transport, nil)
		if err != nil {
			return err
		}

		c.client = client
		c.session = session

		logConn.Printf("Reconnected SDK session for serverID=%s", c.serverID)
		return nil
	})
}

// callSDKMethodWithReconnect calls the SDK method and, if the session has expired,
// reconnects and retries exactly once before propagating the error.
// ctx is the per-request context (e.g. carrying a tool-timeout deadline) and is
// forwarded to callSDKMethod so that cancellations and deadlines are respected.
func (c *Connection) callSDKMethodWithReconnect(ctx context.Context, method string, params any) (*Response, error) {
	result, err := c.callSDKMethod(ctx, method, params)
	if err != nil && isSessionNotFoundError(err) {
		logConn.Printf("Session not found error from SDK (serverID=%s), attempting reconnect", c.serverID)
		if reconnErr := c.reconnectSDKTransport(); reconnErr != nil {
			logConn.Printf("SDK session reconnect failed for serverID=%s: %v; returning original error", c.serverID, reconnErr)
			logger.LogError("backend", "SDK session reconnect failed for %s: %v", c.serverID, reconnErr)
			// Return the original session-not-found error so the caller sees a meaningful message.
			return result, err
		}
		result, err = c.callSDKMethod(ctx, method, params)
	}
	return result, err
}
