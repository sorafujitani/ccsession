package codex

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

func TestScanReadsCodexSessionLayout(t *testing.T) {
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
	if got.Label != "last user asks about unique needle" {
		t.Errorf("Label = %q, want last user prompt", got.Label)
	}
	if got.LastEpoch == 0 {
		t.Error("LastEpoch = 0, want parsed timestamp")
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
	msgs, startedAt, total, err := store.Messages(id, 1)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if startedAt.IsZero() {
		t.Fatal("startedAt is zero, want session_meta timestamp")
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" || msgs[0].Body != "last user asks about unique needle" {
		t.Fatalf("limited messages = %#v, want last user message", msgs)
	}
}

func TestGrepKeysFeedScanFiltered(t *testing.T) {
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	home, _, id := fixture(t)
	store := OpenAt(home)

	keys, err := store.GrepKeys("assistant answer", false)
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

func TestScanReusesRepresentativeSessionMetadata(t *testing.T) {
	home, _, _ := fixture(t)
	store := OpenAt(home)
	calls := countParseCalls(t)

	ss, err := store.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("Scan returned %d sessions, want 1", len(ss))
	}
	if calls.Load() != 1 {
		t.Fatalf("parse calls = %d, want 1", calls.Load())
	}
}

func TestGrepKeysReusesRepresentativeSessionMetadataForLabelMatch(t *testing.T) {
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	home, _, id := fixture(t)
	store := OpenAt(home)
	calls := countParseCalls(t)

	keys, err := store.GrepKeys("last user asks", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if _, ok := keys[id]; !ok {
		t.Fatalf("GrepKeys did not include %s: %#v", id, keys)
	}
	if calls.Load() != 1 {
		t.Fatalf("parse calls = %d, want 1", calls.Load())
	}
}

func TestInternalUserContextIsHidden(t *testing.T) {
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	home := t.TempDir()
	cwd := t.TempDir()
	id := "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /repo\n\n<INSTRUCTIONS>\nsecret project rule\n</INSTRUCTIONS>"}]}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# Files mentioned by the user:\n\n## screenshot.png\n\n## My request for Codex:\nactual user request"}]}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"assistant reply"}]}}` + "\n"
	writeSession(t, home, id, body)
	store := OpenAt(home)

	ss, err := store.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("Scan returned %d sessions, want 1", len(ss))
	}
	if ss[0].Label != "actual user request" {
		t.Fatalf("Label = %q, want actual request only", ss[0].Label)
	}
	keys, err := store.GrepKeys("secret project rule", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("internal context matched grep: %#v", keys)
	}
	msgs, _, total, err := store.Messages(id, 30)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want visible user + assistant only", total)
	}
	for _, m := range msgs {
		if strings.Contains(m.Body, "secret project rule") || strings.Contains(m.Body, "AGENTS.md") {
			t.Fatalf("internal context leaked into messages: %#v", msgs)
		}
	}
}

func TestScanSkipsDuplicateIDs(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	id := "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	first := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first copy"}]}}` + "\n"
	second := `{"timestamp":"2026-06-15T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-15T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-15T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second copy"}]}}` + "\n"
	writeSessionNamed(t, home, "2026/06/14/rollout-2026-06-14T00-00-00-"+id+".jsonl", first)
	writeSessionNamed(t, home, "2026/06/15/rollout-2026-06-15T00-00-00-"+id+".jsonl", second)

	ss, err := OpenAt(home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("Scan returned %d sessions, want first duplicate only", len(ss))
	}
	if ss[0].Label != "first copy" {
		t.Fatalf("Label = %q, want first copy", ss[0].Label)
	}
}

func TestScanParsesDuplicateCandidatesOnce(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	id := "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	first := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first copy"}]}}` + "\n"
	second := `{"timestamp":"2026-06-15T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-15T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-15T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second copy"}]}}` + "\n"
	writeSessionNamed(t, home, "2026/06/14/rollout-2026-06-14T00-00-00-"+id+".jsonl", first)
	writeSessionNamed(t, home, "2026/06/15/rollout-2026-06-15T00-00-00-"+id+".jsonl", second)
	calls := countParseCalls(t)

	ss, err := OpenAt(home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].Label != "first copy" {
		t.Fatalf("Scan = %#v, want first representative only", ss)
	}
	if calls.Load() != 2 {
		t.Fatalf("parse calls = %d, want 2", calls.Load())
	}
}

