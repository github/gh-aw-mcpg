package logger

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFormatRPCMessage(t *testing.T) {
	tests := []struct {
		name string
		info *RPCMessageInfo
		want []string // Strings that should be present in output
	}{
		{
			name: "outbound request",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionOutbound,
				MessageType: RPCMessageRequest,
				ServerID:    "github",
				Method:      "tools/list",
				PayloadSize: 50,
				Payload:     `{"jsonrpc":"2.0","method":"tools/list"}`,
			},
			want: []string{"github→tools/list", "50b", `{"jsonrpc":"2.0","method":"tools/list"}`},
		},
		{
			name: "inbound response with error",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionInbound,
				MessageType: RPCMessageResponse,
				ServerID:    "github",
				PayloadSize: 100,
				Payload:     `{"jsonrpc":"2.0","error":{"code":-32600}}`,
				Error:       "Invalid request",
			},
			want: []string{"github←resp", "100b", "err:Invalid request"},
		},
		{
			name: "client request",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionInbound,
				MessageType: RPCMessageRequest,
				ServerID:    "client",
				Method:      "tools/call",
				PayloadSize: 200,
				Payload:     `{"method":"tools/call","params":{}}`,
			},
			want: []string{"client←tools/call", "200b"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRPCMessage(tt.info)
			for _, expected := range tt.want {
				assert.Contains(t, result, expected)
			}
		})
	}
}

func TestFormatRPCMessageMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		info    *RPCMessageInfo
		want    []string // Strings that should be present in output
		notWant []string // Strings that should NOT be present in output
	}{
		{
			name: "outbound request",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionOutbound,
				MessageType: RPCMessageRequest,
				ServerID:    "github",
				Method:      "tools/list",
				PayloadSize: 50,
				Payload:     `{"jsonrpc":"2.0","method":"tools/list","params":{}}`,
			},
			want:    []string{"**github**→`tools/list`", "```json", `"params"`, "{}"},
			notWant: []string{`"jsonrpc"`, `"method"`},
		},
		{
			name: "inbound response",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionInbound,
				MessageType: RPCMessageResponse,
				ServerID:    "github",
				PayloadSize: 100,
				Payload:     `{"result":{}}`,
			},
			want:    []string{"**github**←`resp`", "```json", `"result"`},
			notWant: []string{`"jsonrpc"`, `"method"`},
		},
		{
			name: "response with error",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionInbound,
				MessageType: RPCMessageResponse,
				ServerID:    "github",
				PayloadSize: 100,
				Error:       "Connection timeout",
			},
			want:    []string{"**github**←`resp`", "⚠️`Connection timeout`"},
			notWant: []string{},
		},
		{
			name: "invalid JSON payload uses inline backticks",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionOutbound,
				MessageType: RPCMessageRequest,
				ServerID:    "github",
				Method:      "tools/call",
				PayloadSize: 30,
				Payload:     `{invalid json syntax}`,
			},
			want:    []string{"**github**→`tools/call`", "`{invalid json syntax}`"},
			notWant: []string{"```json"}, // Should NOT use code blocks for invalid JSON
		},
		{
			name: "request with only params null after field removal",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionOutbound,
				MessageType: RPCMessageRequest,
				ServerID:    "github",
				Method:      "tools/list",
				PayloadSize: 50,
				Payload:     `{"jsonrpc":"2.0","method":"tools/list","params":null}`,
			},
			want:    []string{"**github**→`tools/list`"},
			notWant: []string{"```json", `"params"`}, // Should NOT show JSON block when only params: null
		},
		{
			name: "request with empty object after field removal",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionOutbound,
				MessageType: RPCMessageRequest,
				ServerID:    "github",
				Method:      "tools/list",
				PayloadSize: 50,
				Payload:     `{"jsonrpc":"2.0","method":"tools/list"}`,
			},
			want:    []string{"**github**→`tools/list`"},
			notWant: []string{"```json"}, // Should NOT show JSON block when empty
		},
		{
			name: "tools/call with tool name",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionOutbound,
				MessageType: RPCMessageRequest,
				ServerID:    "github",
				Method:      "tools/call",
				PayloadSize: 100,
				Payload:     `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"search_code","arguments":{"query":"test"}}}`,
			},
			want:    []string{"**github**→`tools/call` `search_code`", "```json", `"arguments"`},
			notWant: []string{`"jsonrpc"`, `"method"`},
		},
		{
			name: "tools/call without tool name in params",
			info: &RPCMessageInfo{
				Direction:   RPCDirectionOutbound,
				MessageType: RPCMessageRequest,
				ServerID:    "github",
				Method:      "tools/call",
				PayloadSize: 50,
				Payload:     `{"jsonrpc":"2.0","method":"tools/call","params":{}}`,
			},
			want:    []string{"**github**→`tools/call`", "```json", `"params"`},
			notWant: []string{`"jsonrpc"`, `"method"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatRPCMessageMarkdown(tt.info)

			for _, expected := range tt.want {
				assert.Contains(t, result, expected)
			}

			for _, notExpected := range tt.notWant {
				assert.NotContains(t, result, notExpected)
			}
		})
	}
}

func TestFormatJSONWithoutFields(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		fieldsToRemove []string
		wantContains   []string
		wantNotContain []string
		wantValid      bool
		wantEmpty      bool
	}{
		{
			name:           "remove jsonrpc and method",
			input:          `{"jsonrpc":"2.0","method":"tools/call","params":{"arg":"value"},"id":1}`,
			fieldsToRemove: []string{"jsonrpc", "method"},
			wantContains:   []string{`"params"`, `"arg"`, `"value"`, `"id"`},
			wantNotContain: []string{`"jsonrpc"`, `"method"`},
			wantValid:      true,
			wantEmpty:      false,
		},
		{
			name:           "compact single line format",
			input:          `{"a":"b","c":{"d":"e"}}`,
			fieldsToRemove: []string{},
			wantContains:   []string{`"a":"b"`, `"c":`, `"d":"e"`},
			wantNotContain: []string{"\n", "  "},
			wantValid:      true,
			wantEmpty:      false,
		},
		{
			name:           "invalid JSON returns as-is with false",
			input:          `{invalid json}`,
			fieldsToRemove: []string{"jsonrpc"},
			wantContains:   []string{`{invalid json}`},
			wantNotContain: []string{},
			wantValid:      false,
			wantEmpty:      false,
		},
		{
			name:           "empty object",
			input:          `{}`,
			fieldsToRemove: []string{"jsonrpc"},
			wantContains:   []string{`{}`},
			wantNotContain: []string{},
			wantValid:      true,
			wantEmpty:      true,
		},
		{
			name:           "only params null after removal",
			input:          `{"jsonrpc":"2.0","method":"tools/list","params":null}`,
			fieldsToRemove: []string{"jsonrpc", "method"},
			wantContains:   []string{`"params"`, `null`},
			wantNotContain: []string{},
			wantValid:      true,
			wantEmpty:      true,
		},
		{
			name:           "params with value is not empty",
			input:          `{"jsonrpc":"2.0","method":"tools/list","params":{"key":"value"}}`,
			fieldsToRemove: []string{"jsonrpc", "method"},
			wantContains:   []string{`"params"`},
			wantNotContain: []string{},
			wantValid:      true,
			wantEmpty:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, isValid, isEmpty := formatJSONWithoutFields(tt.input, tt.fieldsToRemove)

			assert.Equal(t, tt.wantValid, isValid)
			assert.Equal(t, tt.wantEmpty, isEmpty)

			for _, want := range tt.wantContains {
				assert.Contains(t, result, want)
			}

			for _, notWant := range tt.wantNotContain {
				assert.NotContains(t, result, notWant)
			}
		})
	}
}

// setupRPCLoggers initializes file and markdown loggers in a temporary directory.
// Returns the log directory path. Loggers are closed automatically via t.Cleanup.
func setupRPCLoggers(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	logDir := filepath.Join(tmpDir, "logs")
	require.NoError(t, InitFileLogger(logDir, "test.log"), "InitFileLogger failed")
	t.Cleanup(CloseGlobalLogger)
	require.NoError(t, InitMarkdownLogger(logDir, "test.md"), "InitMarkdownLogger failed")
	t.Cleanup(CloseMarkdownLogger)
	return logDir
}

func TestLogRPCRequest(t *testing.T) {
	logDir := setupRPCLoggers(t)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	LogRPCRequest(RPCDirectionOutbound, "github", "tools/list", payload)

	// Close loggers to flush before reading
	CloseGlobalLogger()
	CloseMarkdownLogger()

	textContent, err := os.ReadFile(filepath.Join(logDir, "test.log"))
	require.NoError(t, err, "Failed to read text log")
	textStr := string(textContent)
	assert.Contains(t, textStr, "github→tools/list")
	assert.Contains(t, textStr, "58b")

	mdContent, err := os.ReadFile(filepath.Join(logDir, "test.md"))
	require.NoError(t, err, "Failed to read markdown log")
	assert.Contains(t, string(mdContent), "**github**→`tools/list`")
}

func TestLogRPCResponse(t *testing.T) {
	logDir := setupRPCLoggers(t)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Invalid request"}}`)
	rpcErr := errors.New("backend connection failed")
	LogRPCResponse(RPCDirectionInbound, "github", payload, rpcErr)

	// Close loggers to flush before reading
	CloseGlobalLogger()
	CloseMarkdownLogger()

	textContent, err := os.ReadFile(filepath.Join(logDir, "test.log"))
	require.NoError(t, err, "Failed to read text log")
	textStr := string(textContent)
	assert.Contains(t, textStr, "github←resp")
	assert.Contains(t, textStr, "err:backend connection failed")

	mdContent, err := os.ReadFile(filepath.Join(logDir, "test.md"))
	require.NoError(t, err, "Failed to read markdown log")
	mdStr := string(mdContent)
	assert.Contains(t, mdStr, "**github**←`resp`")
	assert.Contains(t, mdStr, "⚠️`backend connection failed`")
}

