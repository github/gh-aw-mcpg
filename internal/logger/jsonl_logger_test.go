package logger

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitJSONLLogger(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	// Test successful initialization
	output := captureStdLog(t, func() {
		err := InitJSONLLogger(logDir, "test.jsonl")
		require.NoError(err, "InitJSONLLogger failed")
	})
	defer CloseAllLoggers()
	assert.Contains(output, "Initializing JSONL logger: dir="+logDir+", file=test.jsonl")

	// Verify log file was created
	logPath := filepath.Join(logDir, "test.jsonl")
	_, err := os.Stat(logPath)
	require.NoError(err, "Log file should exist at %s", logPath)
}

func TestJSONLRPCMessage_SetAgentTags(t *testing.T) {
	entry := &JSONLRPCMessage{}
	secrecy := []string{"private", "repo"}
	integrity := []string{"approved"}
	entry.setAgentTags(secrecy, integrity)
	assert.Equal(t, secrecy, entry.AgentSecrecy)
	assert.Equal(t, integrity, entry.AgentIntegrity)

	secrecy[0] = "changed"
	integrity[0] = "changed"
	assert.Equal(t, []string{"private", "repo"}, entry.AgentSecrecy)
	assert.Equal(t, []string{"approved"}, entry.AgentIntegrity)
	entry.AgentSecrecy[1] = "snapshot"
	assert.Equal(t, "repo", secrecy[1])
	entry.AgentIntegrity[0] = "snapshot"
	assert.Equal(t, "changed", integrity[0])

	for _, tags := range [][]string{nil, {}} {
		entry.setAgentTags(tags, tags)
		assert.Nil(t, entry.AgentSecrecy)
		assert.Nil(t, entry.AgentIntegrity)
		data, err := json.Marshal(entry)
		require.NoError(t, err)
		assert.NotContains(t, string(data), "agent_secrecy")
		assert.NotContains(t, string(data), "agent_integrity")
	}
}

func TestJSONLLoggerClose(t *testing.T) {
	require := require.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")

	// Test closing
	err = CloseAllLoggers()
	require.NoError(err, "CloseAllLoggers should not error")

	// Test closing again (should not error)
	err = CloseAllLoggers()
	require.NoError(err, "CloseAllLoggers should not error on second call")
}

func TestLogRPCMessageJSONL(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	// Log a request
	requestPayload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	LogRPCMessageJSONL(RPCDirectionOutbound, RPCMessageRequest, "github", "tools/list", requestPayload, nil)

	// Log a response
	responsePayload := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	LogRPCMessageJSONL(RPCDirectionInbound, RPCMessageResponse, "github", "", responsePayload, nil)

	// Close to flush
	CloseAllLoggers()

	// Read and verify the log file
	logPath := filepath.Join(logDir, "test.jsonl")
	file, err := os.Open(logPath)
	require.NoError(err, "Failed to open log file")
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineCount := 0

	for scanner.Scan() {
		lineCount++
		line := scanner.Text()

		var entry JSONLRPCMessage
		err := json.Unmarshal([]byte(line), &entry)
		require.NoError(err, "Failed to parse JSONL line %d: %s", lineCount, line)

		// Verify common fields
		assert.NotEmpty(entry.Timestamp, "Line %d: missing timestamp", lineCount)
		assert.NotEmpty(entry.Direction, "Line %d: missing direction", lineCount)
		assert.NotEmpty(entry.Event, "Line %d: missing event", lineCount)
		assert.NotEmpty(entry.Schema, "Line %d: missing _schema", lineCount)
		assert.NotEmpty(entry.ServerID, "Line %d: missing server_id", lineCount)
		assert.NotNil(entry.Payload, "Line %d: missing payload", lineCount)
		_, tsErr := time.Parse(jsonTimestampLayout, entry.Timestamp)
		require.NoError(tsErr, "Line %d: timestamp must be ISO 8601 with milliseconds", lineCount)
		assert.Equal("rpc-message/v2", entry.Schema, "Line %d: expected schema rpc-message/v2", lineCount)

		// Verify line-specific fields
		switch lineCount {
		case 1:
			// First line should be a REQUEST
			assert.Equal("rpc_request", entry.Event, "Line 1: expected event rpc_request")
			assert.Equal("tools/list", entry.Method, "Line 1: expected method tools/list")
			assert.Equal("OUT", entry.Direction, "Line 1: expected direction OUT")
		case 2:
			// Second line should be a RESPONSE
			assert.Equal("rpc_response", entry.Event, "Line 2: expected event rpc_response")
			assert.Equal("IN", entry.Direction, "Line 2: expected direction IN")
		}
	}

	err = scanner.Err()
	require.NoError(err, "Error reading log file")

	assert.Equal(2, lineCount, "Expected 2 log entries")
}

