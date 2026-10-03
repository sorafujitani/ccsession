package last

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sorafujitani/ccsession/internal/source"
)

func TestIsUnderOrEqual_Symlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	if !isUnderOrEqual(link, real) {
		t.Errorf("expected link→real to be under/equal")
	}
	if !isUnderOrEqual(real, link) {
		t.Errorf("expected real→link to be under/equal")
	}

	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir outside/sub: %v", err)
	}
	linkOut := filepath.Join(real, "to-out")
	if err := os.Symlink(outside, linkOut); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	target := filepath.Join(linkOut, "sub")
	if isUnderOrEqual(real, target) {
		t.Errorf("expected link-inside-base pointing outside to be NOT under base")
	}
}

func TestRun_Here_WithSymlink(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "proj")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	link := filepath.Join(root, "proj-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(source.EnvVar, "")

	oldwd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	if err := os.Chdir(link); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	writeListSession(t, home, real, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "2026-05-26T11:00:00Z", "under-here")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatalf("mkdir outside: %v", err)
	}
	writeListSession(t, home, outside, "11111111-1111-1111-1111-111111111111", "2026-05-26T10:00:00Z", "outside")

	id, _, err := Run(Options{Here: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if id != "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" {
		t.Errorf("expected under-here to be chosen, got %s", id)
	}
}
