package kiro

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
)

func TestClassicCacheWarmDoesNotReadBodies(t *testing.T) {
	for _, mode := range []string{"DELETE", "WAL"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			if mode == "WAL" {
				if _, err := f.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
					t.Fatal(err)
				}
			}
			f.classic("id", "prompt", "answer", 1000)
			store := OpenAt(f.home)
			if classicDatabaseSnapshot(store.dbPath) == "" {
				t.Skip("metadata-only validation is unavailable on this platform")
			}
			if _, err := store.Scan(); err != nil {
				t.Fatal(err)
			}
			db, err := store.openClassic()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			conn, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			err = conn.Raw(func(raw any) error {
				return raw.(driver.Conn).Raw().SetAuthorizer(func(action sqlite3.AuthorizerActionCode, table, column, schema, inner string) sqlite3.AuthorizerReturnCode {
					if action == sqlite3.AUTH_READ && table == "conversations_v2" && column == "value" {
						return sqlite3.AUTH_DENY
					}
					return sqlite3.AUTH_OK
				})
			})
			conn.Close()
			if err != nil {
				t.Fatal(err)
			}
			ss, err := store.cachedClassicSessions(db, "", "", false)
			if err != nil || len(ss) != 1 || ss[0].Label != "prompt" {
				t.Fatalf("warm scan attempted a body read: %#v, error=%v", ss, err)
			}
		})
	}
}

func TestClassicCacheSingleRefreshDoesNotValidateOtherRows(t *testing.T) {
	f := newFixture(t)
	f.classic("first", "first prompt", "answer", 1000)
	f.classic("second", "old prompt", "answer", 2000)
	store := OpenAt(f.home)
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE conversations_v2 SET value=json_set(value,
		'$.history[0].user.content.Prompt.prompt', ?) WHERE conversation_id=?`, "new prompt", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindByLocator("first", classicPrefix+f.cwd); err != nil {
		t.Fatal(err)
	}
	ss, count, err := countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 1 || len(ss) != 2 || sessionsByID(ss)["second"].Label != "new prompt" {
		t.Fatalf("single lookup falsely validated another row: %#v, count=%d, error=%v", ss, count, err)
	}
}

func TestClassicDatabaseSnapshotWALPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.sqlite3")
	wal := make([]byte, 32+24+512)
	binary.BigEndian.PutUint32(wal[8:12], 512)
	for name, data := range map[string][]byte{path: make([]byte, 100), path + "-wal": wal, path + "-shm": make([]byte, 136)} {
		if err := os.WriteFile(name, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := classicDatabaseSnapshot(path)
	if before == "" {
		t.Skip("metadata-only validation is unavailable on this platform")
	}
	shared, err := os.OpenFile(path+"-shm", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer shared.Close()
	if _, err := shared.WriteAt([]byte{1}, 100); err != nil {
		t.Fatal(err)
	}
	if got := classicDatabaseSnapshot(path); got != before {
		t.Fatal("reader marks must not invalidate the DB snapshot")
	}
	if _, err := shared.WriteAt([]byte{1}, 16); err != nil {
		t.Fatal(err)
	}
	if got := classicDatabaseSnapshot(path); got == before || got == "" {
		t.Fatal("WAL publication was not detected independently of WAL file writes")
	}
}

func TestClassicCacheUpdateDuringMetadataRead(t *testing.T) {
	f := newFixture(t)
	if _, err := f.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	f.classic("id", "old prompt", "answer", 1000)
	store := OpenAt(f.home)
	if classicDatabaseSnapshot(store.dbPath) == "" {
		t.Skip("metadata-only validation is unavailable on this platform")
	}
	if _, err := store.Scan(); err != nil {
		t.Fatal(err)
	}
	db, err := store.openClassic()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wrote := false
	var writeErr error
	err = conn.Raw(func(raw any) error {
		return raw.(driver.Conn).Raw().SetAuthorizer(func(action sqlite3.AuthorizerActionCode, table, column, schema, inner string) sqlite3.AuthorizerReturnCode {
			if !wrote && action == sqlite3.AUTH_READ && table == "conversations_v2" && column == "updated_at" {
				wrote = true
				// Keep updated_at unchanged to exercise the snapshot guard itself.
				_, writeErr = f.db.Exec(`UPDATE conversations_v2 SET value=json_set(value,
					'$.history[0].user.content.Prompt.prompt', ?)`, "new prompt")
			}
			return sqlite3.AUTH_OK
		})
	})
	conn.Close()
	if err != nil {
		t.Fatal(err)
	}
	ss, err := store.cachedClassicSessions(db, "", "", false)
	if err != nil || !wrote || writeErr != nil || len(ss) != 1 {
		t.Fatalf("concurrent metadata scan: %#v, wrote=%v, write error=%v, error=%v", ss, wrote, writeErr, err)
	}
	path, abs := classicCachePath(store.dbPath)
	if cache := readClassicCache(path, abs); cache.Snapshot != "" {
		t.Fatal("concurrent write must not save a reusable DB snapshot")
	}
	fresh, err := store.Scan()
	if err != nil || len(fresh) != 1 || fresh[0].Label != "new prompt" {
		t.Fatalf("next scan reused a stale label: %#v, error=%v", fresh, err)
	}
}
