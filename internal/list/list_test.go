package list

import (
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
)

func TestFormatLine_NoColor(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	s := &session.Session{
		ID:          "abc",
		LastTime:    now.Add(-30 * time.Minute),
		LastEpoch:   now.Add(-30 * time.Minute).Unix(),
		CWDBasename: "ccsession",
		CWDExists:   true,
		Label:       "hello",
	}
	got := formatLine(s, now, false)
	fields := strings.Split(got, "\t")
	if len(fields) != 5 {
		t.Fatalf("got %d fields, want 5: %q", len(fields), got)
	}
	if fields[0] != "abc" {
		t.Errorf("col1 = %q", fields[0])
	}
	if !strings.HasPrefix(fields[2], "30m ago") {
		t.Errorf("col3 = %q, want prefix '30m ago'", fields[2])
	}
	if fields[3] != "ccsession" {
		t.Errorf("col4 = %q", fields[3])
	}
	if fields[4] != "hello" {
		t.Errorf("col5 = %q", fields[4])
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("unexpected ANSI in no-color output: %q", got)
	}
}

func TestFormatLine_WithColor(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	s := &session.Session{
		ID:          "abc",
		LastTime:    now.Add(-time.Hour),
		CWDBasename: "proj",
		CWDExists:   true,
		Label:       "x",
	}
	got := formatLine(s, now, true)
	if !strings.Contains(got, ansiCyan) {
		t.Errorf("expected cyan dirname colorization in %q", got)
	}
	if !strings.Contains(got, ansiDim) {
		t.Errorf("expected dim time in %q", got)
	}
}

func TestFormatLine_GoneMarker(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	s := &session.Session{
		ID:          "abc",
		LastTime:    now.Add(-time.Hour),
		CWDBasename: "proj",
		CWDExists:   false,
		Label:       "x",
	}
	got := formatLine(s, now, false)
	if !strings.Contains(got, "[gone] ") {
		t.Errorf("expected '[gone] ' marker in %q", got)
	}

	gotColor := formatLine(s, now, true)
	if !strings.Contains(gotColor, ansiYellow) {
		t.Errorf("expected yellow marker color in %q", gotColor)
	}
}

func TestFormatLine_EmptyBasenameFallsBackToUnknown(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	s := &session.Session{
		ID:          "abc",
		LastTime:    now,
		CWDBasename: "",
		CWDExists:   true,
		Label:       "x",
	}
	got := formatLine(s, now, false)
	fields := strings.Split(got, "\t")
	if fields[3] != "(unknown)" {
		t.Errorf("col4 = %q, want '(unknown)'", fields[3])
	}
}

func TestPadRight(t *testing.T) {
	if got := padRight("ab", 5); got != "ab   " {
		t.Errorf("padRight(\"ab\", 5) = %q", got)
	}
	if got := padRight("abcdef", 3); got != "abcdef" {
		t.Errorf("padRight should not shrink: got %q", got)
	}
	if got := padRight("あ", 3); got != "あ  " {
		t.Errorf("padRight should count runes: got %q (%d runes)", got, len([]rune(got)))
	}
}
