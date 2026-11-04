package difc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetItems_PathNotFoundInMap covers the error path when the items_path
// key does not exist in the response map.
func TestGetItems_PathNotFoundInMap(t *testing.T) {
	data := map[string]interface{}{
		"total_count": float64(1),
		"results":     []interface{}{},
	}
	pathLabels := &PathLabels{
		ItemsPath:    "/missing_key",
		LabeledPaths: nil,
	}

	_, err := NewPathLabeledData(data, pathLabels)
	require.Error(t, err)
	assert.ErrorContains(t, err, "missing_key")
}

// TestGetItems_FinalPathNotArray covers the error path when items_path navigates
// successfully to a non-array value.
func TestGetItems_FinalPathNotArray(t *testing.T) {
	data := map[string]interface{}{
		"count": float64(42),
	}
	pathLabels := &PathLabels{
		ItemsPath:    "/count",
		LabeledPaths: nil,
	}

	_, err := NewPathLabeledData(data, pathLabels)
	require.Error(t, err)
	assert.ErrorContains(t, err, "does not point to an array")
}

// TestGetItems_ArrayNavigationSuccess covers the case where items_path navigates
// through an array index to reach the items collection.
func TestGetItems_ArrayNavigationSuccess(t *testing.T) {
	// Data structure: {"outer": [ ["item1", "item2"] ]}
	// ItemsPath "/outer/0" navigates through outer (array) → index 0 (which is itself an array)
	data := map[string]interface{}{
		"outer": []interface{}{
			[]interface{}{
				map[string]interface{}{"id": "item1"},
				map[string]interface{}{"id": "item2"},
			},
		},
	}
	pathLabels := &PathLabels{
		ItemsPath: "/outer/0",
		LabeledPaths: []PathLabel{
			{
				Path:   "/outer/0/0",
				Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"untrusted"}},
			},
			{
				Path:   "/outer/0/1",
				Labels: PathLabelEntry{Secrecy: []string{"private"}, Integrity: []string{"verified"}},
			},
		},
		DefaultLabels: &PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"untrusted"}},
	}

	pld, err := NewPathLabeledData(data, pathLabels)
	require.NoError(t, err)

	items := pld.GetItems()
	require.Len(t, items, 2)
	assert.Equal(t, "item1", items[0].Data.(map[string]interface{})["id"])
	assert.Equal(t, "item2", items[1].Data.(map[string]interface{})["id"])
}

// TestGetItems_ArrayIndexOutOfBounds covers the error path when an array index
// in items_path is out of range.
func TestGetItems_ArrayIndexOutOfBounds(t *testing.T) {
	data := map[string]interface{}{
		"outer": []interface{}{
			map[string]interface{}{"id": "only-item"},
		},
	}
	pathLabels := &PathLabels{
		ItemsPath:    "/outer/5",
		LabeledPaths: nil,
	}

	_, err := NewPathLabeledData(data, pathLabels)
	require.Error(t, err)
	assert.ErrorContains(t, err, "out of bounds")
}

// TestGetItems_NonNumericArrayIndex covers the error path when an array
// segment of items_path cannot be parsed as an integer.
func TestGetItems_NonNumericArrayIndex(t *testing.T) {
	data := map[string]interface{}{
		"outer": []interface{}{
			map[string]interface{}{"id": "item"},
		},
	}
	pathLabels := &PathLabels{
		ItemsPath:    "/outer/abc",
		LabeledPaths: nil,
	}

	_, err := NewPathLabeledData(data, pathLabels)
	require.Error(t, err)
	assert.ErrorContains(t, err, "expected array index")
}

// TestExtractRawItems_UnexpectedTypeAtPath covers the default case in extractRawItems where
// the value at a path segment is neither a map nor an array.
func TestExtractRawItems_UnexpectedTypeAtPath(t *testing.T) {
	data := map[string]interface{}{
		"count": float64(42), // scalar, not navigable
	}
	pathLabels := &PathLabels{
		ItemsPath:    "/count/items",
		LabeledPaths: nil,
	}

	_, err := NewPathLabeledData(data, pathLabels)
	require.Error(t, err)
	assert.ErrorContains(t, err, "unexpected type")
}

