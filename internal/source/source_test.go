package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sorafujitani/ccsession/internal/opencode"
	"github.com/sorafujitani/ccsession/internal/session"
)

func TestFromEnv_SelectsBackend(t *testing.T) {
	// opencode resolves its DB from OPENCODE_DB; point it at a real file so the
	// backend constructs (the file isn't opened until first query).
	db := filepath.Join(t.TempDir(), "opencode.db")
	if err := os.WriteFile(db, nil, 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	t.Setenv(opencode.EnvDBPath, db)

	cases := []struct {
		env      string
		wantName string
		wantErr  bool
	}{
		{"", "claude", false},
		{"claude", "claude", false},
		{"opencode", "opencode", false},
		// An unknown value is an error, not a silent fall back to claude:
		// a typo must surface, not quietly show the wrong agent's sessions.
		{"clauded", "", true},
	}
	for _, c := range cases {
		t.Setenv(EnvVar, c.env)
		got, err := FromEnv()
		if c.wantErr {
			if err == nil {
				t.Errorf("FromEnv(%q) = %v, want error", c.env, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("FromEnv(%q): %v", c.env, err)
		}
		if got.Name() != c.wantName {
			t.Errorf("FromEnv(%q).Name() = %q, want %q", c.env, got.Name(), c.wantName)
		}
	}
}

func TestClaude_ResumeSpec(t *testing.T) {
	bin, args, err := claudeSource{}.ResumeSpec(&session.Session{ID: "abc123"})
	if err != nil {
		t.Fatalf("ResumeSpec: %v", err)
	}
	if bin != "claude" {
		t.Errorf("bin = %q, want claude", bin)
	}
	want := []string{"claude", "--resume", "abc123"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// fixtureHome writes one parseable session under a fake ~/.claude/projects and
// points HOME at it. Returns the session id.
func fixtureHome(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := "11111111-1111-1111-1111-111111111111"
	body := `{"type":"user","timestamp":"2026-05-26T10:00:00Z","cwd":"` + dir + `","message":{"role":"user","content":"` + content + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("HOME", home)
	return id
}

func TestClaude_ScanStampsSource(t *testing.T) {
	fixtureHome(t, "hello")
	ss, err := claudeSource{}.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) == 0 {
		t.Fatal("Scan returned no sessions")
	}
	for _, s := range ss {
		if s.Source != "claude" {
			t.Errorf("session %s Source = %q, want claude", s.ID, s.Source)
		}
	}
}

// GrepKeys keys are opaque tokens that feed straight back into ScanFiltered;
// this round trip is the only contract between the two methods.
func TestClaude_GrepKeysFeedScanFiltered(t *testing.T) {
	fixtureHome(t, "the unique NEEDLE token")
	src := claudeSource{}

	keys, err := src.GrepKeys("needle", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("GrepKeys found nothing for a matching session")
	}

	ss, err := src.ScanFiltered(keys)
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("ScanFiltered returned %d sessions, want 1", len(ss))
	}
}

func TestClaude_FindByIDStampsSource(t *testing.T) {
	id := fixtureHome(t, "hello")
	s, err := claudeSource{}.FindByID(id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if s == nil || s.Source != "claude" {
		t.Errorf("FindByID Source = %v, want claude", s)
	}
}
