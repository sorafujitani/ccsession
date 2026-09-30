package cortex

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

const validMetadata = `{"session_id":"test","title":"session title","created_at":"2026-01-15T10:00:00Z","last_updated":"2026-01-15T11:00:00Z"}`
const validHistory = `{"role":"user","content":[{"type":"text","text":"search hit"}]}`

func errorTestStore(t *testing.T, meta, history string) (*Store, string) {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, "conversations")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test.history.jsonl")
	for name, body := range map[string]string{metadataPathFor(path): meta, path: history} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(grep.EnvCacheDir, t.TempDir())
	return OpenAt(home), path
}

func TestMetadataErrorsPropagate(t *testing.T) {
	for _, failure := range []string{"malformed", "missing", "directory"} {
		t.Run(failure, func(t *testing.T) {
			store, path := errorTestStore(t, validMetadata, validHistory)
			metaPath := metadataPathFor(path)
			if failure == "malformed" {
				if err := os.WriteFile(metaPath, []byte(`{"session_id":`), 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(metaPath); err != nil {
					t.Fatal(err)
				}
				if failure == "directory" {
					if err := os.Mkdir(metaPath, 0o700); err != nil {
						t.Fatal(err)
					}
				}
			}
			operations := map[string]func() error{
				"scan": func() error { _, err := store.Scan(); return err },
				"filtered scan": func() error {
					_, err := store.ScanFiltered(map[string]struct{}{"test": {}})
					return err
				},
				"find": func() error { _, err := store.FindByID("test"); return err },
				"grep": func() error { _, err := store.GrepKeys("hit", false); return err },
			}
			for name, run := range operations {
				t.Run(name, func(t *testing.T) {
					err := run()
					if err == nil || !strings.Contains(err.Error(), metaPath) {
						t.Fatalf("error = %v, want metadata error with filename", err)
					}
					if failure == "malformed" {
						var syntaxErr *json.SyntaxError
						if !errors.As(err, &syntaxErr) {
							t.Fatalf("error = %v, want JSON syntax error", err)
						}
					} else {
						var pathErr *os.PathError
						if !errors.As(err, &pathErr) || pathErr.Path != metaPath {
							t.Fatalf("error = %v, want metadata read error", err)
						}
						if failure == "missing" && (!errors.Is(err, os.ErrNotExist) || !errors.Is(err, session.ErrSessionFileMissing)) {
							t.Fatalf("error = %v, want missing file identifiers", err)
						}
					}
				})
			}
		})
	}
}

func TestInvalidSessionIDs(t *testing.T) {
	for _, id := range []string{"", "bad\tid", "bad\nid", "bad\rid"} {
		t.Run(id, func(t *testing.T) {
			data, err := json.Marshal(metadata{SessionID: id, Title: "session title"})
			if err != nil {
				t.Fatal(err)
			}
			store, path := errorTestStore(t, string(data), validHistory)
			_, err = store.Scan()
			if !errors.Is(err, ErrInvalidSessionID) || !strings.Contains(err.Error(), metadataPathFor(path)) {
				t.Fatalf("error = %v, want invalid ID with metadata filename", err)
			}
		})
	}
}

func TestHistoryDecodingErrorsPropagate(t *testing.T) {
	for _, invalid := range []string{`{"role":`, `{"role":"user","content":{}}`} {
		t.Run(invalid, func(t *testing.T) {
			store, path := errorTestStore(t, validMetadata, validHistory+"\n"+invalid+"\n"+validHistory)
			operations := map[string]func() error{
				"preview": func() error {
					msgs, _, total, err := store.MessagesForSession(&session.Session{JSONLPath: path}, 1)
					if err != nil && (msgs != nil || total != 0) {
						t.Fatal("preview returned partial results on error")
					}
					return err
				},
				"grep texts": func() error { _, err := fileMessageTexts(path); return err },
				"grep":       func() error { _, err := store.GrepKeys("hit", false); return err },
			}
			for name, run := range operations {
				t.Run(name, func(t *testing.T) {
					err := run()
					var syntaxErr *json.SyntaxError
					var typeErr *json.UnmarshalTypeError
					if !errors.As(err, &syntaxErr) && !errors.As(err, &typeErr) {
						t.Fatalf("error = %v, want JSON decoding error", err)
					}
					if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "line 2:") {
						t.Fatalf("error = %v, want history filename and line number", err)
					}
				})
			}
		})
	}
}

func TestLabelFallbackDecodingErrorsPropagate(t *testing.T) {
	store, path := errorTestStore(t, `{"session_id":"test"}`, validHistory+"\n"+`{"role":`)
	_, err := store.Scan()
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) || !strings.Contains(err.Error(), path) {
		t.Fatalf("error = %v, want history decoding error", err)
	}
}

