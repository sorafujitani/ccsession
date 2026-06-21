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
	"github.com/sorafujitani/ccsession/internal/codex"
	"github.com/sorafujitani/ccsession/internal/opencode"
	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/source"
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

func TestRender_DefaultsToNoColorForNonTTYOutput(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","timestamp":"2026-05-26T10:00:00Z","message":{"role":"user","content":"hello world"}}` + "\n"
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
	if err := render(s, &buf, Options{Query: "hello", Out: &buf}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("non-TTY preview leaked ANSI: %q", buf.String())
	}
}

func TestRender_ColorAlwaysEnablesANSI(t *testing.T) {
	tmp := t.TempDir()
	body := `{"type":"user","timestamp":"2026-05-26T10:00:00Z","message":{"role":"user","content":"hello world"}}` + "\n"
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
	if err := render(s, &buf, Options{Query: "hello", Color: "always"}); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(buf.String(), ansi.Highlight+"hello"+ansi.Reset) {
		t.Fatalf("color=always did not highlight query: %q", buf.String())
	}
}

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

func TestRenderJSONFrom_StructuredBoundedPreview(t *testing.T) {
	tmp := t.TempDir()
	long := strings.Repeat("x", maxBodyLen+20)
	body := strings.Join([]string{
		`{"type":"user","timestamp":"2026-05-26T10:00:00Z","message":{"role":"user","content":"hello world"}}`,
		`{"type":"assistant","timestamp":"2026-05-26T10:00:05Z","message":{"role":"assistant","content":"` + long + `"}}`,
	}, "\n") + "\n"
	jsonl := filepath.Join(tmp, "a.jsonl")
	if err := os.WriteFile(jsonl, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := &session.Session{
		ID:        "abc",
		Source:    "claude",
		JSONLPath: jsonl,
		CWD:       tmp,
		CWDExists: true,
		Label:     "preview label",
		LastTime:  time.Date(2026, 5, 26, 10, 0, 5, 0, time.UTC),
	}

	var buf bytes.Buffer
	if err := renderJSONFrom(fakePreviewSource{}, s, &buf, Options{}); err != nil {
		t.Fatalf("renderJSONFrom: %v", err)
	}
	if strings.Contains(buf.String(), "\x1b[") {
		t.Fatalf("JSON preview leaked ANSI: %q", buf.String())
	}

	var got JSONPreview
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, buf.String())
	}
	if got.Source != "claude" || got.ID != "abc" || got.CWD != tmp || got.Label != "preview label" {
		t.Fatalf("metadata = %+v", got)
	}
	if got.Locator == "" {
		t.Fatal("locator is empty")
	}
	if got.StartedAt != "2026-05-26T10:00:00Z" || got.LastActivity != "2026-05-26T10:00:05Z" {
		t.Fatalf("times = started:%q last:%q", got.StartedAt, got.LastActivity)
	}
	if got.TotalMessages != 2 || len(got.Messages) != 2 {
		t.Fatalf("messages = total:%d len:%d", got.TotalMessages, len(got.Messages))
	}
	if got.Messages[0].Role != "user" || got.Messages[0].Body != "hello world" {
		t.Fatalf("first message = %+v", got.Messages[0])
	}
	if got.Messages[1].Role != "assistant" || !strings.HasSuffix(got.Messages[1].Body, "…") {
		t.Fatalf("second message should be truncated assistant body: %+v", got.Messages[1])
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

func TestRender_UsesPreviewMessageEnv(t *testing.T) {
	t.Setenv(EnvMessages, "2")
	tmp := t.TempDir()
	lines := []string{
		`{"type":"user","timestamp":"2026-05-26T10:00:00Z","message":{"role":"user","content":"msg0"}}`,
		`{"type":"user","timestamp":"2026-05-26T10:01:00Z","message":{"role":"user","content":"msg1"}}`,
		`{"type":"user","timestamp":"2026-05-26T10:02:00Z","message":{"role":"user","content":"msg2"}}`,
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
		LastTime:  time.Date(2026, 5, 26, 10, 2, 0, 0, time.UTC),
	}
	var buf bytes.Buffer
	if err := render(s, &buf, Options{}); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "msg0") || !strings.Contains(out, "msg1") || !strings.Contains(out, "msg2") {
		t.Fatalf("expected env limit to keep only msg1/msg2, got %q", out)
	}
	if !strings.Contains(out, "(3 msgs)") {
		t.Fatalf("expected total count to stay 3, got %q", out)
	}
}

