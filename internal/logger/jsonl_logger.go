package logger

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/github/gh-aw-mcpg/internal/sanitize"
)

// JSONLLogger manages logging RPC messages to a JSONL file (one JSON object per line)
type JSONLLogger struct {
	lockable
	logFile  *os.File
	logDir   string
	fileName string
	encoder  *json.Encoder
}

var (
	globalJSONLLogger *JSONLLogger
	globalJSONLMu     sync.RWMutex
	jsonlLoggerRef    = bindGlobalLogger(&globalJSONLMu, &globalJSONLLogger)
)

const (
	jsonTimestampLayout = "2006-01-02T15:04:05.000Z07:00"
	rpcMessageSchemaV2  = "rpc-message/v2"
	difcSchemaV2        = "difc-filtered/v2"
	difcEventSchemaV1   = "difc-event/v1"
)

// JSONLRPCMessage represents a single RPC message log entry in JSONL format
type JSONLRPCMessage struct {
	Timestamp      string          `json:"timestamp"`
	Event          string          `json:"event"`     // "rpc_request" or "rpc_response"
	Schema         string          `json:"_schema"`   // "rpc-message/v2"
	Direction      string          `json:"direction"` // "IN" or "OUT"
	ServerID       string          `json:"server_id"`
	Method         string          `json:"method,omitempty"`
	Error          string          `json:"error,omitempty"`
	AgentSecrecy   []string        `json:"agent_secrecy,omitempty"`
	AgentIntegrity []string        `json:"agent_integrity,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`     // Tool name, preserved when the payload is redacted
	Redacted       bool            `json:"redacted,omitempty"`      // True when payload holds metadata only
	PayloadSize    int             `json:"payload_bytes,omitempty"` // Original payload size, recorded when redacted
	Payload        json.RawMessage `json:"payload"`                 // Full sanitized payload as raw JSON
}

func (entry *JSONLRPCMessage) setAgentTags(agentSecrecy, agentIntegrity []string) {
	entry.AgentSecrecy = append([]string(nil), agentSecrecy...)
	entry.AgentIntegrity = append([]string(nil), agentIntegrity...)
}

// jsonlLoggerFactory bundles the setup and error-handler for JSONLLogger.
var jsonlLoggerFactory = newLoggerFactory(
	func(file *os.File, logDir, fileName string) (*JSONLLogger, error) {
		jl := &JSONLLogger{
			logDir:   logDir,
			fileName: fileName,
			logFile:  file,
			encoder:  json.NewEncoder(file),
		}
		return jl, nil
	},
	func(err error, _ string, _ string) (*JSONLLogger, error) {
		// JSONLLogger has no fallback mode — return the error immediately.
		return strictLoggerOnInitError[*JSONLLogger](err)
	},
)

// InitJSONLLogger initializes the global JSONL logger
func InitJSONLLogger(logDir, fileName string) error {
	log.Printf("Initializing JSONL logger: dir=%s, file=%s", logDir, fileName)
	return jsonlLoggerRef.initOnSuccess(logDir, fileName, os.O_APPEND, jsonlLoggerFactory)
}

// Close closes the JSONL log file
func (jl *JSONLLogger) Close() error {
	return closeLogFileWithLock(&jl.lockable, jl.logFile, "JSONL")
}

// LogMessage logs an RPC message to the JSONL file
func (jl *JSONLLogger) LogMessage(entry *JSONLRPCMessage) error {
	return jl.logEntry(entry)
}

