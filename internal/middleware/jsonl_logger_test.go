package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/githubnext/gh-aw-mcpg/internal/mcp"
)

func TestJSONLLogger_Creation(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.jsonl")

	logger, err := NewJSONLLogger(logPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	if logger.Name() != "jsonl-logger" {
		t.Errorf("Expected name 'jsonl-logger', got '%s'", logger.Name())
	}

	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("Log file was not created")
	}
}

func TestJSONLLogger_OnRequest(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.jsonl")

	logger, err := NewJSONLLogger(logPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "tools/list",
		ID:      "req-123",
		Params:  json.RawMessage(`{"arg1":"value1"}`),
	}

	ctx := context.WithValue(context.Background(), "awmg-session-id", "session-123")
	ctx = context.WithValue(ctx, "backend-id", "github")

	_ = logger.OnRequest(ctx, req)

	// Close to flush
	logger.Close()

	// Read and verify log entry
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open log file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("Expected at least one log entry")
	}

	var entry LogEntry
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.Type != "request" {
		t.Errorf("Expected type 'request', got '%s'", entry.Type)
	}

	if entry.Method != "tools/list" {
		t.Errorf("Expected method 'tools/list', got '%s'", entry.Method)
	}

	if entry.RequestID != "req-123" {
		t.Errorf("Expected request ID 'req-123', got '%v'", entry.RequestID)
	}

	if entry.SessionID != "session-123" {
		t.Errorf("Expected session ID 'session-123', got '%s'", entry.SessionID)
	}

	if entry.BackendID != "github" {
		t.Errorf("Expected backend ID 'github', got '%s'", entry.BackendID)
	}

	if entry.Params == nil {
		t.Error("Expected params to be parsed")
	} else if entry.Params["arg1"] != "value1" {
		t.Errorf("Expected params.arg1 'value1', got '%v'", entry.Params["arg1"])
	}
}

func TestJSONLLogger_OnResponse(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.jsonl")

	logger, err := NewJSONLLogger(logPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      "req-456",
	}

	resp := &mcp.Response{
		JSONRPC: "2.0",
		ID:      "req-456",
		Result:  json.RawMessage(`{"status":"success"}`),
	}

	ctx := context.WithValue(context.Background(), "awmg-session-id", "session-456")
	duration := 123 * time.Millisecond

	logger.OnResponse(ctx, req, resp, duration)

	// Close to flush
	logger.Close()

	// Read and verify log entry
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open log file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("Expected at least one log entry")
	}

	var entry LogEntry
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.Type != "response" {
		t.Errorf("Expected type 'response', got '%s'", entry.Type)
	}

	if entry.Method != "tools/call" {
		t.Errorf("Expected method 'tools/call', got '%s'", entry.Method)
	}

	if entry.Duration < 120.0 || entry.Duration > 130.0 {
		t.Errorf("Expected duration around 123ms, got %.2fms", entry.Duration)
	}

	if entry.Result == nil {
		t.Error("Expected result to be parsed")
	} else if entry.Result["status"] != "success" {
		t.Errorf("Expected result.status 'success', got '%v'", entry.Result["status"])
	}
}

func TestJSONLLogger_OnError(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.jsonl")

	logger, err := NewJSONLLogger(logPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "tools/call",
		ID:      "req-789",
	}

	testErr := fmt.Errorf("internal error")

	ctx := context.Background()
	duration := 50 * time.Millisecond

	logger.OnError(ctx, req, testErr, duration)

	// Close to flush
	logger.Close()

	// Read and verify log entry
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open log file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("Expected at least one log entry")
	}

	var entry LogEntry
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.Type != "error" {
		t.Errorf("Expected type 'error', got '%s'", entry.Type)
	}

	if entry.Method != "tools/call" {
		t.Errorf("Expected method 'tools/call', got '%s'", entry.Method)
	}

	if entry.ErrorDetail == "" {
		t.Error("Expected error detail to be set")
	}
}

func TestJSONLLogger_ResponseWithError(t *testing.T) {
	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.jsonl")

	logger, err := NewJSONLLogger(logPath)
	if err != nil {
		t.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Close()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "test/method",
		ID:      "req-error",
	}

	resp := &mcp.Response{
		JSONRPC: "2.0",
		ID:      "req-error",
		Error: &mcp.ResponseError{
			Code:    -32601,
			Message: "Method not found",
		},
	}

	ctx := context.Background()
	duration := 10 * time.Millisecond

	logger.OnResponse(ctx, req, resp, duration)

	// Close to flush
	logger.Close()

	// Read and verify log entry
	file, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("Failed to open log file: %v", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("Expected at least one log entry")
	}

	var entry LogEntry
	if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse log entry: %v", err)
	}

	if entry.Type != "response" {
		t.Errorf("Expected type 'response', got '%s'", entry.Type)
	}

	if entry.Error == nil {
		t.Fatal("Expected error to be present in response")
	}

	if entry.Error.Code != -32601 {
		t.Errorf("Expected error code -32601, got %d", entry.Error.Code)
	}

	if entry.Error.Message != "Method not found" {
		t.Errorf("Expected error message 'Method not found', got '%s'", entry.Error.Message)
	}
}
