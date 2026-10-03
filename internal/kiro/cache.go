package kiro

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/sorafujitani/ccsession/internal/session"
)

const classicCacheVersion = 2

const classicCachedLabelQuery = `SELECT c.key, c.conversation_id, COALESCE(
	ccsession_kiro_cached_label(c.key, c.conversation_id, c.updated_at, c.value), (
		SELECT json_extract(h.value, '$.user.content.Prompt.prompt')
		FROM jsonb_each(c.value, '$.history') h
		WHERE json_type(h.value, '$.user.content.Prompt.prompt') = 'text'
		ORDER BY CAST(h.key AS INTEGER) DESC LIMIT 1
	), ''), c.updated_at FROM conversations_v2 c`

type classicCacheKey struct {
	CWD string `json:"cwd"`
	ID  string `json:"id"`
}

type classicCacheEntry struct {
	classicCacheKey
	UpdatedAt int64  `json:"updated_at"`
	Hash      string `json:"hash"`
	Label     string `json:"label"`
}

type classicCache struct {
	Version  int                 `json:"version"`
	DBPath   string              `json:"db_path"`
	Snapshot string              `json:"snapshot,omitempty"`
	Entries  []classicCacheEntry `json:"entries"`
}

func (s *Store) cachedClassicSessions(db *sql.DB, id, cwd string, single bool) ([]*session.Session, error) {
	cachePath, dbPath := classicCachePath(s.dbPath)
	cache := readClassicCache(cachePath, dbPath)
	byKey := make(map[classicCacheKey]classicCacheEntry, len(cache.Entries))
	for _, entry := range cache.Entries {
		byKey[entry.classicCacheKey] = entry
	}
	beforeSnapshot := classicDatabaseSnapshot(s.dbPath)
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if beforeSnapshot != "" && cache.Snapshot == beforeSnapshot {
		q := `SELECT key, conversation_id, updated_at FROM conversations_v2`
		var args []any
		if single {
			q += ` WHERE key = ? AND conversation_id = ?`
			args = []any{cwd, id}
		}
		rows, err := tx.QueryContext(context.Background(), q+` ORDER BY key`, args...)
		if err != nil {
			return nil, err
		}
		valid, count := true, 0
		var out []*session.Session
		for rows.Next() {
			var key classicCacheKey
			var updatedAt int64
			if err := rows.Scan(&key.CWD, &key.ID, &updatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			count++
			entry, ok := byKey[key]
			if !ok || entry.UpdatedAt != updatedAt {
				valid = false
				continue
			}
			if sess := newSession(key.ID, key.CWD, entry.Label, msToTime(updatedAt),
				filepath.Dir(s.dbPath), classicPrefix+key.CWD); sess != nil {
				out = append(out, sess)
			}
		}
		err = rows.Err()
		if closeErr := rows.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return nil, err
		}
		if valid && (single || count == len(cache.Entries)) && classicDatabaseSnapshot(s.dbPath) == beforeSnapshot {
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return out, nil
		}
	}
	changed := false
	err = conn.Raw(func(raw any) error {
		return raw.(driver.Conn).Raw().CreateFunction("ccsession_kiro_cached_label", 4, sqlite3.DIRECTONLY,
			func(ctx sqlite3.Context, args ...sqlite3.Value) {
				key := classicCacheKey{CWD: args[0].Text(), ID: args[1].Text()}
				updatedAt := args[2].Int64()
				// ponytail: changed DBs still hash all rows; selective validation
				// needs a verified per-row revision, not just updated_at.
				// SQLite owns these bytes; no conversation-sized Go copy is made.
				sum := sha256.Sum256(args[3].RawText())
				hash := hex.EncodeToString(sum[:])
				entry, ok := byKey[key]
				if ok && entry.Hash == hash && entry.UpdatedAt == updatedAt {
					ctx.ResultText(entry.Label)
					return
				}
				byKey[key] = classicCacheEntry{classicCacheKey: key, Hash: hash, UpdatedAt: updatedAt}
				changed = true
				ctx.ResultNull()
			})
	})
	if err != nil {
		return nil, err
	}
	q := classicCachedLabelQuery
	var args []any
	if single {
		q += ` WHERE c.key = ? AND c.conversation_id = ?`
		args = []any{cwd, id}
	}
	// The same transaction pins metadata, fingerprints, and cache misses,
	// even when another connection updates the WAL.
	rows, err := tx.QueryContext(context.Background(), q+` ORDER BY c.key`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []classicCacheEntry
	var out []*session.Session
	for rows.Next() {
		var key classicCacheKey
		var label string
		var updatedAt int64
		if err := rows.Scan(&key.CWD, &key.ID, &label, &updatedAt); err != nil {
			return nil, err
		}
		entry := byKey[key]
		entry.Label = ""
		sess := newSession(key.ID, key.CWD, label, msToTime(updatedAt),
			filepath.Dir(s.dbPath), classicPrefix+key.CWD)
		if sess != nil {
			entry.Label = sess.Label
			out = append(out, sess)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	snapshot := classicDatabaseSnapshot(s.dbPath)
	// A single-row refresh cannot validate the untouched cache entries.
	if single || snapshot != beforeSnapshot {
		snapshot = ""
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if changed || (!single && len(entries) != len(cache.Entries)) || cache.Snapshot != snapshot {
		cache.Snapshot = snapshot
		if single {
			for _, entry := range entries {
				found := false
				for i := range cache.Entries {
					if cache.Entries[i].classicCacheKey == entry.classicCacheKey {
						cache.Entries[i] = entry
						found = true
						break
					}
				}
				if !found {
					cache.Entries = append(cache.Entries, entry)
				}
			}
		} else {
			cache.Entries = entries
		}
		if cachePath != "" {
			_ = writeClassicCache(cachePath, cache)
		}
	}
	return out, nil
}

func classicCachePath(dbPath string) (string, string) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return "", ""
	}
	dir := strings.TrimSpace(os.Getenv(session.EnvCacheDir))
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", abs
		}
		dir = filepath.Join(base, "ccsession", "scan")
	}
	sum := sha256.Sum256([]byte(abs))
	return filepath.Join(dir, "kiro", hex.EncodeToString(sum[:])+".json"), abs
}

func readClassicCache(path, dbPath string) classicCache {
	var cache classicCache
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &cache) == nil && cache.Version == classicCacheVersion && cache.DBPath == dbPath {
			return cache
		}
	}
	return classicCache{Version: classicCacheVersion, DBPath: dbPath}
}

func writeClassicCache(path string, cache classicCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(cache); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
