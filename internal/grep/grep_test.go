package grep

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func skipIfNoRipgrep(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep (rg) not in PATH; skipping")
	}
}

func makeProjects(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("HOME", home)
	return filepath.Join(home, ".claude", "projects")
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestFilter_EmptyQueryReturnsNil(t *testing.T) {
	got, err := Filter("   ")
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestFilter_FindsMatchingFiles(t *testing.T) {
	skipIfNoRipgrep(t)
	projects := makeProjects(t)

	hit1 := filepath.Join(projects, "-p1", "11111111-1111-1111-1111-111111111111.jsonl")
	hit2 := filepath.Join(projects, "-p2", "22222222-2222-2222-2222-222222222222.jsonl")
	miss := filepath.Join(projects, "-p1", "33333333-3333-3333-3333-333333333333.jsonl")

	writeFile(t, hit1, `{"type":"user","message":{"role":"user","content":"please find NEEDLE here"}}`+"\n")
	writeFile(t, hit2, `{"type":"user","message":{"role":"user","content":"another needle hit"}}`+"\n")
	writeFile(t, miss, `{"type":"user","message":{"role":"user","content":"unrelated"}}`+"\n")

	set, err := Filter("needle")
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[hit1]; !ok {
		t.Errorf("expected %s in result", hit1)
	}
	if _, ok := set[hit2]; !ok {
		t.Errorf("expected %s in result", hit2)
	}
	if _, ok := set[miss]; ok {
		t.Errorf("did not expect %s in result", miss)
	}
}

func TestFilter_NoMatchesReturnsEmptySet(t *testing.T) {
	skipIfNoRipgrep(t)
	projects := makeProjects(t)

	writeFile(t, filepath.Join(projects, "-p", "a.jsonl"),
		`{"type":"user","message":{"role":"user","content":"hello"}}`+"\n")

	set, err := Filter("zzz-not-present-anywhere")
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if set == nil {
		t.Fatal("expected empty (non-nil) map, got nil")
	}
	if len(set) != 0 {
		t.Errorf("expected empty set, got %v", set)
	}
}

func TestFilter_ExcludesAgentJSONL(t *testing.T) {
	skipIfNoRipgrep(t)
	projects := makeProjects(t)

	user := filepath.Join(projects, "-p", "11111111-1111-1111-1111-111111111111.jsonl")
	agent := filepath.Join(projects, "-p", "agent-deadbeef.jsonl")
	writeFile(t, user, `{"type":"user","message":{"role":"user","content":"shared TOKEN here"}}`+"\n")
	writeFile(t, agent, `{"type":"agent","content":"shared TOKEN here"}`+"\n")

	set, err := Filter("TOKEN")
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[user]; !ok {
		t.Errorf("expected user session %s", user)
	}
	if _, ok := set[agent]; ok {
		t.Errorf("agent-*.jsonl should be excluded but found %s", agent)
	}
}

func TestFilter_CaseInsensitive(t *testing.T) {
	skipIfNoRipgrep(t)
	projects := makeProjects(t)
	p := filepath.Join(projects, "-p", "a.jsonl")
	writeFile(t, p, `{"type":"user","message":{"role":"user","content":"Lowercase pattern here"}}`+"\n")

	set, err := Filter("LOWERCASE")
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[p]; !ok {
		t.Errorf("expected case-insensitive hit on %s", p)
	}
}