func TestSanitizePayload(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		expectRedacted bool
		checkField     string
	}{
		{
			name:           "token in payload",
			input:          `{"token":"ghp_1234567890123456789012345678901234567890"}`,
			expectRedacted: true,
			checkField:     "token",
		},
		{
			name:           "nested token in params",
			input:          `{"params":{"auth":"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.test.sig"}}`,
			expectRedacted: true,
			checkField:     "params.auth",
		},
		{
			name:           "password field",
			input:          `{"password":"supersecret123"}`,
			expectRedacted: true,
			checkField:     "password",
		},
		{
			name:           "clean payload",
			input:          `{"method":"tools/list","id":1}`,
			expectRedacted: false,
			checkField:     "method",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require := require.New(t)
			assert := assert.New(t)

			result := sanitize.SanitizeJSON([]byte(tt.input))
			require.NotNil(result, "sanitize.SanitizeJSON returned nil")

			// The result is already a sanitized string
			sanitizedStr := string(result)

			if tt.expectRedacted {
				// Should contain [REDACTED]
				assert.Contains(sanitizedStr, "[REDACTED]", "Expected sanitized payload to contain [REDACTED]")

				// Should NOT contain the original secret patterns
				assert.NotContains(sanitizedStr, "ghp_", "Sanitized payload should not contain GitHub token")
				assert.NotContains(sanitizedStr, "Bearer eyJ", "Sanitized payload should not contain Bearer token")
				assert.NotContains(sanitizedStr, "supersecret", "Sanitized payload should not contain password")
			} else {
				// Should not contain [REDACTED] for clean payloads
				assert.NotContains(sanitizedStr, "[REDACTED]", "Clean payload should not be redacted")
			}
		})
	}
}

func TestSanitizePayloadWithNestedStructures(t *testing.T) {
	assert := assert.New(t)
	input := `{
		"params": {
			"credentials": {
				"apiKey": "test_fake_api_key_1234567890abcdefghij",
				"token": "ghp_1234567890123456789012345678901234567890"
			},
			"data": {
				"items": [
					{"name": "item1", "secret": "password123"},
					{"name": "item2", "value": "safe"}
				]
			}
		}
	}`

	result := sanitize.SanitizeJSON([]byte(input))

	// The result is already a sanitized string
	sanitizedStr := string(result)

	// Should redact all secrets at all levels
	assert.Contains(sanitizedStr, "[REDACTED]", "Expected [REDACTED] in sanitized output")

	// Should NOT contain original secrets
	assert.NotContains(sanitizedStr, "test_fake_api_key", "API key should be sanitized")
	assert.NotContains(sanitizedStr, "ghp_", "GitHub token should be sanitized")
	assert.NotContains(sanitizedStr, "password123", "Password should be sanitized")

	// Should preserve non-secret values
	assert.Contains(sanitizedStr, "item1", "Non-secret value 'item1' should be preserved")
	assert.Contains(sanitizedStr, "safe", "Non-secret value 'safe' should be preserved")
}

func TestLogRPCMessageJSONLWithError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	// Log a response with error
	responsePayload := []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid request"}}`)
	testErr := fmt.Errorf("backend connection failed")
	LogRPCMessageJSONL(RPCDirectionInbound, RPCMessageResponse, "github", "", responsePayload, testErr)

	// Close to flush
	CloseAllLoggers()

	// Read and verify
	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL")

	assert.Equal("backend connection failed", entry.Error, "Error field should match")
}

func TestLogRPCMessageJSONLWithInvalidJSON(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	// Log invalid JSON
	invalidPayload := []byte(`{invalid json}`)
	LogRPCMessageJSONL(RPCDirectionOutbound, RPCMessageRequest, "github", "test", invalidPayload, nil)

	// Close to flush
	CloseAllLoggers()

	// Read and verify
	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL")

	// The payload should be wrapped in a valid JSON object with an error marker
	var payloadObj map[string]interface{}
	err = json.Unmarshal(entry.Payload, &payloadObj)
	require.NoError(err, "Failed to parse payload")

	assert.Equal("invalid JSON", payloadObj["_error"], "Expected _error field in payload")
	assert.Contains(fmt.Sprintf("%v", payloadObj["_raw"]), "invalid", "Expected _raw field to contain original invalid JSON")
}

func TestJSONLLoggerNotInitialized(t *testing.T) {
	// Ensure no global logger is set
	CloseAllLoggers()

	// Should not panic when logging without initialization
	requestPayload := []byte(`{"jsonrpc":"2.0","id":1,"method":"test"}`)
	LogRPCMessageJSONL(RPCDirectionOutbound, RPCMessageRequest, "github", "test", requestPayload, nil)
	// Test passes if no panic occurs
}

