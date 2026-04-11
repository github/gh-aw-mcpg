package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/github/gh-aw-mcpg/internal/envutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGuardBackendCallerCollaboratorPermission(t *testing.T) {
	// Start a mock GitHub API server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/myorg/myrepo/collaborators/admin-user/permission", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Contains(t, r.Header.Get("Authorization"), "token ")
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"permission": "admin",
			"user": map[string]interface{}{
				"login": "admin-user",
				"id":    12345,
			},
		})
	})
	mux.HandleFunc("/repos/myorg/myrepo/collaborators/write-user/permission", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"permission": "write",
			"user": map[string]interface{}{
				"login": "write-user",
			},
		})
	})
	mux.HandleFunc("/repos/myorg/myrepo/collaborators/read-user/permission", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"permission": "read",
			"user": map[string]interface{}{
				"login": "read-user",
			},
		})
	})
	mux.HandleFunc("/repos/myorg/myrepo/collaborators/none-user/permission", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"permission": "none",
			"user": map[string]interface{}{
				"login": "none-user",
			},
		})
	})
	mux.HandleFunc("/repos/myorg/myrepo/collaborators/not-found-user/permission", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message": "Not Found"}`)
	})
	mux.HandleFunc("/repos/myorg/myrepo/collaborators/forbidden-user/permission", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message": "Resource not accessible by integration"}`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// Set env vars for the test
	t.Setenv("GITHUB_API_URL", server.URL)
	t.Setenv("GITHUB_TOKEN", "test-token-12345")

	caller := &guardBackendCaller{
		server:   &UnifiedServer{},
		serverID: "github",
		ctx:      context.Background(),
	}

	t.Run("admin permission", func(t *testing.T) {
		result, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"owner":    "myorg",
			"repo":     "myrepo",
			"username": "admin-user",
		})
		require.NoError(t, err)
		require.NotNil(t, result)

		resultMap, ok := result.(map[string]interface{})
		require.True(t, ok)

		content, ok := resultMap["content"].([]map[string]interface{})
		require.True(t, ok)
		require.Len(t, content, 1)

		text, ok := content[0]["text"].(string)
		require.True(t, ok)

		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(text), &parsed)
		require.NoError(t, err)
		assert.Equal(t, "admin", parsed["permission"])
		user, ok := parsed["user"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "admin-user", user["login"])
	})

	t.Run("write permission", func(t *testing.T) {
		result, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"owner":    "myorg",
			"repo":     "myrepo",
			"username": "write-user",
		})
		require.NoError(t, err)
		require.NotNil(t, result)

		text := extractMCPText(t, result)
		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(text), &parsed)
		require.NoError(t, err)
		assert.Equal(t, "write", parsed["permission"])
	})

	t.Run("read permission", func(t *testing.T) {
		result, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"owner":    "myorg",
			"repo":     "myrepo",
			"username": "read-user",
		})
		require.NoError(t, err)
		text := extractMCPText(t, result)
		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(text), &parsed)
		require.NoError(t, err)
		assert.Equal(t, "read", parsed["permission"])
	})

	t.Run("none permission", func(t *testing.T) {
		result, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"owner":    "myorg",
			"repo":     "myrepo",
			"username": "none-user",
		})
		require.NoError(t, err)
		text := extractMCPText(t, result)
		var parsed map[string]interface{}
		err = json.Unmarshal([]byte(text), &parsed)
		require.NoError(t, err)
		assert.Equal(t, "none", parsed["permission"])
	})

	t.Run("404 returns error", func(t *testing.T) {
		_, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"owner":    "myorg",
			"repo":     "myrepo",
			"username": "not-found-user",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "404")
	})

	t.Run("403 returns error", func(t *testing.T) {
		_, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"owner":    "myorg",
			"repo":     "myrepo",
			"username": "forbidden-user",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "403")
	})

	t.Run("missing owner returns error", func(t *testing.T) {
		_, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"repo":     "myrepo",
			"username": "admin-user",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing owner/repo/username")
	})

	t.Run("missing repo returns error", func(t *testing.T) {
		_, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"owner":    "myorg",
			"username": "admin-user",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing owner/repo/username")
	})

	t.Run("missing username returns error", func(t *testing.T) {
		_, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
			"owner": "myorg",
			"repo":  "myrepo",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing owner/repo/username")
	})

	t.Run("invalid args type returns error", func(t *testing.T) {
		_, err := caller.CallTool(context.Background(), "get_collaborator_permission", "not-a-map")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected args type")
	})
}

func TestGuardBackendCallerNoToken(t *testing.T) {
	// Unset all possible token env vars
	t.Setenv("GITHUB_MCP_SERVER_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GITHUB_PERSONAL_ACCESS_TOKEN", "")
	t.Setenv("GH_TOKEN", "")

	caller := &guardBackendCaller{
		server:   &UnifiedServer{},
		serverID: "github",
		ctx:      context.Background(),
	}

	_, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
		"owner":    "myorg",
		"repo":     "myrepo",
		"username": "user",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no GitHub token available")
}

