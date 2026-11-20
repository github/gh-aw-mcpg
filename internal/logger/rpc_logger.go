// Package logger provides structured logging for the MCP Gateway.
//
// This file contains RPC message logging coordination, managing the flow of messages
// across multiple output formats (text, markdown, JSONL).
//
// File Organization:
//
// - rpc_logger.go (this file): Coordination of RPC logging across formats
// - rpc_format.go: Formatting and payload helper functions
//
// The package supports logging RPC messages in three formats:
//
// 1. Text logs: Compact single-line format for grep-friendly searching
// 2. Markdown logs: Human-readable format with syntax highlighting
// 3. JSONL logs: Machine-readable format for structured analysis
//
// Example:
//
//	logger.LogRPCRequest(logger.RPCDirectionOutbound, "github", "tools/list", payload, nil, nil)
//	logger.LogRPCResponse(logger.RPCDirectionInbound, "github", responsePayload, nil, nil, nil)
package logger

import (
	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/github/gh-aw-mcpg/internal/util"
)

// RPCMessageType represents the direction of an RPC message
type RPCMessageType string

const (
	// RPCMessageRequest represents an outbound request or inbound client request
	RPCMessageRequest RPCMessageType = "REQUEST"
	// RPCMessageResponse represents an inbound response from backend or outbound response to client
	RPCMessageResponse RPCMessageType = "RESPONSE"
)

// JSONLEvent returns the standardized JSONL event name for this RPC message type.
func (t RPCMessageType) JSONLEvent() string {
	switch t {
	case RPCMessageRequest:
		return "rpc_request"
	case RPCMessageResponse:
		return "rpc_response"
	default:
		return "rpc_unknown"
	}
}

// RPCMessageDirection represents whether the message is inbound or outbound
type RPCMessageDirection string

const (
	// RPCDirectionInbound represents messages coming into the gateway
	RPCDirectionInbound RPCMessageDirection = "IN"
	// RPCDirectionOutbound represents messages going out from the gateway
	RPCDirectionOutbound RPCMessageDirection = "OUT"
)

const (
	// MaxPayloadPreviewLengthText is the maximum number of characters to include in text log preview (10KB)
	MaxPayloadPreviewLengthText = 10 * 1024 // 10KB
	// MaxPayloadPreviewLengthMarkdown is the maximum number of characters to include in markdown log preview
	MaxPayloadPreviewLengthMarkdown = 512
)

// RPCMessageInfo contains information about an RPC message for logging
type RPCMessageInfo struct {
	Direction   RPCMessageDirection // IN or OUT
	MessageType RPCMessageType      // REQUEST or RESPONSE
	ServerID    string              // Backend server ID or "client" for client messages
	Method      string              // RPC method name (for requests)
	PayloadSize int                 // Size of the payload in bytes
	Payload     string              // First N characters of payload (sanitized)
	Error       string              // Error message if any (for responses)
	ToolName    string              // Tool name for tools/call requests, preserved when the payload is redacted
	Redacted    bool                // Whether the payload was reduced to metadata only
}

// newRPCMessageInfoFromSanitized builds an RPCMessageInfo from an already-sanitized
// payload string, truncating to maxPayload without re-running the regex sanitization pass.
// Use this when the same payload is logged at multiple preview lengths to avoid
// redundant sanitization work.
func newRPCMessageInfoFromSanitized(direction RPCMessageDirection, messageType RPCMessageType, serverID, method string, payload []byte, err error, sanitized string, maxPayload int) *RPCMessageInfo {
	info := &RPCMessageInfo{
		Direction:   direction,
		MessageType: messageType,
		ServerID:    serverID,
		Method:      method,
		PayloadSize: len(payload),
		Payload:     util.Truncate(sanitized, maxPayload),
	}
	if err != nil {
		info.Error = err.Error()
	}
	return info
}

func logRPCMarkdown(info *RPCMessageInfo) {
	withGlobalLogger(&globalMarkdownMu, &globalMarkdownLogger, func(logger *MarkdownLogger) {
		logger.Log(LogLevelDebug, "rpc", "%s", formatRPCMessageMarkdown(info))
	})
}