func TestJSONLLoggerLogEntryWithoutInitializationLogsWarning(t *testing.T) {
	assert := assert.New(t)
	logger := &JSONLLogger{}

	output := captureStdLog(t, func() {
		err := logger.logEntry("entry")
		assert.Error(err)
	})

	assert.Contains(output, "WARNING: JSONL logEntry called but logger not initialized, dropping entry")
}

func TestMultipleMessagesInJSONL(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	// Log multiple messages
	messages := []struct {
		direction RPCMessageDirection
		msgType   RPCMessageType
		serverID  string
		method    string
		payload   string
	}{
		{RPCDirectionOutbound, RPCMessageRequest, "github", "tools/list", `{"jsonrpc":"2.0","method":"tools/list"}`},
		{RPCDirectionInbound, RPCMessageResponse, "github", "", `{"jsonrpc":"2.0","result":{}}`},
		{RPCDirectionOutbound, RPCMessageRequest, "backend", "tools/call", `{"jsonrpc":"2.0","method":"tools/call"}`},
		{RPCDirectionInbound, RPCMessageResponse, "backend", "", `{"jsonrpc":"2.0","result":{}}`},
	}

	for _, msg := range messages {
		LogRPCMessageJSONL(msg.direction, msg.msgType, msg.serverID, msg.method, []byte(msg.payload), nil)
	}

	// Close to flush
	CloseAllLoggers()

	// Read and verify all lines
	logPath := filepath.Join(logDir, "test.jsonl")
	file, err := os.Open(logPath)
	require.NoError(err, "Failed to open log file")
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineCount := 0

	for scanner.Scan() {
		lineCount++
		line := scanner.Text()

		var entry JSONLRPCMessage
		err := json.Unmarshal([]byte(line), &entry)
		require.NoError(err, "Failed to parse JSONL line %d", lineCount)

		// Each line should be valid JSONL with required fields
		assert.NotEmpty(entry.Timestamp, "Line %d: missing timestamp", lineCount)
		assert.NotEmpty(entry.ServerID, "Line %d: missing server_id", lineCount)
	}

	assert.Equal(len(messages), lineCount, "Expected %d log entries", len(messages))
}

func TestSanitizePayloadCompactsJSON(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	// Test that multi-line JSON is compacted to a single line
	multilineJSON := `{
		"jsonrpc": "2.0",
		"method": "test",
		"params": {
			"nested": {
				"value": "test"
			}
		}
	}`

	result := sanitize.SanitizeJSON([]byte(multilineJSON))
	resultStr := string(result)

	// The result should not contain newlines
	assert.NotContains(resultStr, "\n", "Result should not contain newlines")

	// Should still be valid JSON
	var tmp interface{}
	err := json.Unmarshal(result, &tmp)
	require.NoError(err, "Result should be valid JSON")

	// Should contain the expected values
	assert.Contains(resultStr, "jsonrpc", "Result should contain 'jsonrpc'")
	assert.Contains(resultStr, "test", "Result should contain 'test'")
}

func TestInitJSONLLoggerWithInvalidPath(t *testing.T) {
	assert := assert.New(t)

	// Test initialization with an invalid directory path (permission denied scenario)
	// Using /proc/self as it's read-only and will fail to create subdirectories
	output := captureStdLog(t, func() {
		err := InitJSONLLogger("/proc/self/invalid", "test.jsonl")
		assert.Error(err, "InitJSONLLogger should fail with invalid directory path")
	})
	assert.Contains(output, "Initializing JSONL logger: dir=/proc/self/invalid, file=test.jsonl")
}

func TestLogRPCMessageJSONLDirectionTypes(t *testing.T) {
	require := require.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	tests := []struct {
		name      string
		direction RPCMessageDirection
		msgType   RPCMessageType
		expected  map[string]string
	}{
		{
			name:      "outbound request",
			direction: RPCDirectionOutbound,
			msgType:   RPCMessageRequest,
			expected:  map[string]string{"direction": "OUT", "event": "rpc_request", "schema": "rpc-message/v2"},
		},
		{
			name:      "inbound request",
			direction: RPCDirectionInbound,
			msgType:   RPCMessageRequest,
			expected:  map[string]string{"direction": "IN", "event": "rpc_request", "schema": "rpc-message/v2"},
		},
		{
			name:      "outbound response",
			direction: RPCDirectionOutbound,
			msgType:   RPCMessageResponse,
			expected:  map[string]string{"direction": "OUT", "event": "rpc_response", "schema": "rpc-message/v2"},
		},
		{
			name:      "inbound response",
			direction: RPCDirectionInbound,
			msgType:   RPCMessageResponse,
			expected:  map[string]string{"direction": "IN", "event": "rpc_response", "schema": "rpc-message/v2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := assert.New(t)
			testPayload := []byte(`{"jsonrpc":"2.0","id":1}`)

			// Clear previous log file
			logPath := filepath.Join(logDir, "test.jsonl")
			os.Remove(logPath)

			// Re-init logger for each subtest
			CloseAllLoggers()
			err := InitJSONLLogger(logDir, "test.jsonl")
			require.NoError(err, "Re-init failed")

			LogRPCMessageJSONL(tt.direction, tt.msgType, "test-server", "test-method", testPayload, nil)
			CloseAllLoggers()

			// Read and verify
			content, err := os.ReadFile(logPath)
			require.NoError(err, "Failed to read log file")

			var entry JSONLRPCMessage
			err = json.Unmarshal(content, &entry)
			require.NoError(err, "Failed to parse JSONL entry")

			a.Equal(tt.expected["direction"], entry.Direction, "Direction should match")
			a.Equal(tt.expected["event"], entry.Event, "Event should match")
			a.Equal(tt.expected["schema"], entry.Schema, "Schema should match")
		})
	}
}