// TestNewPathLabeledData_ResolveError covers the error path in NewPathLabeledData
// when resolve() returns an error.
func TestNewPathLabeledData_ResolveError(t *testing.T) {
	// Use a map with a path that does not exist
	data := map[string]interface{}{
		"results": []interface{}{},
	}
	pathLabels := &PathLabels{
		ItemsPath:    "/nonexistent",
		LabeledPaths: nil,
	}

	_, err := NewPathLabeledData(data, pathLabels)
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to resolve path labels")
}

// TestParsePathLabels_InvalidJSON covers the error path when the input
// cannot be parsed as JSON.
func TestParsePathLabels_InvalidJSON(t *testing.T) {
	_, err := ParsePathLabels([]byte(`{invalid json`))
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to parse path labels")
}

// TestPathEntryToResource_NilEntry covers the branch where pathEntryToResource
// receives a nil entry and returns an "unlabeled" resource.
func TestPathEntryToResource_NilEntry(t *testing.T) {
	// Construct a PathLabeledData with items but no explicit labels and no DefaultLabels.
	// Item 1 has an explicit label but item 0 has neither explicit label nor DefaultLabels.
	originalData := []interface{}{
		map[string]interface{}{"id": "unlabeled-item"},
		map[string]interface{}{"id": "labeled-item"},
	}
	pathLabels := &PathLabels{
		ItemsPath: "",
		LabeledPaths: []PathLabel{
			{
				Path:   "/1",
				Labels: PathLabelEntry{Secrecy: []string{"private"}, Integrity: []string{"verified"}},
			},
		},
		DefaultLabels: nil, // No default labels
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	items := pld.GetItems()
	require.Len(t, items, 2)

	// Item 0 has no explicit label and no default → pathEntryToResource(nil) → "unlabeled"
	require.NotNil(t, items[0].Labels, "label should not be nil even when entry is nil")
	// Item 1 has an explicit label
	assert.True(t, items[1].Labels.Secrecy.Label.Contains(Tag("private")))
}

// TestOverall_Empty covers the case where Overall() is called with no resolved items.
func TestOverall_Empty(t *testing.T) {
	// Create a PathLabeledData that resolves to zero items (empty array).
	originalData := []interface{}{}
	pathLabels := &PathLabels{
		ItemsPath:    "",
		LabeledPaths: nil,
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)
	require.Empty(t, pld.GetItems())

	overall := pld.Overall()
	require.NotNil(t, overall, "Overall should return a non-nil resource even for empty data")
}

// TestOverall_NotYetResolved covers the !p.resolved branch in Overall().
func TestOverall_NotYetResolved(t *testing.T) {
	// Bypass NewPathLabeledData to create an unresolved instance.
	originalData := []interface{}{
		map[string]interface{}{"id": "item1"},
	}
	pathLabels := &PathLabels{
		ItemsPath: "",
		LabeledPaths: []PathLabel{
			{
				Path:   "/0",
				Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"trusted"}},
			},
		},
	}

	pld := &PathLabeledData{
		OriginalData:  originalData,
		UnwrappedData: originalData,
		IsMCPWrapped:  false,
		PathLabels:    pathLabels,
		// resolved = false (zero value)
	}

	// Overall() should lazily resolve and return the correct labels
	overall := pld.Overall()
	require.NotNil(t, overall)
	assert.True(t, pld.resolved, "should be resolved after calling Overall()")
	assert.True(t, overall.Secrecy.Label.Contains(Tag("public")))
}

