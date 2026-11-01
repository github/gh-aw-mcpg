package difc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/mcpresult"
)

var logPathLabels = logger.ForFile()

// PathLabels represents a collection of labeled paths in a JSON response.
// Guards return this structure to indicate which elements in the response
// have specific DIFC labels, without copying the data itself.
//
// Example guard response:
//
//	{
//	  "labeled_paths": [
//	    { "path": "/items/0", "labels": { "secrecy": ["public"], "integrity": ["untrusted"] } },
//	    { "path": "/items/1", "labels": { "secrecy": ["repo_private"], "integrity": ["github_verified"] } }
//	  ],
//	  "default_labels": { "secrecy": ["public"], "integrity": ["untrusted"] }
//	}
type PathLabels struct {
	// LabeledPaths maps JSON Pointer paths (RFC 6901) to their labels
	LabeledPaths []PathLabel `json:"labeled_paths"`

	// DefaultLabels are applied to elements not matched by any path
	// If nil, unmatched elements inherit the resource-level labels
	DefaultLabels *PathLabelEntry `json:"default_labels,omitempty"`

	// ItemsPath specifies where the collection items are located (e.g., "/items", "" for root array)
	// This helps the gateway understand the structure for filtering
	ItemsPath string `json:"items_path,omitempty"`
}

// PathLabel associates a JSON Pointer path with DIFC labels
type PathLabel struct {
	// Path is a JSON Pointer (RFC 6901) to the element
	// Examples: "/items/0", "/results/5", "/data/users/0"
	Path string `json:"path"`

	// Labels for this path
	Labels PathLabelEntry `json:"labels"`
}

// PathLabelEntry contains the DIFC labels for a path
type PathLabelEntry struct {
	Description string   `json:"description,omitempty"`
	Secrecy     []string `json:"secrecy"`
	Integrity   []string `json:"integrity"`
}

// PathLabeledData implements LabeledData for path-based labels.
// It combines the original response data with path labels from the guard.
type PathLabeledData struct {
	// OriginalData is the unmodified response from the backend
	OriginalData interface{}

	// UnwrappedData is the data to apply paths against (may be unwrapped from MCP format)
	UnwrappedData interface{}

	// IsMCPWrapped indicates if the original data was MCP-wrapped
	IsMCPWrapped bool

	// PathLabels contains the guard's labeling decisions
	PathLabels *PathLabels

	// resolvedItems caches the resolved items with their labels
	resolvedItems []LabeledItem
	resolved      bool
}

// NewPathLabeledData creates a new PathLabeledData from the original response and path labels.
// It automatically detects and unwraps MCP-formatted responses ({"content":[{"text":"..."}]})
// so that path labels can be applied to the inner JSON structure.
func NewPathLabeledData(originalData interface{}, pathLabels *PathLabels) (*PathLabeledData, error) {
	unwrapped, isMCPWrapped := unwrapMCPResponse(originalData)
	logPathLabels.Printf("NewPathLabeledData: isMCPWrapped=%v, labeledPaths=%d", isMCPWrapped, len(pathLabels.LabeledPaths))

	pld := &PathLabeledData{
		OriginalData:  originalData,
		UnwrappedData: unwrapped,
		IsMCPWrapped:  isMCPWrapped,
		PathLabels:    pathLabels,
	}

	// Resolve items eagerly to catch any path resolution errors
	if err := pld.resolve(); err != nil {
		return nil, fmt.Errorf("failed to resolve path labels: %w", err)
	}

	return pld, nil
}

// unwrapMCPResponse detects if the data is an MCP-wrapped response and extracts the inner JSON.
// MCP responses have the format: {"content":[{"text":"<json-string>","type":"text"}]}
// Returns (unwrapped data, true) if MCP-wrapped, or (original data, false) if not.
func unwrapMCPResponse(data interface{}) (interface{}, bool) {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return data, false
	}

	text := mcpresult.ExtractTextContent(dataMap)
	if text != "" {
		// Try to parse the text as JSON
		var inner interface{}
		if err := json.Unmarshal([]byte(text), &inner); err == nil {
			logPathLabels.Print("unwrapMCPResponse: detected MCP-wrapped response, unwrapping inner JSON")
			return inner, true
		}
	}

	if structured, ok := dataMap["structuredContent"]; ok {
		logPathLabels.Print("unwrapMCPResponse: detected structuredContent response")
		return structured, true
	}

	return data, false
}

