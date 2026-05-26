package preview

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
)

func TestExtractText_String(t *testing.T) {
	raw, _ := json.Marshal("hello")
	if got := extractText(raw); got != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestExtractText_Blocks(t *testing.T) {
	raw := json.RawMessage(`[{"type":"text","text":"foo"},{"type":"image"},{"type":"text","text":"bar"}]`)
	got := extractText(raw)
	if got != "foo\nbar" {
		t.Errorf("got %q, want %q", got, "foo\nbar")
	}
}

func TestExtractText_Empty(t *testing.T) {
	if got := extractText(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := extractText(json.RawMessage(``)); got != "" {
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
	if err := render(s, &buf); err != nil {
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
	if err := render(s, &buf); err != nil {
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
	if err := render(s, &buf); err != nil {
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
	if err := render(s, &buf); err != nil {
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
	if err := render(s, &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), "in the future") {
		t.Errorf("expected 'in the future' in header, got %q", buf.String())
	}
	if strings.Contains(buf.String(), "just now") {
		t.Errorf("did not expect 'just now' for a future session, got %q", buf.String())
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
