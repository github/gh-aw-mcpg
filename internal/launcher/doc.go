// Package launcher manages backend MCP server connections for the MCP gateway.
// It handles process lifecycle for stdio-based servers, connection pooling per session,
// and connection reuse across concurrent requests to the same backend.
package launcher
