package kiro

import (
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/sorafujitani/ccsession/internal/session"
)

// forceParallel sends small fixtures through the parallel path, which
// production reserves for large stores.
func forceParallel(t *testing.T) {
	old := classicParallelMinBytes
	classicParallelMinBytes = 0
	t.Cleanup(func() { classicParallelMinBytes = old })
}

func TestClassicParallelMatchesSerial(t *testing.T) {
	forceParallel(t)
	f := newFixture(t)
	other := t.TempDir()
	for i := range 40 {
		f.classic(fmt.Sprintf("id-%02d", i), fmt.Sprintf("prompt %d", i), "answer", int64(1000+i))
		if i%3 == 0 {
			// The same ID in another directory exercises representative selection.
			f.classicAt(other, fmt.Sprintf("id-%02d", i), fmt.Sprintf("other %d", i), "answer", int64(1000+i))
		}
	}
	store := OpenAt(f.home)
	serial, count, err := countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 54 {
		t.Fatalf("serial: count=%d, error=%v", count, err)
	}
	t.Setenv(session.EnvCacheDir, t.TempDir())
	db, err := store.openClassic()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshot := classicDatabaseSnapshot(store.dbPath)
	if snapshot == "" {
		t.Skip("parallel scans need a database snapshot on this platform")
	}
	rows, fresh, ok := store.parallelClassicRows(db, nil, snapshot)
	if !ok || len(rows) != 54 || len(fresh) != 54 {
		t.Fatalf("parallel: ok=%v, rows=%d, fresh=%d", ok, len(rows), len(fresh))
	}
	parallel, err := store.cachedClassicSessions(db, "", "", false)
	if err != nil || !reflect.DeepEqual(serial, parallel) {
		t.Fatalf("parallel sessions differ: %v\nserial=%#v\nparallel=%#v", err, serial, parallel)
	}
	want, err := store.Scan()
	if err != nil {
		t.Fatal(err)
	}
	_, count, err = countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 0 {
		t.Fatalf("parallel scan did not populate the cache: count=%d, error=%v", count, err)
	}
	got, err := store.Scan()
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("warm scan after parallel scan: %v", err)
	}
}

func TestClassicParallelFallsBackOnConcurrentWrite(t *testing.T) {
	forceParallel(t)
	f := newFixture(t)
	// A rollback-journal writer would wait for the scanning reader to finish.
	if _, err := f.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatal(err)
	}
	f.classic("first", "old prompt", "answer", 1000)
	f.classic("second", "second prompt", "answer", 2000)
	store := OpenAt(f.home)
	if classicDatabaseSnapshot(store.dbPath) == "" {
		t.Skip("parallel scans need a database snapshot on this platform")
	}
	native := extractClassicLabel
	t.Cleanup(func() { extractClassicLabel = native })
	// Workers call this concurrently; only the first writes.
	var wrote atomic.Bool
	extractClassicLabel = func(raw []byte) (string, bool) {
		if wrote.CompareAndSwap(false, true) {
			if _, err := f.db.Exec(`UPDATE conversations_v2 SET value=json_set(value,
				'$.history[0].user.content.Prompt.prompt', 'new prompt') WHERE conversation_id='first'`); err != nil {
				t.Error(err)
			}
		}
		return native(raw)
	}
	ss, err := store.Scan()
	if err != nil || !wrote.Load() || sessionsByID(ss)["first"].Label != "new prompt" {
		t.Fatalf("parallel scan kept a mixed snapshot: %#v, error=%v", ss, err)
	}
	extractClassicLabel = native
	path, abs := classicCachePath(store.dbPath)
	if cache := readClassicCache(path, abs); cache.Snapshot != "" {
		t.Fatal("a write during the scan must not save a reusable DB snapshot")
	}
	_, count, err := countedClassicScan(t, store, "", "", false, nil)
	if err != nil || count != 0 {
		t.Fatalf("fallback labels were not cached: count=%d, error=%v", count, err)
	}
}

func TestClassicRowRangesCoverAllRows(t *testing.T) {
	forceParallel(t)
	f := newFixture(t)
	for i := range 25 {
		f.classic(fmt.Sprintf("id-%02d", i), "prompt", fmt.Sprintf("%0*d", i*100, 0), int64(i))
	}
	if _, err := f.db.Exec(`DELETE FROM conversations_v2 WHERE conversation_id IN ('id-03', 'id-10')`); err != nil {
		t.Fatal(err)
	}
	db, err := OpenAt(f.home).openClassic()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, workers := range []int{2, 3, 8, 40} {
		ranges, err := classicRowRanges(t.Context(), db, workers)
		if err != nil || len(ranges) == 0 || len(ranges) > workers {
			t.Fatalf("workers=%d: ranges=%v, error=%v", workers, ranges, err)
		}
		var covered int
		for i, r := range ranges {
			if r[0] > r[1] || (i > 0 && r[0] <= ranges[i-1][1]) {
				t.Fatalf("workers=%d: overlapping or empty ranges %v", workers, ranges)
			}
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM conversations_v2 WHERE rowid BETWEEN ? AND ?`, r[0], r[1]).Scan(&n); err != nil {
				t.Fatal(err)
			}
			covered += n
		}
		if covered != 23 {
			t.Fatalf("workers=%d: ranges %v cover %d rows, want 23", workers, ranges, covered)
		}
	}
}
