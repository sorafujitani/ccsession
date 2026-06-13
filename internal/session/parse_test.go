package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/timefmt"
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

func TestParseSessionTail_NoUserReturnsErrSessionEmpty(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"assistant","timestamp":"2026-05-26T11:00:00Z"}` + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if !errors.Is(err, ErrSessionEmpty) {
		t.Fatalf("err = %v, want ErrSessionEmpty", err)
	}
	if s != nil {
		t.Errorf("expected nil session, got %+v", s)
	}
}

func TestParseSessionTail_NoLabelReturnsErrSessionEmpty(t *testing.T) {
	tmp := t.TempDir()
	// user message with empty content blocks → no extractable text
	body := `{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":[]}}` + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if !errors.Is(err, ErrSessionEmpty) {
		t.Fatalf("err = %v, want ErrSessionEmpty", err)
	}
	if s != nil {
		t.Errorf("expected nil session, got %+v", s)
	}
}

// B-1: a label that is only whitespace must be treated as empty, not silently
// passed through to render a blank label cell.
func TestParseSessionTail_WhitespaceLabelReturnsErrSessionEmpty(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","timestamp":"2026-05-26T11:00:00Z","message":{"role":"user","content":"   "}}` + "\n"
	p := writeJSONL(t, tmp, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if !errors.Is(err, ErrSessionEmpty) {
		t.Fatalf("err = %v, want ErrSessionEmpty", err)
	}
	if s != nil {
		t.Errorf("expected nil session, got %+v", s)
	}
}

// B-3: a session id with no file on disk must return ErrSessionFileMissing
// via the wrapping read path.
func TestParseSessionTail_MissingFileReturnsErrSessionFileMissing(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "ghost.jsonl")

	s, err := ParseSessionTail(p, TailReadBytes)
	if !errors.Is(err, ErrSessionFileMissing) {
		t.Fatalf("err = %v, want ErrSessionFileMissing", err)
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

// Regression: a session that `cd`-ed mid-conversation records multiple
// distinct cwds. `claude --resume` only looks under the project dir that
// encodes session-start cwd, so we must pick the cwd whose `/`→`-`
// encoding matches the project dir name — not the chronologically latest
// one. See issue #6.
func TestParseSessionTail_CWDChangedMidSession_PicksProjectMatchingCWD(t *testing.T) {
	tmp := t.TempDir()
	realCWD := filepath.Join(tmp, "myproj")
	sub := filepath.Join(realCWD, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	encoded := strings.ReplaceAll(realCWD, "/", "-")
	projDir := filepath.Join(tmp, "projects", encoded)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir projDir: %v", err)
	}
	body := strings.Join([]string{
		// chronologically first: session-start cwd, encodes to projDir name.
		`{"type":"user","timestamp":"2024-01-01T10:00:00Z","cwd":"` + realCWD + `","message":{"role":"user","content":"hi"}}`,
		// after `cd sub/`: a deeper cwd that does NOT encode to projDir name.
		`{"type":"user","timestamp":"2024-01-01T10:05:00Z","cwd":"` + sub + `","message":{"role":"user","content":"there"}}`,
	}, "\n") + "\n"
	p := writeJSONL(t, projDir, "abc.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.CWD != realCWD {
		t.Errorf("CWD = %q, want %q (must pick the cwd whose encoding matches "+
			"the project dir; picking %q would make claude --resume look in "+
			"the wrong folder)", s.CWD, realCWD, sub)
	}
}

// Regression: large sessions push the session-start cwd out of the 64 KiB
// tail, leaving only post-`cd` cwds. ParseSessionTail must read the head
// of the file for the session-start cwd in this case, since that's what
// the project dir was named after.
func TestParseSessionTail_LargeSession_UsesStartCWDFromHead(t *testing.T) {
	tmp := t.TempDir()
	realCWD := filepath.Join(tmp, "myproj")
	sub := filepath.Join(realCWD, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	encoded := strings.ReplaceAll(realCWD, "/", "-")
	projDir := filepath.Join(tmp, "projects", encoded)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir projDir: %v", err)
	}

	// First entry carries session-start cwd; many padded entries follow
	// with the post-`cd` cwd so that the tail window does not reach the
	// first entry.
	var lines []string
	lines = append(lines,
		`{"type":"user","timestamp":"2024-01-01T10:00:00Z","cwd":"`+realCWD+`","message":{"role":"user","content":"first"}}`)
	pad := strings.Repeat("X", 1000)
	for range 200 {
		lines = append(lines,
			`{"type":"user","timestamp":"2024-01-01T10:05:00Z","cwd":"`+sub+`","message":{"role":"user","content":"`+pad+`"}}`)
	}
	body := strings.Join(lines, "\n") + "\n"
	p := writeJSONL(t, projDir, "abc.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.CWD != realCWD {
		t.Errorf("CWD = %q, want %q (start cwd recovered from file head)",
			s.CWD, realCWD)
	}
}

// Fallback for transcripts that have NO cwd field anywhere — neither tail
// nor head can supply one. Use the project dir name decoded back to a
// path, if that path exists on disk.
func TestParseSessionTail_NoCWDAnywhere_UsesDecodedProjectDirGuess(t *testing.T) {
	tmp := t.TempDir()
	realCWD := filepath.Join(tmp, "myproj")
	if err := os.MkdirAll(realCWD, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	encoded := strings.ReplaceAll(realCWD, "/", "-")
	projDir := filepath.Join(tmp, "projects", encoded)
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir projDir: %v", err)
	}
	body := `{"type":"user","timestamp":"2024-01-01T10:00:00Z","message":{"role":"user","content":"hi"}}` + "\n"
	p := writeJSONL(t, projDir, "abc.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.CWD != realCWD {
		t.Errorf("CWD = %q, want %q (decoded project dir guess)", s.CWD, realCWD)
	}
}

// Fallback: when no recorded cwd encodes to the project dir name (legacy /
// renamed / broken transcripts), use the chronologically earliest cwd —
// that's the session-start cwd and the most likely candidate for the
// directory the JSONL was written into.
func TestParseSessionTail_NoMatchingCWD_FallsBackToEarliest(t *testing.T) {
	tmp := t.TempDir()
	projDir := filepath.Join(tmp, "projects", "-name-does-not-match")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatalf("mkdir projDir: %v", err)
	}
	startCWD := filepath.Join(tmp, "start")
	laterCWD := filepath.Join(startCWD, "sub")
	if err := os.MkdirAll(laterCWD, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	body := strings.Join([]string{
		`{"type":"user","timestamp":"2024-01-01T10:00:00Z","cwd":"` + startCWD + `","message":{"role":"user","content":"first"}}`,
		`{"type":"user","timestamp":"2024-01-01T10:05:00Z","cwd":"` + laterCWD + `","message":{"role":"user","content":"later"}}`,
	}, "\n") + "\n"
	p := writeJSONL(t, projDir, "abc.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if s.CWD != startCWD {
		t.Errorf("CWD = %q, want %q (earliest cwd as fallback)",
			s.CWD, startCWD)
	}
}

// B-10: the dir-name fallback turns "-Users-bob-proj" into "/Users/bob/proj",
// but a real cwd that contained a hyphen (e.g. "/home/foo-bar/proj") would be
// decoded back to a different path ("/home/foo/bar/proj"). To avoid silently
// producing a wrong-but-plausible cwd, the parser now refuses the fallback
// unless it points at a real directory and marks the session CWDUnknown.
func TestParseSessionTail_MarksCWDUnknownWhenFallbackPathMissing(t *testing.T) {
	tmp := t.TempDir()
	encodedProj := filepath.Join(tmp, "-Users-bob-proj")
	if err := os.MkdirAll(encodedProj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"type":"user","timestamp":"2024-05-26T11:00:00Z","message":{"role":"user","content":"x"}}` + "\n"
	p := writeJSONL(t, encodedProj, "a.jsonl", body)

	s, err := ParseSessionTail(p, TailReadBytes)
	if err != nil || s == nil {
		t.Fatalf("ParseSessionTail: %v, s=%v", err, s)
	}
	if !s.CWDUnknown {
		t.Error("CWDUnknown = false, want true")
	}
	if s.CWD != "" {
		t.Errorf("CWD = %q, want empty", s.CWD)
	}
	if s.CWDExists {
		t.Error("CWDExists = true, want false")
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
		// B-6: ESC, BEL, DEL and other control chars are replaced with
		// space so fzf cannot interpret them. The leftover ANSI parameter
		// bytes (`[31m`) are harmless plain text.
		{"\x1b[31mRED\x1b[0m", "[31mRED [0m"},
		{"bel\x07inside", "bel inside"},
		{"trailing\x7f", "trailing"},
		{"with\x00null", "with null"},
	}
	for _, c := range cases {
		got := SanitizeLabel(c.in)
		if got != c.want {
			t.Errorf("SanitizeLabel(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeLabel_TruncatesAtLabelMaxLen(t *testing.T) {
	in := strings.Repeat("a", LabelMaxLen+10)
	got := SanitizeLabel(in)
	if len([]rune(got)) != LabelMaxLen {
		t.Errorf("rune length = %d, want %d", len([]rune(got)), LabelMaxLen)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected truncation marker, got %q", got)
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
	// B-1: whitespace-only candidates must be skipped.
	if got := firstNonEmpty("   ", "\n\t", "real"); got != "real" {
		t.Errorf("whitespace skip: got %q, want real", got)
	}
	if got := firstNonEmpty("   ", "\n"); got != "" {
		t.Errorf("all-whitespace should be empty: got %q", got)
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
	if !timefmt.Parse("").IsZero() {
		t.Error("empty should be zero")
	}
	if !timefmt.Parse("not-a-time").IsZero() {
		t.Error("garbage should be zero")
	}
	want := time.Date(2026, 5, 26, 11, 0, 0, 0, time.UTC)
	if got := timefmt.Parse("2026-05-26T11:00:00Z"); !got.Equal(want) {
		t.Errorf("RFC3339: got %v, want %v", got, want)
	}
	wantNano := time.Date(2026, 5, 26, 11, 0, 0, 123456789, time.UTC)
	if got := timefmt.Parse("2026-05-26T11:00:00.123456789Z"); !got.Equal(wantNano) {
		t.Errorf("RFC3339Nano: got %v, want %v", got, wantNano)
	}
}
