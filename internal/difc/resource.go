package difc

import "github.com/github/gh-aw-mcpg/internal/logger"

var logResource = logger.New("difc:resource")

// Resource represents an external system with label requirements (deprecated - use LabeledResource)
type Resource struct {
	Description string
	Secrecy     SecrecyLabel
	Integrity   IntegrityLabel
}

// NewResource creates a new resource with the given description
func NewResource(description string) *Resource {
	return &Resource{
		Description: description,
		Secrecy:     *NewSecrecyLabel(),
		Integrity:   *NewIntegrityLabel(),
	}
}

// Empty returns a resource with no label requirements
func EmptyResource() *Resource {
	return &Resource{
		Description: "empty resource",
		Secrecy:     *NewSecrecyLabel(),
		Integrity:   *NewIntegrityLabel(),
	}
}

// LabeledResource represents a resource with DIFC labels
// This can be a simple label pair or a complex nested structure for fine-grained filtering
type LabeledResource struct {
	Description string         // Human-readable description of the resource
	Secrecy     SecrecyLabel   // Secrecy requirements for this resource
	Integrity   IntegrityLabel // Integrity requirements for this resource

	// Structure is an optional nested map for fine-grained labeling of response fields
	// Maps JSON paths to their labels (e.g., "items[*].private" -> specific labels)
	// If nil, labels apply uniformly to entire resource
	Structure *ResourceStructure
}

// NewLabeledResource creates a new labeled resource with the given description
func NewLabeledResource(description string) *LabeledResource {
	return &LabeledResource{
		Description: description,
		Secrecy:     *NewSecrecyLabel(),
		Integrity:   *NewIntegrityLabel(),
		Structure:   nil,
	}
}

// ResourceStructure defines fine-grained labels for nested data structures
type ResourceStructure struct {
	// Fields maps field names/paths to their labels
	// For collections, use "items[*]" to indicate per-item labeling
	Fields map[string]*FieldLabels
}

// FieldLabels defines labels for a specific field in the response
type FieldLabels struct {
	Secrecy   *SecrecyLabel
	Integrity *IntegrityLabel

	// Predicate is an optional function to determine labels based on field value
	// For example: label repo as private if repo.Private == true
	Predicate func(value interface{}) (*SecrecyLabel, *IntegrityLabel)
}

// LabeledData represents response data with associated labels
// Used for fine-grained filtering in the reference monitor
type LabeledData interface {
	// Overall returns the aggregate labels for all data
	Overall() *LabeledResource

	// ToResult converts the labeled data to an MCP result
	// This may filter out inaccessible items
	ToResult() (interface{}, error)
}

// SimpleLabeledData represents a single piece of data with uniform labels
type SimpleLabeledData struct {
	Data   interface{}
	Labels *LabeledResource
}

func (s *SimpleLabeledData) Overall() *LabeledResource {
	return s.Labels
}

func (s *SimpleLabeledData) ToResult() (interface{}, error) {
	return s.Data, nil
}

// CollectionLabeledData represents a collection where each item has its own labels
type CollectionLabeledData struct {
	Items []LabeledItem
}

// LabeledItem represents a single item in a collection with its labels
type LabeledItem struct {
	Data   interface{}
	Labels *LabeledResource
}

func (c *CollectionLabeledData) Overall() *LabeledResource {
	// Aggregate labels from all items - most restrictive
	if len(c.Items) == 0 {
		logResource.Print("Overall: empty collection, returning default labels")
		return NewLabeledResource("empty collection")
	}

	logResource.Printf("Overall: aggregating labels for collection: itemCount=%d", len(c.Items))
	overall := NewLabeledResource("collection")
	for _, item := range c.Items {
		if item.Labels != nil {
			// Union all secrecy tags (most restrictive)
			overall.Secrecy.Label.Union(item.Labels.Secrecy.Label)
			// Union all integrity tags (most restrictive)
			overall.Integrity.Label.Union(item.Labels.Integrity.Label)
		}
	}

	return overall
}

func (c *CollectionLabeledData) ToResult() (interface{}, error) {
	logResource.Printf("ToResult: converting collection to result: itemCount=%d", len(c.Items))
	// Return all items as a slice
	result := make([]interface{}, 0, len(c.Items))
	for _, item := range c.Items {
		result = append(result, item.Data)
	}
	return result, nil
}

// FilteredItemDetail pairs a filtered item with the reason it was denied
type FilteredItemDetail struct {
	Item   LabeledItem
	Reason string // Human-readable denial reason from EvaluationResult
}

// FilteredCollectionLabeledData represents a collection with some items filtered out
type FilteredCollectionLabeledData struct {
	Accessible   []LabeledItem
	Filtered     []FilteredItemDetail
	TotalCount   int
	FilterReason string
}

func (f *FilteredCollectionLabeledData) Overall() *LabeledResource {
	// Only aggregate labels from accessible items
	if len(f.Accessible) == 0 {
		logResource.Printf("Overall: filtered collection has no accessible items: filteredCount=%d", len(f.Filtered))
		return NewLabeledResource("empty filtered collection")
	}

	logResource.Printf("Overall: aggregating labels for filtered collection: accessible=%d, filtered=%d", len(f.Accessible), len(f.Filtered))
	overall := NewLabeledResource("filtered collection")
	for _, item := range f.Accessible {
		if item.Labels != nil {
			overall.Secrecy.Label.Union(item.Labels.Secrecy.Label)
			overall.Integrity.Label.Union(item.Labels.Integrity.Label)
		}
	}

	return overall
}

func (f *FilteredCollectionLabeledData) ToResult() (interface{}, error) {
	logResource.Printf("ToResult: returning accessible items from filtered collection: accessible=%d, filtered=%d", len(f.Accessible), len(f.Filtered))
	// Return only accessible items
	result := make([]interface{}, 0, len(f.Accessible))
	for _, item := range f.Accessible {
		result = append(result, item.Data)
	}
	return result, nil
}

// GetAccessibleCount returns the number of accessible items
func (f *FilteredCollectionLabeledData) GetAccessibleCount() int {
	return len(f.Accessible)
}

// GetFilteredCount returns the number of filtered items
func (f *FilteredCollectionLabeledData) GetFilteredCount() int {
	return len(f.Filtered)
}