// logEntry writes any JSON-serializable value as a single JSONL line.
func (jl *JSONLLogger) logEntry(entry interface{}) error {
	jl.mu.Lock()
	defer jl.mu.Unlock()

	if jl.logFile == nil {
		log.Print("WARNING: JSONL logEntry called but logger not initialized, dropping entry")
		return fmt.Errorf("JSONL logger not initialized")
	}

	if err := jl.encoder.Encode(entry); err != nil {
		log.Printf("WARNING: Failed to encode JSONL entry: %v", err)
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	if err := jl.logFile.Sync(); err != nil {
		log.Printf("WARNING: Failed to sync JSONL log file: %v", err)
		return fmt.Errorf("failed to sync log file: %w", err)
	}

	return nil
}

// LogRPCMessageJSONL logs an RPC message to the global JSONL logger
func LogRPCMessageJSONL(direction RPCMessageDirection, messageType RPCMessageType, serverID, method string, payloadBytes []byte, err error) {
	LogRPCMessageJSONLWithTags(direction, messageType, serverID, method, payloadBytes, err, nil, nil)
}

// LogRPCMessageJSONLWithTags logs an RPC message to the global JSONL logger with optional agent tag snapshots.
// It uses the withGlobalLogger helper from global_helpers.go to handle mutex locking and nil-checking.
func LogRPCMessageJSONLWithTags(direction RPCMessageDirection, messageType RPCMessageType, serverID, method string, payloadBytes []byte, err error, agentSecrecy, agentIntegrity []string) {
	logRPCMessageJSONLWithTagsAndSanitized(direction, messageType, serverID, method, sanitize.SanitizeJSON(payloadBytes), err, agentSecrecy, agentIntegrity)
}

// logRPCMessageJSONLWithTagsAndSanitized is the internal variant that accepts a pre-sanitized
// payload to avoid redundant regex passes when the caller already holds a sanitized copy.
func logRPCMessageJSONLWithTagsAndSanitized(direction RPCMessageDirection, messageType RPCMessageType, serverID, method string, sanitizedPayload json.RawMessage, err error, agentSecrecy, agentIntegrity []string) {
	withGlobalLogger(&globalJSONLMu, &globalJSONLLogger, func(logger *JSONLLogger) {
		entry := &JSONLRPCMessage{
			Timestamp: time.Now().UTC().Format(jsonTimestampLayout),
			Event:     messageType.JSONLEvent(),
			Schema:    rpcMessageSchemaV2,
			Direction: string(direction),
			ServerID:  serverID,
			Method:    method,
			Payload:   sanitizedPayload,
		}

		entry.setAgentTags(agentSecrecy, agentIntegrity)

		if err != nil {
			entry.Error = err.Error()
		}

		// Best effort logging - don't fail if JSONL logging fails
		_ = logger.LogMessage(entry)
	})
}

// logRPCMessageJSONLRedacted writes an enclave-scoped RPC message whose payload has already
// been reduced to metadata. The entry records direction, server, method, tool name, size, DIFC
// tags, and a sanitized error category, but never the payload content.
func logRPCMessageJSONLRedacted(direction RPCMessageDirection, messageType RPCMessageType, serverID, method, toolName string, payloadSize int, redactedPayload json.RawMessage, errCategory string, agentSecrecy, agentIntegrity []string) {
	withGlobalLogger(&globalJSONLMu, &globalJSONLLogger, func(logger *JSONLLogger) {
		entry := &JSONLRPCMessage{
			Timestamp:   time.Now().UTC().Format(jsonTimestampLayout),
			Event:       messageType.JSONLEvent(),
			Schema:      rpcMessageSchemaV2,
			Direction:   string(direction),
			ServerID:    serverID,
			Method:      method,
			ToolName:    toolName,
			Redacted:    true,
			PayloadSize: payloadSize,
			Payload:     redactedPayload,
			Error:       errCategory,
		}

		entry.setAgentTags(agentSecrecy, agentIntegrity)

		// Best effort logging - don't fail if JSONL logging fails
		_ = logger.LogMessage(entry)
	})
}

// FilteredItemLogEntry holds the data fields for a DIFC-filtered item.
// It is used for both text log output ([DIFC-FILTERED] JSON lines) and as
// the embedded payload in JSONLFilteredItem for JSONL log output.
type FilteredItemLogEntry struct {
	ServerID            string   `json:"server_id"`
	ToolName            string   `json:"tool_name"`
	Description         string   `json:"description"`
	Reason              string   `json:"reason"`
	SecrecyTags         []string `json:"secrecy_tags"`
	IntegrityTags       []string `json:"integrity_tags"`
	AgentSecrecyTags    []string `json:"agent_secrecy_tags,omitempty"`
	AgentIntegrityTags  []string `json:"agent_integrity_tags,omitempty"`
	AgentLabelsComplete bool     `json:"-"`
	AuthorAssociation   string   `json:"author_association,omitempty"`
	AuthorLogin         string   `json:"author_login,omitempty"`
	HTMLURL             string   `json:"html_url,omitempty"`
	Number              string   `json:"number,omitempty"`
	SHA                 string   `json:"sha,omitempty"`
}

// RedactForEnclave returns a copy of the entry with every item-identifying field reduced to a
// per-process-keyed, non-reversible token and every free-text field passed through private-selector
// redaction. DIFC decisions stay diagnosable (server, tool, tags, and a correlatable item
// token) while the private resource the decision was made about is never persisted.
func (e FilteredItemLogEntry) RedactForEnclave() FilteredItemLogEntry {
	redacted := e
	redacted.Description = "item:" + sanitize.KeyedDigest(e.Description)
	redacted.HTMLURL = "url:" + sanitize.KeyedDigest(e.HTMLURL)
	redacted.AuthorLogin = "user:" + sanitize.KeyedDigest(e.AuthorLogin)
	redacted.Number = "num:" + sanitize.KeyedDigest(e.Number)
	redacted.SHA = "sha:" + sanitize.KeyedDigest(e.SHA)
	redacted.Reason = sanitize.RedactPrivateSelectors(e.Reason)
	redacted.SecrecyTags = redactTags(e.SecrecyTags)
	redacted.IntegrityTags = redactTags(e.IntegrityTags)
	redacted.AgentSecrecyTags = redactTags(e.AgentSecrecyTags)
	redacted.AgentIntegrityTags = redactTags(e.AgentIntegrityTags)
	return redacted
}

// redactTags applies private-selector redaction to each DIFC tag, preserving the tag list
// shape (and therefore the comparability of agent and resource labels).
func redactTags(tags []string) []string {
	if tags == nil {
		return nil
	}
	redacted := make([]string, len(tags))
	for i, tag := range tags {
		redacted[i] = sanitize.RedactPrivateSelectors(tag)
	}
	return redacted
}

// JSONLFilteredItem represents a DIFC-filtered item logged to the JSONL stream.
// These entries appear alongside RPC messages so filter events are visible
// in context with the request/response that triggered them.
// It embeds FilteredItemLogEntry so that adding a data field requires only a
// single edit rather than parallel edits in two structs.
type JSONLFilteredItem struct {
	Timestamp string `json:"timestamp"`
	Event     string `json:"event"`   // Always "difc_filtered"
	Schema    string `json:"_schema"` // "difc-filtered/v2"
	FilteredItemLogEntry
}

// MarshalJSON preserves omitted agent snapshots for ordinary filtered items,
// while allowing denial entries to encode an explicitly empty snapshot.
func (entry JSONLFilteredItem) MarshalJSON() ([]byte, error) {
	type alias JSONLFilteredItem
	data, err := json.Marshal(alias(entry))
	if err != nil || !entry.AgentLabelsComplete {
		return data, err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	secrecy, err := json.Marshal(entry.AgentSecrecyTags)
	if err != nil {
		return nil, err
	}
	integrity, err := json.Marshal(entry.AgentIntegrityTags)
	if err != nil {
		return nil, err
	}
	fields["agent_secrecy_tags"] = secrecy
	fields["agent_integrity_tags"] = integrity
	return json.Marshal(fields)
}

// JSONLUnrecognizedEndpointPassthrough records when the proxy forwards an
// unrecognized endpoint with empty DIFC labels.
type JSONLUnrecognizedEndpointPassthrough struct {
	Timestamp string `json:"timestamp"`
	Event     string `json:"event"`
	Schema    string `json:"_schema"`
	Method    string `json:"method"`
	Path      string `json:"path"`
	Action    string `json:"action"`
	Note      string `json:"note"`
}

// LogDifcFilteredItem writes a DIFC filter event to the JSONL log.
func LogDifcFilteredItem(entry *JSONLFilteredItem) {
	if entry == nil {
		// Best-effort logging: avoid panicking on nil input.
		log.Print("LogDifcFilteredItem called with nil entry, skipping")
		return
	}

	entry.Timestamp = time.Now().UTC().Format(jsonTimestampLayout)
	entry.Event = "difc_filtered"
	entry.Schema = difcSchemaV2
	withGlobalLogger(&globalJSONLMu, &globalJSONLLogger, func(logger *JSONLLogger) {
		_ = logger.logEntry(entry)
	})
}

// LogUnrecognizedEndpointPassthrough writes the proxy's unrecognized-endpoint
// passthrough event to the standard text log, markdown artifact log, and JSONL log.
func LogUnrecognizedEndpointPassthrough(method, path string) {
	entry := &JSONLUnrecognizedEndpointPassthrough{
		Timestamp: time.Now().UTC().Format(jsonTimestampLayout),
		Event:     "unrecognized_endpoint_passthrough",
		Schema:    difcEventSchemaV1,
		Method:    method,
		Path:      path,
		Action:    "passthrough_with_empty_labels",
		Note:      "Endpoint not in route table or metadata allowlist -- forwarded with no integrity and no secrecy labels",
	}

	b, _ := json.Marshal(entry)
	LogWarnToMarkdown("proxy", "[UNRECOGNIZED-ENDPOINT] %s", string(b))

	withGlobalLogger(&globalJSONLMu, &globalJSONLLogger, func(logger *JSONLLogger) {
		_ = logger.logEntry(entry)
	})
}
