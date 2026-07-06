package pi

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

func TestScanReadsPiSessionLayout(t *testing.T) {
	dir, cwd, id := fixture(t)
	store := OpenAt(dir)

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
	if got.Label != "first user prompt" {
		t.Errorf("Label = %q, want first user prompt", got.Label)
	}
	if got.LastEpoch == 0 {
		t.Error("LastEpoch = 0, want parsed timestamp")
	}
}

func TestSessionInfoNameOverridesLabelLatestWins(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	id := "019f3876-219b-7070-a3d0-ef577213d9ad"
	body := header(id, cwd, "2026-07-06T00:00:00.000Z") +
		userLine("2026-07-06T00:00:01Z", "first user prompt") +
		`{"type":"session_info","id":"aa","parentId":null,"timestamp":"2026-07-06T00:00:02Z","name":"old name"}` + "\n" +
		`{"type":"session_info","id":"bb","parentId":"aa","timestamp":"2026-07-06T00:00:03Z","name":"renamed session"}` + "\n"
	writeSession(t, dir, id, body)

	ss, err := OpenAt(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].Label != "renamed session" {
		t.Fatalf("Label = %#v, want latest session_info name", ss)
	}
}

func TestSessionInfoNameClearedRevertsToFirstUser(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	id := "019f3876-219b-7070-a3d0-ef577213d9ad"
	body := header(id, cwd, "2026-07-06T00:00:00.000Z") +
		userLine("2026-07-06T00:00:01Z", "first user prompt") +
		`{"type":"session_info","id":"aa","parentId":null,"timestamp":"2026-07-06T00:00:02Z","name":"renamed session"}` + "\n" +
		`{"type":"session_info","id":"bb","parentId":"aa","timestamp":"2026-07-06T00:00:03Z","name":""}` + "\n"
	writeSession(t, dir, id, body)

	ss, err := OpenAt(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].Label != "first user prompt" {
		t.Fatalf("Label = %#v, want first user prompt after cleared name", ss)
	}
}

