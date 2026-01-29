package difc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitJSONPointer(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected []string
	}{
		{
			name:     "empty path",
			path:     "",
			expected: nil,
		},
		{
			name:     "root path",
			path:     "/",
			expected: nil,
		},
		{
			name:     "simple path",
			path:     "/items",
			expected: []string{"items"},
		},
		{
			name:     "nested path",
			path:     "/items/0",
			expected: []string{"items", "0"},
		},
		{
			name:     "deeply nested path",
			path:     "/results/data/users/5",
			expected: []string{"results", "data", "users", "5"},
		},
		{
			name:     "escaped tilde",
			path:     "/foo~0bar",
			expected: []string{"foo~bar"},
		},
		{
			name:     "escaped slash",
			path:     "/foo~1bar",
			expected: []string{"foo/bar"},
		},
		{
			name:     "multiple escapes",
			path:     "/~0~1test",
			expected: []string{"~/test"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitJSONPointer(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestPathLabeledData_SimpleArray(t *testing.T) {
	// Original response is a simple array
	originalData := []interface{}{
		map[string]interface{}{"id": 1, "name": "public-item"},
		map[string]interface{}{"id": 2, "name": "private-item"},
		map[string]interface{}{"id": 3, "name": "another-public"},
	}

	pathLabels := &PathLabels{
		ItemsPath: "", // Root-level array
		LabeledPaths: []PathLabel{
			{
				Path: "/0",
				Labels: PathLabelEntry{
					Description: "Public item #1",
					Secrecy:     []string{"public"},
					Integrity:   []string{"untrusted"},
				},
			},
			{
				Path: "/1",
				Labels: PathLabelEntry{
					Description: "Private item #2",
					Secrecy:     []string{"repo_private"},
					Integrity:   []string{"github_verified"},
				},
			},
			{
				Path: "/2",
				Labels: PathLabelEntry{
					Description: "Public item #3",
					Secrecy:     []string{"public"},
					Integrity:   []string{"untrusted"},
				},
			},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	// Check that items were resolved
	items := pld.GetItems()
	require.Len(t, items, 3)

	// Check first item labels
	assert.Equal(t, 1, items[0].Data.(map[string]interface{})["id"])
	assert.True(t, items[0].Labels.Secrecy.Label.Contains(Tag("public")))
	assert.True(t, items[0].Labels.Integrity.Label.Contains(Tag("untrusted")))

	// Check second item labels (private)
	assert.Equal(t, 2, items[1].Data.(map[string]interface{})["id"])
	assert.True(t, items[1].Labels.Secrecy.Label.Contains(Tag("repo_private")))
	assert.True(t, items[1].Labels.Integrity.Label.Contains(Tag("github_verified")))

	// Check overall labels (should be union of all)
	overall := pld.Overall()
	assert.True(t, overall.Secrecy.Label.Contains(Tag("public")))
	assert.True(t, overall.Secrecy.Label.Contains(Tag("repo_private")))

	// Check ToResult returns original data unchanged
	result, err := pld.ToResult()
	require.NoError(t, err)
	assert.Equal(t, originalData, result)
}

func TestPathLabeledData_NestedItems(t *testing.T) {
	// Original response has items in a nested path (simulate JSON unmarshaling)
	originalDataJSON := `{
		"total_count": 2,
		"items": [
			{"number": 42, "title": "Bug report"},
			{"number": 43, "title": "Feature request"}
		]
	}`
	var originalData interface{}
	require.NoError(t, json.Unmarshal([]byte(originalDataJSON), &originalData))

	pathLabels := &PathLabels{
		ItemsPath: "/items",
		LabeledPaths: []PathLabel{
			{
				Path: "/items/0",
				Labels: PathLabelEntry{
					Description: "Issue #42",
					Secrecy:     []string{"public"},
					Integrity:   []string{"untrusted"},
				},
			},
			{
				Path: "/items/1",
				Labels: PathLabelEntry{
					Description: "Issue #43",
					Secrecy:     []string{"repo_private"},
					Integrity:   []string{"github_verified"},
				},
			},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	items := pld.GetItems()
	require.Len(t, items, 2)

	// Check labels were correctly applied
	assert.Equal(t, float64(42), items[0].Data.(map[string]interface{})["number"])
	assert.True(t, items[0].Labels.Secrecy.Label.Contains(Tag("public")))

	assert.Equal(t, float64(43), items[1].Data.(map[string]interface{})["number"])
	assert.True(t, items[1].Labels.Secrecy.Label.Contains(Tag("repo_private")))
}

func TestPathLabeledData_DefaultLabels(t *testing.T) {
	// Some items have explicit labels, others use defaults
	originalData := []interface{}{
		map[string]interface{}{"id": 1},
		map[string]interface{}{"id": 2},
		map[string]interface{}{"id": 3},
	}

	pathLabels := &PathLabels{
		ItemsPath: "",
		LabeledPaths: []PathLabel{
			{
				Path: "/1", // Only the second item has explicit labels
				Labels: PathLabelEntry{
					Description: "Special item",
					Secrecy:     []string{"secret"},
					Integrity:   []string{"verified"},
				},
			},
		},
		DefaultLabels: &PathLabelEntry{
			Description: "Default item",
			Secrecy:     []string{"public"},
			Integrity:   []string{"untrusted"},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	items := pld.GetItems()
	require.Len(t, items, 3)

	// Item 0 should have default labels
	assert.True(t, items[0].Labels.Secrecy.Label.Contains(Tag("public")))

	// Item 1 should have explicit labels
	assert.True(t, items[1].Labels.Secrecy.Label.Contains(Tag("secret")))
	assert.False(t, items[1].Labels.Secrecy.Label.Contains(Tag("public")))

	// Item 2 should have default labels
	assert.True(t, items[2].Labels.Secrecy.Label.Contains(Tag("public")))
}

func TestPathLabeledData_SingleObject(t *testing.T) {
	// Response is a single object, not a collection
	originalData := map[string]interface{}{
		"number":  42,
		"title":   "Bug report",
		"private": false,
	}

	pathLabels := &PathLabels{
		ItemsPath:    "", // No items path - single object
		LabeledPaths: nil,
		DefaultLabels: &PathLabelEntry{
			Description: "Single issue",
			Secrecy:     []string{"public"},
			Integrity:   []string{"github_verified"},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	// Should have exactly one item (the whole object)
	items := pld.GetItems()
	require.Len(t, items, 1)

	assert.Equal(t, originalData, items[0].Data)
	assert.True(t, items[0].Labels.Secrecy.Label.Contains(Tag("public")))
}

func TestPathLabeledData_ToCollectionLabeledData(t *testing.T) {
	originalData := []interface{}{
		map[string]interface{}{"id": 1},
		map[string]interface{}{"id": 2},
	}

	pathLabels := &PathLabels{
		ItemsPath: "",
		LabeledPaths: []PathLabel{
			{Path: "/0", Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"untrusted"}}},
			{Path: "/1", Labels: PathLabelEntry{Secrecy: []string{"private"}, Integrity: []string{"verified"}}},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	// Convert to CollectionLabeledData for compatibility
	collection := pld.ToCollectionLabeledData()
	require.NotNil(t, collection)
	require.Len(t, collection.Items, 2)

	assert.True(t, collection.Items[0].Labels.Secrecy.Label.Contains(Tag("public")))
	assert.True(t, collection.Items[1].Labels.Secrecy.Label.Contains(Tag("private")))
}

func TestParsePathLabels(t *testing.T) {
	jsonData := `{
		"items_path": "/items",
		"labeled_paths": [
			{
				"path": "/items/0",
				"labels": {
					"description": "First item",
					"secrecy": ["public"],
					"integrity": ["untrusted"]
				}
			},
			{
				"path": "/items/1",
				"labels": {
					"description": "Second item",
					"secrecy": ["repo_private"],
					"integrity": ["github_verified"]
				}
			}
		],
		"default_labels": {
			"description": "Default",
			"secrecy": ["public"],
			"integrity": ["untrusted"]
		}
	}`

	pl, err := ParsePathLabels([]byte(jsonData))
	require.NoError(t, err)

	assert.Equal(t, "/items", pl.ItemsPath)
	require.Len(t, pl.LabeledPaths, 2)
	assert.Equal(t, "/items/0", pl.LabeledPaths[0].Path)
	assert.Equal(t, []string{"public"}, pl.LabeledPaths[0].Labels.Secrecy)

	require.NotNil(t, pl.DefaultLabels)
	assert.Equal(t, []string{"public"}, pl.DefaultLabels.Secrecy)
}

func TestPathLabeledData_GitHubSearchIssuesExample(t *testing.T) {
	// Realistic GitHub search_issues response
	originalDataJSON := `{
		"total_count": 3,
		"incomplete_results": false,
		"items": [
			{
				"number": 1,
				"title": "Public bug report",
				"repository": {"full_name": "octocat/hello-world", "private": false}
			},
			{
				"number": 2,
				"title": "Private security issue",
				"repository": {"full_name": "corp/internal-tools", "private": true}
			},
			{
				"number": 3,
				"title": "Another public issue",
				"repository": {"full_name": "octocat/hello-world", "private": false}
			}
		]
	}`

	var originalData interface{}
	require.NoError(t, json.Unmarshal([]byte(originalDataJSON), &originalData))

	// Guard returns path labels based on repo visibility
	pathLabels := &PathLabels{
		ItemsPath: "/items",
		LabeledPaths: []PathLabel{
			{
				Path: "/items/0",
				Labels: PathLabelEntry{
					Description: "Issue #1 in octocat/hello-world",
					Secrecy:     []string{"public"},
					Integrity:   []string{"untrusted"},
				},
			},
			{
				Path: "/items/1",
				Labels: PathLabelEntry{
					Description: "Issue #2 in corp/internal-tools",
					Secrecy:     []string{"repo:corp/internal-tools"},
					Integrity:   []string{"github_verified"},
				},
			},
			{
				Path: "/items/2",
				Labels: PathLabelEntry{
					Description: "Issue #3 in octocat/hello-world",
					Secrecy:     []string{"public"},
					Integrity:   []string{"untrusted"},
				},
			},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	items := pld.GetItems()
	require.Len(t, items, 3)

	// First item - public
	assert.True(t, items[0].Labels.Secrecy.Label.Contains(Tag("public")))

	// Second item - private repo
	assert.True(t, items[1].Labels.Secrecy.Label.Contains(Tag("repo:corp/internal-tools")))
	assert.False(t, items[1].Labels.Secrecy.Label.Contains(Tag("public")))

	// Third item - public
	assert.True(t, items[2].Labels.Secrecy.Label.Contains(Tag("public")))

	// Overall should contain all tags
	overall := pld.Overall()
	assert.True(t, overall.Secrecy.Label.Contains(Tag("public")))
	assert.True(t, overall.Secrecy.Label.Contains(Tag("repo:corp/internal-tools")))

	// Can convert to CollectionLabeledData for filtering
	collection := pld.ToCollectionLabeledData()
	require.Len(t, collection.Items, 3)
}

func TestExtractIndexFromPath(t *testing.T) {
	pld := &PathLabeledData{}

	tests := []struct {
		name      string
		path      string
		itemsPath string
		wantIdx   int
		wantErr   bool
	}{
		{
			name:      "root array index",
			path:      "/0",
			itemsPath: "",
			wantIdx:   0,
			wantErr:   false,
		},
		{
			name:      "nested items array",
			path:      "/items/5",
			itemsPath: "/items",
			wantIdx:   5,
			wantErr:   false,
		},
		{
			name:      "deeply nested",
			path:      "/results/data/10",
			itemsPath: "/results/data",
			wantIdx:   10,
			wantErr:   false,
		},
		{
			name:      "path without leading slash",
			path:      "items/3",
			itemsPath: "items",
			wantIdx:   3,
			wantErr:   false,
		},
		{
			name:      "non-numeric index",
			path:      "/items/foo",
			itemsPath: "/items",
			wantIdx:   -1,
			wantErr:   true,
		},
		{
			name:      "mismatched path",
			path:      "/other/0",
			itemsPath: "/items",
			wantIdx:   -1,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, err := pld.extractIndexFromPath(tt.path, tt.itemsPath)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantIdx, idx)
			}
		})
	}
}