// logRPCMessageToAll routes a single RPC message to all log sinks (text, markdown, JSONL).
// It uses the withGlobalLogger helper from global_helpers.go to handle mutex locking and nil-checking.
// When enclaveSession is set (or process-wide payload redaction is active), the payload never
// reaches any sink: every format receives metadata only.
func logRPCMessageToAll(direction RPCMessageDirection, messageType RPCMessageType, serverID, method string, payload []byte, err error, agentSecrecy, agentIntegrity []string, enclaveSession bool) {
	if sanitize.ShouldRedactPayload(enclaveSession) {
		logRedactedRPCMessageToAll(direction, messageType, serverID, method, payload, err, agentSecrecy, agentIntegrity)
		return
	}
	// Sanitize the payload string once, then share across all sinks.
	// SanitizeString runs 10 compiled regex patterns; computing it once and
	// passing the result to both preview builders and the JSONL logger avoids
	// running the same patterns a second time per RPC hop.
	sanitized := sanitize.SanitizeString(string(payload))

	// Log to text file (with larger payload preview)
	infoText := newRPCMessageInfoFromSanitized(direction, messageType, serverID, method, payload, err, sanitized, MaxPayloadPreviewLengthText)
	LogDebug("rpc", "%s", formatRPCMessage(infoText))

	// Log to markdown file (with shorter payload preview)
	infoMarkdown := newRPCMessageInfoFromSanitized(direction, messageType, serverID, method, payload, err, sanitized, MaxPayloadPreviewLengthMarkdown)
	logRPCMarkdown(infoMarkdown)

	// Log to JSONL file (full payload, sanitized).
	// Use the pre-sanitized string variant to avoid running the 10 regex patterns again.
	logRPCMessageJSONLWithTagsAndSanitized(direction, messageType, serverID, method, sanitize.SanitizeJSONFromString(sanitized), err, agentSecrecy, agentIntegrity)
}

// logRedactedRPCMessageToAll routes an enclave-scoped RPC message to all log sinks with the
// payload replaced by metadata (byte count and a stable digest) and the error reduced to a
// coarse category. Direction, server, method, tool name, size, and DIFC tags are preserved so
// the call stays diagnosable without its content.
func logRedactedRPCMessageToAll(direction RPCMessageDirection, messageType RPCMessageType, serverID, method string, payload []byte, err error, agentSecrecy, agentIntegrity []string) {
	toolName := toolNameFromRequestPayload(method, payload)
	info := &RPCMessageInfo{
		Direction:   direction,
		MessageType: messageType,
		ServerID:    serverID,
		Method:      method,
		PayloadSize: len(payload),
		Payload:     sanitize.RedactedPayloadText(payload),
		Error:       sanitize.RedactErrorForLog(err),
		ToolName:    toolName,
		Redacted:    true,
	}

	LogDebug("rpc", "%s", formatRPCMessage(info))

	logRPCMarkdown(info)

	logRPCMessageJSONLRedacted(direction, messageType, serverID, method, toolName, len(payload), sanitize.RedactedPayloadJSON(payload), info.Error, agentSecrecy, agentIntegrity)
}

// LogRPCRequest logs an RPC request message to text, markdown, and JSONL logs.
// agentSecrecy and agentIntegrity are optional and only affect JSONL output.
func LogRPCRequest(direction RPCMessageDirection, serverID, method string, payload []byte, agentSecrecy, agentIntegrity []string) {
	LogRPCRequestForSession(direction, serverID, method, payload, agentSecrecy, agentIntegrity, false)
}

// LogRPCRequestForSession logs an RPC request message, redacting the payload when the call
// originates from an enclave-scoped session.
func LogRPCRequestForSession(direction RPCMessageDirection, serverID, method string, payload []byte, agentSecrecy, agentIntegrity []string, enclaveSession bool) {
	logRPCMessageToAll(direction, RPCMessageRequest, serverID, method, payload, nil, agentSecrecy, agentIntegrity, enclaveSession)
}

// LogRPCResponse logs an RPC response message to text, markdown, and JSONL logs.
// agentSecrecy and agentIntegrity are optional and only affect JSONL output.
func LogRPCResponse(direction RPCMessageDirection, serverID string, payload []byte, err error, agentSecrecy, agentIntegrity []string) {
	LogRPCResponseForSession(direction, serverID, payload, err, agentSecrecy, agentIntegrity, false)
}

// LogRPCResponseForSession logs an RPC response message, redacting the payload when the call
// originates from an enclave-scoped session.
func LogRPCResponseForSession(direction RPCMessageDirection, serverID string, payload []byte, err error, agentSecrecy, agentIntegrity []string, enclaveSession bool) {
	logRPCMessageToAll(direction, RPCMessageResponse, serverID, "", payload, err, agentSecrecy, agentIntegrity, enclaveSession)
}

// LogRPCMessage logs a generic RPC message with custom info.
// It uses the withGlobalLogger helper from global_helpers.go to handle mutex locking and nil-checking.
func LogRPCMessage(info *RPCMessageInfo) {
	// Log to text file
	LogDebug("rpc", "%s", formatRPCMessage(info))

	// Log to markdown file using withGlobalLogger helper
	logRPCMarkdown(info)
}
