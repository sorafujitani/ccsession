package kiro

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/ncruces/go-sqlite3"
	"github.com/ncruces/go-sqlite3/driver"
	"github.com/sorafujitani/ccsession/internal/session"
)

const classicCacheVersion = 2

const classicCachedLabelQuery = `SELECT c.key, c.conversation_id, COALESCE(
	ccsession_kiro_cached_label(c.key, c.conversation_id, c.value, c.updated_at), (
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
	scan := classicScan{store: s, db: db, id: id, cwd: cwd, single: single}
	scan.cache = readClassicCache(cachePath, dbPath)
	scan.byKey = make(map[classicCacheKey]classicCacheEntry, len(scan.cache.Entries))
	for _, entry := range scan.cache.Entries {
		scan.byKey[entry.classicCacheKey] = entry
	}
	scan.before = classicDatabaseSnapshot(s.dbPath)
	if scan.before != "" && scan.cache.Snapshot == scan.before {
		out, ok, err := scan.metadataSessions()
		if err != nil || ok {
			return out, err
		}
	}
	rows, fresh, snapshot, err := scan.rows()
	if err != nil {
		return nil, err
	}
	entries := make([]classicCacheEntry, 0, len(rows))
	out := make([]*session.Session, 0, len(rows))
	for _, row := range rows {
		entry, ok := fresh[row.key]
		if !ok {
			entry = scan.byKey[row.key]
		}
		entry.Label = ""
		sess := newSession(row.key.ID, row.key.CWD, row.label, msToTime(row.updatedAt),
			filepath.Dir(s.dbPath), classicPrefix+row.key.CWD)
		if sess != nil {
			entry.Label = sess.Label
			out = append(out, sess)
		}
		entries = append(entries, entry)
	}
	cache := scan.cache
	if len(fresh) > 0 || (!single && len(entries) != len(cache.Entries)) || cache.Snapshot != snapshot {
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

// classicScan is one cached read of the classic store: all rows, or the
// single row id/cwd, validated against the snapshot taken before reading.
type classicScan struct {
	store   *Store
	db      *sql.DB
	id, cwd string
	single  bool
	cache   classicCache
	byKey   map[classicCacheKey]classicCacheEntry
	before  string
}

// metadataSessions serves an unchanged database from cached labels without
// reading any conversation body.
func (c *classicScan) metadataSessions() ([]*session.Session, bool, error) {
	ctx := context.Background()
	tx, err := c.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	q := `SELECT key, conversation_id, updated_at FROM conversations_v2`
	var args []any
	if c.single {
		q += ` WHERE key = ? AND conversation_id = ?`
		args = []any{c.cwd, c.id}
	}
	rows, err := tx.QueryContext(ctx, q+` ORDER BY key`, args...)
	if err != nil {
		return nil, false, err
	}
	valid, count := true, 0
	var out []*session.Session
	for rows.Next() {
		var key classicCacheKey
		var updatedAt int64
		if err := rows.Scan(&key.CWD, &key.ID, &updatedAt); err != nil {
			rows.Close()
			return nil, false, err
		}
		count++
		entry, ok := c.byKey[key]
		if !ok || entry.UpdatedAt != updatedAt {
			valid = false
			continue
		}
		if sess := newSession(key.ID, key.CWD, entry.Label, msToTime(updatedAt),
			filepath.Dir(c.store.dbPath), classicPrefix+key.CWD); sess != nil {
			out = append(out, sess)
		}
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, false, err
	}
	if !valid || (!c.single && count != len(c.cache.Entries)) || classicDatabaseSnapshot(c.store.dbPath) != c.before {
		return nil, false, nil
	}
	return out, true, tx.Commit()
}

type classicRow struct {
	key       classicCacheKey
	updatedAt int64
	label     string
}

// rows fingerprints and labels rows, returning cache misses in fresh and
// the snapshot the result is valid for, or "" when it cannot vouch for
// untouched rows. Full scans try parallel connections first; a concurrent
// commit or any error there falls back to one transaction, which also owns
// error reporting.
func (c *classicScan) rows() (rows []classicRow, fresh map[classicCacheKey]classicCacheEntry, snapshot string, err error) {
	if !c.single && c.before != "" {
		if rows, fresh, ok := c.store.parallelClassicRows(c.db, c.byKey, c.before); ok {
			return rows, fresh, c.before, nil
		}
	}
	rows, fresh, err = c.serialRows()
	if err != nil {
		return nil, nil, "", err
	}
	snapshot = classicDatabaseSnapshot(c.store.dbPath)
	// A single-row refresh cannot validate the untouched cache entries.
	if c.single || snapshot != c.before {
		snapshot = ""
	}
	return rows, fresh, snapshot, nil
}

// serialRows pins metadata, fingerprints, and cache misses in one read
// transaction, even when another connection updates the WAL.
func (c *classicScan) serialRows() ([]classicRow, map[classicCacheKey]classicCacheEntry, error) {
	ctx := context.Background()
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	fresh := make(map[classicCacheKey]classicCacheEntry)
	if err := registerClassicLabel(conn, c.byKey, fresh); err != nil {
		return nil, nil, err
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	q := classicCachedLabelQuery
	var args []any
	if c.single {
		q += ` WHERE c.key = ? AND c.conversation_id = ?`
		args = []any{c.cwd, c.id}
	}
	rows, err := queryClassicRows(ctx, tx, q+` ORDER BY c.key, c.conversation_id`, args...)
	if err != nil {
		return nil, nil, err
	}
	return rows, fresh, tx.Commit()
}

type classicQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func queryClassicRows(ctx context.Context, q classicQuerier, query string, args ...any) ([]classicRow, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []classicRow
	for rows.Next() {
		var row classicRow
		if err := rows.Scan(&row.key.CWD, &row.key.ID, &row.label, &row.updatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, rows.Close()
}

// registerClassicLabel installs the per-row fingerprint check on conn. Cache
// hits return their stored label; misses are recorded in fresh and labelled
// natively, or return NULL so classicCachedLabelQuery asks SQLite instead.
func registerClassicLabel(conn *sql.Conn, byKey, fresh map[classicCacheKey]classicCacheEntry) error {
	return conn.Raw(func(raw any) error {
		return raw.(driver.Conn).Raw().CreateFunction("ccsession_kiro_cached_label", 4, sqlite3.DIRECTONLY,
			func(ctx sqlite3.Context, args ...sqlite3.Value) {
				key := classicCacheKey{CWD: args[0].Text(), ID: args[1].Text()}
				// The body comes before updated_at so SQLite walks its overflow
				// chain once; updated_at, stored after it, then costs one page.
				// Read the type first: RawText may convert the value.
				text := args[2].Type() == sqlite3.TEXT
				body := args[2].RawText()
				updatedAt := args[3].Int64()
				sum := sha256.Sum256(body)
				hash := hex.EncodeToString(sum[:])
				entry, ok := byKey[key]
				if ok && entry.Hash == hash && entry.UpdatedAt == updatedAt {
					ctx.ResultText(entry.Label)
					return
				}
				fresh[key] = classicCacheEntry{classicCacheKey: key, Hash: hash, UpdatedAt: updatedAt}
				if text {
					if label, ok := extractClassicLabel(body); ok {
						ctx.ResultText(label)
						return
					}
				}
				ctx.ResultNull()
			})
	})
}

func classicCachePath(dbPath string) (string, string) {
	return session.StoreCachePath("kiro", dbPath)
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
	return session.WriteJSONAtomic(path, cache)
}
