package codex

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
	"pgregory.net/rapid"
)

// sameListResult compares a list scan against the reference list parse.
func sameListResult(t interface{ Fatalf(string, ...any) }, path string, got *session.Session, gotErr error) {
	want, _, _, _, wantErr := referenceParseFile(path, false, 0)
	if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && !errors.Is(gotErr, wantErr) && gotErr.Error() != wantErr.Error()) {
		t.Fatalf("error: got %v, want %v", gotErr, wantErr)
	}
	if (got == nil) != (want == nil) {
		t.Fatalf("session: got %#v, want %#v", got, want)
	}
	if got == nil {
		return
	}
	if !got.LastTime.Equal(want.LastTime) || got.LastTime.Format(time.RFC3339Nano) != want.LastTime.Format(time.RFC3339Nano) {
		t.Fatalf("LastTime: got %v, want %v", got.LastTime, want.LastTime)
	}
	g, w := *got, *want
	g.LastTime, w.LastTime = time.Time{}, time.Time{}
	if g != w {
		t.Fatalf("session:\n got %#v\nwant %#v", g, w)
	}
}

var recordShapes = []string{
	`{"timestamp":%[1]s,"type":"session_meta","payload":{"id":%[2]s,"timestamp":%[1]s,"cwd":%[3]s}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":%[4]s}]}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":{"type":"message","role":"user","content":%[4]s}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":%[4]s}]}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":{"type":"function_call_output","output":%[4]s}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":{"role":"user","type":"message","content":[{"text":%[4]s}],"Role":"assistant"}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":{"type":"message","role":"user","role":"assistant","content":%[4]s}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":{"type":"message","role":"user","content":%[4]s}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":{"type":"message","role":5,"content":%[4]s}}`,
	`{"timestamp":%[1]s,"type":"response_item","payload":null}`,
	`{"timestamp":%[1]s,"type":"response_item"}`,
	`{"timestamp":%[1]s,"type":"session_meta"}`,
	`{"timestamp":%[1]s,"Type":"session_meta","payload":{"id":%[2]s}}`,
	`{"TIMESTAMP":%[1]s,"type":"compacted","payload":{"message":%[4]s}}`,
	`{"timestamp":%[1]s,"timestamp":"2027-01-01T00:00:00Z","type":"event_msg","payload":{}}`,
	`{"timestamp":%[1]s,"type":"event_msg","payload":{}}`,
	`{"timeſtamp":%[1]s,"type":"event_msg","payload":{}}`,
	`{"timestamp":%[1]s,"type":"compacted","payload":{"replacement_history":[{"content":[{"text":%[4]s}]}],"n":[1,2,3]}}`,
	`{"timestamp":5,"type":"session_meta","payload":{"id":%[2]s}}`,
	`[%[1]s]`,
	`null`,
	`{"timestamp":%[1]s,"type":"event_msg","payload":{}} trailing`,
	`{"timestamp":%[1]s,"type":"session_meta","payload":{"id":%[2]s,"cwd":%[3]s}`,
}

var recordTimes = []string{`"2026-06-14T00:00:00Z"`, `"2026-06-14T09:00:00.123+09:00"`, `"2025-01-01T00:00:00Z"`, `""`, `null`, `"garbage"`, `"2026-06-14T00:00:00Z"`}
var recordIDs = []string{`"11111111-2222-3333-4444-555555555555"`, `""`, `"id\twith tab"`, `null`, `"other-id"`}
var recordTexts = []string{`"hello"`, `"  "`, `"# AGENTS.md instructions for x"`, `"<environment_context>x"`, `"x\n## My request for Codex:\nreal ask"`, `"escé \"q\""`, `"\u001b[31mred"`, `"\ud800"`, "\"\xff\"", `["a"]`, `[{"type":"input_text","text":"block"},{"text":""}]`, `42`}

func recordLine(t *rapid.T, cwd string) string {
	shape := rapid.SampledFrom(recordShapes).Draw(t, "shape")
	return fmt.Sprintf(shape,
		rapid.SampledFrom(recordTimes).Draw(t, "time"),
		rapid.SampledFrom(recordIDs).Draw(t, "id"),
		rapid.SampledFrom([]string{fmt.Sprintf("%q", cwd), `""`, `"/no/such/dir"`}).Draw(t, "cwd"),
		rapid.SampledFrom(recordTexts).Draw(t, "text"))
}

