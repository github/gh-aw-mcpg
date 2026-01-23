package guard

import (
	"context"
	"testing"

	"github.com/githubnext/gh-aw-mcpg/internal/difc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockBackendCaller implements a mock backend caller for testing
type mockBackendCaller struct {
	callToolFunc func(ctx context.Context, toolName string, args interface{}) (interface{}, error)
}

func (m *mockBackendCaller) CallTool(ctx context.Context, toolName string, args interface{}) (interface{}, error) {
	if m.callToolFunc != nil {
		return m.callToolFunc(ctx, toolName, args)
	}
	return nil, nil
}

// createMockRemoteGuard creates a remote guard with a mock connection for testing
func createMockRemoteGuard(name string) *RemoteGuard {
	// We have to work around the fact that RemoteGuard expects *mcp.Connection
	// For testing purposes, we'll just test the parsing logic separately
	return &RemoteGuard{
		name:       name,
		connection: nil, // Set to nil for unit tests, we'll test logic in integration
	}
}

func TestRemoteGuard_Name(t *testing.T) {
	guard := createMockRemoteGuard("test-guard")
	assert.Equal(t, "test-guard", guard.Name())
}

// Test helper functions for parsing response data
func TestParseLabeledResource(t *testing.T) {
	tests := []struct {
		name     string
		data     map[string]interface{}
		wantDesc string
		wantSec  []difc.Tag
		wantInt  []difc.Tag
	}{
		{
			name: "simple labels",
			data: map[string]interface{}{
				"description": "test-resource",
				"secrecy":     []interface{}{"public"},
				"integrity":   []interface{}{"maintainer"},
			},
			wantDesc: "test-resource",
			wantSec:  []difc.Tag{"public"},
			wantInt:  []difc.Tag{"maintainer"},
		},
		{
			name: "multiple tags",
			data: map[string]interface{}{
				"description": "multi-tag-resource",
				"secrecy":     []interface{}{"public", "repo_private"},
				"integrity":   []interface{}{"maintainer", "contributor"},
			},
			wantDesc: "multi-tag-resource",
			wantSec:  []difc.Tag{"public", "repo_private"},
			wantInt:  []difc.Tag{"maintainer", "contributor"},
		},
		{
			name: "empty labels",
			data: map[string]interface{}{
				"description": "empty-resource",
			},
			wantDesc: "empty-resource",
			wantSec:  []difc.Tag{},
			wantInt:  []difc.Tag{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resource, err := parseLabeledResource(tt.data)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDesc, resource.Description)

			// Check secrecy tags
			secTags := resource.Secrecy.Label.GetTags()
			assert.ElementsMatch(t, tt.wantSec, secTags)

			// Check integrity tags
			intTags := resource.Integrity.Label.GetTags()
			assert.ElementsMatch(t, tt.wantInt, intTags)
		})
	}
}

func TestParseCollectionLabeledData(t *testing.T) {
	items := []interface{}{
		map[string]interface{}{
			"data": map[string]interface{}{"id": 1},
			"labels": map[string]interface{}{
				"description": "item-1",
				"secrecy":     []interface{}{"public"},
				"integrity":   []interface{}{"maintainer"},
			},
		},
		map[string]interface{}{
			"data": map[string]interface{}{"id": 2},
			"labels": map[string]interface{}{
				"description": "item-2",
				"secrecy":     []interface{}{"public"},
				"integrity":   []interface{}{"contributor"},
			},
		},
	}

	collection, err := parseCollectionLabeledData(items)
	require.NoError(t, err)
	require.NotNil(t, collection)
	assert.Len(t, collection.Items, 2)

	// Check first item
	assert.NotNil(t, collection.Items[0].Data)
	assert.Equal(t, "item-1", collection.Items[0].Labels.Description)
	assert.True(t, collection.Items[0].Labels.Secrecy.Label.Contains("public"))

	// Check second item
	assert.NotNil(t, collection.Items[1].Data)
	assert.Equal(t, "item-2", collection.Items[1].Labels.Description)
	assert.True(t, collection.Items[1].Labels.Integrity.Label.Contains("contributor"))
}
