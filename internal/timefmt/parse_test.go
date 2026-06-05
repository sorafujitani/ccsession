package timefmt

import (
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Time
	}{
		{
			name: "RFC3339",
			raw:  "2026-05-26T12:34:56Z",
			want: time.Date(2026, 5, 26, 12, 34, 56, 0, time.UTC),
		},
		{
			name: "RFC3339Nano",
			raw:  "2026-05-26T12:34:56.123456789Z",
			want: time.Date(2026, 5, 26, 12, 34, 56, 123456789, time.UTC),
		},
		{
			name: "empty string",
			raw:  "",
			want: time.Time{},
		},
		{
			name: "invalid string",
			raw:  "not-a-timestamp",
			want: time.Time{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.raw)
			if !got.Equal(tc.want) {
				t.Fatalf("Parse(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
