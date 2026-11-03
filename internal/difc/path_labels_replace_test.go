package difc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReplaceCollectionAtPath(t *testing.T) {
	replacement := []interface{}{"new"}

	tests := []struct {
		name      string
		data      func() interface{}
		path      string
		want      interface{}
		errSubstr string
	}{
		{
			name: "empty path returns replacement",
			data: func() interface{} { return []interface{}{"old"} },
			path: "",
			want: replacement,
		},
		{
			name: "single map key",
			data: func() interface{} { return map[string]interface{}{"items": []interface{}{"old"}, "total": 1} },
			path: "/items",
			want: map[string]interface{}{"items": replacement, "total": 1},
		},
		{
			name: "nested map keys",
			data: func() interface{} {
				return map[string]interface{}{"a": map[string]interface{}{"b": []interface{}{"old"}}}
			},
			path: "/a/b",
			want: map[string]interface{}{"a": map[string]interface{}{"b": replacement}},
		},
		{
			name: "escaped key",
			data: func() interface{} { return map[string]interface{}{"a/b": []interface{}{"old"}} },
			path: "/a~1b",
			want: map[string]interface{}{"a/b": replacement},
		},
		{
			name: "array index intermediate and final",
			data: func() interface{} {
				return []interface{}{map[string]interface{}{"items": []interface{}{"old"}}, "x"}
			},
			path: "/0/items",
			want: []interface{}{map[string]interface{}{"items": replacement}, "x"},
		},
		{
			name: "final array index",
			data: func() interface{} { return []interface{}{"a", "b"} },
			path: "/1",
			want: []interface{}{"a", replacement},
		},
		{
			name:      "missing intermediate key",
			data:      func() interface{} { return map[string]interface{}{"a": 1} },
			path:      "/x/y",
			errSubstr: "not found in response",
		},
		{
			name:      "invalid intermediate array index",
			data:      func() interface{} { return []interface{}{"a"} },
			path:      "/foo/bar",
			errSubstr: "invalid array index",
		},
		{
			name:      "intermediate array index out of range",
			data:      func() interface{} { return []interface{}{map[string]interface{}{}} },
			path:      "/5/bar",
			errSubstr: "invalid array index",
		},
		{
			name:      "negative intermediate array index",
			data:      func() interface{} { return []interface{}{"a"} },
			path:      "/-1/bar",
			errSubstr: "invalid array index",
		},
		{
			name:      "cannot navigate through scalar",
			data:      func() interface{} { return map[string]interface{}{"a": "str"} },
			path:      "/a/b/c",
			errSubstr: "cannot navigate",
		},
		{
			name:      "missing final map key",
			data:      func() interface{} { return map[string]interface{}{"a": 1} },
			path:      "/missing",
			errSubstr: "not found in response",
		},
		{
			name:      "invalid final array index",
			data:      func() interface{} { return []interface{}{"a"} },
			path:      "/abc",
			errSubstr: "invalid array index",
		},
		{
			name:      "final array index out of range",
			data:      func() interface{} { return []interface{}{"a"} },
			path:      "/3",
			errSubstr: "invalid array index",
		},
		{
			name:      "cannot replace in scalar parent",
			data:      func() interface{} { return map[string]interface{}{"a": "str"} },
			path:      "/a/b",
			errSubstr: "cannot replace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceCollectionAtPath(tt.data(), tt.path, replacement)
			if tt.errSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errSubstr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