// resolve applies path labels to the original data
func (p *PathLabeledData) resolve() error {
	if p.resolved {
		return nil
	}

	// Get the items array from the original data
	items, err := p.extractRawItems()
	if err != nil {
		logPathLabels.Printf("resolve: extractRawItems failed: %v", err)
		return err
	}

	if items == nil {
		logPathLabels.Print("resolve: no collection found, treating data as single item")
		// No collection to label, treat as single item
		p.resolvedItems = []LabeledItem{{
			Data:   p.OriginalData,
			Labels: p.pathEntryToResource(p.PathLabels.DefaultLabels),
		}}
		p.resolved = true
		return nil
	}

	// Create a map of index -> labels for quick lookup
	indexLabels := make(map[int]*PathLabelEntry)
	for _, pl := range p.PathLabels.LabeledPaths {
		idx, err := p.extractIndexFromPath(pl.Path, p.PathLabels.ItemsPath)
		if err != nil {
			logPathLabels.Printf("resolve: skipping labeled path %q: %v", pl.Path, err)
			continue
		}
		entry := pl.Labels // Create a copy
		indexLabels[idx] = &entry
	}

	// Build labeled items
	logPathLabels.Printf("resolve: resolving %d items with %d explicit path labels", len(items), len(indexLabels))
	p.resolvedItems = make([]LabeledItem, len(items))
	for i, item := range items {
		labels := indexLabels[i]
		if labels == nil {
			labels = p.PathLabels.DefaultLabels
		}

		p.resolvedItems[i] = LabeledItem{
			Data:   item,
			Labels: p.pathEntryToResource(labels),
		}
	}

	p.resolved = true
	return nil
}

// extractRawItems extracts the raw []interface{} items array from the unwrapped data
// based on ItemsPath. It is an internal helper for path resolution; callers that
// need typed resolved items should use GetItems instead.
func (p *PathLabeledData) extractRawItems() ([]interface{}, error) {
	if p.PathLabels.ItemsPath == "" {
		// Root-level array
		if arr, ok := p.UnwrappedData.([]interface{}); ok {
			logPathLabels.Printf("extractRawItems: found root-level array with %d items", len(arr))
			return arr, nil
		}
		// Not an array, return nil (single item)
		return nil, nil
	}

	// Navigate to the items path using UnwrappedData (which may have been extracted from MCP wrapper)
	logPathLabels.Printf("extractRawItems: navigating to items at path=%s", p.PathLabels.ItemsPath)
	current := p.UnwrappedData
	parts := splitJSONPointer(p.PathLabels.ItemsPath)

	for _, part := range parts {
		if part == "" {
			continue
		}

		switch v := current.(type) {
		case map[string]interface{}:
			var ok bool
			current, ok = v[part]
			if !ok {
				return nil, fmt.Errorf("path %q not found in response", p.PathLabels.ItemsPath)
			}
		case []interface{}:
			idx, err := strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("expected array index at %q, got %q", p.PathLabels.ItemsPath, part)
			}
			if idx < 0 || idx >= len(v) {
				return nil, fmt.Errorf("array index %d out of bounds", idx)
			}
			current = v[idx]
		default:
			return nil, fmt.Errorf("cannot navigate path %q: unexpected type at %q", p.PathLabels.ItemsPath, part)
		}
	}

	if arr, ok := current.([]interface{}); ok {
		logPathLabels.Printf("extractRawItems: found %d items at path=%s", len(arr), p.PathLabels.ItemsPath)
		return arr, nil
	}

	return nil, fmt.Errorf("items_path %q does not point to an array", p.PathLabels.ItemsPath)
}

// extractIndexFromPath extracts the array index from a JSON Pointer path
// For example, "/items/5" with itemsPath "/items" returns 5
func (p *PathLabeledData) extractIndexFromPath(path, itemsPath string) (int, error) {
	// Normalize paths
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if itemsPath != "" && !strings.HasPrefix(itemsPath, "/") {
		itemsPath = "/" + itemsPath
	}

	// Check if path starts with itemsPath
	var remainder string
	if itemsPath == "" {
		remainder = path
	} else if strings.HasPrefix(path, itemsPath+"/") {
		remainder = strings.TrimPrefix(path, itemsPath)
	} else {
		return -1, fmt.Errorf("path %q does not match items path %q", path, itemsPath)
	}

	// Extract the index (first segment after itemsPath)
	parts := splitJSONPointer(remainder)
	if len(parts) == 0 {
		return -1, fmt.Errorf("no index in path %q", path)
	}

	idx, err := strconv.Atoi(parts[0])
	if err != nil {
		return -1, fmt.Errorf("expected array index in path %q, got %q", path, parts[0])
	}

	return idx, nil
}

// pathEntryToResource converts a PathLabelEntry to a LabeledResource
func (p *PathLabeledData) pathEntryToResource(entry *PathLabelEntry) *LabeledResource {
	if entry == nil {
		logPathLabels.Print("pathEntryToResource: no entry provided, returning unlabeled resource")
		// Return empty labels if no entry
		return NewLabeledResource("unlabeled")
	}

	resource := NewLabeledResource(entry.Description)

	for _, s := range entry.Secrecy {
		resource.Secrecy.Label.Add(Tag(s))
	}

	for _, i := range entry.Integrity {
		resource.Integrity.Label.Add(Tag(i))
	}

	return resource
}

