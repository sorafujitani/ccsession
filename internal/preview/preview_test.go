package preview

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/ansi"
	"github.com/sorafujitani/ccsession/internal/session"
)

func TestExtractText_String(t *testing.T) {
	raw, _ := json.Marshal("hello")
	if got := session.ExtractText(raw, "\n"); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestExtractText_Blocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"foo"},{"type":"image"},{"type":"text","text":"bar"}]`)
	got := session.ExtractText(raw, "\n")
	if got != "foo\nbar" {
		t.Errorf("got %q, want %q", got, "foo\nbar")
	}
}

func TestExtractText_Empty(t *testing.T) {
	if got := session.ExtractText(nil, "\n"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := session.ExtractText(json.RawMessage(``), "\n"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestTruncateBody_TakesFirstTwoNonEmptyLinesJoined(t *testing.T) {
	in := "  \nfirst line\n\nsecond line\nthird line\n"
	got := truncateBody(in)
	if got != "first line | second line" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateBody_StripsCR(t *testing.T) {
	got := truncateBody("a\r\nb")
	if got != "a | b" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateBody_LongBodyTruncated(t *testing.T) {
	in := strings.Repeat("あ", maxBodyLen+50)
	got := truncateBody(in)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis, got %q", got)
	}
	if len([]rune(got)) != maxBodyLen {
		t.Errorf("rune length = %d, want %d", len([]rune(got)), maxBodyLen)
	}
}

func TestRender_HeaderAndMessages(t *testing.T) {
	tmp := t.TempDir()
	body := strings.Join([]string{
		`{"type":"user","timestamp":"2026-05-26T10:00:00Z","message":{"role":"user","content":"hello world"}}`,
		`{"type":"assistant","timestamp":"2026-05-26T10:00:05Z","message":{"role":"assistant","content":"hi there"}}`,
	}, "\n") + "\n"
	jsonl := filepath.Join(tmp, "a.jsonl")
	if err := os.WriteFile(jsonl, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := &session.Session{
		ID:        "abc",
		JSONLPath: jsonl,
		CWD:       tmp,
		CWDExists: true,
		LastTime:  time.Date(2026, 5, 26, 10, 0, 5, 0, time.UTC),
	}

	var buf bytes.Buffer
	if err := render(s, &buf, Options{}); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "session") || !strings.Contains(out, "abc") {
		t.Errorf("missing session header: %q", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("missing user body: %q", out)
	}
	if !strings.Contains(out, "hi there") {
		t.Errorf("missing assistant body: %q", out)
	}
	if !strings.Contains(out, "[user") {
		t.Errorf("expected [user ...] role marker: %q", out)
	}
	if !strings.Contains(out, "[asst") {
		t.Errorf("expected [asst ...] role marker: %q", out)
	}
}

func TestRender_GoneCWDMarker(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","timestamp":"2026-05-26T10:00:00Z","message":{"role":"user","content":"x"}}` + "\n"
	jsonl := filepath.Join(tmp, "a.jsonl")
	if err := os.WriteFile(jsonl, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &session.Session{
		ID:        "abc",
		JSONLPath: jsonl,
		CWD:       "/no/such/path",
		CWDExists: false,
		LastTime:  time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := render(s, &buf, Options{}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), "[gone]") {
		t.Errorf("expected [gone] marker, got %q", buf.String())
	}
}

