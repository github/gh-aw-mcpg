package difc

import (
	"github.com/github/gh-aw-mcpg/internal/logger"
	"github.com/github/gh-aw-mcpg/internal/util"
)

var logResource = logger.ForFile()

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
	logResource.Printf("Creating labeled resource: %s", description)
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
	Items            []LabeledItem
	OriginalData     interface{}
	ItemsPath        string
	PreserveEnvelope bool
}

// LabeledItem represents a single item in a collection with its labels
type LabeledItem struct {
	Data   interface{}
	Labels *LabeledResource
}

func (c *CollectionLabeledData) Overall() *LabeledResource {
	if len(c.Items) == 0 {
		logResource.Print("CollectionLabeledData.Overall: empty collection, returning empty labels")
	} else {
		logResource.Printf("CollectionLabeledData.Overall: aggregating labels from %d items", len(c.Items))
	}
	return aggregateLabels(c.Items, "empty collection", "collection")
}

func (c *CollectionLabeledData) ToResult() (interface{}, error) {
	logResource.Printf("CollectionLabeledData.ToResult: returning %d items", len(c.Items))
	result := itemsToResult(c.Items)
	if c.PreserveEnvelope {
		return replaceCollectionAtPath(util.DeepCloneJSON(c.OriginalData), c.ItemsPath, result)
	}
	return result, nil
}

// FilteredItemDetail pairs a filtered item with the reason it was denied
type FilteredItemDetail struct {
	Item               LabeledItem
	Reason             string // Human-readable denial reason from EvaluationResult
	IsSecrecyViolation bool   // True when the item was blocked due to secrecy requirements; false when due to integrity
}

// FilteredCollectionLabeledData represents a collection with some items filtered out
type FilteredCollectionLabeledData struct {
	Accessible       []LabeledItem
	Filtered         []FilteredItemDetail
	TotalCount       int
	FilterReason     string
	OriginalData     interface{}
	ItemsPath        string
	PreserveEnvelope bool
}

func (f *FilteredCollectionLabeledData) Overall() *LabeledResource {
	if len(f.Accessible) == 0 {
		logResource.Print("FilteredCollectionLabeledData.Overall: no accessible items, returning empty labels")
	} else {
		logResource.Printf("FilteredCollectionLabeledData.Overall: aggregating labels from %d accessible items (%d filtered)", len(f.Accessible), len(f.Filtered))
	}
	return aggregateLabels(f.Accessible, "empty filtered collection", "filtered collection")
}

func aggregateLabels(items []LabeledItem, emptyDescription, description string) *LabeledResource {
	if len(items) == 0 {
		return NewLabeledResource(emptyDescription)
	}

	overall := NewLabeledResource(description)
	unlabeled := 0
	for _, item := range items {
		if item.Labels != nil {
			overall.Secrecy.Label.Union(item.Labels.Secrecy.Label)
			overall.Integrity.Label.Union(item.Labels.Integrity.Label)
		} else {
			unlabeled++
		}
	}
	if unlabeled > 0 {
		logResource.Printf("aggregateLabels: %s has %d of %d items without labels (skipped)", description, unlabeled, len(items))
	}
	return overall
}

func (f *FilteredCollectionLabeledData) ToResult() (interface{}, error) {
	logResource.Printf("FilteredCollectionLabeledData.ToResult: returning %d accessible items (filter_reason=%s)", len(f.Accessible), f.FilterReason)
	result := itemsToResult(f.Accessible)
	if f.PreserveEnvelope {
		return replaceCollectionAtPath(util.DeepCloneJSON(f.OriginalData), f.ItemsPath, result)
	}
	return result, nil
}

func itemsToResult(items []LabeledItem) []interface{} {
	result := make([]interface{}, 0, len(items))
	for _, item := range items {
		result = append(result, item.Data)
	}
	return result
}

// GetAccessibleCount returns the number of accessible items
func (f *FilteredCollectionLabeledData) GetAccessibleCount() int {
	return len(f.Accessible)
}

// GetFilteredCount returns the number of filtered items
func (f *FilteredCollectionLabeledData) GetFilteredCount() int {
	return len(f.Filtered)
}
