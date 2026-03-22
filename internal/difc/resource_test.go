package difc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResource(t *testing.T) {
	tests := []struct {
		name        string
		description string
	}{
		{name: "basic description", description: "my-resource"},
		{name: "empty description", description: ""},
		{name: "long description", description: "a very long resource description with spaces and special chars: @#$%"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewResource(tt.description)

			require.NotNil(t, r)
			assert.Equal(t, tt.description, r.Description)
			assert.True(t, r.Secrecy.Label.IsEmpty(), "NewResource should have empty secrecy label")
			assert.True(t, r.Integrity.Label.IsEmpty(), "NewResource should have empty integrity label")
		})
	}
}

func TestEmptyResource(t *testing.T) {
	r := EmptyResource()

	require.NotNil(t, r)
	assert.Equal(t, "empty resource", r.Description)
	assert.True(t, r.Secrecy.Label.IsEmpty(), "EmptyResource should have empty secrecy label")
	assert.True(t, r.Integrity.Label.IsEmpty(), "EmptyResource should have empty integrity label")
}

func TestNewLabeledResource(t *testing.T) {
	r := NewLabeledResource("labeled-resource")

	require.NotNil(t, r)
	assert.Equal(t, "labeled-resource", r.Description)
	assert.True(t, r.Secrecy.Label.IsEmpty(), "NewLabeledResource should have empty secrecy label")
	assert.True(t, r.Integrity.Label.IsEmpty(), "NewLabeledResource should have empty integrity label")
	assert.Nil(t, r.Structure, "NewLabeledResource should have nil Structure")
}

func TestSimpleLabeledData_Overall(t *testing.T) {
	t.Run("returns the labels set on the data", func(t *testing.T) {
		assert := assert.New(t)

		labels := NewLabeledResource("simple-data")
		labels.Secrecy.Label.Add("private")
		labels.Integrity.Label.Add("trusted")

		data := &SimpleLabeledData{
			Data:   "some data",
			Labels: labels,
		}

		result := data.Overall()

		assert.Equal(labels, result)
		assert.True(result.Secrecy.Label.Contains("private"), "Expected secrecy tag to be present")
		assert.True(result.Integrity.Label.Contains("trusted"), "Expected integrity tag to be present")
	})

	t.Run("returns nil labels when not set", func(t *testing.T) {
		data := &SimpleLabeledData{
			Data:   42,
			Labels: nil,
		}

		result := data.Overall()
		assert.Nil(t, result)
	})
}

func TestSimpleLabeledData_ToResult(t *testing.T) {
	tests := []struct {
		name string
		data interface{}
	}{
		{name: "string data", data: "hello world"},
		{name: "integer data", data: 42},
		{name: "nil data", data: nil},
		{name: "map data", data: map[string]interface{}{"key": "value"}},
		{name: "slice data", data: []string{"a", "b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			labels := NewLabeledResource("test")
			sld := &SimpleLabeledData{
				Data:   tt.data,
				Labels: labels,
			}

			result, err := sld.ToResult()

			assert.NoError(t, err, "ToResult should not return an error")
			assert.Equal(t, tt.data, result, "ToResult should return the data unchanged")
		})
	}
}

func TestCollectionLabeledData_Overall_EmptyCollection(t *testing.T) {
	c := &CollectionLabeledData{
		Items: []LabeledItem{},
	}

	result := c.Overall()

	require.NotNil(t, result)
	assert.Equal(t, "empty collection", result.Description)
	assert.True(t, result.Secrecy.Label.IsEmpty(), "Empty collection should have empty secrecy")
	assert.True(t, result.Integrity.Label.IsEmpty(), "Empty collection should have empty integrity")
}