func TestListScanMatchesParseFile(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	path := filepath.Join(dir, "rollout-2026-06-14T00-00-00-11111111-2222-3333-4444-000000000000.jsonl")
	rapid.Check(t, func(t *rapid.T) {
		var body strings.Builder
		for range rapid.IntRange(0, 8).Draw(t, "lines") {
			body.WriteString(rapid.SampledFrom([]string{"", " ", "\r"}).Draw(t, "pad"))
			body.WriteString(recordLine(t, cwd))
			body.WriteString(rapid.SampledFrom([]string{"\n", "\r\n", "\n\n"}).Draw(t, "newline"))
		}
		data := body.String()
		if rapid.Bool().Draw(t, "unterminated") {
			data = strings.TrimRight(data, "\r\n")
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		sess, err := listFile(path)
		sameListResult(t, path, sess, err)
		sess, _, err = scanCachedFile(path, nil)
		sameListResult(t, path, sess, err)
		limit := rapid.IntRange(0, 3).Draw(t, "limit")
		msgs, started, total, err := parseMessages(path, limit)
		_, wantMsgs, wantStarted, wantTotal, wantErr := referenceParseFile(path, true, limit)
		if (err == nil) != (wantErr == nil) || !started.Equal(wantStarted) || total != wantTotal || fmt.Sprint(msgs) != fmt.Sprint(wantMsgs) {
			t.Fatalf("messages: got %v %v %d %v, want %v %v %d %v", msgs, started, total, err, wantMsgs, wantStarted, wantTotal, wantErr)
		}
	})
}

// Each step changes the file and must match a full parse. The checked
// records show which steps resume instead of rescanning.
func TestCodexCacheRecordLifecycle(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(t.TempDir(), "rollout-11111111-2222-3333-4444-555555555555.jsonl")
	meta := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"11111111-2222-3333-4444-555555555555","cwd":"` + cwd + `"}}` + "\n"
	user := func(ts, text string) string {
		return `{"timestamp":"` + ts + `","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + text + `"}]}}` + "\n"
	}
	write := func(data string) {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scan := func(prev *codexCacheRecord) codexCacheRecord {
		t.Helper()
		sess, rec, err := scanCachedFile(path, prev)
		sameListResult(t, path, sess, err)
		raw, _ := os.ReadFile(path)
		h, err := prefixDigest(bytes.NewReader(raw), rec.Offset)
		if err != nil || rec.Prefix != h.Digest() || (rec.Offset > 0 && raw[rec.Offset-1] != '\n') {
			t.Fatalf("record does not cover whole lines: offset=%d of %d", rec.Offset, len(raw))
		}
		return rec
	}

	write(meta + user("2026-06-14T00:00:01Z", "first"))
	rec := scan(nil)
	if rec.Stamp == "" {
		t.Skip("no file stamps on this platform")
	}

	// A matching stamp serves the record without reading; a sentinel proves it.
	cached := rec
	cached.State.Label = "from stamp"
	if sess, _, err := scanCachedFile(path, &cached); err != nil || sess.Label != "from stamp" {
		t.Fatalf("unchanged file was read: %#v, error=%v", sess, err)
	}
	// Timestamps that have not settled are not trusted, though the prefix is.
	t.Cleanup(func(orig func() time.Time) func() { return func() { now = orig } }(now))
	now = time.Now
	if fresh := scan(nil); fresh.Stamp != "" {
		t.Fatal("a just-written file got a stamp")
	}
	now = func() time.Time { return time.Now().Add(time.Hour) }

	// A resumed scan starts from the cached state; a sentinel proves it.
	resumed := rec
	resumed.Stamp = ""
	resumed.State.Label = "from cache"
	write(meta + user("2026-06-14T00:00:01Z", "first") + `{"timestamp":"2026-06-14T00:00:02Z","type":"event_msg","payload":{}}` + "\n")
	if sess, _, err := scanCachedFile(path, &resumed); err != nil || sess.Label != "from cache" {
		t.Fatalf("append did not resume: %#v, error=%v", sess, err)
	}
	rec = scan(&rec)

	// An unterminated record counts now but is rescanned when completed.
	partial := user("2026-06-14T00:00:03Z", "partial")
	data, _ := os.ReadFile(path)
	write(string(data) + strings.TrimSuffix(partial, "\n"))
	rec = scan(&rec)
	if rec.final().Label != "partial" || rec.State.Label != "first" {
		t.Fatalf("unterminated record: %#v", rec)
	}
	write(string(data) + partial)
	rec = scan(&rec)
	if rec.State.Label != "partial" {
		t.Fatalf("completed record: %#v", rec)
	}

	// Same-size edits with a restored mtime, truncation, and replacement all
	// fail the prefix check.
	info, _ := os.Stat(path)
	data, _ = os.ReadFile(path)
	write(strings.Replace(string(data), "first", "FIRST", 1))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	stale := rec
	stale.State.Label, stale.Tail = "stale", nil
	if sess, _, err := scanCachedFile(path, &stale); err != nil || sess.Label != "partial" {
		t.Fatalf("same-size edit reused the cache: %#v, error=%v", sess, err)
	}
	rec = scan(&rec)
	write(meta)
	rec = scan(&rec)
	replacement := filepath.Join(filepath.Dir(path), "replacement")
	if err := os.WriteFile(replacement, []byte(meta+user("2026-06-14T00:00:09Z", "other")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	rec = scan(&rec)
	if rec.final().Label != "other" {
		t.Fatalf("replacement: %#v", rec)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := scanCachedFile(path, &rec); !errors.Is(err, session.ErrSessionFileMissing) {
		t.Fatalf("deleted file: %v", err)
	}
}

func TestCodexCachePersistsAcrossScans(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"cached-id","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}` + "\n"
	writeSession(t, home, "cached-id", body)
	writeSession(t, home, "empty-id", `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"empty-id"}}`+"\n")
	calls := countParseCalls(t)
	store := OpenAt(home)
	cold, err := store.Scan()
	if err != nil || len(cold) != 1 {
		t.Fatalf("cold: %#v, error=%v", cold, err)
	}
	path, abs := codexCachePath(home)
	if cache := readCodexCache(path, abs); len(cache.Files) != 2 {
		t.Fatalf("cache entries=%d, want 2 including the empty transcript", len(cache.Files))
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache permissions: %v, %v", info, err)
	}
	warm, err := store.Scan()
	if err != nil || len(warm) != 1 || warm[0].Label != cold[0].Label || !warm[0].LastTime.Equal(cold[0].LastTime) {
		t.Fatalf("warm: %#v, error=%v", warm, err)
	}
	if calls.Load() != 4 {
		t.Fatalf("scan calls=%d, want 4", calls.Load())
	}
	if err := os.Remove(cwd); err != nil {
		t.Fatal(err)
	}
	if ss, err := store.Scan(); err != nil || ss[0].CWDExists {
		t.Fatalf("cached session hid the deleted CWD: %#v, error=%v", ss, err)
	}
	for _, corrupt := range []string{`{"files":`, `{"version":1,"home":"elsewhere","files":{}}`} {
		if err := os.WriteFile(path, []byte(corrupt), 0o600); err != nil {
			t.Fatal(err)
		}
		if ss, err := store.Scan(); err != nil || len(ss) != 1 || ss[0].Label != "hello" {
			t.Fatalf("recovery from %q: %#v, error=%v", corrupt, ss, err)
		}
	}
}