func TestLogRPCMessageJSONLEmptyPayload(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	// Log with empty payload
	emptyPayload := []byte(`{}`)
	LogRPCMessageJSONL(RPCDirectionOutbound, RPCMessageRequest, "github", "test", emptyPayload, nil)

	CloseAllLoggers()

	// Read and verify
	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL")

	// Should still have a valid payload field
	assert.NotNil(entry.Payload, "Payload should not be nil even when empty")
	assert.NotEmpty(entry.Timestamp, "Timestamp should be present")
	assert.Equal("github", entry.ServerID, "ServerID should match")
}

func TestLogRPCMessageJSONLWithNilError(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	// Log with nil error (normal case)
	payload := []byte(`{"jsonrpc":"2.0","id":1}`)
	LogRPCMessageJSONL(RPCDirectionOutbound, RPCMessageRequest, "github", "test", payload, nil)

	CloseAllLoggers()

	// Read and verify
	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL")

	// Error field should be empty when nil error is passed
	assert.Empty(entry.Error, "Error field should be empty when no error")
}

// TestLogDifcFilteredItem_NilEntryDoesNotPanic verifies that calling
// LogDifcFilteredItem with a nil entry is safe.  The DIFC audit log path
// must never crash the gateway even when passed unexpected input.
func TestLogDifcFilteredItem_NilEntryDoesNotPanic(t *testing.T) {
	var output string
	assert.NotPanics(t, func() {
		output = captureStdLog(t, func() {
			LogDifcFilteredItem(nil)
		})
	}, "LogDifcFilteredItem(nil) must not panic")

	assert.Contains(t, output, "LogDifcFilteredItem called with nil entry, skipping")
}

// TestLogDifcFilteredItem_WritesAuditEntryToJSONL verifies that
// LogDifcFilteredItem correctly writes a difc_filtered entry to the JSONL
// audit log.  Audit trail continuity requires every filtered item to appear
// in rpc-messages.jsonl so privileged audit agents can inspect them.
func TestLogDifcFilteredItem_WritesAuditEntryToJSONL(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "rpc-messages.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	entry := &JSONLFilteredItem{
		FilteredItemLogEntry: FilteredItemLogEntry{
			ServerID:          "github",
			ToolName:          "list_issues",
			Description:       "issue:org/repo#42",
			Reason:            "agent lacks secrecy clearance for private:org/repo",
			SecrecyTags:       []string{"private:org/repo"},
			IntegrityTags:     []string{"approved:org/repo"},
			AuthorLogin:       "octocat",
			AuthorAssociation: "CONTRIBUTOR",
			HTMLURL:           "https://github.com/org/repo/issues/42",
			Number:            "42",
		},
	}
	LogDifcFilteredItem(entry)

	CloseAllLoggers()

	logPath := filepath.Join(logDir, "rpc-messages.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "log file must exist after LogDifcFilteredItem")

	var logged JSONLFilteredItem
	err = json.Unmarshal(content, &logged)
	require.NoError(err, "JSONL entry must be valid JSON")

	assert.Equal("difc_filtered", logged.Event, "Event must be difc_filtered")
	assert.Equal("difc-filtered/v2", logged.Schema, "Schema must be difc-filtered/v2")
	assert.NotEmpty(logged.Timestamp, "Timestamp must be set for audit trail")
	_, tsErr := time.Parse(jsonTimestampLayout, logged.Timestamp)
	require.NoError(tsErr, "Timestamp must be ISO 8601 with milliseconds")
	assert.Equal("github", logged.ServerID)
	assert.Equal("list_issues", logged.ToolName)
	assert.Equal("issue:org/repo#42", logged.Description)
	assert.Equal("agent lacks secrecy clearance for private:org/repo", logged.Reason)
	assert.Equal([]string{"private:org/repo"}, logged.SecrecyTags)
	assert.Equal([]string{"approved:org/repo"}, logged.IntegrityTags)
	assert.Equal("octocat", logged.AuthorLogin)
	assert.Equal("42", logged.Number)
}

