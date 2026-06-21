package grok

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

func TestScanReadsSummaryLayout(t *testing.T) {
	home, cwd, id := fixture(t)
	store := OpenAt(home)

	ss, err := store.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("Scan returned %d sessions, want 1", len(ss))
	}
	got := ss[0]
	if got.ID != id {
		t.Errorf("ID = %q, want %q", got.ID, id)
	}
	if got.CWD != cwd || !got.CWDExists {
		t.Errorf("cwd = %q exists=%v, want %q exists=true", got.CWD, got.CWDExists, cwd)
	}
	if got.Label != "Title from summary" {
		t.Errorf("Label = %q, want summary title", got.Label)
	}
	if got.LastEpoch == 0 {
		t.Error("LastEpoch = 0, want parsed updated_at")
	}
}

func TestFindByIDAndMessages(t *testing.T) {
	home, _, id := fixture(t)
	store := OpenAt(home)

	s, err := store.FindByID(id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if s.ID != id {
		t.Fatalf("FindByID returned %q, want %q", s.ID, id)
	}
	msgs, _, total, err := store.Messages(id, 1)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if len(msgs) != 1 || msgs[0].Role != "assistant" || msgs[0].Body != "assistant answer" {
		t.Fatalf("limited messages = %#v, want last assistant answer", msgs)
	}
	if msgs[0].Timestamp.IsZero() {
		t.Fatal("message timestamp is zero, want timestamp from updates.jsonl")
	}
}

func TestGrepKeysFeedScanFiltered(t *testing.T) {
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	home, _, id := fixture(t)
	store := OpenAt(home)

	keys, err := store.GrepKeys("unique needle", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if _, ok := keys[id]; !ok {
		t.Fatalf("GrepKeys did not include %s: %#v", id, keys)
	}
	ss, err := store.ScanFiltered(keys)
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	if len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("ScanFiltered = %#v, want only %s", ss, id)
	}
}

func TestScanSkipsBadSummaries(t *testing.T) {
	home, cwd, id := fixture(t)
	badDir := filepath.Join(home, "sessions", url.PathEscape(cwd), "bad\tid")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "summary.json"), []byte(`{"info":{"id":"bad\tid","cwd":"`+cwd+`"}}`), 0o644); err != nil {
		t.Fatalf("write corrupt summary: %v", err)
	}
	brokenDir := filepath.Join(home, "sessions", url.PathEscape("/tmp/broken"))
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "summary.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write broken summary: %v", err)
	}

	ss, err := OpenAt(home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("Scan = %#v, want only valid session %s", ss, id)
	}
}

func TestFindByIDSkipsCorruptSummary(t *testing.T) {
	home, cwd, id := fixture(t)
	badDir := filepath.Join(home, "sessions", url.PathEscape(cwd), "bad\tid")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "summary.json"), []byte(`{"info":{"id":"bad\tid","cwd":"`+cwd+`"}}`), 0o644); err != nil {
		t.Fatalf("write corrupt summary: %v", err)
	}

	s, err := OpenAt(home).FindByID(id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if s.ID != id {
		t.Fatalf("FindByID returned %q, want %q", s.ID, id)
	}
}

func TestScanSinksFutureUpdatedAt(t *testing.T) {
	home := t.TempDir()
	writeSummaryOnly(t, home, "past", "/tmp/past", "2020-01-01T00:00:00Z")
	writeSummaryOnly(t, home, "future", "/tmp/future", "2099-01-01T00:00:00Z")

	ss, err := OpenAt(home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("Scan returned %d sessions, want 2", len(ss))
	}
	if ss[0].ID != "past" || ss[1].ID != "future" {
		t.Fatalf("order = %s, %s; want past before future", ss[0].ID, ss[1].ID)
	}
}

func TestScanReadsSummariesInParallel(t *testing.T) {
	home := t.TempDir()
	for i := range 12 {
		id := fmt.Sprintf("parallel-%02d", i)
		writeSummaryOnly(t, home, id, "/tmp/"+id, "2026-06-13T14:04:51.248996Z")
	}
	orig := readSummaryFile
	var active atomic.Int32
	var maxActive atomic.Int32
	readSummaryFile = func(s *Store, path string) (*session.Session, error) {
		current := active.Add(1)
		for {
			maxSeen := maxActive.Load()
			if current <= maxSeen || maxActive.CompareAndSwap(maxSeen, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		defer active.Add(-1)
		return s.readSummary(path)
	}
	t.Cleanup(func() {
		readSummaryFile = orig
	})

	ss, err := OpenAt(home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 12 {
		t.Fatalf("Scan returned %d sessions, want 12", len(ss))
	}
	if maxActive.Load() < 2 {
		t.Fatalf("max concurrent summary reads = %d, want at least 2", maxActive.Load())
	}
}

func TestReadJSONLLineSkipsOversizeLine(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(strings.Repeat("x", 10) + "\n" + `{"ok":true}` + "\n"))

	line, err := readJSONLLine(r, 4)
	if err != nil {
		t.Fatalf("first read err: %v", err)
	}
	if line != nil {
		t.Fatalf("oversize line = %q, want nil", line)
	}
	line, err = readJSONLLine(r, 4*1024)
	if err != nil {
		t.Fatalf("second read err: %v", err)
	}
	if string(line) != `{"ok":true}` {
		t.Fatalf("second line = %q, want JSON line", line)
	}
}

func TestResolveHomeHonorsGrokHome(t *testing.T) {
	want := t.TempDir()
	t.Setenv(EnvHome, want)
	got, err := ResolveHome()
	if err != nil {
		t.Fatalf("ResolveHome: %v", err)
	}
	if got != want {
		t.Errorf("ResolveHome = %q, want %q", got, want)
	}
}

func writeSummaryOnly(t testing.TB, home, id, cwd, updatedAt string) {
	t.Helper()
	dir := filepath.Join(home, "sessions", url.PathEscape(cwd), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summary := `{
  "info": {"id": "` + id + `", "cwd": "` + cwd + `"},
  "session_summary": "` + id + `",
  "created_at": "2020-01-01T00:00:00Z",
  "updated_at": "` + updatedAt + `"
}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
}

func fixture(t *testing.T) (home, cwd, id string) {
	t.Helper()
	home = t.TempDir()
	cwd = t.TempDir()
	id = "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	group := filepath.Join(home, "sessions", url.PathEscape(cwd))
	dir := filepath.Join(group, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	summary := `{
  "info": {"id": "` + id + `", "cwd": "` + cwd + `"},
  "session_summary": "Summary fallback",
  "generated_title": "Title from summary",
  "created_at": "2026-06-13T14:04:50.330068Z",
  "updated_at": "2026-06-13T14:04:51.248996Z"
}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		t.Fatalf("write summary: %v", err)
	}
	chat := `{"type":"system","content":"ignore me"}` + "\n" +
		`{"type":"user","content":[{"type":"text","text":"hello unique needle"}]}` + "\n" +
		`{"type":"assistant","content":"fallback answer"}` + "\n" +
		`{"type":"tool_result","content":"ignore tool"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "chat_history.jsonl"), []byte(chat), 0o644); err != nil {
		t.Fatalf("write chat: %v", err)
	}
	updates := `{"timestamp":1780068196,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"hello unique needle"}}}}` + "\n" +
		`{"timestamp":1780068197,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"assistant "}}}}` + "\n" +
		`{"timestamp":1780068198,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"answer"}}}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(updates), 0o644); err != nil {
		t.Fatalf("write updates: %v", err)
	}
	return home, cwd, id
}

func BenchmarkScanManySessions(b *testing.B) {
	home := b.TempDir()
	for i := range 512 {
		id := fmt.Sprintf("bench-%04d", i)
		writeSummaryOnly(b, home, id, "/tmp/"+id, "2026-06-13T14:04:51.248996Z")
	}
	store := OpenAt(home)
	b.ResetTimer()
	for range b.N {
		ss, err := store.Scan()
		if err != nil {
			b.Fatalf("Scan: %v", err)
		}
		if len(ss) != 512 {
			b.Fatalf("Scan returned %d sessions, want 512", len(ss))
		}
	}
}

func BenchmarkGrepKeysHitAndMiss(b *testing.B) {
	home := b.TempDir()
	cache := b.TempDir()
	b.Setenv(grep.EnvCacheDir, cache)
	for i := range 512 {
		body := "ordinary body"
		if i%32 == 0 {
			body = "needle body"
		}
		writeBenchmarkSession(b, home, fmt.Sprintf("bench-grep-%04d", i), body, 1)
	}
	store := OpenAt(home)
	for _, query := range []string{"needle", "not-present"} {
		if _, err := store.GrepKeys(query, false); err != nil {
			b.Fatalf("prewarm GrepKeys: %v", err)
		}
	}

	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "hit", query: "needle"},
		{name: "miss", query: "not-present"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			for range b.N {
				keys, err := store.GrepKeys(tc.query, false)
				if err != nil {
					b.Fatalf("GrepKeys: %v", err)
				}
				if keys == nil {
					b.Fatal("GrepKeys returned nil set")
				}
			}
		})
	}
}

func BenchmarkMessagesLargeSession(b *testing.B) {
	home := b.TempDir()
	id := "bench-messages"
	writeBenchmarkSession(b, home, id, "message", 1000)
	store := OpenAt(home)

	b.ResetTimer()
	for range b.N {
		msgs, _, total, err := store.Messages(id, 30)
		if err != nil {
			b.Fatalf("Messages: %v", err)
		}
		if total != 1000 || len(msgs) != 30 {
			b.Fatalf("Messages returned total=%d len=%d, want 1000/30", total, len(msgs))
		}
	}
}

func writeBenchmarkSession(b *testing.B, home, id, body string, messages int) {
	b.Helper()
	cwd := filepath.Join("/tmp", id)
	dir := filepath.Join(home, "sessions", url.PathEscape(cwd), id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatalf("mkdir: %v", err)
	}
	summary := `{
  "info": {"id": "` + id + `", "cwd": "` + cwd + `"},
  "session_summary": "` + id + `",
  "created_at": "2026-06-13T14:04:50.330068Z",
  "updated_at": "2026-06-13T14:04:51.248996Z"
}`
	if err := os.WriteFile(filepath.Join(dir, "summary.json"), []byte(summary), 0o644); err != nil {
		b.Fatalf("write summary: %v", err)
	}
	var updates strings.Builder
	for i := range messages {
		updates.WriteString(`{"timestamp":1780068196,"method":"session/update","params":{"sessionId":"` + id + `","update":{"sessionUpdate":"user_message_chunk","content":{"type":"text","text":"` + body + ` ` + fmt.Sprint(i) + `"}}}}` + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "updates.jsonl"), []byte(updates.String()), 0o644); err != nil {
		b.Fatalf("write updates: %v", err)
	}
}
