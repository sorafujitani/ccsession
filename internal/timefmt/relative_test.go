package timefmt

import (
	"testing"
	"time"
)

func TestRelative(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		t    time.Time
		want string
	}{
		{"zero", time.Time{}, "?"},
		{"future clamps to just now", now.Add(time.Hour), "just now"},
		{"under a minute", now.Add(-30 * time.Second), "just now"},
		{"exactly one minute", now.Add(-time.Minute), "1m ago"},
		{"under an hour", now.Add(-59 * time.Minute), "59m ago"},
		{"exactly one hour", now.Add(-time.Hour), "1h ago"},
		{"under a day", now.Add(-23 * time.Hour), "23h ago"},
		{"exactly one day", now.Add(-24 * time.Hour), "1d ago"},
		{"six days", now.Add(-6 * 24 * time.Hour), "6d ago"},
		{"seven days falls back to date", now.Add(-7 * 24 * time.Hour), "2026-05-19"},
		{"one year", now.Add(-365 * 24 * time.Hour), "2025-05-26"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Relative(tc.t, now)
			if got != tc.want {
				t.Fatalf("Relative(%v, %v) = %q, want %q", tc.t, now, got, tc.want)
			}
		})
	}
}
