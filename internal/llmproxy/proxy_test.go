package llmproxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startUpstream starts a fake upstream that echoes back the request body as
// the response body and records the last received request.
func startUpstream(t *testing.T) (server *httptest.Server, getLastReq func() *http.Request, getLastBody func() []byte) {
	t.Helper()
	var lastReq *http.Request
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastReq = r
		b, _ := io.ReadAll(r.Body)
		lastBody = b
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv, func() *http.Request { return lastReq }, func() []byte { return lastBody }
}

func makeRequest(t *testing.T, proxyURL, method, path string, body interface{}) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		bodyReader = strings.NewReader(string(b))
	}
	req, err := http.NewRequest(method, proxyURL+path, bodyReader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func decodeBody(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var out map[string]interface{}
	require.NoError(t, json.Unmarshal(b, &out))
	return out
}

func TestProxy_Passthrough_NonMessages(t *testing.T) {
	upstream, _, getLastBody := startUpstream(t)
	srv := New(Config{Upstream: upstream.URL, AutoCache: true, StripANSI: true})
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)

	resp := makeRequest(t, proxy.URL, http.MethodGet, "/v1/models", nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()
	// GET is passthrough — upstream should see an empty body
	assert.Empty(t, getLastBody())
}

func TestProxy_AutoCache_InjectsBreakpoints(t *testing.T) {
	upstream, getLastReq, getLastBody := startUpstream(t)
	srv := New(Config{
		Upstream:  upstream.URL,
		AutoCache: true,
		TailTTL:   "5m",
		StripANSI: true,
	})
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)

	reqBody := map[string]interface{}{
		"model": "claude-opus-4-5",
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash", "description": "run bash"},
		},
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{"type": "text", "text": "do something"},
				},
			},
		},
	}
	resp := makeRequest(t, proxy.URL, http.MethodPost, "/v1/messages", reqBody)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Beta header must be present in the forwarded request.
	require.NotNil(t, getLastReq())
	assert.Contains(t, getLastReq().Header.Get("Anthropic-Beta"), betaFlag)

	// Forwarded body must have cache_control on the last tool.
	var forwarded map[string]interface{}
	require.NoError(t, json.Unmarshal(getLastBody(), &forwarded))
	tools := forwarded["tools"].([]interface{})
	lastTool := tools[len(tools)-1].(map[string]interface{})
	cc := lastTool["cache_control"].(map[string]interface{})
	assert.Equal(t, "ephemeral", cc["type"])
	assert.Equal(t, "1h", cc["ttl"])
}

func TestProxy_StripANSI_CleansToolResults(t *testing.T) {
	upstream, _, getLastBody := startUpstream(t)
	srv := New(Config{Upstream: upstream.URL, StripANSI: true})
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)

	reqBody := map[string]interface{}{
		"model": "claude-opus-4-5",
		"messages": []interface{}{
			map[string]interface{}{
				"role": "user",
				"content": []interface{}{
					map[string]interface{}{
						"type": "tool_result",
						"content": []interface{}{
							map[string]interface{}{
								"type": "text",
								"text": "\x1b[32mok\x1b[0m output",
							},
						},
					},
				},
			},
		},
	}
	resp := makeRequest(t, proxy.URL, http.MethodPost, "/v1/messages", reqBody)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	var forwarded map[string]interface{}
	require.NoError(t, json.Unmarshal(getLastBody(), &forwarded))
	msgs := forwarded["messages"].([]interface{})
	blocks := msgs[0].(map[string]interface{})["content"].([]interface{})
	inner := blocks[0].(map[string]interface{})["content"].([]interface{})
	assert.Equal(t, "ok output", inner[0].(map[string]interface{})["text"])
}

func TestProxy_DropTools_RemovesTool(t *testing.T) {
	upstream, _, getLastBody := startUpstream(t)
	srv := New(Config{
		Upstream:  upstream.URL,
		DropTools: map[string]bool{"NotebookEdit": true},
	})
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)

	reqBody := map[string]interface{}{
		"model": "claude-opus-4-5",
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash"},
			map[string]interface{}{"name": "NotebookEdit"},
		},
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hello"},
		},
	}
	resp := makeRequest(t, proxy.URL, http.MethodPost, "/v1/messages", reqBody)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	var forwarded map[string]interface{}
	require.NoError(t, json.Unmarshal(getLastBody(), &forwarded))
	tools := forwarded["tools"].([]interface{})
	require.Len(t, tools, 1)
	assert.Equal(t, "Bash", tools[0].(map[string]interface{})["name"])
}

func TestProxy_TrimBashGit_TrimsDescription(t *testing.T) {
	upstream, _, getLastBody := startUpstream(t)
	srv := New(Config{Upstream: upstream.URL, TrimBashGit: true})
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)

	desc := "Run shell commands.\n\n# Committing changes with git\nUse git commit..."
	reqBody := map[string]interface{}{
		"model": "claude-opus-4-5",
		"tools": []interface{}{
			map[string]interface{}{"name": "Bash", "description": desc},
		},
		"messages": []interface{}{
			map[string]interface{}{"role": "user", "content": "hi"},
		},
	}
	resp := makeRequest(t, proxy.URL, http.MethodPost, "/v1/messages", reqBody)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	var forwarded map[string]interface{}
	require.NoError(t, json.Unmarshal(getLastBody(), &forwarded))
	tools := forwarded["tools"].([]interface{})
	d := tools[0].(map[string]interface{})["description"].(string)
	assert.NotContains(t, d, "Committing changes")
	assert.Contains(t, d, "Run shell commands.")
}

func TestProxy_NonJSONBody_PassedThrough(t *testing.T) {
	upstream, _, getLastBody := startUpstream(t)
	srv := New(Config{Upstream: upstream.URL, AutoCache: true})
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)

	req, _ := http.NewRequest(http.MethodPost, proxy.URL+"/v1/messages", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// Body was not parseable; should forward as-is.
	assert.Equal(t, "not json", string(getLastBody()))
}

func TestProxy_CountTokensPath_AlsoMutated(t *testing.T) {
	upstream, getLastReq, _ := startUpstream(t)
	srv := New(Config{Upstream: upstream.URL, AutoCache: true})
	proxy := httptest.NewServer(srv.Handler())
	t.Cleanup(proxy.Close)

	reqBody := map[string]interface{}{
		"model":    "claude-opus-4-5",
		"tools":    []interface{}{map[string]interface{}{"name": "Bash", "description": "desc"}},
		"messages": []interface{}{map[string]interface{}{"role": "user", "content": "hi"}},
	}
	resp := makeRequest(t, proxy.URL, http.MethodPost, "/v1/messages/count_tokens", reqBody)
	resp.Body.Close()
	require.NotNil(t, getLastReq())
	assert.Contains(t, getLastReq().Header.Get("Anthropic-Beta"), betaFlag)
}

func TestProxy_UpstreamHost_IsSetCorrectly(t *testing.T) {
	assert.Equal(t, "api.anthropic.com", upstreamHost("https://api.anthropic.com"))
	assert.Equal(t, "localhost:8787", upstreamHost("http://localhost:8787"))
}
