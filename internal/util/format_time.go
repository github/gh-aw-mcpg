package util

import (
	"fmt"
	"time"
)

// FormatFutureTime returns a human-readable representation of a future time,
// combining an RFC3339 timestamp with a relative countdown (e.g. "2026-05-03T12:00:00Z (in 5.0m)").
// Returns "unknown" when t is the zero value.
func FormatFutureTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return fmt.Sprintf("%s (in %s)", t.UTC().Format(time.RFC3339), FormatDuration(time.Until(t).Round(time.Second)))
}

// FormatDuration formats a duration for display like the debug npm package.
// It provides granular formatting from nanoseconds to hours.
func FormatDuration(d time.Duration) string {
	if d < time.Microsecond {
		return fmt.Sprintf("%dns", d.Nanoseconds())
	}
	if d < time.Millisecond {
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.1fm", d.Minutes())
	}
	return fmt.Sprintf("%.1fh", d.Hours())
}
