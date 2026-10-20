package kiro

import (
	"cmp"
	"context"
	"database/sql"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// Below this much conversation text, opening extra connections costs more
// than it saves. Tests lower it to exercise the parallel path.
var classicParallelMinBytes int64 = 32 << 20

// Read-ahead removed pread contention, so scans use every core up to this.
const classicMaxWorkers = 16

// parallelClassicRows fingerprints and labels rowid ranges on separate
// connections. Each worker reads in its own transaction, so the result is used
// only when the database snapshot is unchanged after every worker finishes;
// otherwise ok is false and the caller rescans in one transaction.
func (s *Store) parallelClassicRows(db *sql.DB, byKey map[classicCacheKey]classicCacheEntry, beforeSnapshot string) ([]classicRow, map[classicCacheKey]classicCacheEntry, bool) {
	workers := min(runtime.GOMAXPROCS(0), classicMaxWorkers)
	if limit := db.Stats().MaxOpenConnections; limit > 0 {
		workers = min(workers, limit)
	}
	if workers < 2 {
		return nil, nil, false
	}
	ctx := context.Background()
	ranges, err := classicRowRanges(ctx, db, workers)
	if err != nil || len(ranges) < 2 {
		return nil, nil, false
	}
	type result struct {
		rows  []classicRow
		fresh map[classicCacheKey]classicCacheEntry
		err   error
	}
	results := make([]result, len(ranges))
	var wg sync.WaitGroup
	for i, r := range ranges {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := &results[i]
			conn, err := db.Conn(ctx)
			if err != nil {
				res.err = err
				return
			}
			defer conn.Close()
			res.fresh = make(map[classicCacheKey]classicCacheEntry)
			if res.err = registerClassicLabel(conn, byKey, res.fresh); res.err != nil {
				return
			}
			res.rows, res.err = queryClassicRows(ctx, conn,
				classicCachedLabelQuery+` WHERE c.rowid BETWEEN ? AND ?`, r[0], r[1])
		}()
	}
	wg.Wait()
	if classicDatabaseSnapshot(s.dbPath) != beforeSnapshot {
		return nil, nil, false
	}
	var rows []classicRow
	fresh := make(map[classicCacheKey]classicCacheEntry)
	for _, res := range results {
		if res.err != nil {
			return nil, nil, false
		}
		rows = append(rows, res.rows...)
		for key, entry := range res.fresh {
			fresh[key] = entry
		}
	}
	// Match the serial scan's ORDER BY key, conversation_id.
	slices.SortFunc(rows, func(a, b classicRow) int {
		return cmp.Or(strings.Compare(a.key.CWD, b.key.CWD), strings.Compare(a.key.ID, b.key.ID))
	})
	return rows, fresh, true
}

// classicRowRanges splits rowids into contiguous ranges of similar body size.
// octet_length reads only record headers, not conversation bodies.
func classicRowRanges(ctx context.Context, db *sql.DB, workers int) ([][2]int64, error) {
	rows, err := db.QueryContext(ctx, `SELECT rowid, octet_length(value) FROM conversations_v2 ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type sized struct{ rowid, bytes int64 }
	var all []sized
	var total int64
	for rows.Next() {
		var r sized
		if err := rows.Scan(&r.rowid, &r.bytes); err != nil {
			return nil, err
		}
		all = append(all, r)
		total += r.bytes
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(all) == 0 || total < classicParallelMinBytes {
		return nil, nil
	}
	share := total/int64(workers) + 1
	var ranges [][2]int64
	start, acc := all[0].rowid, int64(0)
	for i, r := range all {
		acc += r.bytes
		if acc >= share || i == len(all)-1 {
			ranges = append(ranges, [2]int64{start, r.rowid})
			if i+1 < len(all) {
				start = all[i+1].rowid
			}
			acc = 0
		}
	}
	return ranges, nil
}
