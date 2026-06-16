package grep

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCachedFileTextsReusesValidCache(t *testing.T) {
	t.Setenv(EnvCacheDir, t.TempDir())
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, "body")

	calls := 0
	read := func(string) ([]string, error) {
		calls++
		return []string{"cached text"}, nil
	}

	for range 2 {
		texts, err := CachedFileTexts(path, read)
		if err != nil {
			t.Fatalf("CachedFileTexts: %v", err)
		}
		if len(texts) != 1 || texts[0] != "cached text" {
			t.Fatalf("texts = %#v, want cached text", texts)
		}
	}
	if calls != 1 {
		t.Fatalf("read calls = %d, want 1", calls)
	}
}

func TestCachedFileTextsInvalidatesOnFileUpdate(t *testing.T) {
	t.Setenv(EnvCacheDir, t.TempDir())
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	firstTime := time.Unix(1_800_000_000, 0)
	secondTime := firstTime.Add(time.Second)

	writeFile(t, path, "old")
	if err := os.Chtimes(path, firstTime, firstTime); err != nil {
		t.Fatalf("chtimes first: %v", err)
	}
	read := func(path string) ([]string, error) {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return []string{string(b)}, nil
	}

	texts, err := CachedFileTexts(path, read)
	if err != nil {
		t.Fatalf("first CachedFileTexts: %v", err)
	}
	if texts[0] != "old" {
		t.Fatalf("first text = %q, want old", texts[0])
	}

	writeFile(t, path, "new")
	if err := os.Chtimes(path, secondTime, secondTime); err != nil {
		t.Fatalf("chtimes second: %v", err)
	}
	texts, err = CachedFileTexts(path, read)
	if err != nil {
		t.Fatalf("second CachedFileTexts: %v", err)
	}
	if texts[0] != "new" {
		t.Fatalf("second text = %q, want new", texts[0])
	}
}

func TestCachedFileTextsCacheUnavailableFallsBackToReader(t *testing.T) {
	disabledPath := filepath.Join(t.TempDir(), "not-a-dir")
	writeFile(t, disabledPath, "cache dir blocker")
	t.Setenv(EnvCacheDir, disabledPath)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, "body")

	calls := 0
	read := func(string) ([]string, error) {
		calls++
		return []string{"text"}, nil
	}
	for range 2 {
		if _, err := CachedFileTexts(path, read); err != nil {
			t.Fatalf("CachedFileTexts: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("read calls = %d, want 2", calls)
	}
}