func TestUserStringContentAndBlockContent(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	id := "019f3876-219b-7070-a3d0-ef577213d9ad"
	body := header(id, cwd, "2026-07-06T00:00:00.000Z") +
		`{"type":"message","id":"m1","parentId":null,"timestamp":"2026-07-06T00:00:01Z","message":{"role":"user","content":"plain string prompt","timestamp":1783358814215}}` + "\n" +
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-07-06T00:00:02Z","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"assistant answer"},{"type":"toolCall","id":"c1","name":"read"}],"timestamp":1783358815000}}` + "\n"
	writeSession(t, dir, id, body)
	store := OpenAt(dir)

	ss, err := store.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].Label != "plain string prompt" {
		t.Fatalf("Label = %#v, want plain string prompt", ss)
	}
	msgs, _, total, err := store.Messages(id, 30)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if msgs[1].Role != "assistant" || msgs[1].Body != "assistant answer" {
		t.Fatalf("assistant message = %#v, want text blocks only", msgs[1])
	}
	if strings.Contains(msgs[1].Body, "hidden") {
		t.Fatalf("thinking block leaked into body: %q", msgs[1].Body)
	}
	want := time.UnixMilli(1783358814215)
	if !msgs[0].Timestamp.Equal(want) {
		t.Fatalf("user timestamp = %v, want message unix ms %v", msgs[0].Timestamp, want)
	}
}

func TestFindByIDAndMessages(t *testing.T) {
	dir, _, id := fixture(t)
	store := OpenAt(dir)

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
		t.Fatal("startedAt is zero, want header timestamp")
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(msgs) != 1 || msgs[0].Role != "user" || msgs[0].Body != "last user asks about unique needle" {
		t.Fatalf("limited messages = %#v, want last user message", msgs)
	}
}

func TestScanToleratesTreeEntries(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	id := "019f3876-219b-7070-a3d0-ef577213d9ad"
	body := header(id, cwd, "2026-07-06T00:00:00.000Z") +
		userLine("2026-07-06T00:00:01Z", "first user prompt") +
		`{"type":"compaction","id":"aa","parentId":null,"timestamp":"2026-07-06T00:00:02Z","summary":"earlier work","firstKeptEntryId":"x","tokensBefore":50000}` + "\n" +
		`{"type":"branch_summary","id":"bb","parentId":"aa","timestamp":"2026-07-06T00:00:03Z","fromId":"x","summary":"abandoned branch"}` + "\n" +
		`{"type":"custom_message","id":"cc","parentId":"bb","timestamp":"2026-07-06T00:00:04Z","customType":"ext","content":"injected","display":true}` + "\n" +
		`{"type":"label","id":"dd","parentId":"cc","timestamp":"2026-07-06T00:00:05Z","targetId":"aa","label":"checkpoint"}` + "\n"
	writeSession(t, dir, id, body)

	store := OpenAt(dir)
	ss, err := store.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].Label != "first user prompt" {
		t.Fatalf("sessions = %#v, want one labeled by first user prompt", ss)
	}
	_, _, total, err := store.Messages(id, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if total != 1 {
		t.Fatalf("total = %d, want only user/assistant messages counted", total)
	}
}

func TestFindByLocator(t *testing.T) {
	dir, _, id := fixture(t)
	store := OpenAt(dir)

	path := filepath.Join(dir, "--cwd--", "2026-07-06T00-00-00-000Z_"+id+".jsonl")
	s, err := store.FindByLocator(id, path)
	if err != nil {
		t.Fatalf("FindByLocator: %v", err)
	}
	if s.ID != id || s.JSONLPath != path {
		t.Fatalf("FindByLocator = %#v, want session at %s", s, path)
	}
	if _, err := store.FindByLocator("wrong-id", path); err == nil {
		t.Fatal("FindByLocator with mismatched id succeeded, want error")
	}
}

func TestGrepKeysFeedScanFiltered(t *testing.T) {
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	dir, _, id := fixture(t)
	store := OpenAt(dir)

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

func TestGrepKeysRegex(t *testing.T) {
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	dir, _, id := fixture(t)
	store := OpenAt(dir)

	keys, err := store.GrepKeys("unique\\s+needle", true)
	if err != nil {
		t.Fatalf("GrepKeys regex: %v", err)
	}
	if _, ok := keys[id]; !ok {
		t.Fatalf("regex GrepKeys did not include %s: %#v", id, keys)
	}
	keys, err = store.GrepKeys("no-such-token-anywhere", false)
	if err != nil {
		t.Fatalf("GrepKeys miss: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("GrepKeys matched nothing-sessions: %#v", keys)
	}
}

func TestScanReusesRepresentativeSessionMetadata(t *testing.T) {
	dir, _, _ := fixture(t)
	store := OpenAt(dir)
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

func TestScanSkipsBadSessions(t *testing.T) {
	dir, _, id := fixture(t)
	bad := `{"type":"session","version":3,"id":"bad\tid","timestamp":"2026-07-06T00:00:00Z","cwd":"/tmp"}` + "\n" +
		userLine("2026-07-06T00:00:01Z", "bad") + "\n"
	writeSessionNamed(t, dir, "--cwd--/bad.jsonl", bad)
	empty := header("22222222-2222-2222-2222-222222222222", "/tmp", "2026-07-06T00:00:00Z")
	writeSessionNamed(t, dir, "--cwd--/2026-07-06T00-00-00-000Z_22222222-2222-2222-2222-222222222222.jsonl", empty)
	garbage := "{not json}\nplain text\n"
	writeSessionNamed(t, dir, "--cwd--/garbage.jsonl", garbage)

	ss, err := OpenAt(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("Scan = %#v, want only valid session %s", ss, id)
	}
}

func TestScanToleratesCorruptLinesWithinSession(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	id := "019f3876-219b-7070-a3d0-ef577213d9ad"
	body := header(id, cwd, "2026-07-06T00:00:00.000Z") +
		"{corrupt line\n" +
		userLine("2026-07-06T00:00:01Z", "still parsed")
	writeSession(t, dir, id, body)

	ss, err := OpenAt(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].Label != "still parsed" {
		t.Fatalf("Scan = %#v, want session despite corrupt line", ss)
	}
}

func TestIDFallsBackToFilenameUUID(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	id := "33333333-3333-3333-3333-333333333333"
	body := `{"type":"session","version":3,"timestamp":"2026-07-06T00:00:00Z","cwd":"` + cwd + `"}` + "\n" +
		userLine("2026-07-06T00:00:01Z", "no header id")
	writeSessionNamed(t, dir, "--cwd--/2026-07-06T00-00-00-000Z_"+id+".jsonl", body)

	ss, err := OpenAt(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 1 || ss[0].ID != id {
		t.Fatalf("Scan = %#v, want filename uuid %s", ss, id)
	}
}

func TestScanMarksMissingCWDUnknown(t *testing.T) {
	dir := t.TempDir()
	id := "019f3876-219b-7070-a3d0-ef577213d9ad"
	body := `{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-07-06T00:00:00Z"}` + "\n" +
		userLine("2026-07-06T00:00:01Z", "missing cwd")
	writeSession(t, dir, id, body)

	ss, err := OpenAt(dir).Scan()
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

func TestScanOrdersByLastActivityDescending(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	older := "11111111-1111-1111-1111-111111111111"
	newer := "22222222-2222-2222-2222-222222222222"
	writeSession(t, dir, older, header(older, cwd, "2026-07-01T00:00:00Z")+userLine("2026-07-01T00:00:01Z", "older"))
	writeSession(t, dir, newer, header(newer, cwd, "2026-07-05T00:00:00Z")+userLine("2026-07-05T00:00:01Z", "newer"))

	ss, err := OpenAt(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 2 || ss[0].ID != newer || ss[1].ID != older {
		t.Fatalf("Scan order = %#v, want newest first", ss)
	}
}

func TestScanMissingDirReturnsEmpty(t *testing.T) {
	ss, err := OpenAt(filepath.Join(t.TempDir(), "does-not-exist")).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 0 {
		t.Fatalf("Scan = %#v, want empty", ss)
	}
}

func TestResolveSessionsDirHonorsEnv(t *testing.T) {
	want := t.TempDir()
	t.Setenv(EnvSessionsDir, want)
	got, err := ResolveSessionsDir()
	if err != nil {
		t.Fatalf("ResolveSessionsDir: %v", err)
	}
	if got != want {
		t.Errorf("ResolveSessionsDir = %q, want %q", got, want)
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

func fixture(t testing.TB) (dir, cwd, id string) {
	t.Helper()
	dir = t.TempDir()
	cwd = t.TempDir()
	id = "019f3876-219b-7070-a3d0-ef577213d9ad"
	body := header(id, cwd, "2026-07-06T00:00:00.000Z") +
		"{not json}\n" +
		`{"type":"thinking_level_change","id":"aa","parentId":null,"timestamp":"2026-07-06T00:00:01Z","thinkingLevel":"off"}` + "\n" +
		userLine("2026-07-06T00:00:02Z", "first user prompt") +
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-07-06T00:00:03Z","message":{"role":"assistant","content":[{"type":"text","text":"assistant answer"}],"timestamp":1783358815000}}` + "\n" +
		`{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-07-06T00:00:04Z","message":{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ignore tool result"}]}}` + "\n" +
		`{"type":"model_change","id":"m4","parentId":"m3","timestamp":"2026-07-06T00:00:05Z","provider":"anthropic","modelId":"model-x"}` + "\n" +
		userLine("2026-07-06T00:00:06Z", "last user asks about unique needle")
	writeSession(t, dir, id, body)
	return dir, cwd, id
}

func header(id, cwd, ts string) string {
	return `{"type":"session","version":3,"id":"` + id + `","timestamp":"` + ts + `","cwd":"` + cwd + `"}` + "\n"
}

func userLine(ts, text string) string {
	return `{"type":"message","id":"m1","parentId":null,"timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"text","text":"` + text + `"}],"timestamp":1783358814215}}` + "\n"
}

func writeSession(t testing.TB, dir, id, body string) {
	t.Helper()
	writeSessionNamed(t, dir, "--cwd--/2026-07-06T00-00-00-000Z_"+id+".jsonl", body)
}

func writeSessionNamed(t testing.TB, dir, rel, body string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
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
	dir := b.TempDir()
	cwd := b.TempDir()
	for i := range 512 {
		id := fmt.Sprintf("bench-%04d", i)
		body := header(id, cwd, "2026-07-06T00:00:00Z") + userLine("2026-07-06T00:00:01Z", id)
		writeSessionNamed(b, dir, fmt.Sprintf("--cwd--/%s.jsonl", id), body)
	}
	store := OpenAt(dir)
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
