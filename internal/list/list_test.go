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
	// B-9: cyan (the "healthy basename" color) must NOT appear on a gone row;
	// the basename should be yellow too so it stands out at a glance.
	if strings.Contains(gotColor, ansiCyan) {
		t.Errorf("gone row should not use cyan basename: %q", gotColor)
	}
}

// B-10: a session whose cwd we never learned must surface as [cwd?] rather
// than masquerading as healthy or as [gone].
func TestFormatLine_CWDUnknownMarker(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	s := &session.Session{
		ID:         "abc",
		LastTime:   now.Add(-time.Hour),
		CWDUnknown: true,
		Label:      "x",
	}
	got := formatLine(s, now, false)
	if !strings.Contains(got, "[cwd?] ") {
		t.Errorf("expected '[cwd?] ' marker in %q", got)
	}
	if strings.Contains(got, "[gone] ") {
		t.Errorf("unknown-cwd row should not say [gone]: %q", got)
	}
	// Yellow on color path to draw attention.
	gotColor := formatLine(s, now, true)
	if !strings.Contains(gotColor, ansiYellow) {
		t.Errorf("expected yellow on cwd-unknown row: %q", gotColor)
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
