package proxy

import (
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/difc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyOpaqueResponseAccess(t *testing.T) {
	formats := []struct {
		name        string
		path        string
		contentType string
		body        string
		status      int
		location    string
	}{
		{"job logs", "/repos/org/repo/actions/jobs/1/logs", "text/plain; charset=utf-8", "job output\n", http.StatusOK, ""},
		{"run logs", "/repos/org/repo/actions/runs/1/logs", "application/zip", "PK\x03\x04\x00\xff", http.StatusOK, ""},
		{"raw file", "/repos/org/repo/contents/file.txt", "application/vnd.github.raw", "file contents\n", http.StatusOK, ""},
		{"JSON-looking text", "/repos/org/repo/contents/file.txt", "text/plain", " { \"value\": 1 }\n", http.StatusOK, ""},
		{"diff", "/repos/org/repo/pulls/1", "application/vnd.github.diff", "diff --git a/file b/file\n", http.StatusOK, ""},
		{"binary", "/repos/org/repo/contents/file.bin", "application/octet-stream", "\x00\xff\x01", http.StatusOK, ""},
		{"no content", "/repos/org/repo/contents/file.txt", "application/json", "", http.StatusNoContent, ""},
		{"reset content", "/repos/org/repo/contents/file.txt", "application/json", "", http.StatusResetContent, ""},
		{"log redirect", "/repos/org/repo/actions/jobs/1/logs", "text/plain", "download", http.StatusFound, "https://downloads.example.test/logs"},
		{"artifact", "/repos/org/repo/actions/artifacts/1/zip", "application/zip", "PK\x03\x04\x00\xff", http.StatusOK, ""},
		{"artifact redirect", "/repos/org/repo/actions/artifacts/1/zip", "text/plain", "download", http.StatusFound, "https://downloads.example.test/artifact"},
		{"artifact error", "/repos/org/repo/actions/artifacts/1/zip", "application/json", `{"message":"artifact error marker"}`, http.StatusNotFound, ""},
		{"client error", "/repos/org/repo/issues", "application/json", `{"message":"client error marker"}`, http.StatusNotFound, ""},
		{"server error", "/repos/org/repo/issues", "application/json", `{"message":"server error marker"}`, http.StatusInternalServerError, ""},
	}
	accessCases := []struct {
		name    string
		mode    difc.EnforcementMode
		cleared bool
		allowed bool
	}{
		{"filter denied", difc.EnforcementFilter, false, false},
		{"strict denied", difc.EnforcementStrict, false, false},
		{"filter authorized", difc.EnforcementFilter, true, true},
		{"strict authorized", difc.EnforcementStrict, true, true},
		{"propagate", difc.EnforcementPropagate, false, true},
	}
	for _, format := range formats {
		t.Run(format.name, func(t *testing.T) {
			for _, access := range accessCases {
				t.Run(access.name, func(t *testing.T) {
					upstreamCalls := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						upstreamCalls++
						w.Header().Set("Content-Type", format.contentType)
						w.Header().Set("Content-Disposition", `attachment; filename="response"`)
						w.Header().Set("Location", format.location)
						w.WriteHeader(format.status)
						_, err := w.Write([]byte(format.body))
						assert.NoError(t, err)
					}))
					defer upstream.Close()

					g := &stubGuard{labelResourceResult: privateResource(), labelResourceOp: difc.OperationRead}
					s := newTestServerWithStub(t, upstream.URL, g, access.mode)
					if access.cleared {
						s = newTestServerWithPrivateAgent(t, upstream.URL, g, access.mode)
					}
					s.httpClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
						return http.ErrUseLastResponse
					}
					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, format.path, nil))

					if !access.allowed {
						assert.Equal(t, http.StatusForbidden, w.Code)
						assert.Empty(t, w.Header().Get("Location"))
						assert.Empty(t, w.Header().Get("Content-Disposition"))
						if format.body != "" {
							assert.NotContains(t, w.Body.String(), format.body)
						}
						if format.name == "artifact" || format.name == "artifact redirect" || format.name == "artifact error" {
							assert.Zero(t, upstreamCalls)
						}
						assert.Empty(t, s.AgentRegistry.GetOrCreate(proxyAgentID).GetSecrecyTags())
						return
					}
					assert.Equal(t, format.status, w.Code)
					assert.Equal(t, format.body, w.Body.String())
					assert.Equal(t, format.contentType, w.Header().Get("Content-Type"))
					assert.Equal(t, format.location, w.Header().Get("Location"))
					assert.Equal(t, `attachment; filename="response"`, w.Header().Get("Content-Disposition"))
					assert.Equal(t, 1, upstreamCalls)
					assert.Contains(t, s.AgentRegistry.GetOrCreate(proxyAgentID).GetSecrecyTags(), difc.Tag("private:test-org/test-repo"))
				})
			}
		})
	}
}

