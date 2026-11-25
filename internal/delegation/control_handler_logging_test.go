package delegation

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/github/gh-aw-mcpg/internal/logger"
)

func captureControlHandlerLogs(t *testing.T, f func()) string {
	t.Helper()
	t.Setenv("DEBUG", "*")
	t.Setenv("DEBUG_COLORS", "0")

	logDir := t.TempDir()
	require.NoError(t, logger.InitFileLogger(logDir, "mcp-gateway.log"))
	t.Cleanup(func() { _ = logger.CloseAllLoggers() })

	previous := logControlHandler
	logControlHandler = logger.New("delegation:control_handler")
	require.True(t, logControlHandler.Enabled(), "the capture harness must actually enable debug logging")
	t.Cleanup(func() { logControlHandler = previous })

	original := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w

	var (
		wg  sync.WaitGroup
		buf bytes.Buffer
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&buf, r)
	}()

	func() {
		defer func() {
			os.Stderr = original
			_ = w.Close()
		}()
		f()
	}()
	wg.Wait()
	_ = r.Close()

	require.NoError(t, logger.CloseAllLoggers())
	content, err := os.ReadFile(filepath.Join(logDir, "mcp-gateway.log"))
	require.NoError(t, err)
	return buf.String() + string(content)
}

func TestHandleControl_CreateOrConfirmSuccessLogging(t *testing.T) {
	wire := CreateOrConfirmRequestWire{
		RequestCore: RequestCore{
			RunID:          "run-123",
			EnclaveBackend: "awf-enclave",
			EnclaveEntryID: "entry-1",
			InvocationID:   "inv-1",
			Repository:     "github/gh-aw",
			ToolPolicy:     ToolPolicyGitHubRepositoryReadV1,
			SchemaHash:     "sha256:abc",
		},
		RequestedTTLSeconds: 60,
		IdempotencyKey:      "idem-1",
	}

	t.Run("logs success with invocation hash after persistence succeeds", func(t *testing.T) {
		deps, secret := newControlTestDeps(t)
		var responseStatus int
		var result IdentityResult
		logs := captureControlHandlerLogs(t, func() {
			response := doControlRequest(t, deps, secret, ControlPathPrefix+"create-or-confirm", http.MethodPost, wire)
			responseStatus = response.Code
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
		})

		require.Equal(t, http.StatusOK, responseStatus)
		assert.Contains(t, logs, "create-or-confirm succeeded:")
		assert.Contains(t, logs, "run_hash="+string(HashForAudit(wire.RunID)))
		assert.Contains(t, logs, "enclave_entry_id_hash="+string(HashForAudit(wire.EnclaveEntryID)))
		assert.Contains(t, logs, "invocation_id_hash="+string(HashForAudit(wire.InvocationID)))
		require.NotEmpty(t, result.Handle)
		assert.Contains(t, logs, "handle_hash="+string(HashForAudit(result.Handle)))
		assert.NotContains(t, logs, result.Handle)
		assert.NotContains(t, logs, wire.RunID)
	})

	t.Run("does not log success when persistence fails", func(t *testing.T) {
		deps, secret := newControlTestDeps(t)
		badDir := filepath.Join(t.TempDir(), "not-a-file")
		require.NoError(t, os.MkdirAll(badDir, 0o755))
		deps.StatePath = badDir

		var responseStatus int
		logs := captureControlHandlerLogs(t, func() {
			responseStatus = doControlRequest(t, deps, secret, ControlPathPrefix+"create-or-confirm", http.MethodPost, wire).Code
		})

		require.Equal(t, http.StatusInternalServerError, responseStatus)
		assert.NotContains(t, logs, "create-or-confirm succeeded:")
	})
}
