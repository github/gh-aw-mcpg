package logger

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/sanitize"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// privateSentinel stands in for private repository content that an enclave-scoped
// MCP response may carry. It must never reach an exported log sink.
const privateSentinel = "SENTINEL-PRIVATE-ISSUE-BODY"

func initAllRPCSinks(t *testing.T) string {
	t.Helper()
	logDir := filepath.Join(t.TempDir(), "logs")
	require.NoError(t, InitFileLogger(logDir, "test.log"))
	require.NoError(t, InitMarkdownLogger(logDir, "test.md"))
	require.NoError(t, InitJSONLLogger(logDir, "test.jsonl"))
	t.Cleanup(func() { CloseAllLoggers() })
	return logDir
}

func readSink(t *testing.T, logDir, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(logDir, name))
	require.NoError(t, err)
	return string(content)
}

func TestLogRPCForSession_EnclaveSessionRedactsEverySink(t *testing.T) {
	assert := assert.New(t)
	require := require.New(t)

	logDir := initAllRPCSinks(t)

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"issue_read","arguments":{"owner":"acme","repo":"secret","query":"` + privateSentinel + `"}}}`)
	response := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"` + privateSentinel + `"}]}}`)

	LogRPCRequestForSession(RPCDirectionOutbound, "github", "tools/call", request, []string{"private:acme/secret"}, []string{"approved"}, true)
	LogRPCResponseForSession(RPCDirectionInbound, "github", response, errors.New(privateSentinel), nil, nil, true)

	CloseAllLoggers()

	for _, sink := range []string{"test.log", "test.md", "test.jsonl"} {
		content := readSink(t, logDir, sink)
		assert.NotContains(content, privateSentinel, "%s must not contain enclave payload content", sink)
	}

	// Metadata stays diagnosable.
	textLog := readSink(t, logDir, "test.log")
	assert.Contains(textLog, "github→tools/call")
	assert.Contains(textLog, "tool:issue_read")
	assert.Contains(textLog, "[REDACTED enclave payload")

	markdownLog := readSink(t, logDir, "test.md")
	assert.Contains(markdownLog, "rpc **github**→`tools/call` `issue_read`")
	assert.Contains(markdownLog, "[REDACTED enclave payload")
	assert.Contains(markdownLog, "rpc **github**←`resp`")

	jsonl := readSink(t, logDir, "test.jsonl")
	lines := strings.Split(strings.TrimSpace(jsonl), "\n")
	require.Len(lines, 2)

	var requestEntry JSONLRPCMessage
	require.NoError(json.Unmarshal([]byte(lines[0]), &requestEntry))
	assert.Equal("rpc_request", requestEntry.Event)
	assert.Equal("rpc-message/v2", requestEntry.Schema)
	assert.Equal("OUT", requestEntry.Direction)
	assert.Equal("github", requestEntry.ServerID)
	assert.Equal("tools/call", requestEntry.Method)
	assert.Equal("issue_read", requestEntry.ToolName)
	assert.True(requestEntry.Redacted)
	assert.Equal(len(request), requestEntry.PayloadSize)
	assert.ElementsMatch([]string{"private:acme/secret"}, requestEntry.AgentSecrecy)
	assert.Equal([]string{"approved"}, requestEntry.AgentIntegrity)

	var payload map[string]any
	require.NoError(json.Unmarshal(requestEntry.Payload, &payload))
	assert.Equal(true, payload["redacted"])
	assert.Equal("enclave_payload_redacted", payload["reason"])
	assert.Contains(payload["digest"], "hmac:")

	var responseEntry JSONLRPCMessage
	require.NoError(json.Unmarshal([]byte(lines[1]), &responseEntry))
	assert.Equal("rpc_response", responseEntry.Event)
	assert.Equal("rpc-message/v2", responseEntry.Schema)
	assert.Equal("IN", responseEntry.Direction)
	assert.True(responseEntry.Redacted)
	assert.NotEmpty(responseEntry.Error, "error category should remain recorded")
	assert.NotContains(responseEntry.Error, privateSentinel)
}

func TestLogRPCForSession_NonEnclaveSessionKeepsPayloads(t *testing.T) {
	assert := assert.New(t)

	logDir := initAllRPCSinks(t)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"result":{"text":"` + privateSentinel + `"}}`)
	LogRPCResponseForSession(RPCDirectionInbound, "github", payload, nil, nil, nil, false)

	CloseAllLoggers()

	assert.Contains(readSink(t, logDir, "test.jsonl"), privateSentinel,
		"non-enclave logging behavior must remain unchanged")
}

func TestLogRPCForSession_ProcessWideRedactionCoversAllSessions(t *testing.T) {
	assert := assert.New(t)

	previous := sanitize.PayloadRedactionEnabled()
	sanitize.SetPayloadRedaction(true)
	t.Cleanup(func() { sanitize.SetPayloadRedaction(previous) })

	logDir := initAllRPCSinks(t)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"result":{"text":"` + privateSentinel + `"}}`)
	LogRPCResponse(RPCDirectionInbound, "github", payload, nil, nil, nil)

	CloseAllLoggers()

	assert.NotContains(readSink(t, logDir, "test.jsonl"), privateSentinel)
}

func TestLogRPCForSession_RawPayloadOptInRestoresPayloads(t *testing.T) {
	assert := assert.New(t)

	sanitize.SetRawPayloadLogsAllowed(true)
	t.Cleanup(func() { sanitize.SetRawPayloadLogsAllowed(false) })

	logDir := initAllRPCSinks(t)

	payload := []byte(`{"jsonrpc":"2.0","id":1,"result":{"text":"` + privateSentinel + `"}}`)
	LogRPCResponseForSession(RPCDirectionInbound, "github", payload, nil, nil, nil, true)

	CloseAllLoggers()

	assert.Contains(readSink(t, logDir, "test.jsonl"), privateSentinel,
		"privileged opt-in should restore raw payload logging")
}

func TestFilteredItemLogEntry_RedactForEnclave(t *testing.T) {
	assert := assert.New(t)

	entry := FilteredItemLogEntry{
		ServerID:      "github",
		ToolName:      "list_issues",
		Description:   "issue:acme/secret#42 " + privateSentinel,
		Reason:        "resource secrecy repo:acme/secret not permitted",
		SecrecyTags:   []string{"private:acme/secret"},
		IntegrityTags: []string{"approved"},
		AuthorLogin:   "private-user",
		HTMLURL:       "https://github.com/acme/secret/issues/42",
		Number:        "42",
		SHA:           "deadbeef",
	}

	redacted := entry.RedactForEnclave()

	serialized, err := json.Marshal(redacted)
	require.NoError(t, err)
	rendered := string(serialized)

	assert.NotContains(rendered, privateSentinel)
	assert.NotContains(rendered, "acme/secret")
	assert.NotContains(rendered, "private-user")

	// The decision itself stays diagnosable.
	assert.Equal("github", redacted.ServerID)
	assert.Equal("list_issues", redacted.ToolName)
	assert.Len(redacted.SecrecyTags, 1)
	assert.Equal([]string{"approved"}, redacted.IntegrityTags)
}