func TestProxyStructuredResponseFormats(t *testing.T) {
	for _, contentType := range []string{
		"application/json",
		"Application/JSON; charset=utf-8",
		"application/vnd.github+json",
		"application/json; profile=\"https://example.test/schema\"",
		"",
	} {
		for _, compressed := range []bool{false, true} {
			name := contentType
			if compressed {
				name += "/gzip"
			}
			t.Run(name, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header()["Content-Type"] = []string{contentType}
					if compressed {
						w.Header().Set("Content-Encoding", "gzip")
						zw := gzip.NewWriter(w)
						_, err := zw.Write([]byte(`[{"id":1},{"id":2}]`))
						assert.NoError(t, err)
						assert.NoError(t, zw.Close())
					} else {
						_, err := w.Write([]byte(`[{"id":1},{"id":2}]`))
						assert.NoError(t, err)
					}
				}))
				defer upstream.Close()

				g := &stubGuard{
					labelResourceResult: privateResource(),
					labelResourceOp:     difc.OperationRead,
					labelResponseData: &difc.CollectionLabeledData{Items: []difc.LabeledItem{
						{Data: map[string]interface{}{"id": 1}, Labels: publicResource()},
						{Data: map[string]interface{}{"id": 2}, Labels: privateResource()},
					}},
				}
				s := newTestServerWithStub(t, upstream.URL, g, difc.EnforcementFilter)
				h := &proxyHandler{server: s}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues", nil))

				assert.Equal(t, http.StatusOK, w.Code)
				assert.JSONEq(t, `[{"id":1}]`, w.Body.String())
				assert.Empty(t, w.Header().Get("Content-Encoding"))
			})
		}
	}
}

func TestProxyInvalidStructuredResponse(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		encoding    string
		body        string
	}{
		{"malformed JSON", "application/json", "", `{"value":`},
		{"empty JSON", "application/json", "", ""},
		{"malformed vendor JSON", "application/vnd.github+json; charset=utf-8", "", `{"value":`},
		{"invalid media type", "application/json; charset", "", `{"value":`},
		{"unsupported encoding", "application/json", "br", "encoded response"},
		{"encoded JSON bytes", "application/json", "br", `{"value":1}`},
		{"truncated gzip", "application/json", "gzip", "\x1f\x8b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.contentType)
				w.Header().Set("Content-Encoding", tc.encoding)
				_, err := w.Write([]byte(tc.body))
				assert.NoError(t, err)
			}))
			defer upstream.Close()

			for _, cleared := range []bool{false, true} {
				g := &stubGuard{labelResourceResult: privateResource(), labelResourceOp: difc.OperationRead}
				s := newTestServerWithStub(t, upstream.URL, g, difc.EnforcementFilter)
				if cleared {
					s = newTestServerWithPrivateAgent(t, upstream.URL, g, difc.EnforcementFilter)
				}
				w := httptest.NewRecorder()
				h := &proxyHandler{server: s}
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues", nil))

				require.Equal(t, http.StatusBadGateway, w.Code)
				if tc.body != "" {
					assert.NotContains(t, w.Body.String(), tc.body)
				}
			}
		})
	}
}

func TestProxyResponseLabelingFallbackAccumulatesLabels(t *testing.T) {
	upstream := mockUpstream(t, http.StatusOK, map[string]interface{}{"id": 1})
	defer upstream.Close()

	g := &stubGuard{
		labelResourceResult: privateResource(),
		labelResourceOp:     difc.OperationRead,
		labelResponseErr:    errors.New("response labeling unavailable"),
	}
	s := newTestServerWithStub(t, upstream.URL, g, difc.EnforcementPropagate)
	w := httptest.NewRecorder()
	h := &proxyHandler{server: s}
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/repos/org/repo/issues/1", nil))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"id":1}`, w.Body.String())
	assert.Contains(t, s.AgentRegistry.GetOrCreate(proxyAgentID).GetSecrecyTags(), difc.Tag("private:test-org/test-repo"))
}
