package session

import (
	"os"
	"path/filepath"
	"testing"
)

// makeFakeHome builds a fake ~/.claude/projects layout under tmp and returns
// the (tmpHome, projectsDir) pair. The caller is expected to t.Setenv("HOME", tmpHome).
func makeFakeHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	projects := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(projects, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return home, projects
}

func writeSessionFile(t *testing.T, dir, name, ts, label string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"type":"user","timestamp":"` + ts + `","cwd":"` + dir + `","message":{"role":"user","content":"hi"}}` + "\n" +
		`{"type":"ai-title","aiTitle":"` + label + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestProjectsDir_UsesHomeEnv(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	got, err := ProjectsDir()
	if err != nil {
		t.Fatalf("ProjectsDir: %v", err)
	}
	if got != projects {
		t.Errorf("ProjectsDir = %q, want %q", got, projects)
	}
}

func TestScan_EmptyWhenNoProjectsDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home) // no .claude/projects under here

	sessions, err := Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("got %d sessions, want 0", len(sessions))
	}
}

func TestScan_SortedNewestFirstAndExcludesAgentAndNonJSONL(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	projA := filepath.Join(projects, "-tmp-a")
	projB := filepath.Join(projects, "-tmp-b")

	// older session in A
	writeSessionFile(t, projA, "11111111-1111-1111-1111-111111111111.jsonl",
		"2026-05-26T10:00:00Z", "older")
	// newer session in A
	writeSessionFile(t, projA, "22222222-2222-2222-2222-222222222222.jsonl",
		"2026-05-26T12:00:00Z", "newer")
	// middle session in B
	writeSessionFile(t, projB, "33333333-3333-3333-3333-333333333333.jsonl",
		"2026-05-26T11:00:00Z", "middle")

	// excluded: agent-* prefix
	writeSessionFile(t, projA, "agent-deadbeef.jsonl",
		"2026-05-26T13:00:00Z", "agent")
	// excluded: not .jsonl
	if err := os.WriteFile(filepath.Join(projA, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	// excluded: project entry that is a regular file, not a dir
	if err := os.WriteFile(filepath.Join(projects, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write stray: %v", err)
	}

	got, err := Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d sessions, want 3", len(got))
	}
	if got[0].Label != "newer" {
		t.Errorf("first = %q, want newer", got[0].Label)
	}
	if got[1].Label != "middle" {
		t.Errorf("second = %q, want middle", got[1].Label)
	}
	if got[2].Label != "older" {
		t.Errorf("third = %q, want older", got[2].Label)
	}
	for _, s := range got {
		if filepath.Base(s.JSONLPath) == "agent-deadbeef.jsonl" {
			t.Error("agent-* session should have been excluded")
		}
	}
}

func TestScanFiltered_AllowSetRestrictsResults(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	proj := filepath.Join(projects, "-tmp-a")
	writeSessionFile(t, proj, "11111111-1111-1111-1111-111111111111.jsonl",
		"2026-05-26T10:00:00Z", "keep")
	writeSessionFile(t, proj, "22222222-2222-2222-2222-222222222222.jsonl",
		"2026-05-26T11:00:00Z", "drop")

	allow := map[string]struct{}{
		filepath.Join(proj, "11111111-1111-1111-1111-111111111111.jsonl"): {},
	}
	got, err := ScanFiltered(allow)
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	if len(got) != 1 || got[0].Label != "keep" {
		t.Fatalf("got %+v, want single 'keep' session", got)
	}
}

func TestScanFiltered_EmptyAllowReturnsNothing(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	proj := filepath.Join(projects, "-tmp-a")
	writeSessionFile(t, proj, "11111111-1111-1111-1111-111111111111.jsonl",
		"2026-05-26T10:00:00Z", "x")

	got, err := ScanFiltered(map[string]struct{}{})
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d sessions, want 0", len(got))
	}
}

func TestFindByID_HitAndMiss(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	proj := filepath.Join(projects, "-tmp-a")
	writeSessionFile(t, proj, "11111111-1111-1111-1111-111111111111.jsonl",
		"2026-05-26T10:00:00Z", "found")

	got, err := FindByID("11111111-1111-1111-1111-111111111111")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if got == nil || got.Label != "found" {
		t.Fatalf("got %+v, want 'found' session", got)
	}

	miss, err := FindByID("00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("FindByID miss: %v", err)
	}
	if miss != nil {
		t.Errorf("expected nil for missing id, got %+v", miss)
	}
}
