package omp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSessionsDirDefaultsToOMPAgentRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAgentDir, "")
	t.Setenv(EnvConfigDir, "")

	got, err := ResolveSessionsDir()
	if err != nil {
		t.Fatalf("ResolveSessionsDir: %v", err)
	}
	want := filepath.Join(home, ".omp", "agent", "sessions")
	if got != want {
		t.Fatalf("ResolveSessionsDir = %q, want %q", got, want)
	}
}

func TestResolveSessionsDirHonorsAgentRootOverride(t *testing.T) {
	root := t.TempDir()
	t.Setenv(EnvAgentDir, root)
	t.Setenv(EnvConfigDir, "ignored-config-root")

	got, err := ResolveSessionsDir()
	if err != nil {
		t.Fatalf("ResolveSessionsDir: %v", err)
	}
	want := filepath.Join(root, "sessions")
	if got != want {
		t.Fatalf("ResolveSessionsDir = %q, want %q", got, want)
	}
}

func TestResolveSessionsDirHonorsConfigRootWhenAgentRootIsUnset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvAgentDir, "")
	t.Setenv(EnvConfigDir, ".omp-work")

	got, err := ResolveSessionsDir()
	if err != nil {
		t.Fatalf("ResolveSessionsDir: %v", err)
	}
	want := filepath.Join(home, ".omp-work", "agent", "sessions")
	if got != want {
		t.Fatalf("ResolveSessionsDir = %q, want %q", got, want)
	}
}

func TestScanIncludesNestedTaskAndSubagentSessions(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	fixtures := []struct {
		rel   string
		id    string
		title string
	}{
		{"project/main.jsonl", "11111111-1111-1111-1111-111111111111", "main session"},
		{"project/tasks/task.jsonl", "22222222-2222-2222-2222-222222222222", "task session"},
		{"project/tasks/subagents/reviewer.jsonl", "33333333-3333-3333-3333-333333333333", "reviewer session"},
	}
	for _, fixture := range fixtures {
		writeSession(t, dir, fixture.rel, fixture.id, cwd, fixture.title)
	}

	ss, err := OpenAt(dir).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != len(fixtures) {
		t.Fatalf("Scan returned %d sessions, want %d: %#v", len(ss), len(fixtures), ss)
	}
	got := make(map[string]string, len(ss))
	for _, sess := range ss {
		got[sess.ID] = sess.Label
	}
	for _, fixture := range fixtures {
		if got[fixture.id] != fixture.title {
			t.Errorf("session %s label = %q, want %q", fixture.id, got[fixture.id], fixture.title)
		}
	}
}

func TestGrepAndMessagesUseSharedPiParser(t *testing.T) {
	dir := t.TempDir()
	cwd := t.TempDir()
	id := "44444444-4444-4444-4444-444444444444"
	path := writeSession(t, dir, "project/tasks/worker.jsonl", id, cwd, "worker title")
	store := OpenAt(dir)

	keys, err := store.GrepKeys("unique omp prompt", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if _, ok := keys[id]; !ok {
		t.Fatalf("GrepKeys = %#v, want %s", keys, id)
	}
	sess, err := store.FindByLocator(id, path)
	if err != nil {
		t.Fatalf("FindByLocator: %v", err)
	}
	msgs, _, total, err := store.MessagesForSession(sess, 30)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	if total != 1 || len(msgs) != 1 || msgs[0].Body != "unique omp prompt" {
		t.Fatalf("messages = %#v total=%d, want shared parser output", msgs, total)
	}
}

func writeSession(t *testing.T, dir, rel, id, cwd, title string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"type":"title","v":1,"title":"` + title + `","source":"auto","updatedAt":"2026-08-04T00:00:01Z"}` + "\n" +
		`{"type":"session","version":3,"id":"` + id + `","timestamp":"2026-08-04T00:00:00Z","cwd":"` + cwd + `"}` + "\n" +
		`{"type":"message","id":"message","timestamp":"2026-08-04T00:00:02Z","message":{"role":"user","content":"unique omp prompt","timestamp":1785801602000}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
	return path
}