// TestLogDifcFilteredItem_MultipleEntriesAuditTrail verifies that multiple
// filtered items all appear in the JSONL audit log in order, without loss.
// This exercises the audit trail continuity recommendation from the integrity
// audit: every filtered item must be retained for retrospective analysis.
func TestLogDifcFilteredItem_MultipleEntriesAuditTrail(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "rpc-messages.jsonl")
	require.NoError(err)
	defer CloseAllLoggers()

	entries := []*JSONLFilteredItem{
		{FilteredItemLogEntry: FilteredItemLogEntry{ServerID: "github", ToolName: "list_issues", Number: "1", Reason: "secrecy mismatch"}},
		{FilteredItemLogEntry: FilteredItemLogEntry{ServerID: "github", ToolName: "list_prs", Number: "2", Reason: "integrity too low"}},
		{FilteredItemLogEntry: FilteredItemLogEntry{ServerID: "github", ToolName: "list_commits", SHA: "abc123", Reason: "secrecy mismatch"}},
	}

	for _, e := range entries {
		LogDifcFilteredItem(e)
	}
	CloseAllLoggers()

	logPath := filepath.Join(logDir, "rpc-messages.jsonl")
	file, err := os.Open(logPath)
	require.NoError(err)
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var lines []JSONLFilteredItem
	for scanner.Scan() {
		var item JSONLFilteredItem
		err := json.Unmarshal([]byte(scanner.Text()), &item)
		require.NoError(err, "each line must be valid JSON")
		lines = append(lines, item)
	}
	require.NoError(scanner.Err())

	assert.Len(lines, 3, "all 3 filtered items must appear in the audit log")
	for i, line := range lines {
		assert.Equal("difc_filtered", line.Event, "entry[%d] must have event=difc_filtered", i)
		assert.Equal("difc-filtered/v2", line.Schema, "entry[%d] must have _schema=difc-filtered/v2", i)
		assert.NotEmpty(line.Timestamp, "entry[%d] must have Timestamp", i)
		_, tsErr := time.Parse(jsonTimestampLayout, line.Timestamp)
		require.NoError(tsErr, "entry[%d] timestamp must be ISO 8601 with milliseconds", i)
		assert.NotEmpty(line.Reason, "entry[%d] must have Reason", i)
	}
}

// TestLogRPCMessageJSONLWithTags_AgentSecrecyTags verifies that agent secrecy tags
// are written into the JSONL entry when provided, and that integrity is omitted.
func TestLogRPCMessageJSONLWithTags_AgentSecrecyTags(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	payload := []byte(`{"jsonrpc":"2.0","id":1}`)
	secrecyTags := []string{"private:org/repo", "public"}

	LogRPCMessageJSONLWithTags(RPCDirectionInbound, RPCMessageResponse, "github", "tools/call", payload, nil, secrecyTags, nil)
	CloseAllLoggers()

	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL entry")

	assert.Equal(secrecyTags, entry.AgentSecrecy, "AgentSecrecy should match provided tags")
	assert.Empty(entry.AgentIntegrity, "AgentIntegrity should be absent when not provided")
}

// TestLogRPCMessageJSONLWithTags_AgentIntegrityTags verifies that agent integrity tags
// are written into the JSONL entry when provided, and that secrecy is omitted.
func TestLogRPCMessageJSONLWithTags_AgentIntegrityTags(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	payload := []byte(`{"jsonrpc":"2.0","id":1}`)
	integrityTags := []string{"approved:org/repo", "merged"}

	LogRPCMessageJSONLWithTags(RPCDirectionOutbound, RPCMessageRequest, "github", "tools/list", payload, nil, nil, integrityTags)
	CloseAllLoggers()

	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL entry")

	assert.Empty(entry.AgentSecrecy, "AgentSecrecy should be absent when not provided")
	assert.Equal(integrityTags, entry.AgentIntegrity, "AgentIntegrity should match provided tags")
}

// TestLogRPCMessageJSONLWithTags_BothTagTypes verifies that both agent secrecy and
// integrity tags are correctly written when both are provided in the same call.
func TestLogRPCMessageJSONLWithTags_BothTagTypes(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	payload := []byte(`{"jsonrpc":"2.0","id":2}`)
	secrecyTags := []string{"private:org/repo"}
	integrityTags := []string{"approved:org/repo", "merged:org/repo"}

	LogRPCMessageJSONLWithTags(RPCDirectionInbound, RPCMessageResponse, "github", "tools/call", payload, nil, secrecyTags, integrityTags)
	CloseAllLoggers()

	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL entry")

	assert.Equal(secrecyTags, entry.AgentSecrecy, "AgentSecrecy should match")
	assert.Equal(integrityTags, entry.AgentIntegrity, "AgentIntegrity should match")
	assert.Equal("github", entry.ServerID)
	assert.Equal("tools/call", entry.Method)
}

