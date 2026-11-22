package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteUpstreamAPIURL(t *testing.T) {
	const client = "https://localhost:18443/api/v3"

	tests := []struct {
		name     string
		value    string
		upstream string
		client   string
		want     string
	}{
		{"rewrites matching URL", "https://api.github.com/repos/o/r", "https://api.github.com", client, "https://localhost:18443/api/v3/repos/o/r"},
		{"upstream trailing slash trimmed", "https://api.github.com/repos/o/r", "https://api.github.com/", client, "https://localhost:18443/api/v3/repos/o/r"},
		{"exact upstream path match", "https://api.github.com", "https://api.github.com", client, "https://localhost:18443/api/v3"},
		{"preserves query string", "https://api.github.com/issues?page=2&per_page=5", "https://api.github.com", client, "https://localhost:18443/api/v3/issues?page=2&per_page=5"},
		{"preserves escaped path", "https://api.github.com/contents/a%2Fb", "https://api.github.com", client, "https://localhost:18443/api/v3/contents/a%2Fb"},
		{"case-insensitive scheme and host", "HTTPS://API.GITHUB.COM/x", "https://api.github.com", client, "https://localhost:18443/api/v3/x"},
		{"upstream with path prefix (GHES)", "https://ghe.example.com/api/v3/repos/o/r", "https://ghe.example.com/api/v3", client, "https://localhost:18443/api/v3/repos/o/r"},
		{"path prefix boundary not matched", "https://ghe.example.com/api/v3x/repos", "https://ghe.example.com/api/v3", client, "https://ghe.example.com/api/v3x/repos"},
		{"path outside upstream base unchanged", "https://ghe.example.com/other/repos", "https://ghe.example.com/api/v3", client, "https://ghe.example.com/other/repos"},
		{"different host unchanged", "https://objects.githubusercontent.com/a.zip", "https://api.github.com", client, "https://objects.githubusercontent.com/a.zip"},
		{"different scheme unchanged", "http://api.github.com/repos", "https://api.github.com", client, "http://api.github.com/repos"},
		{"different port unchanged", "https://api.github.com:8443/repos", "https://api.github.com", client, "https://api.github.com:8443/repos"},
		{"default port normalized", "https://api.github.com/repos/o/r", "https://api.github.com:443", client, "https://localhost:18443/api/v3/repos/o/r"},
		{"invalid upstream URL returns value", "https://api.github.com/repos", "http://[::1", client, "https://api.github.com/repos"},
		{"invalid client URL returns value", "https://api.github.com/repos", "https://api.github.com", "http://[::1", "https://api.github.com/repos"},
		{"invalid value URL returns value", "http://[::1", "https://api.github.com", client, "http://[::1"},
		{"empty value unchanged", "", "https://api.github.com", client, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, rewriteUpstreamAPIURL(tt.value, tt.upstream, tt.client))
		})
	}
}

func TestNormalizedURLPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "explicit port", raw: "https://example.com:8443", want: "8443"},
		{name: "HTTP default port", raw: "http://example.com", want: "80"},
		{name: "HTTPS default port", raw: "https://example.com", want: "443"},
		{name: "unknown scheme without port", raw: "ftp://example.com", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, err := url.Parse(tt.raw)
			require.NoError(t, err)
			assert.Equal(t, tt.want, normalizedURLPort(u))
		})
	}
}

func TestCopyResponseHeaders_ClientFollowsAPIRedirectReferences(t *testing.T) {
	for _, location := range []string{
		"https://api.github.com/repos/o/r/issues",
		"//api.github.com/repos/o/r/issues",
		"../r/issues",
	} {
		t.Run(location, func(t *testing.T) {
			var followedPath string
			upstreamRequestURL, err := url.Parse("https://api.github.com/repos/o/r/old")
			require.NoError(t, err)
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v3/start" {
					followedPath = r.URL.Path
					w.WriteHeader(http.StatusNoContent)
					return
				}
				resp := &http.Response{
					StatusCode: http.StatusMovedPermanently,
					Header:     http.Header{"Location": []string{location}},
					Request:    &http.Request{URL: upstreamRequestURL},
				}
				copyResponseHeaders(w, r, resp, "https://api.github.com:443")
				w.WriteHeader(resp.StatusCode)
			})
			proxy := httptest.NewServer(handler)
			defer proxy.Close()

			resp, err := http.Get(proxy.URL + "/api/v3/start")
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusNoContent, resp.StatusCode)
			assert.Equal(t, "/api/v3/repos/o/r/issues", followedPath)
		})
	}
}