// readListLine must split and cap lines like the reference, and hash exactly
// the newline-terminated prefix.
func TestReadListLineMatchesReference(t *testing.T) {
	long := strings.Repeat("x", listReadSize*2+7)
	for _, data := range []string{
		"", "\n\n", " a \r\n", "a\nb\n", "a\n" + long + "\ntail", "a\n" + long, "a\n" + long + "\n",
		"a\n" + strings.Repeat("y", 64) + "\nb",
	} {
		for _, max := range []int{32, len(long) + 10} {
			var want []string
			r := bufio.NewReaderSize(strings.NewReader(data), 64*1024)
			for {
				line, err := referenceReadLine(r, max)
				if len(line) > 0 {
					want = append(want, string(line))
				}
				if err != nil {
					break
				}
			}
			var got []string
			var offset int64
			h := newPrefixHasher()
			r = bufio.NewReaderSize(strings.NewReader(data), listReadSize)
			for {
				line, n, terminated, err := readListLine(r, h, max)
				if len(line) > 0 {
					got = append(got, string(line))
				}
				if terminated {
					offset += n
				}
				if err != nil {
					break
				}
			}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("max=%d: lines differ for %.20q", max, data)
			}
			if offset != int64(strings.LastIndexByte(data, '\n')+1) {
				t.Fatalf("max=%d: offset=%d for %.20q", max, offset, data)
			}
			if want, _ := prefixDigest(strings.NewReader(data), offset); h.Digest() != want.Digest() {
				t.Fatalf("max=%d: hash does not cover exactly [0, %d) of %.20q", max, offset, data)
			}
		}
	}
}

func TestPrefixDigest(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), prefixBlock/8+3) // Two blocks and a tail.
	var sums []byte
	for start := 0; start < len(data); start += prefixBlock {
		sum := sha256.Sum256(data[start:min(start+prefixBlock, len(data))])
		sums = append(sums, sum[:]...)
	}
	want := sha256.Sum256(sums)
	r := bytes.NewReader(data)
	for _, n := range []int64{0, 1, prefixBlock, prefixBlock + 1, int64(len(data))} {
		parallel, err := prefixDigest(r, n)
		if err != nil {
			t.Fatal(err)
		}
		// Writing in odd pieces must agree with the parallel digest, and a
		// digest resumed from it must agree with hashing everything at once.
		streamed := newPrefixHasher()
		for chunk := data[:n]; len(chunk) > 0; chunk = chunk[min(len(chunk), 1<<20+3):] {
			streamed.Write(chunk[:min(len(chunk), 1<<20+3)])
		}
		if parallel.Digest() != streamed.Digest() {
			t.Fatalf("n=%d: parallel and streamed digests differ", n)
		}
		parallel.Write(data[n:])
		if parallel.Digest() != hex.EncodeToString(want[:]) {
			t.Fatalf("n=%d: resumed digest is not the SHA-256 of block SHA-256s", n)
		}
	}
	if _, err := prefixDigest(r, int64(len(data)+1)); err == nil {
		t.Fatal("a prefix longer than the file must fail")
	}
}