// TestLogRPCMessageJSONLWithTags_EmptyTagsOmitted verifies that empty (non-nil) tag
// slices are treated the same as nil — they must NOT appear in the JSON output due to
// the omitempty struct tag on AgentSecrecy and AgentIntegrity.
func TestLogRPCMessageJSONLWithTags_EmptyTagsOmitted(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	payload := []byte(`{"jsonrpc":"2.0","id":1}`)

	// Pass explicitly empty (non-nil) slices.
	LogRPCMessageJSONLWithTags(RPCDirectionOutbound, RPCMessageRequest, "github", "tools/list", payload, nil, []string{}, []string{})
	CloseAllLoggers()

	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL entry")

	// Empty slices must not be stored (len == 0 check in implementation).
	assert.Empty(entry.AgentSecrecy, "AgentSecrecy must be absent for empty slice input")
	assert.Empty(entry.AgentIntegrity, "AgentIntegrity must be absent for empty slice input")

	// Verify the raw JSON does not contain the tag fields at all.
	assert.NotContains(string(content), "agent_secrecy", "raw JSON must not contain agent_secrecy key for empty slice")
	assert.NotContains(string(content), "agent_integrity", "raw JSON must not contain agent_integrity key for empty slice")
}

// TestLogRPCMessageJSONLWithTags_TagsCopied verifies that the tags stored in the log
// entry are independent copies of the caller's slice. Mutating the original after the
// call must not alter the data that was written to disk.
func TestLogRPCMessageJSONLWithTags_TagsCopied(t *testing.T) {
	require := require.New(t)
	assert := assert.New(t)

	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	err := InitJSONLLogger(logDir, "test.jsonl")
	require.NoError(err, "InitJSONLLogger failed")
	defer CloseAllLoggers()

	payload := []byte(`{"jsonrpc":"2.0","id":1}`)
	secrecyTags := []string{"private:org/repo"}
	integrityTags := []string{"approved:org/repo"}

	LogRPCMessageJSONLWithTags(RPCDirectionInbound, RPCMessageResponse, "github", "tools/call", payload, nil, secrecyTags, integrityTags)
	CloseAllLoggers()

	// Mutate the originals after the call.
	secrecyTags[0] = "MUTATED"
	integrityTags[0] = "MUTATED"

	logPath := filepath.Join(logDir, "test.jsonl")
	content, err := os.ReadFile(logPath)
	require.NoError(err, "Failed to read log file")

	var entry JSONLRPCMessage
	err = json.Unmarshal(content, &entry)
	require.NoError(err, "Failed to parse JSONL entry")

	// The logged values must reflect the originals at call time, not the mutation.
	assert.Equal([]string{"private:org/repo"}, entry.AgentSecrecy, "AgentSecrecy must be an independent copy")
	assert.Equal([]string{"approved:org/repo"}, entry.AgentIntegrity, "AgentIntegrity must be an independent copy")
}

// TestLogEntry_NilLogFile verifies that logEntry returns a descriptive error when
// the JSONLLogger has not been initialized (logFile == nil).
func TestLogEntry_NilLogFile(t *testing.T) {
	// Create a logger with nil logFile — simulates an uninitialized logger.
	jl := &JSONLLogger{}

	err := jl.logEntry(map[string]string{"key": "value"})

	require.Error(t, err, "logEntry with nil logFile should return an error")
	assert.ErrorContains(t, err, "not initialized", "error message should indicate logger is not initialized")
}

// TestLogEntry_EncodeError verifies that logEntry returns a wrapped error when
// json.Encoder.Encode fails (e.g., for a value that cannot be marshaled to JSON).
func TestLogEntry_EncodeError(t *testing.T) {
	tmpDir := t.TempDir()
	f, err := os.CreateTemp(tmpDir, "encode-error-*.jsonl")
	require.NoError(t, err, "failed to create temp file")
	t.Cleanup(func() { f.Close() })

	jl := &JSONLLogger{
		logFile: f,
		encoder: json.NewEncoder(f),
	}

	// Channels cannot be marshaled to JSON; Encode must return an error.
	err = jl.logEntry(make(chan int))

	require.Error(t, err, "logEntry should return error for un-encodable type")
	assert.ErrorContains(t, err, "failed to encode JSON", "error should be wrapped with context")
}