// splitJSONPointer splits a JSON Pointer path into segments
// Handles RFC 6901 escaping (~0 = ~, ~1 = /)
func splitJSONPointer(path string) []string {
	if path == "" || path == "/" {
		return nil
	}

	// Remove leading slash
	path = strings.TrimPrefix(path, "/")

	parts := strings.Split(path, "/")
	result := make([]string, len(parts))

	for i, part := range parts {
		// Unescape JSON Pointer special characters
		part = strings.ReplaceAll(part, "~1", "/")
		part = strings.ReplaceAll(part, "~0", "~")
		result[i] = part
	}

	return result
}

// Overall returns the aggregate labels for all items
func (p *PathLabeledData) Overall() *LabeledResource {
	if !p.resolved {
		_ = p.resolve()
	}

	if len(p.resolvedItems) == 0 {
		return NewLabeledResource("empty path-labeled data")
	}

	overall := NewLabeledResource("path-labeled collection")
	for _, item := range p.resolvedItems {
		if item.Labels != nil {
			overall.Secrecy.Label.Union(item.Labels.Secrecy.Label)
			overall.Integrity.Label.Union(item.Labels.Integrity.Label)
		}
	}

	logPathLabels.Printf("Overall: computed aggregate labels for %d items: secrecy=%v, integrity=%v",
		len(p.resolvedItems), overall.Secrecy.Label.GetTags(), overall.Integrity.Label.GetTags())
	return overall
}

// ToResult returns the original data (path labels don't modify the data structure)
func (p *PathLabeledData) ToResult() (interface{}, error) {
	return p.OriginalData, nil
}

// GetItems returns the resolved labeled items for filtering
func (p *PathLabeledData) GetItems() []LabeledItem {
	if !p.resolved {
		_ = p.resolve()
	}
	logPathLabels.Printf("GetItems: returning %d resolved items", len(p.resolvedItems))
	return p.resolvedItems
}

// ToCollectionLabeledData converts to CollectionLabeledData for compatibility with existing filtering
func (p *PathLabeledData) ToCollectionLabeledData() *CollectionLabeledData {
	if !p.resolved {
		_ = p.resolve()
	}
	logPathLabels.Printf("ToCollectionLabeledData: converting %d items", len(p.resolvedItems))
	return &CollectionLabeledData{
		Items:            p.resolvedItems,
		OriginalData:     p.UnwrappedData,
		ItemsPath:        p.PathLabels.ItemsPath,
		PreserveEnvelope: p.PathLabels.ItemsPath != "",
	}
}

func replaceCollectionAtPath(data interface{}, path string, replacement []interface{}) (interface{}, error) {
	parts := splitJSONPointer(path)
	if len(parts) == 0 {
		return replacement, nil
	}

	current := data
	for _, part := range parts[:len(parts)-1] {
		switch value := current.(type) {
		case map[string]interface{}:
			next, ok := value[part]
			if !ok {
				return nil, fmt.Errorf("items_path %q not found in response", path)
			}
			current = next
		case []interface{}:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(value) {
				return nil, fmt.Errorf("invalid array index %q in items_path %q", part, path)
			}
			current = value[index]
		default:
			return nil, fmt.Errorf("cannot navigate items_path %q at %q", path, part)
		}
	}

	last := parts[len(parts)-1]
	switch value := current.(type) {
	case map[string]interface{}:
		if _, ok := value[last]; !ok {
			return nil, fmt.Errorf("items_path %q not found in response", path)
		}
		value[last] = replacement
	case []interface{}:
		index, err := strconv.Atoi(last)
		if err != nil || index < 0 || index >= len(value) {
			return nil, fmt.Errorf("invalid array index %q in items_path %q", last, path)
		}
		value[index] = replacement
	default:
		return nil, fmt.Errorf("cannot replace items_path %q", path)
	}
	return data, nil
}

// ParsePathLabels parses a JSON response from a guard into PathLabels
func ParsePathLabels(data []byte) (*PathLabels, error) {
	logPathLabels.Printf("ParsePathLabels: parsing %d bytes", len(data))
	var pl PathLabels
	if err := json.Unmarshal(data, &pl); err != nil {
		return nil, fmt.Errorf("failed to parse path labels: %w", err)
	}
	logPathLabels.Printf("ParsePathLabels: parsed %d labeled paths, hasDefaultLabels=%v", len(pl.LabeledPaths), pl.DefaultLabels != nil)
	return &pl, nil
}