func TestCollectionLabeledData_Overall_WithItems(t *testing.T) {
	tests := []struct {
		name            string
		items           []LabeledItem
		expectSecrecy   []Tag
		expectIntegrity []Tag
	}{
		{
			name: "single item with labels",
			items: []LabeledItem{
				{
					Data: "item1",
					Labels: &LabeledResource{
						Description: "item",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"private"}),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"trusted"}),
					},
				},
			},
			expectSecrecy:   []Tag{"private"},
			expectIntegrity: []Tag{"trusted"},
		},
		{
			name: "multiple items - labels are unioned",
			items: []LabeledItem{
				{
					Data: "item1",
					Labels: &LabeledResource{
						Description: "item1",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"tag1"}),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"int1"}),
					},
				},
				{
					Data: "item2",
					Labels: &LabeledResource{
						Description: "item2",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"tag2"}),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"int2"}),
					},
				},
			},
			expectSecrecy:   []Tag{"tag1", "tag2"},
			expectIntegrity: []Tag{"int1", "int2"},
		},
		{
			name: "item with nil labels is skipped",
			items: []LabeledItem{
				{Data: "item1", Labels: nil},
				{
					Data: "item2",
					Labels: &LabeledResource{
						Description: "item2",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"secret"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
			},
			expectSecrecy:   []Tag{"secret"},
			expectIntegrity: []Tag{},
		},
		{
			name: "all items with nil labels",
			items: []LabeledItem{
				{Data: "item1", Labels: nil},
				{Data: "item2", Labels: nil},
			},
			expectSecrecy:   []Tag{},
			expectIntegrity: []Tag{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)

			c := &CollectionLabeledData{Items: tt.items}
			result := c.Overall()

			require.NotNil(t, result)
			assert.Equal("collection", result.Description)

			for _, tag := range tt.expectSecrecy {
				assert.True(result.Secrecy.Label.Contains(tag), "Expected secrecy tag %q in overall", tag)
			}
			for _, tag := range tt.expectIntegrity {
				assert.True(result.Integrity.Label.Contains(tag), "Expected integrity tag %q in overall", tag)
			}
		})
	}
}

func TestCollectionLabeledData_ToResult(t *testing.T) {
	tests := []struct {
		name     string
		items    []LabeledItem
		wantLen  int
		wantData []interface{}
	}{
		{
			name:     "empty collection returns empty slice",
			items:    []LabeledItem{},
			wantLen:  0,
			wantData: []interface{}{},
		},
		{
			name: "single item",
			items: []LabeledItem{
				{Data: "hello", Labels: NewLabeledResource("item")},
			},
			wantLen:  1,
			wantData: []interface{}{"hello"},
		},
		{
			name: "multiple items returns all data",
			items: []LabeledItem{
				{Data: "first", Labels: NewLabeledResource("a")},
				{Data: 42, Labels: NewLabeledResource("b")},
				{Data: nil, Labels: NewLabeledResource("c")},
			},
			wantLen:  3,
			wantData: []interface{}{"first", 42, nil},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &CollectionLabeledData{Items: tt.items}

			result, err := c.ToResult()

			require.NoError(t, err)
			require.NotNil(t, result)

			resultSlice, ok := result.([]interface{})
			require.True(t, ok, "ToResult should return []interface{}")
			assert.Len(t, resultSlice, tt.wantLen)
			assert.Equal(t, tt.wantData, resultSlice)
		})
	}
}

