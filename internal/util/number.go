package util

import (
	"encoding/json"
	"fmt"
	"math"
)

// InterfaceToIntString attempts to convert a JSON-decoded numeric value
// (float64 or json.Number) to its decimal integer string representation.
// Returns ("", false) if the value is not a numeric type or is non-integer.
func InterfaceToIntString(v any) (string, bool) {
	switch n := v.(type) {
	case float64:
		// Explicitly guard against out-of-range values before conversion, since
		// converting an out-of-range float64 to int64 is implementation-defined in Go.
		// float64(math.MaxInt64) rounds up to 9.223372036854776e18, so use >=
		// for the upper bound. float64(math.MinInt64) = -(2^63) is exactly
		// representable, so < is appropriate for the lower bound.
		if n < float64(math.MinInt64) || n >= float64(math.MaxInt64) {
			return "", false // out of int64 range
		}
		i := int64(n)
		if n != float64(i) {
			return "", false // non-integer float
		}
		return fmt.Sprintf("%d", i), true
	case json.Number:
		// Validate that the json.Number represents a valid integer and convert to
		// a canonical decimal string (avoids non-canonical forms like "00123").
		i, err := n.Int64()
		if err != nil {
			return "", false // non-integer or out-of-range json.Number
		}
		return fmt.Sprintf("%d", i), true
	}
	return "", false
}