// TestGetItems_NotYetResolved covers the !p.resolved branch in GetItems().
func TestGetItems_NotYetResolved(t *testing.T) {
	originalData := []interface{}{
		map[string]interface{}{"id": "item1"},
		map[string]interface{}{"id": "item2"},
	}
	pathLabels := &PathLabels{
		ItemsPath: "",
		LabeledPaths: []PathLabel{
			{Path: "/0", Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"untrusted"}}},
			{Path: "/1", Labels: PathLabelEntry{Secrecy: []string{"private"}, Integrity: []string{"verified"}}},
		},
	}

	pld := &PathLabeledData{
		OriginalData:  originalData,
		UnwrappedData: originalData,
		PathLabels:    pathLabels,
		// resolved = false
	}

	items := pld.GetItems()
	require.Len(t, items, 2)
	assert.True(t, pld.resolved)
	assert.True(t, items[0].Labels.Secrecy.Label.Contains(Tag("public")))
	assert.True(t, items[1].Labels.Secrecy.Label.Contains(Tag("private")))
}

// TestToCollectionLabeledData_NotYetResolved covers the !p.resolved branch
// in ToCollectionLabeledData().
func TestToCollectionLabeledData_NotYetResolved(t *testing.T) {
	originalData := []interface{}{
		map[string]interface{}{"id": "a"},
		map[string]interface{}{"id": "b"},
	}
	pathLabels := &PathLabels{
		ItemsPath: "",
		LabeledPaths: []PathLabel{
			{Path: "/0", Labels: PathLabelEntry{Secrecy: []string{"s1"}, Integrity: []string{"i1"}}},
			{Path: "/1", Labels: PathLabelEntry{Secrecy: []string{"s2"}, Integrity: []string{"i2"}}},
		},
	}

	pld := &PathLabeledData{
		OriginalData:  originalData,
		UnwrappedData: originalData,
		PathLabels:    pathLabels,
		// resolved = false
	}

	collection := pld.ToCollectionLabeledData()
	require.NotNil(t, collection)
	require.Len(t, collection.Items, 2)
	assert.True(t, pld.resolved)
	assert.True(t, collection.Items[0].Labels.Secrecy.Label.Contains(Tag("s1")))
	assert.True(t, collection.Items[1].Labels.Secrecy.Label.Contains(Tag("s2")))
}

// TestExtractIndexFromPath_NoSlashSeparator covers the case where the path starts
// with itemsPath but is not followed by a "/" separator.
// Per RFC 6901, "/items3" refers to key "items3" and must not match prefix "/items".
func TestExtractIndexFromPath_NoSlashSeparator(t *testing.T) {
	// path "items3" normalizes to "/items3", itemsPath "items" normalizes to "/items"
	// "/items3" does not start with "/items/" → error: does not match items path
	pld := &PathLabeledData{}
	_, err := pld.extractIndexFromPath("items3", "items")
	require.Error(t, err)
	assert.ErrorContains(t, err, "does not match items path")
}

// TestExtractIndexFromPath_EmptyRemainder covers the branch where the extracted
// remainder produces no path segments (no index can be found).
func TestExtractIndexFromPath_EmptyRemainder(t *testing.T) {
	// Empty path "" with empty itemsPath "" → after normalization path="/"
	// remainder = "/" → splitJSONPointer("/") = nil → len(parts) == 0 → error
	pld := &PathLabeledData{}
	_, err := pld.extractIndexFromPath("", "")
	require.Error(t, err)
	assert.ErrorContains(t, err, "no index in path")
}

// TestUnwrapMCPResponse_ContentItemNotMap covers the branch where the first
// element of the content array is not a map.
func TestUnwrapMCPResponse_ContentItemNotMap(t *testing.T) {
	data := map[string]interface{}{
		"content": []interface{}{
			"not-a-map", // string, not map[string]interface{}
		},
	}

	unwrapped, isMCPWrapped := unwrapMCPResponse(data)

	assert.False(t, isMCPWrapped)
	assert.Equal(t, data, unwrapped)
}

