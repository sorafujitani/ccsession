package last

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/source"
)

func TestRun_Basic(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(source.EnvVar, "")

	writeListSession(t, home, cwd, "11111111-1111-1111-1111-111111111111", "2026-05-26T10:00:00Z", "older")
	writeListSession(t, home, cwd, "22222222-2222-2222-2222-222222222222", "2026-05-26T11:00:00Z", "newer")

	id, _, err := Run(Options{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if id != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("unexpected id: %s", id)
	}
}

func writeListSession(t testing.TB, home, cwd, id, ts, label string) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"type":"user","timestamp":"` + ts + `","cwd":"` + cwd + `","message":{"role":"user","content":"` + label + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write session: %v", err)
	}
}

func TestRun_SkipMissingCWD(t *testing.T) {
	home := t.TempDir()
	goodCWD := t.TempDir()
	badCWD := filepath.Join(t.TempDir(), "deleted-dir")

	t.Setenv("HOME", home)
	t.Setenv(source.EnvVar, "")

	// older session with missing cwd (should be skipped)
	writeListSession(t, home, badCWD, "11111111-1111-1111-1111-111111111111", "2026-05-26T10:00:00Z", "bad")
	// newer session with valid cwd
	writeListSession(t, home, goodCWD, "22222222-2222-2222-2222-222222222222", "2026-05-26T11:00:00Z", "good")

	id, _, err := Run(Options{})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if id != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("expected to skip missing cwd and pick valid one, got %s", id)
	}
}

func TestIsUnderOrEqual(t *testing.T) {
	tests := []struct {
		name   string
		base   string
		target string
		want   bool
	}{
		{"exact match", "/home/user", "/home/user", true},
		{"subdirectory", "/home/user", "/home/user/proj", true},
		{"deep subdirectory", "/home/user", "/home/user/proj/sub", true},
		{"sibling directory", "/home/user", "/home/other", false},
		{"parent directory", "/home/user/proj", "/home/user", false},
		{"root base", "/", "/home/user", true},
		{"root exact", "/", "/", true},
		{"unrelated paths", "/var/log", "/home/user", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnderOrEqual(tt.base, tt.target)
			if got != tt.want {
				t.Errorf("isUnderOrEqual(%q, %q) = %v, want %v", tt.base, tt.target, got, tt.want)
			}
		})
	}
}

func TestFilterOutByDir(t *testing.T) {
	mk := func(id, cwd, base string) *session.Session {
		return &session.Session{ID: id, CWD: cwd, CWDBasename: base}
	}
	all := []*session.Session{
		mk("a", "/Users/x/work/myproj", "myproj"),
		mk("b", "/Users/x/scratch/test-thing", "test-thing"),
		mk("c", "/Users/x/work/Test", "Test"),
		mk("d", "", ""),
		mk("e", "", "test-fallback"),
	}
	in := append([]*session.Session(nil), all...)
	got := filterOutByDir(in, "test")

	ids := make([]string, len(got))
	for i, s := range got {
		ids[i] = s.ID
	}
	want := []string{"a", "d"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, ids[i], want[i])
		}
	}
}
