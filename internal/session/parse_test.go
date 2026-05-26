package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeJSONL(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return p
}

func TestParseSessionTail_AITitleWins(t *testing.T) {
	tmp := t.TempDir()
	cwdReal := t.TempDir() // exists, so CWDExists should be true
	body := strings.Join([]string{
		`{"type":"user","timestamp":"2026-05-26T11:00:00Z","cwd":"` + cwdReal + `","message":{"role":"user","content":"hi"}}`,
		`{"type":"assistant","timestamp":"2026-05-26T11:00:05Z"}`,
		`{"type":"ai-title","aiTitle":"My Session"}`,
		`{"type":"last-prompt","lastPrompt":"unused because aiTitle wins"}`,
	}, "\n") + "\n"

	p := writeJSONL(t, tmp, "11111111-1111-1111-1111-111111111111.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil {
		t.Fatalf("ParseSessionTail: %v", err)
	}
	if s == nil {
		t.Fatal("got nil session")
	}
	if s.Label != "My Session" {
		t.Errorf("Label = %q, want %q", s.Label, "My Session")
	}
	if s.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("ID = %q", s.ID)
	}
	if s.CWD != cwdReal {
		t.Errorf("CWD = %q, want %q", s.CWD, cwdReal)
	}
	if s.CWDBasename != filepath.Base(cwdReal) {
		t.Errorf("CWDBasename = %q", s.CWDBasename)
	}
	if !s.CWDExists {
		t.Error("CWDExists = false, want true")
	}
	want := time.Date(2026, 5, 26, 11, 0, 5, 0, time.UTC)
	if !s.LastTime.Equal(want) {
		t.Errorf("LastTime = %v, want %v", s.LastTime, want)
	}
}

func TestParseSessionTail_FallbackToLastPrompt(t *testing.T) {
	tmp := t.TempDir()
	body := strings.Join([]string{
		`{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":"first"}}`,
		`{"type":"last-prompt","lastPrompt":"from prompt"}`,
	}, "\n") + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.Label != "from prompt" {
		t.Errorf("Label = %q", s.Label)
	}
}

func TestParseSessionTail_FallbackToUserText_StringContent(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":"plain text body"}}` + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.Label != "plain text body" {
		t.Errorf("Label = %q", s.Label)
	}
}

func TestParseSessionTail_FallbackToUserText_BlockContent(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image"},{"type":"text","text":"world"}]}}` + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.Label != "hello world" {
		t.Errorf("Label = %q, want %q", s.Label, "hello world")
	}
}

func TestParseSessionTail_NoUserReturnsNil(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"assistant","timestamp":"2026-05-26T11:00:00Z"}` + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != nil {
		t.Errorf("expected nil session, got %+v", s)
	}
}

func TestParseSessionTail_NoLabelReturnsNil(t *testing.T) {
	tmp := t.TempDir()
	// user message with empty content blocks → no extractable text
	body := `{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":[]}}` + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if s != nil {
		t.Errorf("expected nil session, got %+v", s)
	}
}

func TestParseSessionTail_SkipsBadJSON(t *testing.T) {
	tmp := t.TempDir()
	body := strings.Join([]string{
		`not json at all`,
		`{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":"good"}}`,
	}, "\n") + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.Label != "good" {
		t.Errorf("Label = %q", s.Label)
	}
}

func TestParseSessionTail_TailTruncatesAndDropsPartialLine(t *testing.T) {
	tmp := t.TempDir()
	// Lines that fit in TailReadBytes
	user := `{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":"recent"}}`
	title := `{"type":"ai-title","aiTitle":"Recent Title"}`
	// Add a huge first line of garbage so readTail truncates it
	garbage := "X" + strings.Repeat("x", 100*1024)
	body := garbage + "\n" + user + "\n" + title + "\n"

	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.Label != "Recent Title" {
		t.Errorf("Label = %q, want %q", s.Label, "Recent Title")
	}
}

func TestParseSessionTail_FallsBackToModTimeWhenNoTimestamp(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","message":{"role":"user","content":"x"}}` + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	// Force a known mtime so we can compare.
	mtime := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if !s.LastTime.Equal(mtime) {
		t.Errorf("LastTime = %v, want %v", s.LastTime, mtime)
	}
}

func TestParseSessionTail_RestoresCWDFromDirWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	encodedProj := filepath.Join(tmp, "-Users-bob-proj")
	if err := os.MkdirAll(encodedProj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":"x"}}` + "\n"
	p := writeJSONL(t, encodedProj, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.CWD != "/Users/bob/proj" {
		t.Errorf("CWD = %q, want %q", s.CWD, "/Users/bob/proj")
	}
	if s.CWDExists {
		t.Error("CWDExists = true, want false (path doesn't exist)")
	}
}

func TestSanitizeLabel(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{"  hello   world  ", "hello world"},
		{"a\nb\tc\rd", "a b c d"},
		{"x  y   z", "x y z"},
		{"", ""},
	}
	for _, c := range cases {
		got := sanitizeLabel(c.in)
		if got != c.want {
			t.Errorf("sanitizeLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeLabel_TruncatesAtLabelMaxLen(t *testing.T) {
	in := strings.Repeat("a", LabelMaxLen+10)
	got := sanitizeLabel(in)
	if len([]rune(got)) != LabelMaxLen {
		t.Errorf("rune length = %d, want %d", len([]rune(got)), LabelMaxLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncation marker, got %q", got)
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"helloX", 5, "hell…"},
		{"あいうえお", 5, "あいうえお"},
		{"あいうえおX", 5, "あいうえ…"},
		{"x", 0, "x"},
		{"x", -1, "x"},
	}
	for _, c := range cases {
		got := truncate(c.s, c.n)
		if got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Errorf("got %q, want x", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestRestoreCWDFromDir(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/foo/-Users-bob-proj", "/Users/bob/proj"},
		{"/foo/-tmp", "/tmp"},
		{"/foo/-a-b", "/a/b"},
	}
	for _, c := range cases {
		got := restoreCWDFromDir(c.in)
		if got != c.want {
			t.Errorf("restoreCWDFromDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseTimestamp(t *testing.T) {
	if !parseTimestamp("").IsZero() {
		t.Error("empty should be zero")
	}
	if !parseTimestamp("not-a-time").IsZero() {
		t.Error("garbage should be zero")
	}
	want := time.Date(2026, 5, 26, 11, 0, 0, 0, time.UTC)
	if got := parseTimestamp("2026-05-26T11:00:00Z"); !got.Equal(want) {
		t.Errorf("RFC3339: got %v, want %v", got, want)
	}
	wantNano := time.Date(2026, 5, 26, 11, 0, 0, 123456789, time.UTC)
	if got := parseTimestamp("2026-05-26T11:00:00.123456789Z"); !got.Equal(wantNano) {
		t.Errorf("RFC3339Nano: got %v, want %v", got, wantNano)
	}
}