func TestFilteredCollectionLabeledData_Overall_EmptyAccessible(t *testing.T) {
	f := &FilteredCollectionLabeledData{
		Accessible: []LabeledItem{},
		Filtered: []FilteredItemDetail{
			{
				Item: LabeledItem{
					Data: "filtered-item",
					Labels: &LabeledResource{
						Description: "filtered",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"secret"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				Reason: "test",
			},
		},
		TotalCount: 1,
	}

	result := f.Overall()

	require.NotNil(t, result)
	assert.Equal(t, "empty filtered collection", result.Description)
	// Even though filtered items have tags, empty accessible should give empty labels
	assert.True(t, result.Secrecy.Label.IsEmpty(), "Empty accessible should have empty secrecy")
	assert.True(t, result.Integrity.Label.IsEmpty(), "Empty accessible should have empty integrity")
}

func TestFilteredCollectionLabeledData_Overall_WithAccessibleItems(t *testing.T) {
	tests := []struct {
		name            string
		accessible      []LabeledItem
		filtered        []FilteredItemDetail
		expectSecrecy   []Tag
		expectIntegrity []Tag
	}{
		{
			name: "only accessible items contribute to overall labels",
			accessible: []LabeledItem{
				{
					Data: "accessible",
					Labels: &LabeledResource{
						Description: "accessible-item",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"public"}),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"verified"}),
					},
				},
			},
			filtered: []FilteredItemDetail{
				{
					Item: LabeledItem{
						Data: "filtered",
						Labels: &LabeledResource{
							Description: "filtered-item",
							Secrecy:     *NewSecrecyLabelWithTags([]Tag{"ultra-secret"}),
							Integrity:   *NewIntegrityLabelWithTags([]Tag{"high-trust"}),
						},
					},
					Reason: "test",
				},
			},
			expectSecrecy:   []Tag{"public"},
			expectIntegrity: []Tag{"verified"},
		},
		{
			name: "multiple accessible items - labels are unioned",
			accessible: []LabeledItem{
				{
					Data: "item1",
					Labels: &LabeledResource{
						Description: "item1",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"tag-a"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
				{
					Data: "item2",
					Labels: &LabeledResource{
						Description: "item2",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"tag-b"}),
						Integrity:   *NewIntegrityLabelWithTags([]Tag{"int-x"}),
					},
				},
			},
			filtered:        []FilteredItemDetail{},
			expectSecrecy:   []Tag{"tag-a", "tag-b"},
			expectIntegrity: []Tag{"int-x"},
		},
		{
			name: "accessible items with nil labels are skipped",
			accessible: []LabeledItem{
				{Data: "item1", Labels: nil},
				{
					Data: "item2",
					Labels: &LabeledResource{
						Description: "item2",
						Secrecy:     *NewSecrecyLabelWithTags([]Tag{"conf"}),
						Integrity:   *NewIntegrityLabel(),
					},
				},
			},
			filtered:        []FilteredItemDetail{},
			expectSecrecy:   []Tag{"conf"},
			expectIntegrity: []Tag{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := assert.New(t)

			f := &FilteredCollectionLabeledData{
				Accessible: tt.accessible,
				Filtered:   tt.filtered,
				TotalCount: len(tt.accessible) + len(tt.filtered),
			}

			result := f.Overall()

			require.NotNil(t, result)
			assert.Equal("filtered collection", result.Description)

			for _, tag := range tt.expectSecrecy {
				assert.True(result.Secrecy.Label.Contains(tag), "Expected secrecy tag %q in overall", tag)
			}
			for _, tag := range tt.expectIntegrity {
				assert.True(result.Integrity.Label.Contains(tag), "Expected integrity tag %q in overall", tag)
			}
		})
	}
}

func TestFilteredCollectionLabeledData_ToResult(t *testing.T) {
	tests := []struct {
		name       string
		accessible []LabeledItem
		filtered   []FilteredItemDetail
		wantData   []interface{}
	}{
		{
			name:       "empty accessible returns empty slice",
			accessible: []LabeledItem{},
			filtered: []FilteredItemDetail{
				{Item: LabeledItem{Data: "filtered", Labels: NewLabeledResource("f")}, Reason: "test"},
			},
			wantData: []interface{}{},
		},
		{
			name: "returns only accessible items",
			accessible: []LabeledItem{
				{Data: "visible-1", Labels: NewLabeledResource("a")},
				{Data: "visible-2", Labels: NewLabeledResource("b")},
			},
			filtered: []FilteredItemDetail{
				{Item: LabeledItem{Data: "hidden", Labels: NewLabeledResource("c")}, Reason: "test"},
			},
			wantData: []interface{}{"visible-1", "visible-2"},
		},
		{
			name: "all items accessible",
			accessible: []LabeledItem{
				{Data: "item-1", Labels: NewLabeledResource("a")},
				{Data: "item-2", Labels: NewLabeledResource("b")},
				{Data: "item-3", Labels: NewLabeledResource("c")},
			},
			filtered: []FilteredItemDetail{},
			wantData: []interface{}{"item-1", "item-2", "item-3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FilteredCollectionLabeledData{
				Accessible: tt.accessible,
				Filtered:   tt.filtered,
				TotalCount: len(tt.accessible) + len(tt.filtered),
			}

			result, err := f.ToResult()

			require.NoError(t, err)
			require.NotNil(t, result)

			resultSlice, ok := result.([]interface{})
			require.True(t, ok, "ToResult should return []interface{}")
			assert.Equal(t, tt.wantData, resultSlice)
		})
	}
}

