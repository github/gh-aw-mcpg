package difc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCollectionLabeledData_ToResult_PreserveEnvelope(t *testing.T) {
	newOriginal := func() map[string]interface{} {
		return map[string]interface{}{
			"totalCount": 3,
			"data": map[string]interface{}{
				"items": []interface{}{"a", "b", "c"},
				"list":  []interface{}{"x", "y"},
			},
		}
	}
	items := []LabeledItem{{Data: "a"}, {Data: "c"}}

	t.Run("without envelope returns items only", func(t *testing.T) {
		c := &CollectionLabeledData{Items: items, OriginalData: newOriginal(), ItemsPath: "/data/items"}

		got, err := c.ToResult()

		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "c"}, got)
	})

	t.Run("without envelope and no items returns empty slice", func(t *testing.T) {
		c := &CollectionLabeledData{}

		got, err := c.ToResult()

		require.NoError(t, err)
		assert.Equal(t, []interface{}{}, got)
	})

	t.Run("preserve envelope replaces nested collection", func(t *testing.T) {
		original := newOriginal()
		c := &CollectionLabeledData{Items: items, OriginalData: original, ItemsPath: "/data/items", PreserveEnvelope: true}

		got, err := c.ToResult()

		require.NoError(t, err)
		assert.Equal(t, map[string]interface{}{
			"totalCount": 3,
			"data": map[string]interface{}{
				"items": []interface{}{"a", "c"},
				"list":  []interface{}{"x", "y"},
			},
		}, got)
		// The original must not be mutated (it is deep-cloned).
		assert.Equal(t, newOriginal(), original)
	})

	t.Run("preserve envelope with empty path returns items", func(t *testing.T) {
		c := &CollectionLabeledData{Items: items, OriginalData: newOriginal(), PreserveEnvelope: true}

		got, err := c.ToResult()

		require.NoError(t, err)
		assert.Equal(t, []interface{}{"a", "c"}, got)
	})

	t.Run("preserve envelope replaces array element", func(t *testing.T) {
		original := []interface{}{[]interface{}{"old"}, "keep"}
		c := &CollectionLabeledData{Items: items, OriginalData: original, ItemsPath: "/0", PreserveEnvelope: true}

		got, err := c.ToResult()

		require.NoError(t, err)
		assert.Equal(t, []interface{}{[]interface{}{"a", "c"}, "keep"}, got)
	})

	errTests := []struct {
		name string
		data interface{}
		path string
		want string
	}{
		{"missing intermediate key", map[string]interface{}{"x": 1}, "/data/items", "not found in response"},
		{"missing final key", map[string]interface{}{"data": map[string]interface{}{}}, "/data/items", "not found in response"},
		{"invalid intermediate index", map[string]interface{}{"data": []interface{}{}}, "/data/0/items", "invalid array index"},
		{"non-numeric final index", []interface{}{"a"}, "/abc", "invalid array index"},
		{"final index out of range", []interface{}{"a"}, "/5", "invalid array index"},
		{"cannot navigate scalar", map[string]interface{}{"data": "str"}, "/data/items/more", "cannot navigate"},
		{"cannot replace in scalar", "scalar", "/items", "cannot replace"},
	}
	for _, tt := range errTests {
		t.Run("preserve envelope error: "+tt.name, func(t *testing.T) {
			c := &CollectionLabeledData{Items: items, OriginalData: tt.data, ItemsPath: tt.path, PreserveEnvelope: true}

			got, err := c.ToResult()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Nil(t, got)
		})
	}
}
