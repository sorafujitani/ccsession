package timefmt

import "time"

var layouts = []string{time.RFC3339Nano, time.RFC3339}

// Parse parses an RFC3339 / RFC3339Nano timestamp string, returning the zero
// time.Time on empty input or any parse error.
func Parse(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}
