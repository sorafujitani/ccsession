package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const EnvCacheDir = "CCSESSION_SCAN_CACHE_DIR"

type scanCacheRecord struct {
	Path            string  `json:"path"`
	Size            int64   `json:"size"`
	ModTimeUnixNano int64   `json:"mod_time_unix_nano"`
	Empty           bool    `json:"empty,omitempty"`
	Session         Session `json:"session,omitempty"`
}

func cachedParseSessionTail(path string) (*Session, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	dir, err := scanCacheDir()
	if err != nil {
		return ParseSessionTail(path, TailReadBytes)
	}
	cachePath := filepath.Join(dir, scanCacheFileName(path))
	if rec, ok := readScanCache(cachePath, path, fi); ok {
		if rec.Empty {
			return nil, ErrSessionEmpty
		}
		s := rec.Session
		return &s, nil
	}

	s, err := ParseSessionTail(path, TailReadBytes)
	if err != nil {
		if errors.Is(err, ErrSessionEmpty) {
			_ = writeScanCache(cachePath, scanCacheRecord{
				Path:            path,
				Size:            fi.Size(),
				ModTimeUnixNano: fi.ModTime().UnixNano(),
				Empty:           true,
			})
		}
		return nil, err
	}
	_ = writeScanCache(cachePath, scanCacheRecord{
		Path:            path,
		Size:            fi.Size(),
		ModTimeUnixNano: fi.ModTime().UnixNano(),
		Session:         *s,
	})
	return s, nil
}

func scanCacheDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(EnvCacheDir)); dir != "" {
		return dir, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "ccsession", "scan"), nil
}

func scanCacheFileName(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:]) + ".json"
}

func readScanCache(path, transcriptPath string, fi os.FileInfo) (scanCacheRecord, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return scanCacheRecord{}, false
	}
	var rec scanCacheRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return scanCacheRecord{}, false
	}
	if rec.Path != transcriptPath ||
		rec.Size != fi.Size() ||
		rec.ModTimeUnixNano != fi.ModTime().UnixNano() {
		return scanCacheRecord{}, false
	}
	return rec, true
}

func writeScanCache(path string, rec scanCacheRecord) error {
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
