package grep

import (
	"encoding/json"
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

func TestCachedFileTextsV1RecordIsMiss(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvCacheDir, cacheDir)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, "body")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	v1 := map[string]cacheRecord{
		path: {
			Path:            path,
			Size:            fi.Size(),
			ModTimeUnixNano: fi.ModTime().UnixNano(),
			Text:            "stale",
			Fragments:       1,
		},
	}
	writeIndexJSON(t, filepath.Join(cacheDir, cacheIndexName), v1)

	calls := 0
	texts, err := CachedFileTexts(path, func(string) ([]string, error) {
		calls++
		return []string{"fresh"}, nil
	})
	if err != nil {
		t.Fatalf("CachedFileTexts: %v", err)
	}
	if calls != 1 || len(texts) != 1 || texts[0] != "fresh" {
		t.Fatalf("calls/texts = %d/%#v, want fresh miss", calls, texts)
	}
}

func TestCachedFileTextsCorruptIndexFallsBack(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv(EnvCacheDir, cacheDir)
	if err := os.WriteFile(filepath.Join(cacheDir, cacheIndexName), []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt index: %v", err)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, "body")

	texts, err := CachedFileTexts(path, func(string) ([]string, error) {
		return []string{"fresh"}, nil
	})
	if err != nil {
		t.Fatalf("CachedFileTexts: %v", err)
	}
	if len(texts) != 1 || texts[0] != "fresh" {
		t.Fatalf("texts = %#v, want fresh fallback", texts)
	}
}

func TestCachedFileTextsPreservesRegexFragmentBoundaries(t *testing.T) {
	t.Setenv(EnvCacheDir, t.TempDir())
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeFile(t, path, "body")
	read := func(string) ([]string, error) {
		return []string{"foo", "bar"}, nil
	}
	if _, err := CachedFileTexts(path, read); err != nil {
		t.Fatalf("warm cache: %v", err)
	}

	ok, err := FileContains(path, func(s string) bool {
		return s == "foo"+cacheTextSep+"bar" || s == "foobar"
	}, read)
	if err != nil {
		t.Fatalf("FileContains: %v", err)
	}
	if ok {
		t.Fatal("cache should not match across fragment boundaries")
	}
}

func writeIndexJSON(t *testing.T, path string, records map[string]cacheRecord) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	b, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
}