func TestRepresentativeSessionsParsesInParallel(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	for i := range 12 {
		id := fmt.Sprintf("parallel-%02d", i)
		body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
			`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + id + `"}]}}` + "\n"
		writeSessionNamed(t, home, fmt.Sprintf("2026/06/14/%s.jsonl", id), body)
	}
	orig := parseSessionFile
	var active atomic.Int32
	var maxActive atomic.Int32
	parseSessionFile = func(path string, includeMessages bool, messageLimit int) (*session.Session, []session.Message, time.Time, int, error) {
		current := active.Add(1)
		for {
			maxSeen := maxActive.Load()
			if current <= maxSeen || maxActive.CompareAndSwap(maxSeen, current) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		defer active.Add(-1)
		return parseFile(path, includeMessages, messageLimit)
	}
	t.Cleanup(func() {
		parseSessionFile = orig
	})

	ss, err := OpenAt(home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 12 {
		t.Fatalf("Scan returned %d sessions, want 12", len(ss))
	}
	if maxActive.Load() < 2 {
		t.Fatalf("max concurrent parses = %d, want at least 2", maxActive.Load())
	}
}

func TestGrepIgnoresDuplicateIDNonRepresentative(t *testing.T) {
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	home := t.TempDir()
	cwd := t.TempDir()
	id := "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	first := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first copy"}]}}` + "\n"
	second := `{"timestamp":"2026-06-15T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-15T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-15T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"duplicate-only needle"}]}}` + "\n"
	writeSessionNamed(t, home, "2026/06/14/rollout-2026-06-14T00-00-00-"+id+".jsonl", first)
	writeSessionNamed(t, home, "2026/06/15/rollout-2026-06-15T00-00-00-"+id+".jsonl", second)
	store := OpenAt(home)

	keys, err := store.GrepKeys("duplicate-only needle", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("GrepKeys matched non-representative duplicate: %#v", keys)
	}
	ss, err := store.ScanFiltered(keys)
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	if len(ss) != 0 {
		t.Fatalf("ScanFiltered returned non-matching representative: %#v", ss)
	}
}

func TestScanMarksMissingCWDUnknown(t *testing.T) {
	home := t.TempDir()
	id := "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"missing cwd"}]}}` + "\n"
	writeSession(t, home, id, body)

	ss, err := OpenAt(home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("Scan returned %d sessions, want 1", len(ss))
	}
	if !ss[0].CWDUnknown || ss[0].CWDExists {
		t.Fatalf("cwd flags = unknown:%v exists:%v, want unknown true and exists false", ss[0].CWDUnknown, ss[0].CWDExists)
	}
}

func TestScanSkipsBadSessions(t *testing.T) {
	home, _, id := fixture(t)
	badDir := filepath.Join(home, "sessions", "2026", "06", "15")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bad := `{"type":"session_meta","payload":{"id":"bad\tid","cwd":"/tmp"}}` + "\n" +
		`{"timestamp":"2026-06-15T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"bad"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(badDir, "bad.jsonl"), []byte(bad), 0o644); err != nil {
		t.Fatalf("write bad session: %v", err)
	}
	empty := `{"type":"session_meta","payload":{"id":"22222222-2222-2222-2222-222222222222","cwd":"/tmp"}}` + "\n"
	if err := os.WriteFile(filepath.Join(badDir, "rollout-2026-06-15T00-00-00-22222222-2222-2222-2222-222222222222.jsonl"), []byte(empty), 0o644); err != nil {
		t.Fatalf("write empty session: %v", err)
	}

	ss, err := OpenAt(home).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("Scan = %#v, want only valid session %s", ss, id)
	}
}

func TestResolveHomeHonorsCodexHome(t *testing.T) {
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

func fixture(t *testing.T) (home, cwd, id string) {
	t.Helper()
	home = t.TempDir()
	cwd = t.TempDir()
	id = "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	dir := filepath.Join(home, "sessions", "2026", "06", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{not json}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"ignore developer"}]}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:02Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first user prompt"}]}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"assistant answer"}]}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:04Z","type":"event_msg","payload":{"type":"token_count","info":"ignore event"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"last user asks about unique needle"}]}}` + "\n"
	writeSession(t, home, id, body)
	return home, cwd, id
}

func writeSession(t testing.TB, home, id, body string) {
	t.Helper()
	writeSessionNamed(t, home, "2026/06/14/rollout-2026-06-14T00-00-00-"+id+".jsonl", body)
}

func writeSessionNamed(t testing.TB, home, rel, body string) {
	t.Helper()
	path := filepath.Join(home, "sessions", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}

func countParseCalls(t *testing.T) *atomic.Int32 {
	t.Helper()
	orig := parseSessionFile
	var calls atomic.Int32
	parseSessionFile = func(path string, includeMessages bool, messageLimit int) (*session.Session, []session.Message, time.Time, int, error) {
		calls.Add(1)
		return parseFile(path, includeMessages, messageLimit)
	}
	t.Cleanup(func() {
		parseSessionFile = orig
	})
	return &calls
}

func BenchmarkScanManySessions(b *testing.B) {
	home := b.TempDir()
	cwd := b.TempDir()
	for i := range 512 {
		id := fmt.Sprintf("bench-%04d", i)
		body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
			`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + id + `"}]}}` + "\n"
		writeSessionNamed(b, home, fmt.Sprintf("2026/06/14/%s.jsonl", id), body)
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
	cwd := b.TempDir()
	cache := b.TempDir()
	b.Setenv(grep.EnvCacheDir, cache)
	for i := range 512 {
		id := fmt.Sprintf("bench-grep-%04d", i)
		bodyText := "ordinary body"
		if i%32 == 0 {
			bodyText = "needle body"
		}
		body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
			`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + bodyText + `"}]}}` + "\n"
		writeSessionNamed(b, home, fmt.Sprintf("2026/06/14/%s.jsonl", id), body)
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
	cwd := b.TempDir()
	id := "bench-messages"
	var body strings.Builder
	body.WriteString(`{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n")
	for i := range 1000 {
		ts := time.Date(2026, 6, 14, 0, 0, i%60, 0, time.UTC).Format(time.RFC3339)
		body.WriteString(`{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"message ` + fmt.Sprint(i) + `"}]}}` + "\n")
	}
	writeSessionNamed(b, home, "2026/06/14/bench-messages.jsonl", body.String())
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
