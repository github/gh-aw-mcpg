// Package server implements the MCP gateway HTTP server.
// It supports two routing modes: routed mode (one endpoint per backend server
// at /mcp/{serverID}) and unified mode (a single /mcp endpoint that multiplexes
// across all configured backend servers).
package server