// TestLogEntry_SyncError verifies that logEntry returns a wrapped error when
// logFile.Sync() fails. This happens for file descriptors that do not support
// fsync, such as the write end of an OS pipe.
func TestLogEntry_SyncError(t *testing.T) {
	// os.Pipe returns a connected pair of Files. Calling Sync on the write end
	// returns EINVAL on Linux because pipes do not support fsync.
	r, w, err := os.Pipe()
	require.NoError(t, err, "failed to create OS pipe")
	t.Cleanup(func() {
		r.Close()
		w.Close()
	})

	// Pre-flight: verify that Sync on the write end of a pipe actually fails on
	// this OS/filesystem. If Sync is a no-op or succeeds, skip so the suite
	// stays portable.
	if syncErr := w.Sync(); syncErr == nil {
		t.Skip("os.File.Sync on a pipe does not return an error on this platform")
	}

	jl := &JSONLLogger{
		logFile: w,
		encoder: json.NewEncoder(w),
	}

	// A simple JSON-encodable value: Encode should succeed, but Sync must fail.
	syncErr := jl.logEntry(map[string]string{"event": "test"})

	require.Error(t, syncErr, "logEntry should return an error when Sync fails")
	assert.ErrorContains(t, syncErr, "failed to sync log file", "error should be wrapped with sync context")
}

// TestLogEntry_HappyPath verifies that logEntry succeeds for a valid logger and
// encodable entry, writing a single JSONL line to the underlying file.
func TestLogEntry_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.jsonl")
	f, err := os.Create(logPath)
	require.NoError(t, err, "failed to create log file")

	jl := &JSONLLogger{
		logFile: f,
		encoder: json.NewEncoder(f),
	}

	type testPayload struct {
		Event string `json:"event"`
		Count int    `json:"count"`
	}

	err = jl.logEntry(testPayload{Event: "test-event", Count: 42})
	require.NoError(t, err, "logEntry should succeed for valid logger and encodable entry")

	// Flush and verify the file content.
	require.NoError(t, f.Close(), "close should succeed")

	data, err := os.ReadFile(logPath)
	require.NoError(t, err, "failed to read log file")

	var got testPayload
	require.NoError(t, json.Unmarshal(data, &got), "written data should be valid JSON")
	assert.Equal(t, "test-event", got.Event)
	assert.Equal(t, 42, got.Count)
}

// TestLogEntry_ConcurrentAccess verifies that logEntry is safe to call from
// multiple goroutines at the same time — the mutex must prevent data races.
func TestLogEntry_ConcurrentAccess(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "concurrent.jsonl")
	f, err := os.Create(logPath)
	require.NoError(t, err, "failed to create log file")

	jl := &JSONLLogger{
		logFile: f,
		encoder: json.NewEncoder(f),
	}

	const numGoroutines = 20
	done := make(chan error, numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		i := i
		go func() {
			done <- jl.logEntry(map[string]int{"n": i})
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		require.NoError(t, <-done, "concurrent logEntry call should not error")
	}

	// Count lines written: each successful logEntry writes exactly one JSONL line.
	require.NoError(t, f.Close())
	data, err := os.ReadFile(logPath)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	assert.Len(t, lines, numGoroutines, "each goroutine should write exactly one JSONL line")
}

// TestLogEntry_EncodeErrorMessage verifies that the error wrapping preserves
// the underlying JSON marshal error so callers can inspect it with errors.As / errors.Is.
func TestLogEntry_EncodeErrorMessage(t *testing.T) {
	tmpDir := t.TempDir()
	f, err := os.CreateTemp(tmpDir, "encode-err-*.jsonl")
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })

	jl := &JSONLLogger{logFile: f, encoder: json.NewEncoder(f)}

	err = jl.logEntry(make(chan int))

	require.Error(t, err)
	// The original json.UnsupportedTypeError must be unwrappable.
	var unsupported *json.UnsupportedTypeError
	assert.ErrorAs(t, err, &unsupported, "wrapped error should be a *json.UnsupportedTypeError")
}

// TestLogDifcFilteredItem_NilEntry verifies that LogDifcFilteredItem does not
// panic and returns silently when called with a nil entry.
func TestLogDifcFilteredItem_NilEntry(t *testing.T) {
	// This exercises the early-return guard at the top of LogDifcFilteredItem.
	// It must not panic even when no global JSONL logger is initialised.
	assert.NotPanics(t, func() {
		LogDifcFilteredItem(nil)
	}, "LogDifcFilteredItem(nil) must not panic")
}