func TestMessageLimitInvalidEnvFallsBackToDefault(t *testing.T) {
	t.Setenv(EnvMessages, "nope")
	if got := messageLimit(Options{}); got != maxMessages {
		t.Fatalf("messageLimit invalid env = %d, want %d", got, maxMessages)
	}
	t.Setenv(EnvMessages, "0")
	if got := messageLimit(Options{}); got != maxMessages {
		t.Fatalf("messageLimit zero env = %d, want %d", got, maxMessages)
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

func TestReadJSONLLine_KeepsLinesLargerThanReaderBuffer(t *testing.T) {
	cap := 128
	long := strings.Repeat("X", 96)
	r := bufio.NewReaderSize(strings.NewReader(long+"\n"), 32)

	line, err := readJSONLLine(r, cap)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if line != long {
		t.Errorf("line length = %d, want %d", len(line), len(long))
	}
}

func TestHighlightMatches_BasicWrapsMatch(t *testing.T) {
	got := highlightMatches("fix the login bug", Options{Query: "login", Color: "always"})
	want := "fix the " + ansi.Highlight + "login" + ansi.Reset + " bug"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHighlightMatches_CaseInsensitive(t *testing.T) {
	got := highlightMatches("the Login flow", Options{Query: "login", Color: "always"})
	if !strings.Contains(got, ansi.Highlight+"Login"+ansi.Reset) {
		t.Errorf("expected original-case match highlighted, got %q", got)
	}
}

func TestHighlightMatches_MultipleMatches(t *testing.T) {
	got := highlightMatches("foo and foo", Options{Query: "foo", Color: "always"})
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
	got := highlightMatches("a.b", Options{Query: "a.b", Color: "always"})
	if !strings.Contains(got, ansi.Highlight+"a.b"+ansi.Reset) {
		t.Errorf("expected literal a.b highlighted, got %q", got)
	}
}

func TestHighlightMatches_RegexMode(t *testing.T) {
	got := highlightMatches("axb", Options{Query: "a.b", Regex: true, Color: "always"})
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
	if err := render(s, &buf, Options{Query: "login", Color: "always"}); err != nil {
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

// The OpenCode source must satisfy the messageSource seam, or the preview
// silently falls back to the JSONL path (which OpenCode has none of). This
// guards against a signature drift between the two independent declarations.
func TestOpencodeSourceSatisfiesMessageSeam(t *testing.T) {
	db := filepath.Join(t.TempDir(), "opencode.db")
	if err := os.WriteFile(db, nil, 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	t.Setenv(opencode.EnvDBPath, db)
	t.Setenv(source.EnvVar, "opencode")

	src, err := source.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if _, ok := src.(messageSource); !ok {
		t.Fatal("opencode source no longer satisfies messageSource; preview would fall back to the JSONL path")
	}
}

func TestCodexSourceRendersThroughMessageSeam(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	id := "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	dir := filepath.Join(home, "sessions", "2026", "06", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"codex preview body"}]}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"assistant reply"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-06-14T00-00-00-"+id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(codex.EnvHome, home)
	t.Setenv(source.EnvVar, "codex")

	src, err := source.FromEnv()
	if err != nil {
		t.Fatalf("FromEnv: %v", err)
	}
	if _, ok := src.(messageSource); !ok {
		t.Fatal("codex source no longer satisfies messageSource; preview would fall back to the Claude JSONL parser")
	}
	s, err := src.FindByID(id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	var buf bytes.Buffer
	if err := renderFrom(src, s, &buf, Options{}); err != nil {
		t.Fatalf("renderFrom: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "codex preview body") || !strings.Contains(out, "assistant reply") {
		t.Fatalf("rendered preview missing Codex messages: %q", out)
	}
}

func BenchmarkRenderJSONLargeTranscript(b *testing.B) {
	s := benchmarkPreviewSession(b, 1000)
	b.ResetTimer()
	for range b.N {
		if err := renderJSONFrom(fakePreviewSource{}, s, ioDiscard{}, Options{}); err != nil {
			b.Fatalf("renderJSONFrom: %v", err)
		}
	}
}

func BenchmarkRenderTextLargeTranscript(b *testing.B) {
	s := benchmarkPreviewSession(b, 1000)
	b.ResetTimer()
	for range b.N {
		if err := render(s, ioDiscard{}, Options{}); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}

func benchmarkPreviewSession(b *testing.B, messages int) *session.Session {
	b.Helper()
	tmp := b.TempDir()
	jsonl := filepath.Join(tmp, "bench.jsonl")
	var body strings.Builder
	base := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	for i := range messages {
		ts := base.Add(time.Duration(i) * time.Second).Format(time.RFC3339)
		body.WriteString(`{"type":"user","timestamp":"` + ts + `","message":{"role":"user","content":"message ` + itoa(i) + ` ` + strings.Repeat("x", 256) + `"}}` + "\n")
	}
	if err := os.WriteFile(jsonl, []byte(body.String()), 0o644); err != nil {
		b.Fatalf("write: %v", err)
	}
	return &session.Session{
		ID:        "bench",
		Source:    "claude",
		JSONLPath: jsonl,
		CWD:       tmp,
		CWDExists: true,
		LastTime:  base.Add(time.Duration(messages-1) * time.Second),
	}
}

type fakePreviewSource struct{}

func (fakePreviewSource) Name() string { return "claude" }
func (fakePreviewSource) Scan() ([]*session.Session, error) {
	return nil, nil
}
func (fakePreviewSource) ScanFiltered(map[string]struct{}) ([]*session.Session, error) {
	return nil, nil
}
func (fakePreviewSource) FindByID(string) (*session.Session, error) {
	return nil, session.ErrSessionFileMissing
}
func (fakePreviewSource) GrepKeys(string, bool) (map[string]struct{}, error) {
	return nil, nil
}
func (fakePreviewSource) ResumeSpec(*session.Session) (string, []string, error) {
	return "claude", nil, nil
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
