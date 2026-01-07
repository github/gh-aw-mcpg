package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/githubnext/gh-aw-mcpg/internal/logger"
	"github.com/githubnext/gh-aw-mcpg/internal/mcp"
)

var logJSONL = logger.New("middleware:jsonl")

// JSONLLogger is a middleware that logs requests and responses to a JSONL file
type JSONLLogger struct {
	filepath string
	file     *os.File
	mu       sync.Mutex
}

// LogEntry represents a single log entry in the JSONL file
type LogEntry struct {
	Timestamp   string                 `json:"timestamp"`
	Type        string                 `json:"type"` // "request", "response", or "error"
	Method      string                 `json:"method,omitempty"`
	RequestID   interface{}            `json:"request_id,omitempty"`
	Params      map[string]interface{} `json:"params,omitempty"`
	Result      map[string]interface{} `json:"result,omitempty"`
	Error       *LogError              `json:"error,omitempty"`
	Duration    float64                `json:"duration_ms,omitempty"`
	SessionID   string                 `json:"session_id,omitempty"`
	BackendID   string                 `json:"backend_id,omitempty"`
	ErrorDetail string                 `json:"error_detail,omitempty"`
}

// LogError represents an error in the log entry
type LogError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewJSONLLogger creates a new JSONL logger middleware
func NewJSONLLogger(filepath string) (*JSONLLogger, error) {
	file, err := os.OpenFile(filepath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	logJSONL.Printf("Created JSONL logger: path=%s", filepath)
	return &JSONLLogger{
		filepath: filepath,
		file:     file,
	}, nil
}

// Name returns the middleware name
func (j *JSONLLogger) Name() string {
	return "jsonl-logger"
}

// OnRequest logs the incoming request
func (j *JSONLLogger) OnRequest(ctx context.Context, req *mcp.Request) context.Context {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Type:      "request",
		Method:    req.Method,
		RequestID: req.ID,
	}

	// Parse params if available
	if len(req.Params) > 0 {
		var params map[string]interface{}
		if err := json.Unmarshal(req.Params, &params); err == nil {
			entry.Params = params
		}
	}

	// Extract session ID and backend ID from context if available
	if sessionID, ok := ctx.Value("awmg-session-id").(string); ok {
		entry.SessionID = sessionID
	}
	if backendID, ok := ctx.Value("backend-id").(string); ok {
		entry.BackendID = backendID
	}

	j.writeEntry(entry)
	return ctx
}

// OnResponse logs the response
func (j *JSONLLogger) OnResponse(ctx context.Context, req *mcp.Request, resp *mcp.Response, duration time.Duration) {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Type:      "response",
		Method:    req.Method,
		RequestID: resp.ID,
		Duration:  float64(duration.Microseconds()) / 1000.0, // Convert to milliseconds
	}

	// Parse result if available
	if len(resp.Result) > 0 {
		var result map[string]interface{}
		if err := json.Unmarshal(resp.Result, &result); err == nil {
			entry.Result = result
		}
	}

	// Include error if present
	if resp.Error != nil {
		entry.Error = &LogError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
		}
	}

	// Extract session ID and backend ID from context if available
	if sessionID, ok := ctx.Value("awmg-session-id").(string); ok {
		entry.SessionID = sessionID
	}
	if backendID, ok := ctx.Value("backend-id").(string); ok {
		entry.BackendID = backendID
	}

	j.writeEntry(entry)
}

// OnError logs an error
func (j *JSONLLogger) OnError(ctx context.Context, req *mcp.Request, err error, duration time.Duration) {
	entry := LogEntry{
		Timestamp:   time.Now().UTC().Format(time.RFC3339Nano),
		Type:        "error",
		Method:      req.Method,
		RequestID:   req.ID,
		Duration:    float64(duration.Microseconds()) / 1000.0, // Convert to milliseconds
		ErrorDetail: err.Error(),
	}

	// Extract session ID and backend ID from context if available
	if sessionID, ok := ctx.Value("awmg-session-id").(string); ok {
		entry.SessionID = sessionID
	}
	if backendID, ok := ctx.Value("backend-id").(string); ok {
		entry.BackendID = backendID
	}

	j.writeEntry(entry)
}

// writeEntry writes a log entry to the JSONL file
func (j *JSONLLogger) writeEntry(entry LogEntry) {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		logJSONL.Printf("Failed to marshal log entry: %v", err)
		return
	}

	if _, err := j.file.Write(append(data, '\n')); err != nil {
		logJSONL.Printf("Failed to write log entry: %v", err)
	}
}

// Close closes the log file
func (j *JSONLLogger) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.file != nil {
		logJSONL.Printf("Closing JSONL logger: path=%s", j.filepath)
		return j.file.Close()
	}
	return nil
}
