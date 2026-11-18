package middleware

import (
	"encoding/json"
	"reflect"
)

// inferSchema recursively walks a JSON-compatible Go value and replaces every leaf
// with its jq type name ("null", "boolean", "number", "string"). Objects are
// traversed key-by-key; arrays are collapsed to a single representative element (or
// [] when empty). The output mirrors what the previous pure-jq walk_schema filter
// produced, but runs entirely in Go, bypassing jq interpreter overhead for recursion.
//
// Type mapping (matches jq's built-in type function):
//   - nil                                          → "null"
//   - bool                                         → "boolean"
//   - any integer or floating-point numeric type   → "number"
//     (float32/64, int/8/16/32/64, uint/8/16/32/64, json.Number)
//   - string                                       → "string"
//   - map[string]any                               → recursed object
//   - []any                                        → recursed array (first element only)
func inferSchema(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, child := range val {
			result[k] = inferSchema(child)
		}
		return result
	case []any:
		if len(val) == 0 {
			return []any{}
		}
		return []any{inferSchema(val[0])}
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, float32,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		json.Number:
		return "number"
	case string:
		return "string"
	default:
		// Defensive fallback: classify any remaining numeric reflect.Kind as "number"
		// and everything else as "string" to keep the schema output valid.
		switch reflect.TypeOf(v).Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
			return "number"
		default:
			return "string"
		}
	}
}
