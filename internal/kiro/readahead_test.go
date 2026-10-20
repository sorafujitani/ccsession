package kiro

import (
	"crypto/sha256"
	"database/sql"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ncruces/go-sqlite3/driver"
	"github.com/ncruces/go-sqlite3/util/vfsutil"
	"github.com/ncruces/go-sqlite3/vfs"
)

type countingFile struct {
	vfs.File
	data  []byte
	reads int
}

func (f *countingFile) ReadAt(p []byte, off int64) (int, error) {
	f.reads++
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *countingFile) Lock(vfs.LockLevel) error   { return nil }
func (f *countingFile) Unlock(vfs.LockLevel) error { return nil }

func readPages(t *testing.T, f vfs.File, from, to int) {
	t.Helper()
	page := make([]byte, 4096)
	for i := from; i < to; i++ {
		if n, err := f.ReadAt(page, int64(i)*4096); n != len(page) || err != nil {
			t.Fatalf("page %d: n=%d, error=%v", i, n, err)
		}
	}
}

func TestReadAheadBuffersSequentialReadsUnderLock(t *testing.T) {
	data := make([]byte, 4096*512)
	for i := range data {
		data[i] = byte(i / 4096)
	}
	base := &countingFile{data: data}
	f := &readAheadFile{File: base}
	// SQLite asks for shared memory when it opens any file, before locking;
	// only a request under a lock means WAL.
	f.SharedMemory()

	readPages(t, f, 1, 64)
	if base.reads != 63 {
		t.Fatalf("unlocked reads were buffered: %d", base.reads)
	}
	if err := f.Lock(vfs.LOCK_SHARED); err != nil {
		t.Fatal(err)
	}
	base.reads = 0
	readPages(t, f, 100, 400)
	if base.reads > 20 {
		t.Fatalf("sequential reads were not buffered: %d preads for 300 pages", base.reads)
	}
	page := make([]byte, 4096)
	for i := 100; i < 400; i += 37 {
		if _, err := f.ReadAt(page, int64(i)*4096); err != nil || page[0] != byte(i) || page[4095] != byte(i) {
			t.Fatalf("page %d served wrong data: %d", i, page[0])
		}
	}

	// A new transaction must not see bytes buffered by the previous one.
	data[401*4096] = 0xee
	if err := f.Unlock(vfs.LOCK_NONE); err != nil {
		t.Fatal(err)
	}
	if err := f.Lock(vfs.LOCK_SHARED); err != nil {
		t.Fatal(err)
	}
	base.reads = 0
	if _, err := f.ReadAt(page, 401*4096); err != nil || page[0] != 0xee || base.reads != 1 {
		t.Fatalf("stale buffer after lock transition: byte=%x, reads=%d, error=%v", page[0], base.reads, err)
	}

	// Short reads at EOF still return what the file holds.
	readPages(t, f, 402, 512)
	if n, err := f.ReadAt(page, 512*4096); n != 0 || err != io.EOF {
		t.Fatalf("read past EOF: n=%d, error=%v", n, err)
	}
}

func TestReadAheadDisabledForWALHeader(t *testing.T) {
	data := make([]byte, 4096*64)
	data[18], data[19] = 2, 2
	base := &countingFile{data: data}
	f := &readAheadFile{File: base}
	if err := f.Lock(vfs.LOCK_SHARED); err != nil {
		t.Fatal(err)
	}
	header := make([]byte, 100)
	if _, err := f.ReadAt(header, 0); err != nil {
		t.Fatal(err)
	}
	base.reads = 0
	readPages(t, f, 1, 64)
	if base.reads != 63 {
		t.Fatalf("WAL database was buffered: %d preads for 63 pages", base.reads)
	}
}