// TestPathLabeledData_MCPWrapped_SingleItem covers the single-item case
// with an MCP-wrapped response where ItemsPath is empty and the inner
// JSON is not an array.
func TestPathLabeledData_MCPWrapped_SingleItem(t *testing.T) {
	innerJSON := `{"number": 42, "title": "Single issue"}`
	mcpWrapped := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": innerJSON,
			},
		},
	}

	pathLabels := &PathLabels{
		ItemsPath: "",
		DefaultLabels: &PathLabelEntry{
			Secrecy:   []string{"public"},
			Integrity: []string{"github_verified"},
		},
	}

	pld, err := NewPathLabeledData(mcpWrapped, pathLabels)
	require.NoError(t, err)

	assert.True(t, pld.IsMCPWrapped)

	// Since inner JSON is an object (not array) and ItemsPath="", resolve treats it
	// as a single item using the original (wrapped) data.
	items := pld.GetItems()
	require.Len(t, items, 1)
	assert.Equal(t, mcpWrapped, items[0].Data, "original MCP-wrapped data should be preserved as the item")
	assert.True(t, items[0].Labels.Secrecy.Label.Contains(Tag("public")))
}

// TestPathLabeledData_ResolveNotCalledTwice verifies that GetItems returns
// consistent, cached results across multiple calls on an already-resolved
// PathLabeledData (exercises the p.resolved guard in GetItems).
func TestPathLabeledData_ResolveNotCalledTwice(t *testing.T) {
	originalData := []interface{}{
		map[string]interface{}{"id": "item"},
	}
	pathLabels := &PathLabels{
		ItemsPath: "",
		LabeledPaths: []PathLabel{
			{Path: "/0", Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"trusted"}}},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)
	require.True(t, pld.resolved)

	items1 := pld.GetItems()

	// Call GetItems again — p.resolved is true so resolvedItems is returned directly
	items2 := pld.GetItems()
	assert.Equal(t, items1, items2)
}

// TestPathLabeledData_ToResult_MCPWrapped verifies that ToResult returns the
// original MCP-wrapped data regardless of what was unwrapped for label resolution.
func TestPathLabeledData_ToResult_MCPWrapped(t *testing.T) {
	innerJSON := `[{"id":1},{"id":2}]`
	mcpWrapped := map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": innerJSON,
			},
		},
	}

	pathLabels := &PathLabels{
		ItemsPath: "",
		DefaultLabels: &PathLabelEntry{
			Secrecy:   []string{"public"},
			Integrity: []string{"untrusted"},
		},
	}

	pld, err := NewPathLabeledData(mcpWrapped, pathLabels)
	require.NoError(t, err)

	result, err := pld.ToResult()
	require.NoError(t, err)
	assert.Equal(t, mcpWrapped, result, "ToResult must return original MCP-wrapped data")
}

// TestPathLabeledData_OverallUnion verifies that Overall() takes the union of
// secrecy and integrity labels across all resolved items.
func TestPathLabeledData_OverallIntegrityIntersection(t *testing.T) {
	originalDataJSON := `{"items": [{"id": 1}, {"id": 2}, {"id": 3}]}`
	var originalData interface{}
	require.NoError(t, json.Unmarshal([]byte(originalDataJSON), &originalData))

	pathLabels := &PathLabels{
		ItemsPath: "/items",
		LabeledPaths: []PathLabel{
			{Path: "/items/0", Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"low"}}},
			{Path: "/items/1", Labels: PathLabelEntry{Secrecy: []string{"confidential"}, Integrity: []string{"high"}}},
			{Path: "/items/2", Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"medium"}}},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	overall := pld.Overall()
	require.NotNil(t, overall)

	// Overall secrecy should be union: {public, confidential}
	assert.True(t, overall.Secrecy.Label.Contains(Tag("public")))
	assert.True(t, overall.Secrecy.Label.Contains(Tag("confidential")))

	// No integrity tag is shared by every returned item.
	assert.Empty(t, overall.Integrity.Label.GetTags())
}