func TestRender_CapsMessagesToMaxMessages(t *testing.T) {
	tmp := t.TempDir()
	var lines []string
	base := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	for i := range maxMessages + 5 {
		ts := base.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
		lines = append(lines,
			`{"type":"user","timestamp":"`+ts+`","message":{"role":"user","content":"msg`+itoa(i)+`"}}`)
	}
	jsonl := filepath.Join(tmp, "a.jsonl")
	if err := os.WriteFile(jsonl, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &session.Session{
		ID:        "abc",
		JSONLPath: jsonl,
		CWD:       tmp,
		CWDExists: true,
		LastTime:  base.Add(time.Duration(len(lines)) * time.Minute),
	}
	var buf bytes.Buffer
	if err := render(s, &buf, Options{}); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	// First message (msg0) should have been dropped — only the tail is kept.
	if strings.Contains(out, "msg0\n") || strings.Contains(out, "msg0 ") {
		t.Errorf("expected msg0 to be dropped (older than tail), got %q", out)
	}
	last := "msg" + itoa(len(lines)-1)
	if !strings.Contains(out, last) {
		t.Errorf("expected last %s in output, got %q", last, out)
	}
	// Total msgs counter should reflect ALL messages, not just rendered tail.
	if !strings.Contains(out, "("+itoa(len(lines))+" msgs)") {
		t.Errorf("expected total msg count %d in header, got %q", len(lines), out)
	}
}

// B-4: a user/assistant message with no timestamp should render as "--:--"
// instead of "00:00", which is indistinguishable from real midnight-UTC.
func TestRender_ZeroTimestampShowsDashes(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","message":{"role":"user","content":"no ts here"}}` + "\n"
	jsonl := filepath.Join(tmp, "a.jsonl")
	if err := os.WriteFile(jsonl, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &session.Session{
		ID:        "abc",
		JSONLPath: jsonl,
		CWD:       tmp,
		CWDExists: true,
		LastTime:  time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := render(s, &buf, Options{}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), "--:--") {
		t.Errorf("expected --:-- for zero timestamp, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "[user 00:00]") {
		t.Errorf("expected --:-- instead of 00:00, got %q", buf.String())
	}
}

// B-5: a future-dated session header must not say "(just now)" — the list
// view's clamp is appropriate there but produces a contradictory header
// in preview when the absolute date is also shown.
func TestRender_FutureSessionHeaderSaysFuture(t *testing.T) {
	tmp := t.TempDir()
	future := `{"type":"user","timestamp":"2099-01-01T00:00:00Z","message":{"role":"user","content":"hi"}}` + "\n"
	jsonl := filepath.Join(tmp, "a.jsonl")
	if err := os.WriteFile(jsonl, []byte(future), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &session.Session{
		ID:        "abc",
		JSONLPath: jsonl,
		CWD:       tmp,
		CWDExists: true,
		LastTime:  time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := render(s, &buf, Options{}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), "in the future") {
		t.Errorf("expected 'in the future' in header, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "just now") {
		t.Errorf("did not expect 'just now' for a future session, got %q", buf.String())
	}
}

// B-11: a line longer than the byte cap used to be returned as a
// bufio.Scanner error that aborted the whole scan, leaving the preview
// half-rendered. The Reader-based loader skips the oversize line and
// keeps reading.
func TestReadJSONLLine_SkipsOversizeLinesAndContinues(t *testing.T) {
	cap := 64
	huge := strings.Repeat("X", cap*3)
	body := "first\n" + huge + "\nlast\n"
	r := bufio.NewReaderSize(strings.NewReader(body), 32)

	line, err := readJSONLLine(r, cap)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if line != "first" {
		t.Errorf("first = %q, want %q", line, "first")
	}

	// The oversize line is skipped (returns "" and no terminal error).
	line, err = readJSONLLine(r, cap)
	if err != nil {
		t.Fatalf("oversize read: %v", err)
	}
	if line != "" {
		t.Errorf("oversize should be empty, got %q", line)
	}

	line, err = readJSONLLine(r, cap)
	if err != nil {
		t.Fatalf("last read: %v", err)
	}
	if line != "last" {
		t.Errorf("last = %q, want %q", line, "last")
	}
}

func TestHighlightMatches_BasicWrapsMatch(t *testing.T) {
	got := highlightMatches("fix the login bug", Options{Query: "login"})
	want := "fix the " + ansi.Highlight + "login" + ansi.Reset + " bug"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHighlightMatches_CaseInsensitive(t *testing.T) {
	got := highlightMatches("the Login flow", Options{Query: "login"})
	if !strings.Contains(got, ansi.Highlight+"Login"+ansi.Reset) {
		t.Errorf("expected original-case match highlighted, got %q", got)
	}
}

func TestHighlightMatches_MultipleMatches(t *testing.T) {
	got := highlightMatches("foo and foo", Options{Query: "foo"})
	if strings.Count(got, ansi.Highlight) != 2 {
		t.Errorf("expected 2 highlights, got %q", got)
	}
}

func TestHighlightMatches_NoMatchOrEmptyReturnsInput(t *testing.T) {
	if got := highlightMatches("hello", Options{Query: "zzz"}); got != "hello" {
		t.Errorf("no match should return input, got %q", got)
	}
	if got := highlightMatches("hello", Options{Query: ""}); got != "hello" {
		t.Errorf("empty query should return input, got %q", got)
	}
	if got := highlightMatches("hello", Options{Query: "   "}); got != "hello" {
		t.Errorf("whitespace query should return input, got %q", got)
	}
}

func TestHighlightMatches_FixedStringTreatsMetacharsLiterally(t *testing.T) {
	// "a.b" must match the literal "a.b", not "axb".
	if got := highlightMatches("axb", Options{Query: "a.b"}); got != "axb" {
		t.Errorf("metachar should be literal: got %q", got)
	}
	got := highlightMatches("a.b", Options{Query: "a.b"})
	if !strings.Contains(got, ansi.Highlight+"a.b"+ansi.Reset) {
		t.Errorf("expected literal a.b highlighted, got %q", got)
	}
}

func TestHighlightMatches_RegexMode(t *testing.T) {
	got := highlightMatches("axb", Options{Query: "a.b", Regex: true})
	if !strings.Contains(got, ansi.Highlight+"axb"+ansi.Reset) {
		t.Errorf("expected regex match, got %q", got)
	}
}

func TestHighlightMatches_InvalidRegexReturnsInput(t *testing.T) {
	if got := highlightMatches("hello", Options{Query: "(", Regex: true}); got != "hello" {
		t.Errorf("invalid regex should return input, got %q", got)
	}
}

func TestRender_HighlightsQueryInBody(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","timestamp":"2026-05-26T10:00:00Z","message":{"role":"user","content":"please fix the login flow"}}` + "\n"
	jsonl := filepath.Join(tmp, "a.jsonl")
	if err := os.WriteFile(jsonl, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &session.Session{
		ID:        "abc",
		JSONLPath: jsonl,
		CWD:       tmp,
		CWDExists: true,
		LastTime:  time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := render(s, &buf, Options{Query: "login"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), ansi.Highlight+"login"+ansi.Reset) {
		t.Errorf("expected highlighted query in body, got %q", buf.String())
	}
}

// itoa is a tiny strconv.Itoa stand-in to keep imports minimal.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
