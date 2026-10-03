package kiro

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/sorafujitani/ccsession/internal/session"
)

// Count actual label extractions, not statement preparation. Existing scan
// tests separately exercise SQLite's real json_extract and label semantics.
func countedClassicScan(t *testing.T, store *Store, id, cwd string, single bool, hook func() error) ([]*session.Session, int, error) {
	t.Helper()
	db, err := store.openClassic()
	if err != nil || db == nil {
		t.Fatalf("open classic: db=%v, error=%v", db, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	err = conn.Raw(func(raw any) error {
		native := raw.(driver.Conn).Raw()
		return native.CreateFunction("json_extract", 2, 0,
			func(ctx sqlite3.Context, args ...sqlite3.Value) {
				count++
				if hook != nil {
					fn := hook
					hook = nil
					if err := fn(); err != nil {
						ctx.ResultError(err)
						return
					}
				}
				// jsonb_each returns binary JSON; normalize it through SQLite
				// before this test-only decoder counts the extraction.
				stmt, _, err := native.Prepare(`SELECT json(?)`)
				if err != nil {
					ctx.ResultError(err)
					return
				}
				defer stmt.Close()
				if err := stmt.BindBlob(1, args[0].RawBlob()); err != nil {
					ctx.ResultError(err)
					return
				}
				if !stmt.Step() {
					ctx.ResultError(stmt.Err())
					return
				}
				var turn classicTurn
				if err := json.Unmarshal([]byte(stmt.ColumnText(0)), &turn); err != nil {
					ctx.ResultError(err)
					return
				}
				if turn.User.Content.Prompt == nil {
					ctx.ResultNull()
				} else {
					ctx.ResultText(turn.User.Content.Prompt.Prompt)
				}
			})
	})
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	ss, err := store.cachedClassicSessions(db, id, cwd, single)
	return ss, count, err
}

func TestClassicCacheRowLifecycle(t *testing.T) {
	f := newFixture(t)
	f.classic("first", "first prompt", "answer", 1000)
	f.classic("second", "", "answer", 2000)
	otherCWD := t.TempDir()
	f.classicAt(otherCWD, "first", "other directory", "answer", 3000)
	store := OpenAt(f.home)

	cold, count, err := countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 3 || len(cold) != 3 {
		t.Fatalf("cold: count=%d, sessions=%d, error=%v", count, len(cold), err)
	}
	warm, count, err := countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 0 || !reflect.DeepEqual(cold, warm) {
		t.Fatalf("warm: count=%d, sessions=%#v, error=%v", count, warm, err)
	}
	path, abs := classicCachePath(store.dbPath)
	cache := readClassicCache(path, abs)
	if len(cache.Entries) != 3 {
		t.Fatalf("cache entries=%d, want 3 distinct directory/ID pairs", len(cache.Entries))
	}
	for _, check := range []struct {
		path string
		mode os.FileMode
	}{{path, 0o600}, {filepath.Dir(path), 0o700}} {
		info, err := os.Stat(check.path)
		if err != nil || info.Mode().Perm() != check.mode {
			t.Fatalf("permissions: path=%s, info=%v, error=%v", check.path, info, err)
		}
	}

	before, err := store.Scan()
	if err != nil {
		t.Fatal(err)
	}
	after, err := store.Scan()
	if err != nil || !reflect.DeepEqual(before, after) || len(after) != 2 {
		t.Fatalf("representative selection/order changed: %#v, error=%v", after, err)
	}
	byID := sessionsByID(after)
	if byID["first"].Label != "other directory" || byID["second"].Label != "(no summary)" {
		t.Fatalf("representatives or empty label: %#v", after)
	}

	for _, bumpTime := range []bool{true, false} {
		q := `UPDATE conversations_v2 SET value=json_set(value, '$.history[0].user.content.Prompt.prompt', ?)`
		if bumpTime {
			q += `, updated_at=updated_at+1000`
		}
		q += ` WHERE key=? AND conversation_id=?`
		label := "changed prompt"
		if !bumpTime {
			label = "another prompt" // Same timestamp and byte length as the previous label.
		}
		if _, err := f.db.Exec(q, label, f.cwd, "first"); err != nil {
			t.Fatal(err)
		}
		ss, count, err := countedClassicScan(t, store, "", "", false, nil)
		if err != nil || count != 1 {
			t.Fatalf("updated row: count=%d, error=%v", count, err)
		}
		found := false
		for _, sess := range ss {
			if sess.ID == "first" && sess.CWD == f.cwd {
				found = true
				if sess.Label != label || sess.LastTime.UnixMilli() != 2000 {
					t.Fatalf("stale row: %#v", sess)
				}
			}
		}
		if !found {
			t.Fatal("updated row missing")
		}
	}

	single, count, err := countedClassicScan(t, store, "first", f.cwd, true, nil)
	if err != nil || count != 0 || len(single) != 1 || single[0].CWD != f.cwd {
		t.Fatalf("single cache hit: %#v, count=%d, error=%v", single, count, err)
	}
	if _, err := f.db.Exec(`UPDATE conversations_v2 SET value=json_set(value,
		'$.history[0].user.content.Prompt.prompt', ?) WHERE key=? AND conversation_id=?`,
		"locator refresh", f.cwd, "first"); err != nil {
		t.Fatal(err)
	}
	single, count, err = countedClassicScan(t, store, "first", f.cwd, true, nil)
	if err != nil || count != 1 || len(single) != 1 || single[0].Label != "locator refresh" {
		t.Fatalf("single refresh: %#v, count=%d, error=%v", single, count, err)
	}
	if cache := readClassicCache(path, abs); len(cache.Entries) != 3 {
		t.Fatalf("single refresh discarded unrelated cache entries: %d", len(cache.Entries))
	}
	_, count, err = countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 0 {
		t.Fatalf("full scan after single refresh: count=%d, error=%v", count, err)
	}
	if _, err := store.FindByLocator("missing", classicPrefix+f.cwd); !errors.Is(err, session.ErrSessionFileMissing) {
		t.Fatalf("missing locator: %v", err)
	}
	if _, err := f.db.Exec(`DELETE FROM conversations_v2 WHERE conversation_id=?`, "second"); err != nil {
		t.Fatal(err)
	}
	f.classic("added", "new row", "answer", 4000)
	ss, count, err := countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 1 || len(ss) != 3 {
		t.Fatalf("add/delete: count=%d, sessions=%d, error=%v", count, len(ss), err)
	}
	cache = readClassicCache(path, abs)
	for _, entry := range cache.Entries {
		if entry.ID == "second" {
			t.Fatal("deleted row retained in full-scan cache")
		}
	}
	if err := os.Remove(f.cwd); err != nil {
		t.Fatal(err)
	}
	ss, count, err = countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 0 {
		t.Fatalf("current CWD state: count=%d, error=%v", count, err)
	}
	for _, sess := range ss {
		if sess.CWD == f.cwd && sess.CWDExists {
			t.Fatal("cached Session hid deleted CWD")
		}
	}
}

func TestClassicCacheStoresOnlyDisplayLabel(t *testing.T) {
	f := newFixture(t)
	f.classic("id", "\n\t"+strings.Repeat("long prompt ", 1000), "answer", 1000)
	store := OpenAt(f.home)
	cold, err := store.Scan()
	if err != nil || len(cold) != 1 {
		t.Fatalf("cold scan: %#v, error=%v", cold, err)
	}
	path, abs := classicCachePath(store.dbPath)
	cache := readClassicCache(path, abs)
	if len(cache.Entries) != 1 || cache.Entries[0].Label != cold[0].Label {
		t.Fatal("cache retained the full prompt instead of its display label")
	}
	warm, err := store.Scan()
	if err != nil || !reflect.DeepEqual(cold, warm) {
		t.Fatalf("cached truncation changed the label: %#v, error=%v", warm, err)
	}
}

func TestClassicCacheRecovery(t *testing.T) {
	for _, name := range []string{"corrupt", "old_version", "wrong_db", "write_failure"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.classic("id", "current prompt", "answer", 1000)
			store := OpenAt(f.home)
			if _, err := store.Scan(); err != nil {
				t.Fatal(err)
			}
			path, abs := classicCachePath(store.dbPath)
			cache := readClassicCache(path, abs)
			switch name {
			case "corrupt":
				if err := os.WriteFile(path, []byte(`{"entries":`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "old_version":
				cache.Version--
				if err := writeClassicCache(path, cache); err != nil {
					t.Fatal(err)
				}
			case "wrong_db":
				cache.DBPath += ".other"
				if err := writeClassicCache(path, cache); err != nil {
					t.Fatal(err)
				}
			case "write_failure":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ss, count, err := countedClassicScan(t, store, "", "", false, nil)
			if err != nil || count != 1 || len(ss) != 1 || ss[0].Label != "current prompt" {
				t.Fatalf("fallback: %#v, count=%d, error=%v", ss, count, err)
			}
		})
	}
}

func TestClassicCacheWALSnapshot(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	f.classic("id", "old prompt", "answer", 1000)
	store := OpenAt(f.home)
	before, err := os.Stat(store.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ss, count, err := countedClassicScan(t, store, "", "", false, func() error {
		_, err := f.db.Exec(`UPDATE conversations_v2
			SET value=json_set(value, '$.history[0].user.content.Prompt.prompt', ?), updated_at=?
			WHERE conversation_id=?`, "WAL prompt", 2000, "id")
		return err
	})
	if err != nil || count != 1 || len(ss) != 1 || ss[0].Label != "old prompt" || ss[0].LastTime.UnixMilli() != 1000 {
		t.Fatalf("mixed snapshot: %#v, count=%d, error=%v", ss, count, err)
	}
	after, err := os.Stat(store.dbPath)
	if err != nil || !before.ModTime().Equal(after.ModTime()) || before.Size() != after.Size() {
		t.Fatalf("fixture failed to isolate WAL write: before=%v, after=%v, error=%v", before, after, err)
	}
	ss, count, err = countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 1 || ss[0].Label != "WAL prompt" || ss[0].LastTime.UnixMilli() != 2000 {
		t.Fatalf("WAL update missed: %#v, count=%d, error=%v", ss, count, err)
	}
	_, count, err = countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 0 {
		t.Fatalf("WAL cache hit: count=%d, error=%v", count, err)
	}
}

func TestClassicCacheDatabaseReplacement(t *testing.T) {
	f := newFixture(t)
	f.classic("id", "old prompt", "answer", 1000)
	store := OpenAt(f.home)
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	cacheDir := os.Getenv(session.EnvCacheDir)
	before, err := os.Stat(store.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	replacement := newFixture(t)
	// newFixture isolates caches; reuse the original namespace for this check.
	t.Setenv(session.EnvCacheDir, cacheDir)
	replacement.classicAt(f.cwd, "id", "new prompt", "answer", 1000)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := replacement.db.Close(); err != nil {
		t.Fatal(err)
	}
	replacementPath := filepath.Join(replacement.home, "data.sqlite3")
	if err := os.Chtimes(replacementPath, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementPath, store.dbPath); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(store.dbPath)
	if err != nil || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatalf("replacement should have identical size/mtime: before=%v, after=%v, error=%v", before, after, err)
	}
	ss, count, err := countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 1 || len(ss) != 1 || ss[0].Label != "new prompt" {
		t.Fatalf("replacement reused old label: %#v, count=%d, error=%v", ss, count, err)
	}
}

func TestClassicCacheDoesNotHideDatabaseErrors(t *testing.T) {
	f := newFixture(t)
	f.classic("id", "prompt", "answer", 1000)
	store := OpenAt(f.home)
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE conversations_v2 SET value='invalid JSON'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err == nil {
		t.Fatal("cache hid malformed conversation")
	}
	if _, err := f.db.Exec(`DROP TABLE conversations_v2`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Scan(); err == nil {
		t.Fatal("cache hid missing database table")
	}
}
