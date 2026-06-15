package source

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkClaudeFindByID(b *testing.B) {
	id, _ := benchmarkClaudeStore(b, 1000)
	src := claudeSource{}
	b.ResetTimer()
	for range b.N {
		if _, err := src.FindByID(id); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClaudeFindByLocator(b *testing.B) {
	id, path := benchmarkClaudeStore(b, 1000)
	src := claudeSource{}
	locator := encodeLocator(path)
	b.ResetTimer()
	for range b.N {
		if _, err := src.FindByLocator(id, locator); err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkClaudeStore(b *testing.B, dirs int) (id, targetPath string) {
	b.Helper()
	home := b.TempDir()
	cwd := b.TempDir()
	b.Setenv("CLAUDE_CONFIG_DIR", "")
	b.Setenv("HOME", home)

	base := filepath.Join(home, ".claude", "projects")
	for i := range dirs {
		dir := filepath.Join(base, fmt.Sprintf("-proj-%04d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatalf("mkdir: %v", err)
		}
	}

	id = "11111111-1111-1111-1111-111111111111"
	targetDir := filepath.Join(base, "zz-target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		b.Fatalf("mkdir target: %v", err)
	}
	body := `{"type":"user","timestamp":"2026-05-26T10:00:00Z","cwd":"` + cwd + `","message":{"role":"user","content":"hello"}}` + "\n"
	targetPath = filepath.Join(targetDir, id+".jsonl")
	if err := os.WriteFile(targetPath, []byte(body), 0o644); err != nil {
		b.Fatalf("write target: %v", err)
	}
	return id, targetPath
}