func TestLookupGitHubToken(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected string
	}{
		{
			name: "prefers GITHUB_MCP_SERVER_TOKEN",
			envVars: map[string]string{
				"GITHUB_MCP_SERVER_TOKEN":      "mcp-token",
				"GITHUB_TOKEN":                 "gh-token",
				"GITHUB_PERSONAL_ACCESS_TOKEN": "pat-token",
				"GH_TOKEN":                     "gh-cli-token",
			},
			expected: "mcp-token",
		},
		{
			name: "falls back to GITHUB_TOKEN",
			envVars: map[string]string{
				"GITHUB_MCP_SERVER_TOKEN":      "",
				"GITHUB_TOKEN":                 "gh-token",
				"GITHUB_PERSONAL_ACCESS_TOKEN": "pat-token",
			},
			expected: "gh-token",
		},
		{
			name: "falls back to GITHUB_PERSONAL_ACCESS_TOKEN",
			envVars: map[string]string{
				"GITHUB_MCP_SERVER_TOKEN":      "",
				"GITHUB_TOKEN":                 "",
				"GITHUB_PERSONAL_ACCESS_TOKEN": "pat-token",
			},
			expected: "pat-token",
		},
		{
			name: "falls back to GH_TOKEN",
			envVars: map[string]string{
				"GITHUB_MCP_SERVER_TOKEN":      "",
				"GITHUB_TOKEN":                 "",
				"GITHUB_PERSONAL_ACCESS_TOKEN": "",
				"GH_TOKEN":                     "gh-cli-token",
			},
			expected: "gh-cli-token",
		},
		{
			name: "returns empty when no token set",
			envVars: map[string]string{
				"GITHUB_MCP_SERVER_TOKEN":      "",
				"GITHUB_TOKEN":                 "",
				"GITHUB_PERSONAL_ACCESS_TOKEN": "",
				"GH_TOKEN":                     "",
			},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.envVars {
				t.Setenv(k, v)
			}
			result := envutil.LookupGitHubToken()
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestLookupGitHubAPIBaseURL(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("GITHUB_API_URL", "")
		assert.Equal(t, "https://api.github.com", lookupGitHubAPIBaseURL())
	})

	t.Run("custom URL", func(t *testing.T) {
		t.Setenv("GITHUB_API_URL", "https://github.example.com/api/v3")
		assert.Equal(t, "https://github.example.com/api/v3", lookupGitHubAPIBaseURL())
	})

	t.Run("strips trailing slash", func(t *testing.T) {
		t.Setenv("GITHUB_API_URL", "https://github.example.com/api/v3/")
		assert.Equal(t, "https://github.example.com/api/v3", lookupGitHubAPIBaseURL())
	})
}

func TestCollaboratorPermissionAuthHeader(t *testing.T) {
	var capturedAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"permission": "admin",
			"user":       map[string]interface{}{"login": "user"},
		})
	}))
	defer server.Close()

	t.Setenv("GITHUB_API_URL", server.URL)
	t.Setenv("GITHUB_TOKEN", "my-secret-token")

	caller := &guardBackendCaller{
		server:   &UnifiedServer{},
		serverID: "github",
		ctx:      context.Background(),
	}

	_, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
		"owner":    "org",
		"repo":     "repo",
		"username": "user",
	})
	require.NoError(t, err)
	assert.Equal(t, "token my-secret-token", capturedAuth)
}

func TestCollaboratorPermissionMCPResponseFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"permission": "maintain",
			"user":       map[string]interface{}{"login": "maintainer"},
		})
	}))
	defer server.Close()

	t.Setenv("GITHUB_API_URL", server.URL)
	t.Setenv("GITHUB_TOKEN", "test-token")

	caller := &guardBackendCaller{
		server:   &UnifiedServer{},
		serverID: "github",
		ctx:      context.Background(),
	}

	result, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
		"owner":    "org",
		"repo":     "repo",
		"username": "maintainer",
	})
	require.NoError(t, err)

	// Verify MCP response format: {content: [{type: "text", text: "..."}]}
	resultMap, ok := result.(map[string]interface{})
	require.True(t, ok, "result should be a map")

	content, ok := resultMap["content"].([]map[string]interface{})
	require.True(t, ok, "content should be array of maps")
	require.Len(t, content, 1)
	assert.Equal(t, "text", content[0]["type"])

	text, ok := content[0]["text"].(string)
	require.True(t, ok, "text should be a string")

	// Parse the embedded JSON
	var parsed map[string]interface{}
	err = json.Unmarshal([]byte(text), &parsed)
	require.NoError(t, err)
	assert.Equal(t, "maintain", parsed["permission"])
}

func TestCollaboratorPermissionServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message": "Internal Server Error"}`)
	}))
	defer server.Close()

	t.Setenv("GITHUB_API_URL", server.URL)
	t.Setenv("GITHUB_TOKEN", "test-token")

	caller := &guardBackendCaller{
		server:   &UnifiedServer{},
		serverID: "github",
		ctx:      context.Background(),
	}

	_, err := caller.CallTool(context.Background(), "get_collaborator_permission", map[string]interface{}{
		"owner":    "org",
		"repo":     "repo",
		"username": "user",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

// extractMCPText extracts the text field from an MCP response format.
func extractMCPText(t *testing.T, result interface{}) string {
	t.Helper()
	resultMap, ok := result.(map[string]interface{})
	require.True(t, ok)
	content, ok := resultMap["content"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, content, 1)
	text, ok := content[0]["text"].(string)
	require.True(t, ok)
	return text
}
