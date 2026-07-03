package grep

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const EnvCacheDir = "CCSESSION_GREP_CACHE_DIR"

const (
	cacheVersion     = 2
	cacheIndexName   = "index.json"
	cacheTextSep     = "\x00"
	defaultCachePerm = 0o600
	defaultCacheDir  = 0o700
)

type cacheRecord struct {
	Version         int    `json:"v"`
	Path            string `json:"path"`
	Size            int64  `json:"size"`
	ModTimeUnixNano int64  `json:"mod_time_unix_nano"`
	Text            string `json:"text"`
	Fragments       int    `json:"fragments"`
}

type cacheIndex struct {
	path    string
	records map[string]cacheRecord
}

var (
	indexMu    sync.Mutex
	indexByDir = map[string]*cacheIndex{}
)

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

	idx := loadIndex(dir)
	if idx == nil {
		return read(path)
	}
	if rec, ok := idx.record(path, fi); ok {
		return splitCacheText(rec), nil
	}

	texts, err := read(path)
	if err != nil {
		return nil, err
	}
	idx.set(path, cacheRecord{
		Version:         cacheVersion,
		Path:            path,
		Size:            fi.Size(),
		ModTimeUnixNano: fi.ModTime().UnixNano(),
		Text:            strings.Join(texts, cacheTextSep),
		Fragments:       len(texts),
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

func loadIndex(dir string) *cacheIndex {
	indexMu.Lock()
	defer indexMu.Unlock()
	if idx := indexByDir[dir]; idx != nil {
		return idx
	}
	if fi, err := os.Stat(dir); err == nil && !fi.IsDir() {
		return nil
	}
	idx := &cacheIndex{
		path:    filepath.Join(dir, cacheIndexName),
		records: map[string]cacheRecord{},
	}
	b, err := os.ReadFile(idx.path)
	if err == nil {
		_ = json.Unmarshal(b, &idx.records)
		if idx.records == nil {
			idx.records = map[string]cacheRecord{}
		}
	}
	indexByDir[dir] = idx
	return idx
}

func (idx *cacheIndex) record(path string, fi os.FileInfo) (cacheRecord, bool) {
	indexMu.Lock()
	defer indexMu.Unlock()
	rec, ok := idx.records[path]
	if !ok ||
		rec.Version != cacheVersion ||
		rec.Path != path ||
		rec.Size != fi.Size() ||
		rec.ModTimeUnixNano != fi.ModTime().UnixNano() {
		return cacheRecord{}, false
	}
	return rec, true
}

func (idx *cacheIndex) set(path string, rec cacheRecord) {
	indexMu.Lock()
	idx.records[path] = rec
	snapshot := make(map[string]cacheRecord, len(idx.records))
	for k, v := range idx.records {
		snapshot[k] = v
	}
	indexMu.Unlock()

	_ = writeIndex(idx.path, snapshot)
}

func splitCacheText(rec cacheRecord) []string {
	switch rec.Fragments {
	case 0:
		return nil
	case 1:
		return []string{rec.Text}
	default:
		return strings.Split(rec.Text, cacheTextSep)
	}
}

func writeIndex(path string, records map[string]cacheRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), defaultCacheDir); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := json.NewEncoder(tmp).Encode(records); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(defaultCachePerm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
