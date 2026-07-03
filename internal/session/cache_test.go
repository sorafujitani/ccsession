package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestScanCacheHitSkipsReparse(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	proj := filepath.Join(projects, "-tmp-a")
	path := writeSessionFile(t, proj, "11111111-1111-1111-1111-111111111111.jsonl",
		"2024-05-26T10:00:00Z", "cached")
	got, err := Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 || got[0].Label != "cached" {
		t.Fatalf("first scan = %+v", got)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", int(fi.Size()))), 0o644); err != nil {
		t.Fatalf("corrupt source: %v", err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	got, err = Scan()
	if err != nil {
		t.Fatalf("Scan cached: %v", err)
	}
	if len(got) != 1 || got[0].Label != "cached" {
		t.Fatalf("cached scan = %+v, want cached session", got)
	}
}

func TestScanCacheInvalidatesOnSizeOrMTimeChange(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	proj := filepath.Join(projects, "-tmp-a")
	path := writeSessionFile(t, proj, "11111111-1111-1111-1111-111111111111.jsonl",
		"2024-05-26T10:00:00Z", "old")
	if _, err := Scan(); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	writeSessionFile(t, proj, filepath.Base(path), "2024-05-26T11:00:00Z", "new label")
	got, err := Scan()
	if err != nil {
		t.Fatalf("Scan after change: %v", err)
	}
	if len(got) != 1 || got[0].Label != "new label" {
		t.Fatalf("scan after change = %+v, want new label", got)
	}
}

func TestScanCacheCorruptJSONFallsBackToParse(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	proj := filepath.Join(projects, "-tmp-a")
	path := writeSessionFile(t, proj, "11111111-1111-1111-1111-111111111111.jsonl",
		"2024-05-26T10:00:00Z", "parsed")
	cacheDir := os.Getenv(EnvCacheDir)
	cachePath := filepath.Join(cacheDir, scanCacheFileName(path))
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt cache: %v", err)
	}

	got, err := Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 || got[0].Label != "parsed" {
		t.Fatalf("scan = %+v, want parsed session", got)
	}
}

func TestScanCacheCachesEmptySessions(t *testing.T) {
	home, projects := makeFakeHome(t)
	t.Setenv("HOME", home)

	proj := filepath.Join(projects, "-tmp-a")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(proj, "11111111-1111-1111-1111-111111111111.jsonl")
	body := `{"type":"assistant","timestamp":"2024-05-26T10:00:00Z"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d sessions, want 0", len(got))
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	valid := `{"type":"user","timestamp":"2024-05-26T10:00:00Z","message":{"role":"user","content":"x"}}` + "\n" +
		`{"type":"ai-title","aiTitle":"cached empty"}`
	if len(valid) < int(fi.Size()) {
		valid += strings.Repeat(" ", int(fi.Size())-len(valid))
	} else {
		valid = strings.Repeat("x", int(fi.Size()))
	}
	if err := os.WriteFile(path, []byte(valid), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	got, err = Scan()
	if err != nil {
		t.Fatalf("Scan cached empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("cached empty returned %d sessions, want 0", len(got))
	}
}

func BenchmarkScanManySessions(b *testing.B) {
	home := b.TempDir()
	projects := filepath.Join(home, ".claude", "projects")
	proj := filepath.Join(projects, "-tmp-bench")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		b.Fatalf("mkdir: %v", err)
	}
	for i := range 1000 {
		body := `{"type":"user","timestamp":"2024-05-26T10:00:00Z","cwd":"` + proj + `","message":{"role":"user","content":"hi"}}` + "\n" +
			`{"type":"ai-title","aiTitle":"session ` + strconv.Itoa(i) + `"}` + "\n"
		path := filepath.Join(proj, "session-"+strconv.Itoa(i)+".jsonl")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			b.Fatalf("write: %v", err)
		}
	}
	b.Setenv("HOME", home)
	b.Setenv(EnvCacheDir, filepath.Join(b.TempDir(), "scan-cache"))

	b.Run("cold-cache", func(b *testing.B) {
		for range b.N {
			if err := os.RemoveAll(os.Getenv(EnvCacheDir)); err != nil {
				b.Fatalf("remove cache: %v", err)
			}
			if got, err := Scan(); err != nil || len(got) != 1000 {
				b.Fatalf("Scan = %d, %v", len(got), err)
			}
		}
	})
	if _, err := Scan(); err != nil {
		b.Fatalf("warmup Scan: %v", err)
	}
	b.Run("warm-cache", func(b *testing.B) {
		for range b.N {
			if got, err := Scan(); err != nil || len(got) != 1000 {
				b.Fatalf("Scan = %d, %v", len(got), err)
			}
		}
	})
}

func TestScanCacheDirUsesUserCacheDir(t *testing.T) {
	t.Setenv(EnvCacheDir, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := scanCacheDir()
	if err != nil {
		t.Fatalf("scanCacheDir: %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join("ccsession", "scan")) {
		t.Fatalf("cache dir = %q", dir)
	}
}

func TestScanCacheDirUsesEnv(t *testing.T) {
	want := filepath.Join(t.TempDir(), "cache")
	t.Setenv(EnvCacheDir, want)
	got, err := scanCacheDir()
	if err != nil {
		t.Fatalf("scanCacheDir: %v", err)
	}
	if got != want {
		t.Fatalf("scanCacheDir = %q, want %q", got, want)
	}
}

func TestScanCacheRecordValidatesMetadata(t *testing.T) {
	now := time.Now()
	fi := fakeFileInfo{size: 1, mod: now}
	rec := scanCacheRecord{Path: "a", Size: 1, ModTimeUnixNano: now.UnixNano()}
	path := filepath.Join(t.TempDir(), "cache.json")
	if err := writeScanCache(path, rec); err != nil {
		t.Fatalf("writeScanCache: %v", err)
	}
	if _, ok := readScanCache(path, "a", fi); !ok {
		t.Fatal("cache should be valid")
	}
	if _, ok := readScanCache(path, "b", fi); ok {
		t.Fatal("cache should reject path mismatch")
	}
}

type fakeFileInfo struct {
	size int64
	mod  time.Time
}

func (f fakeFileInfo) Name() string       { return "" }
func (f fakeFileInfo) Size() int64        { return f.size }
func (f fakeFileInfo) Mode() os.FileMode  { return 0 }
func (f fakeFileInfo) ModTime() time.Time { return f.mod }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }
