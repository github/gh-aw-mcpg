package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/delegation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedServer_ControlHandler(t *testing.T) {
	controlPath := delegation.ControlPathPrefix + "no-such-operation"

	t.Run("delegation disabled hides control endpoint", func(t *testing.T) {
		us := &UnifiedServer{}
		h := us.ControlHandler()
		require.NotNil(t, h)

		for _, path := range []string{controlPath, "/mcp"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
			assert.Equal(t, http.StatusNotFound, w.Code, path)
		}
	})

	t.Run("delegation enabled rejects unrelated path", func(t *testing.T) {
		cfg, _ := newUnifiedDelegationConfig(t)
		h := (&UnifiedServer{delegation: cfg}).ControlHandler()

		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", nil))
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("delegation enabled denies missing capability", func(t *testing.T) {
		cfg, _ := newUnifiedDelegationConfig(t)
		h := (&UnifiedServer{delegation: cfg}).ControlHandler()

		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, controlPath, nil))
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("delegation enabled denies non-POST", func(t *testing.T) {
		cfg, _ := newUnifiedDelegationConfig(t)
		h := (&UnifiedServer{delegation: cfg}).ControlHandler()

		req := httptest.NewRequest(http.MethodGet, controlPath, nil)
		req.Header.Set("Authorization", "control-capability-key-32-bytes!!")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("delegation enabled authenticated unknown operation is 404", func(t *testing.T) {
		cfg, _ := newUnifiedDelegationConfig(t)
		h := (&UnifiedServer{delegation: cfg}).ControlHandler()

		req := httptest.NewRequest(http.MethodPost, controlPath, nil)
		req.Header.Set("Authorization", "control-capability-key-32-bytes!!")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestUnifiedServer_LifecycleAccessors(t *testing.T) {
	assert := assert.New(t)
	us := &UnifiedServer{
		payloadSizeThreshold: 1234,
		enableDIFC:           true,
		tools:                map[string]*ToolInfo{},
	}
	assert.Equal(1234, us.GetPayloadSizeThreshold())
	assert.True(us.IsDIFCEnabled())
	assert.False((&UnifiedServer{}).IsDIFCEnabled())

	tool := &ToolInfo{Name: "test-tool"}
	us.RegisterTestTool("srv___test-tool", tool)
	assert.Same(tool, us.tools["srv___test-tool"])
}