// TestLogDifcFilteredItem_SetsTimestampEventAndSchema verifies that LogDifcFilteredItem
// populates the Timestamp, Event, and Schema fields of the entry before writing it.
func TestLogDifcFilteredItem_SetsTimestampEventAndSchema(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	require.NoError(t, InitJSONLLogger(logDir, "difc.jsonl"), "InitJSONLLogger failed")
	defer CloseAllLoggers()

	entry := &JSONLFilteredItem{
		FilteredItemLogEntry: FilteredItemLogEntry{
			ServerID:    "github",
			ToolName:    "create_issue",
			Description: "Create a GitHub issue",
			Reason:      "secrecy constraint violated",
			SecrecyTags: []string{"private:org/repo"},
		},
	}

	LogDifcFilteredItem(entry)
	CloseAllLoggers()

	logPath := filepath.Join(logDir, "difc.jsonl")
	data, err := os.ReadFile(logPath)
	require.NoError(t, err, "log file must be readable")

	var got JSONLFilteredItem
	require.NoError(t, json.Unmarshal(data, &got), "log entry must be valid JSON")

	assert.Equal(t, "difc_filtered", got.Event, "Event must always be difc_filtered")
	assert.Equal(t, "difc-filtered/v2", got.Schema, "Schema must always be difc-filtered/v2")
	assert.NotEmpty(t, got.Timestamp, "Timestamp must be set by LogDifcFilteredItem")
	_, tsErr := time.Parse(jsonTimestampLayout, got.Timestamp)
	require.NoError(t, tsErr, "Timestamp must be ISO 8601 with milliseconds")
	assert.Equal(t, "github", got.ServerID)
	assert.Equal(t, "create_issue", got.ToolName)
	assert.Equal(t, []string{"private:org/repo"}, got.SecrecyTags)
}

// TestLogDifcFilteredItem_NoLogger verifies that LogDifcFilteredItem does nothing
// and does not panic when no global JSONL logger has been initialised.
func TestLogDifcFilteredItem_NoLogger(t *testing.T) {
	// Ensure no global logger is active.
	CloseAllLoggers()

	entry := &JSONLFilteredItem{
		FilteredItemLogEntry: FilteredItemLogEntry{
			ServerID: "test",
			ToolName: "some_tool",
		},
	}

	assert.NotPanics(t, func() {
		LogDifcFilteredItem(entry)
	}, "LogDifcFilteredItem must not panic when no logger is initialised")
}

// TestLogUnrecognizedEndpointPassthrough_NoLogger verifies that
// LogUnrecognizedEndpointPassthrough does not panic when no global JSONL logger
// has been initialised (and no markdown logger is active either).
func TestLogUnrecognizedEndpointPassthrough_NoLogger(t *testing.T) {
	CloseAllLoggers()

	assert.NotPanics(t, func() {
		LogUnrecognizedEndpointPassthrough("GET", "/v1/unknown")
	}, "LogUnrecognizedEndpointPassthrough must not panic when no loggers are initialised")
}

// TestLogUnrecognizedEndpointPassthrough_WritesCorrectFields verifies that
// LogUnrecognizedEndpointPassthrough writes the expected fields to the JSONL log
// and emits the UNRECOGNIZED-ENDPOINT warning to the markdown log.
func TestLogUnrecognizedEndpointPassthrough_WritesCorrectFields(t *testing.T) {
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")

	require.NoError(t, InitJSONLLogger(logDir, "rpc-messages.jsonl"), "InitJSONLLogger failed")

	require.NoError(t, InitMarkdownLogger(logDir, "gateway.md"), "InitMarkdownLogger failed")
	defer CloseAllLoggers()

	LogUnrecognizedEndpointPassthrough("POST", "/v1/unknown/endpoint")

	CloseAllLoggers()

	// Verify JSONL output.
	jsonlPath := filepath.Join(logDir, "rpc-messages.jsonl")
	data, err := os.ReadFile(jsonlPath)
	require.NoError(t, err, "JSONL log file must be readable")

	var got JSONLUnrecognizedEndpointPassthrough
	require.NoError(t, json.Unmarshal(data, &got), "JSONL log entry must be valid JSON")

	assert.Equal(t, "unrecognized_endpoint_passthrough", got.Event)
	assert.Equal(t, difcEventSchemaV1, got.Schema)
	assert.Equal(t, "POST", got.Method)
	assert.Equal(t, "/v1/unknown/endpoint", got.Path)
	assert.Equal(t, "passthrough_with_empty_labels", got.Action)
	assert.NotEmpty(t, got.Note)
	assert.NotEmpty(t, got.Timestamp)
	_, tsErr := time.Parse(jsonTimestampLayout, got.Timestamp)
	require.NoError(t, tsErr, "Timestamp must be ISO 8601 with milliseconds")

	// Verify markdown output.
	mdPath := filepath.Join(logDir, "gateway.md")
	mdData, err := os.ReadFile(mdPath)
	require.NoError(t, err, "Markdown log file must be readable")
	assert.Contains(t, string(mdData), "UNRECOGNIZED-ENDPOINT")
	assert.Contains(t, string(mdData), "passthrough_with_empty_labels")
}