func TestGrepHistoryReadErrorsPropagate(t *testing.T) {
	for _, failure := range []string{"missing", "directory", "permission"} {
		t.Run(failure, func(t *testing.T) {
			store, path := errorTestStore(t, validMetadata, validHistory)
			if failure == "permission" {
				if err := os.Chmod(path, 0); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
				if f, err := os.Open(path); err == nil {
					f.Close()
					t.Skip("filesystem permits reading despite mode 000")
				}
			} else {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				target := t.TempDir()
				if failure == "missing" {
					target = filepath.Join(target, "missing")
				}
				// Keep the history entry discoverable while its target is unreadable.
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			}
			keys, err := store.GrepKeys("hit", false)
			var pathErr *os.PathError
			if !errors.As(err, &pathErr) || pathErr.Path != path || keys != nil {
				t.Fatalf("keys = %v, error = %v, want history read failure", keys, err)
			}
		})
	}
}

func TestReadJSONLLineLimit(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		limit             int
		wantErr           error
	}{
		{"newline overflow", "12345\n", "", 4, ErrJSONLLineTooLong},
		{"EOF overflow", "12345", "", 4, ErrJSONLLineTooLong},
		{"multi chunk overflow", strings.Repeat("x", 100), "", 32, ErrJSONLLineTooLong},
		{"at limit", "123\n", "123", 4, nil},
		{"at limit EOF", "1234", "1234", 4, io.EOF},
		{"multi chunk valid", strings.Repeat("x", 100) + "\n", strings.Repeat("x", 100), 101, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			line, err := readJSONLLine(bufio.NewReaderSize(strings.NewReader(tc.input), 16), tc.limit)
			if !errors.Is(err, tc.wantErr) || string(line) != tc.want {
				t.Fatalf("line = %q, error = %v; want %q, %v", line, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestOversizedHistoryErrorsPropagate(t *testing.T) {
	store, path := errorTestStore(t, validMetadata, "")
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`{"role":"user","content":[{"type":"text","text":"`); err != nil {
		t.Fatal(err)
	}
	chunk := strings.Repeat("x", 64*1024)
	for range jsonlLineCap / len(chunk) {
		if _, err := f.WriteString(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.WriteString("\"}]}\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = store.MessagesForSession(&session.Session{JSONLPath: path}, 0)
	if !errors.Is(err, ErrJSONLLineTooLong) {
		t.Fatalf("preview error = %v, want size limit error", err)
	}
	_, err = store.GrepKeys("hit", false)
	if !errors.Is(err, ErrJSONLLineTooLong) {
		t.Fatalf("grep error = %v, want size limit error", err)
	}
}

func TestInvalidMetadataTimestamps(t *testing.T) {
	for _, field := range []string{"created_at", "last_updated"} {
		t.Run(field, func(t *testing.T) {
			meta := `{"session_id":"test","title":"session title","` + field + `":"not-a-time"}`
			store, path := errorTestStore(t, meta, validHistory)
			_, err := store.Scan()
			var parseErr *time.ParseError
			if !errors.As(err, &parseErr) || !strings.Contains(err.Error(), metadataPathFor(path)) || !strings.Contains(err.Error(), field) {
				t.Fatalf("error = %v, want timestamp parse error with metadata context", err)
			}
		})
	}
}

func TestMissingMetadataTimestampsUseMtime(t *testing.T) {
	_, path := errorTestStore(t, `{"session_id":"test","title":"session title"}`, validHistory)
	mtime := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	sess, _, startedAt, _, err := parseFile(path, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !startedAt.IsZero() || !sess.LastTime.Equal(mtime) {
		t.Fatalf("startedAt = %v, lastTime = %v; want zero and mtime", startedAt, sess.LastTime)
	}
}

func TestGrepRejectsLegacyCacheForMalformedHistory(t *testing.T) {
	store, path := errorTestStore(t, validMetadata, validHistory+"\n"+`{"role":`)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Version 2 could cache partial text after silently skipping malformed records.
	data, err := json.Marshal(map[string]any{
		path: map[string]any{
			"v": 2, "path": path, "size": fi.Size(),
			"mod_time_unix_nano": fi.ModTime().UnixNano(), "text": "", "fragments": 0,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv(grep.EnvCacheDir), "index.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := store.GrepKeys("hit", false)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) || keys != nil {
		t.Fatalf("keys = %v, error = %v, want decoding failure instead of a stale cache hit", keys, err)
	}
}

func TestParseISO(t *testing.T) {
	for _, timestamp := range []string{"", "2026-01-15T10:00:00Z", "2026-01-15T10:00:00.123456789Z", "2026-01-15T10:00:00+09:00"} {
		parsed, err := parseISO(timestamp)
		if err != nil || (parsed.IsZero() != (timestamp == "")) {
			t.Errorf("parseISO(%q) = %v, %v", timestamp, parsed, err)
		}
	}
}