// TestResolve_SkipsNonMatchingLabeledPaths verifies that labeled paths that do
// not match the items_path pattern are silently skipped during resolve().
// This covers the "continue" branch in resolve's loop over LabeledPaths.
func TestResolve_SkipsNonMatchingLabeledPaths(t *testing.T) {
	originalDataJSON := `{"items": [{"id": 1}, {"id": 2}]}`
	var originalData interface{}
	require.NoError(t, json.Unmarshal([]byte(originalDataJSON), &originalData))

	pathLabels := &PathLabels{
		ItemsPath: "/items",
		LabeledPaths: []PathLabel{
			// Matching path — index 0 gets explicit labels
			{
				Path:   "/items/0",
				Labels: PathLabelEntry{Secrecy: []string{"explicit"}, Integrity: []string{"verified"}},
			},
			// Non-matching path — should be silently skipped (extractIndexFromPath errors)
			{
				Path:   "/other/0",
				Labels: PathLabelEntry{Secrecy: []string{"should-be-skipped"}, Integrity: []string{"ignored"}},
			},
		},
		DefaultLabels: &PathLabelEntry{Secrecy: []string{"default"}, Integrity: []string{"untrusted"}},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)

	items := pld.GetItems()
	require.Len(t, items, 2)

	// Item 0 should have explicit labels (not the skipped ones)
	assert.True(t, items[0].Labels.Secrecy.Label.Contains(Tag("explicit")))
	assert.False(t, items[0].Labels.Secrecy.Label.Contains(Tag("should-be-skipped")))

	// Item 1 should fall back to default labels (the "/other/0" label was skipped)
	assert.True(t, items[1].Labels.Secrecy.Label.Contains(Tag("default")))
	assert.False(t, items[1].Labels.Secrecy.Label.Contains(Tag("should-be-skipped")))
}

// TestExtractRawItems_EmptyPartInPath covers the `if part == "" { continue }` branch
// in extractRawItems when the items_path contains consecutive slashes (e.g. "//items").
// splitJSONPointer on "//items" produces ["", "items"]; the empty part is skipped.
func TestExtractRawItems_EmptyPartInPath(t *testing.T) {
	// "//items" is an unusual but parseable path: after removing the leading "/",
	// splitting "/items" on "/" gives ["", "items"], so the first empty segment
	// triggers the `continue` branch and the second segment navigates to "items".
	data := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"id": "a"},
			map[string]interface{}{"id": "b"},
		},
	}
	pathLabels := &PathLabels{
		ItemsPath: "//items", // double leading slash
		LabeledPaths: []PathLabel{
			{Path: "/items/0", Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"trusted"}}},
			{Path: "/items/1", Labels: PathLabelEntry{Secrecy: []string{"private"}, Integrity: []string{"verified"}}},
		},
	}

	pld, err := NewPathLabeledData(data, pathLabels)
	require.NoError(t, err)

	items := pld.GetItems()
	require.Len(t, items, 2, "should navigate past the empty path segment and find the items array")
}

// TestResolve_AlreadyResolved directly calls resolve() on an already-resolved
// PathLabeledData, covering the early-return guard at the top of the function.
func TestResolve_AlreadyResolved(t *testing.T) {
	originalData := []interface{}{map[string]interface{}{"id": "item"}}
	pathLabels := &PathLabels{
		ItemsPath: "",
		LabeledPaths: []PathLabel{
			{Path: "/0", Labels: PathLabelEntry{Secrecy: []string{"public"}, Integrity: []string{"trusted"}}},
		},
	}

	pld, err := NewPathLabeledData(originalData, pathLabels)
	require.NoError(t, err)
	require.True(t, pld.resolved)

	// Capture the items before calling resolve() directly.
	itemsBefore := pld.GetItems()

	// Call resolve() directly when already resolved — hits the early-return path.
	err = pld.resolve()
	require.NoError(t, err)

	// Items should be unchanged.
	itemsAfter := pld.GetItems()
	assert.Equal(t, itemsBefore, itemsAfter)
}
