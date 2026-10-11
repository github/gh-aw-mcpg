package proxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
