package grep

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	EnvCacheDir = "CCSESSION_GREP_CACHE_DIR"
)

type cacheRecord struct {
	Path            string   `json:"path"`
	Size            int64    `json:"size"`
	ModTimeUnixNano int64    `json:"mod_time_unix_nano"`
	Texts           []string `json:"texts"`
}

// CachedFileTexts returns extracted searchable text for path, reusing a
// metadata-validated on-disk cache when possible.
func CachedFileTexts(path string, read func(string) ([]string, error)) ([]string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	dir, err := cacheDir()
	if err != nil {
		return read(path)
	}

	cachePath := filepath.Join(dir, cacheFileName(path))
	if rec, ok := readCache(cachePath, path, fi); ok {
		return rec.Texts, nil
	}

	texts, err := read(path)
	if err != nil {
		return nil, err
	}
	_ = writeCache(cachePath, cacheRecord{
		Path:            path,
		Size:            fi.Size(),
		ModTimeUnixNano: fi.ModTime().UnixNano(),
		Texts:           texts,
	})
	return texts, nil
}

// FileContains reports whether any cached or freshly read text from path
// matches.
func FileContains(path string, match func(string) bool, read func(string) ([]string, error)) (bool, error) {
	texts, err := CachedFileTexts(path, read)
	if err != nil {
		return false, err
	}
	return TextsContain(texts, match), nil
}

// TextsContain reports whether any text fragment matches.
func TextsContain(texts []string, match func(string) bool) bool {
	for _, text := range texts {
		if match(text) {
			return true
		}
	}
	return false
}

func cacheDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(EnvCacheDir)); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ccsession", "grep"), nil
}

func cacheFileName(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:]) + ".json"
}

func readCache(path, transcriptPath string, fi os.FileInfo) (cacheRecord, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return cacheRecord{}, false
	}
	var rec cacheRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return cacheRecord{}, false
	}
	if rec.Path != transcriptPath ||
		rec.Size != fi.Size() ||
		rec.ModTimeUnixNano != fi.ModTime().UnixNano() {
		return cacheRecord{}, false
	}
	return rec, true
}

func writeCache(path string, rec cacheRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := json.NewEncoder(tmp).Encode(rec); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
