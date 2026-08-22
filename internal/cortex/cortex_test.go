package cortex

import (
	"os"
	"path/filepath"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	convDir := filepath.Join(dir, "conversations")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("testdata", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(convDir, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return OpenAt(dir)
}

func TestScan(t *testing.T) {
	store := testStore(t)
	sessions, err := store.Scan()
	if err != nil {
		t.Fatal(err)
	}
	// session_type=="subagent" はフィルタされるので2件のみ
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	// 新しい方が先 (test-session-2 は 2026-02-01, test-session-1 は 2026-01-15)
	if sessions[0].ID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("expected first session to be test-session-2, got %s", sessions[0].ID)
	}
	if sessions[1].ID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("expected second session to be test-session-1, got %s", sessions[1].ID)
	}
}

func TestParseMetadata(t *testing.T) {
	store := testStore(t)
	sess, err := store.FindByID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Label != "Test session about Go patterns" {
		t.Errorf("expected label from title, got %q", sess.Label)
	}
	if sess.CWD != "/Users/test/projects/myapp" {
		t.Errorf("expected cwd /Users/test/projects/myapp, got %q", sess.CWD)
	}
	if sess.CWDBasename != "myapp" {
		t.Errorf("expected cwd_basename myapp, got %q", sess.CWDBasename)
	}
	if sess.ConnectionName != "test_conn" {
		t.Errorf("expected connection_name test_conn, got %q", sess.ConnectionName)
	}
}

func TestDefaultTitleFallsBackToFirstUserMessage(t *testing.T) {
	store := testStore(t)
	sess, err := store.FindByID("11111111-2222-3333-4444-555555555555")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Label != "Explain error handling best practices in Rust" {
		t.Errorf("expected label from first user message, got %q", sess.Label)
	}
}

func TestSubagentSessionsFiltered(t *testing.T) {
	store := testStore(t)
	_, err := store.FindByID("99999999-8888-7777-6666-555544443333")
	if err == nil {
		t.Error("expected subagent session to be filtered out")
	}
}

func TestGrepKeys(t *testing.T) {
	store := testStore(t)
	keys, err := store.GrepKeys("builder pattern", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := keys["aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"]; !ok {
		t.Error("expected test-session-1 to match 'builder pattern'")
	}
	if _, ok := keys["11111111-2222-3333-4444-555555555555"]; ok {
		t.Error("expected test-session-2 to NOT match 'builder pattern'")
	}
}

func TestMessagesForSession(t *testing.T) {
	store := testStore(t)
	sess, err := store.FindByID("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if err != nil {
		t.Fatal(err)
	}
	msgs, _, total, err := store.MessagesForSession(sess, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 {
		t.Errorf("expected 4 messages, got %d", total)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages returned, got %d", len(msgs))
	}
	if msgs[0].Role != "user" {
		t.Errorf("expected first message role=user, got %q", msgs[0].Role)
	}
	if msgs[0].Body != "How do I implement the builder pattern in Go?" {
		t.Errorf("expected user text without system-reminder, got %q", msgs[0].Body)
	}
}

func TestResolveHome(t *testing.T) {
	t.Setenv(EnvHome, "/custom/path")
	home, err := ResolveHome()
	if err != nil {
		t.Fatal(err)
	}
	if home != "/custom/path" {
		t.Errorf("expected /custom/path, got %q", home)
	}
}

func TestMetadataPathFor(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/path/to/abc.history.jsonl", "/path/to/abc.json"},
		{"/a/b/uuid-123.history.jsonl", "/a/b/uuid-123.json"},
	}
	for _, tt := range tests {
		got := metadataPathFor(tt.input)
		if got != tt.want {
			t.Errorf("metadataPathFor(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
