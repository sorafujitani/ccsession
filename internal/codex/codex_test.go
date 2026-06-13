package codex

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-06-14T00-00-00-"+id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return home, cwd, id
}