func TestFilteredCollectionLabeledData_GetAccessibleCount(t *testing.T) {
	tests := []struct {
		name          string
		accessible    []LabeledItem
		expectedCount int
	}{
		{name: "zero accessible items", accessible: []LabeledItem{}, expectedCount: 0},
		{
			name:          "one accessible item",
			accessible:    []LabeledItem{{Data: "a", Labels: NewLabeledResource("a")}},
			expectedCount: 1,
		},
		{
			name: "multiple accessible items",
			accessible: []LabeledItem{
				{Data: "a", Labels: NewLabeledResource("a")},
				{Data: "b", Labels: NewLabeledResource("b")},
				{Data: "c", Labels: NewLabeledResource("c")},
			},
			expectedCount: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FilteredCollectionLabeledData{
				Accessible: tt.accessible,
				Filtered:   []FilteredItemDetail{},
			}
			assert.Equal(t, tt.expectedCount, f.GetAccessibleCount())
		})
	}
}

func TestFilteredCollectionLabeledData_GetFilteredCount(t *testing.T) {
	tests := []struct {
		name          string
		filtered      []FilteredItemDetail
		expectedCount int
	}{
		{name: "zero filtered items", filtered: []FilteredItemDetail{}, expectedCount: 0},
		{
			name:          "one filtered item",
			filtered:      []FilteredItemDetail{{Item: LabeledItem{Data: "f", Labels: NewLabeledResource("f")}, Reason: "test"}},
			expectedCount: 1,
		},
		{
			name: "multiple filtered items",
			filtered: []FilteredItemDetail{
				{Item: LabeledItem{Data: "f1", Labels: NewLabeledResource("f1")}, Reason: "test"},
				{Item: LabeledItem{Data: "f2", Labels: NewLabeledResource("f2")}, Reason: "test"},
			},
			expectedCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FilteredCollectionLabeledData{
				Accessible: []LabeledItem{},
				Filtered:   tt.filtered,
			}
			assert.Equal(t, tt.expectedCount, f.GetFilteredCount())
		})
	}
}

// TestFilteredCollectionLabeledData_Integration verifies that accessible and filtered
// counts are independent and that ToResult only returns accessible items.
func TestFilteredCollectionLabeledData_Integration(t *testing.T) {
	assert := assert.New(t)

	accessible := []LabeledItem{
		{
			Data: map[string]string{"name": "public-item"},
			Labels: &LabeledResource{
				Description: "public",
				Secrecy:     *NewSecrecyLabelWithTags([]Tag{"public"}),
				Integrity:   *NewIntegrityLabel(),
			},
		},
	}
	filtered := []FilteredItemDetail{
		{
			Item: LabeledItem{
				Data: map[string]string{"name": "private-item"},
				Labels: &LabeledResource{
					Description: "private",
					Secrecy:     *NewSecrecyLabelWithTags([]Tag{"private"}),
					Integrity:   *NewIntegrityLabel(),
				},
			},
			Reason: "test",
		},
		{
			Item: LabeledItem{
				Data: map[string]string{"name": "secret-item"},
				Labels: &LabeledResource{
					Description: "secret",
					Secrecy:     *NewSecrecyLabelWithTags([]Tag{"secret"}),
					Integrity:   *NewIntegrityLabel(),
				},
			},
			Reason: "test",
		},
	}

	f := &FilteredCollectionLabeledData{
		Accessible: accessible,
		Filtered:   filtered,
		TotalCount: 3,
	}

	// Counts
	assert.Equal(1, f.GetAccessibleCount(), "Should have 1 accessible item")
	assert.Equal(2, f.GetFilteredCount(), "Should have 2 filtered items")

	// Overall should only reflect accessible labels
	overall := f.Overall()
	assert.Equal("filtered collection", overall.Description)
	assert.True(overall.Secrecy.Label.Contains("public"), "Overall should contain 'public' secrecy tag")
	assert.False(overall.Secrecy.Label.Contains("private"), "Overall should NOT contain 'private' secrecy tag")
	assert.False(overall.Secrecy.Label.Contains("secret"), "Overall should NOT contain 'secret' secrecy tag")

	// ToResult should only return accessible data
	result, err := f.ToResult()
	assert.NoError(err)
	resultSlice, ok := result.([]interface{})
	require.True(t, ok)
	assert.Len(resultSlice, 1)
	assert.Equal(map[string]string{"name": "public-item"}, resultSlice[0])
}

// TestCollectionLabeledData_AsLabeledData verifies CollectionLabeledData implements LabeledData
func TestCollectionLabeledData_AsLabeledData(t *testing.T) {
	var _ LabeledData = &CollectionLabeledData{}
	var _ LabeledData = &SimpleLabeledData{}
	var _ LabeledData = &FilteredCollectionLabeledData{}
}