func TestLogRPCResponse_NilError(t *testing.T) {
	logDir := setupRPCLoggers(t)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	LogRPCResponse(RPCDirectionInbound, "github", payload, nil)

	// Close loggers to flush before reading
	CloseGlobalLogger()
	CloseMarkdownLogger()

	textContent, err := os.ReadFile(filepath.Join(logDir, "test.log"))
	require.NoError(t, err)
	textStr := string(textContent)
	assert.Contains(t, textStr, "github←resp")
	assert.NotContains(t, textStr, "err:")

	mdContent, err := os.ReadFile(filepath.Join(logDir, "test.md"))
	require.NoError(t, err)
	mdStr := string(mdContent)
	assert.Contains(t, mdStr, "**github**←`resp`")
	assert.NotContains(t, mdStr, "⚠️")
}

func TestLogRPCRequestWithSecrets(t *testing.T) {
	logDir := setupRPCLoggers(t)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"authenticate","params":{"token":"ghp_1234567890123456789012345678901234567890"}}`)
	LogRPCRequest(RPCDirectionInbound, "client", "authenticate", payload)

	// Close loggers to flush before reading
	CloseGlobalLogger()
	CloseMarkdownLogger()

	textContent, err := os.ReadFile(filepath.Join(logDir, "test.log"))
	require.NoError(t, err)
	textStr := string(textContent)
	assert.NotContains(t, textStr, "ghp_1234567890123456789012345678901234567890", "Text log should not contain secret")
	assert.Contains(t, textStr, "[REDACTED]", "Text log should contain redaction marker")

	mdContent, err := os.ReadFile(filepath.Join(logDir, "test.md"))
	require.NoError(t, err)
	mdStr := string(mdContent)
	assert.NotContains(t, mdStr, "ghp_1234567890123456789012345678901234567890", "Markdown log should not contain secret")
	assert.Contains(t, mdStr, "[REDACTED]", "Markdown log should contain redaction marker")
}

func TestLogRPCRequestPayloadTruncation(t *testing.T) {
	logDir := setupRPCLoggers(t)

	// Create a large payload (> 10KB for text, > 512 chars for markdown)
	largeData := strings.Repeat("x", 12*1024) // 12KB of x's
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"test","params":{"data":"` + largeData + `"}}`)
	LogRPCRequest(RPCDirectionOutbound, "backend", "test", payload)

	// Close loggers to flush before reading
	CloseGlobalLogger()
	CloseMarkdownLogger()

	// Check text log - payload should be truncated at 10KB
	textContent, err := os.ReadFile(filepath.Join(logDir, "test.log"))
	require.NoError(t, err)
	textStr := string(textContent)
	assert.Contains(t, textStr, "...", "Text log should show truncation marker")
	assert.Equal(t, 0, strings.Count(textStr, strings.Repeat("x", 11*1024)), "Text log should not contain more than 10KB of data")

	// Check markdown log - should be truncated at 512 chars
	mdContent, err := os.ReadFile(filepath.Join(logDir, "test.md"))
	require.NoError(t, err)
	mdStr := string(mdContent)
	assert.Contains(t, mdStr, "...", "Markdown log should show truncation marker")
	assert.Equal(t, 0, strings.Count(mdStr, strings.Repeat("x", 600)), "Markdown log should be truncated at 512 chars")
}

func TestLogRPCMessage(t *testing.T) {
	logDir := setupRPCLoggers(t)

	info := &RPCMessageInfo{
		Direction:   RPCDirectionOutbound,
		MessageType: RPCMessageRequest,
		ServerID:    "github",
		Method:      "tools/call",
		PayloadSize: 42,
		Payload:     `{"tool":"search_code"}`,
	}
	LogRPCMessage(info)

	// Close loggers to flush before reading
	CloseGlobalLogger()
	CloseMarkdownLogger()

	textContent, err := os.ReadFile(filepath.Join(logDir, "test.log"))
	require.NoError(t, err)
	textStr := string(textContent)
	assert.Contains(t, textStr, "github→tools/call")
	assert.Contains(t, textStr, "42b")

	mdContent, err := os.ReadFile(filepath.Join(logDir, "test.md"))
	require.NoError(t, err)
	assert.Contains(t, string(mdContent), "**github**→`tools/call`")
}
