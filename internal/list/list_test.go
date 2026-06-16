package list

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/ansi"
	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/source"
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
	if len(fields) != 6 {
		t.Fatalf("got %d fields, want 6: %q", len(fields), got)
	}
	if fields[0] != "abc" {
		t.Errorf("col1 = %q", fields[0])
	}
	if fields[1] == "" {
		t.Errorf("locator column is empty")
	}
	if !strings.HasPrefix(fields[3], "30m ago") {
		t.Errorf("col4 = %q, want prefix '30m ago'", fields[3])
	}
	if fields[4] != "ccsession" {
		t.Errorf("col5 = %q", fields[4])
	}
	if fields[5] != "hello" {
		t.Errorf("col6 = %q", fields[5])
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("unexpected ANSI in no-color output: %q", got)
	}
}

func TestFormatLine_CompositeIDRemainsHiddenKey(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	s := &session.Session{
		ID:          "codex:abc",
		Source:      "codex",
		LastTime:    now,
		LastEpoch:   now.Unix(),
		CWDBasename: "proj",
		CWDExists:   true,
		Label:       "hello",
	}
	got := formatLine(s, now, false)
	fields := strings.Split(got, "\t")
	if len(fields) != 6 {
		t.Fatalf("got %d fields, want 6: %q", len(fields), got)
	}
	if fields[0] != "codex:abc" {
		t.Errorf("hidden key = %q, want composite id", fields[0])
	}
	if !strings.HasPrefix(fields[1], "codex:") {
		t.Errorf("locator = %q, want source-prefixed locator", fields[1])
	}
	if fields[4] != "proj" || fields[5] != "hello" {
		t.Errorf("visible fields changed: %v", fields)
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
	if !strings.Contains(got, ansi.Cyan) {
		t.Errorf("expected cyan dirname colorization in %q", got)
	}
	if !strings.Contains(got, ansi.Dim) {
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
	if !strings.Contains(gotColor, ansi.Yellow) {
		t.Errorf("expected yellow marker color in %q", gotColor)
	}
	// B-9: cyan (the "healthy basename" color) must NOT appear on a gone row;
	// the basename should be yellow too so it stands out at a glance.
	if strings.Contains(gotColor, ansi.Cyan) {
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
	if !strings.Contains(gotColor, ansi.Yellow) {
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
	if fields[4] != "(unknown)" {
		t.Errorf("col5 = %q, want '(unknown)'", fields[4])
	}
}

// B-12: --color=always|never overrides auto-detection; --no-color and
// NO_COLOR force off when Color is empty/auto; non-TTY stdout defaults to
// no color so `ccsession list | cat` stops leaking ANSI codes.
func TestColorEnabled(t *testing.T) {
	t.Run("Color=always wins over NoColor", func(t *testing.T) {
		if !colorEnabled(Options{Color: "always", NoColor: true}) {
			t.Error("Color=always should override NoColor")
		}
	})
	t.Run("Color=never disables", func(t *testing.T) {
		if colorEnabled(Options{Color: "never"}) {
			t.Error("Color=never should be false")
		}
	})
	t.Run("NoColor disables", func(t *testing.T) {
		if colorEnabled(Options{NoColor: true}) {
			t.Error("NoColor should be false")
		}
	})
	t.Run("NO_COLOR env disables", func(t *testing.T) {
		t.Setenv("NO_COLOR", "1")
		if colorEnabled(Options{}) {
			t.Error("NO_COLOR env should disable color")
		}
	})
	t.Run("non-TTY writer defaults to off", func(t *testing.T) {
		t.Setenv("NO_COLOR", "")
		var buf bytes.Buffer
		if colorEnabled(Options{Out: &buf}) {
			t.Error("non-*os.File Out should default to no color")
		}
	})
}

func TestRun_JSONLimit(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(source.EnvVar, "")
	writeListSession(t, home, cwd, "11111111-1111-1111-1111-111111111111", "2026-05-26T10:00:00Z", "older")
	writeListSession(t, home, cwd, "22222222-2222-2222-2222-222222222222", "2026-05-26T11:00:00Z", "newer")

	var buf bytes.Buffer
	if err := Run(Options{JSON: true, Limit: 1, Out: &buf}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var rows []JSONSession
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, buf.String())
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: %#v", len(rows), rows)
	}
	row := rows[0]
	if row.ID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("id = %q, want newest session", row.ID)
	}
	if row.Source != "claude" {
		t.Errorf("source = %q, want claude", row.Source)
	}
	if row.CWD != cwd || !row.CWDExists || row.CWDUnknown {
		t.Errorf("cwd fields = cwd:%q exists:%v unknown:%v", row.CWD, row.CWDExists, row.CWDUnknown)
	}
	if row.Label != "newer" || row.LastActivity != "2026-05-26T11:00:00Z" {
		t.Errorf("metadata = label:%q last:%q", row.Label, row.LastActivity)
	}
	if row.Locator == "" {
		t.Error("locator is empty")
	}
}

func writeListSession(t *testing.T, home, cwd, id, ts, label string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"type":"user","timestamp":"` + ts + `","cwd":"` + cwd + `","message":{"role":"user","content":"` + label + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}

func TestFilterOutByDir(t *testing.T) {
	mk := func(id, cwd, base string) *session.Session {
		return &session.Session{ID: id, CWD: cwd, CWDBasename: base}
	}
	all := []*session.Session{
		mk("a", "/Users/x/work/myproj", "myproj"),
		mk("b", "/Users/x/scratch/test-thing", "test-thing"),
		mk("c", "/Users/x/work/Test", "Test"),
		mk("d", "", ""),
		mk("e", "", "test-fallback"),
	}
	in := append([]*session.Session(nil), all...)
	got := filterOutByDir(in, "test")

	ids := make([]string, len(got))
	for i, s := range got {
		ids[i] = s.ID
	}
	want := []string{"a", "d"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, ids[i], want[i])
		}
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