// Large rows span many overflow pages, the reads read-ahead serves. Each
// committed rewrite, including a same-length one, must be visible.
func TestReadAheadSeesCommittedRewrites(t *testing.T) {
	f := newFixture(t)
	body := strings.Repeat("a", 3<<20)
	f.classic("id", "prompt", body, 1000)
	db, err := OpenAt(f.home).openClassic()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for i, fill := range []string{"a", "b", "c"} {
		if i > 0 {
			if _, err := f.db.Exec(`UPDATE conversations_v2 SET value=replace(value, ?, ?)`,
				strings.Repeat(string(rune('a'+i-1)), 1000), strings.Repeat(fill, 1000)); err != nil {
				t.Fatal(err)
			}
		}
		var raw []byte
		if err := db.QueryRow(`SELECT value FROM conversations_v2`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var want []byte
		if err := f.db.QueryRow(`SELECT value FROM conversations_v2`).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if sha256.Sum256(raw) != sha256.Sum256(want) || !strings.Contains(string(raw), strings.Repeat(fill, 1000)) {
			t.Fatalf("rewrite %d: read-ahead returned stale pages", i)
		}
	}
}

// A connection opened on a rollback-journal database must stop buffering
// when another connection switches it to WAL; checkpoints then rewrite pages
// under the WAL pager's long-lived lock.
func TestReadAheadStopsWhenDatabaseSwitchesToWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.sqlite3")
	w, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: path}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.SetMaxOpenConns(1)
	mustExec := func(db *sql.DB, q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(w, `PRAGMA journal_mode=DELETE`)
	mustExec(w, `CREATE TABLE t(id INTEGER PRIMARY KEY, v BLOB)`)
	mustExec(w, `WITH RECURSIVE c(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM c WHERE i<600)
		INSERT INTO t SELECT i, CAST(printf('%010d', i) || zeroblob(3000) AS BLOB) FROM c`)

	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&vfs=" + classicVFS + "&_pragma=busy_timeout(5000)&_pragma=query_only(1)"}
	c, err := sql.Open("sqlite3", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetMaxOpenConns(1)
	q := `SELECT sum(CAST(CAST(substr(v,1,10) AS TEXT) AS INTEGER)), count(*) FROM t`
	check := func(step string) {
		t.Helper()
		var gs, gc, ws, wc int64
		if err := c.QueryRow(q).Scan(&gs, &gc); err != nil {
			t.Fatalf("%s: reader: %v", step, err)
		}
		if err := w.QueryRow(q).Scan(&ws, &wc); err != nil {
			t.Fatalf("%s: writer: %v", step, err)
		}
		if gs != ws || gc != wc {
			t.Errorf("%s: stale read sum=%d count=%d, want %d %d", step, gs, gc, ws, wc)
		}
	}
	check("rollback")
	mustExec(w, `PRAGMA journal_mode=WAL`)
	mustExec(w, `INSERT INTO t VALUES (10001, CAST(printf('%010d', 1) || zeroblob(20000) AS BLOB))`)
	check("after switch to WAL")
	mustExec(w, `UPDATE t SET v = CAST(printf('%010d', id+1000) || zeroblob(3000) AS BLOB) WHERE id <= 600`)
	mustExec(w, `PRAGMA wal_checkpoint(PASSIVE)`)
	mustExec(w, `INSERT INTO t VALUES (10002, CAST(printf('%010d', 1) || zeroblob(20000) AS BLOB))`)
	check("after update+checkpoint+restart")
	// Point lookups that land in the window buffered by the last full scan.
	mustExec(w, `UPDATE t SET v = CAST(printf('%010d', id+5000) || zeroblob(3000) AS BLOB) WHERE id <= 600`)
	mustExec(w, `PRAGMA wal_checkpoint(PASSIVE)`)
	mustExec(w, `INSERT INTO t VALUES (10003, CAST(printf('%010d', 1) || zeroblob(20000) AS BLOB))`)
	for _, id := range []int{600, 590, 550, 500, 450} {
		var got, want string
		if err := c.QueryRow(`SELECT CAST(substr(v,1,10) AS TEXT) FROM t WHERE id=?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if err := w.QueryRow(`SELECT CAST(substr(v,1,10) AS TEXT) FROM t WHERE id=?`, id).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("id %d: stale read %s, want %s", id, got, want)
		}
	}
}

func TestOpenClassicUsesReadAhead(t *testing.T) {
	f := newFixture(t)
	f.classic("id", "prompt", "answer", 1000)
	db, err := OpenAt(f.home).openClassic()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRowContext(t.Context(), `SELECT count(*) FROM conversations_v2`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	err = conn.Raw(func(raw any) error {
		// ncruces wraps the file again for checksums.
		file, ok := vfsutil.UnwrapFile[*readAheadFile](raw.(driver.Conn).Raw().Filename("main").DatabaseFile())
		if !ok || file.wal {
			t.Fatalf("main database is not read ahead: wrapped=%v", ok)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
