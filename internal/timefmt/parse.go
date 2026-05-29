package timefmt

import "time"

// Parse parses an RFC3339 / RFC3339Nano timestamp string, returning the zero
// time.Time on empty input or any parse error.
func Parse(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}
