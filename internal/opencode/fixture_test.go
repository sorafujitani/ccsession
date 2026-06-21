package opencode

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
)

// schemaSQL is the minimal subset of OpenCode's schema that ccsession depends
// on. It doubles as documentation: if a query needs a column not listed here,
// the fixture won't have it and the test will fail loudly.
const schemaSQL = `
CREATE TABLE session (
	id            TEXT PRIMARY KEY,
	parent_id     TEXT,
	directory     TEXT NOT NULL DEFAULT '',
	title         TEXT NOT NULL DEFAULT '',
	time_created  INTEGER NOT NULL DEFAULT 0,
	time_updated  INTEGER NOT NULL DEFAULT 0,
	time_archived INTEGER
);
CREATE TABLE message (
	id           TEXT PRIMARY KEY,
	session_id   TEXT NOT NULL,
	time_created INTEGER NOT NULL DEFAULT 0,
	data         TEXT NOT NULL
);
CREATE TABLE part (
	id         TEXT PRIMARY KEY,
	message_id TEXT NOT NULL,
	session_id TEXT NOT NULL,
	data       TEXT NOT NULL
);
CREATE TABLE session_message (
	id         TEXT PRIMARY KEY,
	session_id TEXT NOT NULL,
	type       TEXT NOT NULL,
	data       TEXT NOT NULL,
	seq        INTEGER NOT NULL
);
CREATE TABLE migration (id TEXT PRIMARY KEY);
`

type fixture struct {
	t    testing.TB
	path string
	db   *sql.DB // writable seed connection, kept open so WAL data stays live
	seq  int     // monotonic id counter for parts/messages
}

type fixtureOpts struct {
	wal       bool
	noSchema  bool // skip schema creation (for the "unknown schema" degradation test)
	dropTable string
}

// newFixture creates a real SQLite database at <dir>/opencode.db and returns a
// handle for seeding. The writable connection stays open until cleanup so that
// in WAL mode the rows live in the un-checkpointed -wal sidecar, exercising the
// read path that immutable=1 would have hidden.
func newFixture(t testing.TB, opts fixtureOpts) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.db")

	u := url.URL{Scheme: "file", Path: path}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if opts.wal {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			t.Fatalf("set WAL: %v", err)
		}
	}
	if !opts.noSchema {
		if _, err := db.Exec(schemaSQL); err != nil {
			t.Fatalf("create schema: %v", err)
		}
	}
	f := &fixture{t: t, path: path, db: db}
	if opts.dropTable != "" {
		f.exec("DROP TABLE " + opts.dropTable)
	}
	return f
}

func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	if _, err := f.db.Exec(q, args...); err != nil {
		f.t.Fatalf("exec %q: %v", q, err)
	}
}

// open returns a read-only opencode.DB against the fixture, as production does.
func (f *fixture) open() *DB {
	f.t.Helper()
	d, err := openAt(f.path)
	if err != nil {
		f.t.Fatalf("openAt: %v", err)
	}
	f.t.Cleanup(func() { d.Close() })
	return d
}

// session inserts a root session (parent_id NULL, not archived). created==updated.
func (f *fixture) session(id, dir, title string, updatedMs int64) {
	f.exec(`INSERT INTO session (id, parent_id, directory, title, time_created, time_updated, time_archived)
		VALUES (?, NULL, ?, ?, ?, ?, NULL)`, id, dir, title, updatedMs, updatedMs)
}

func (f *fixture) childSession(id, parentID, dir, title string, updatedMs int64) {
	f.exec(`INSERT INTO session (id, parent_id, directory, title, time_created, time_updated, time_archived)
		VALUES (?, ?, ?, ?, ?, ?, NULL)`, id, parentID, dir, title, updatedMs, updatedMs)
}

func (f *fixture) archivedSession(id, dir, title string, updatedMs int64) {
	f.exec(`INSERT INTO session (id, parent_id, directory, title, time_created, time_updated, time_archived)
		VALUES (?, NULL, ?, ?, ?, ?, ?)`, id, dir, title, updatedMs, updatedMs, updatedMs)
}

// partsTurn inserts a message plus one text part per body, mirroring the live
// write path. createdMs sets the message time.
func (f *fixture) partsTurn(sessionID, role string, createdMs int64, bodies ...string) {
	f.seq++
	msgID := mkID("msg", f.seq)
	data := `{"role":"` + role + `","time":{"created":` + itoa(createdMs) + `}}`
	f.exec(`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
		msgID, sessionID, createdMs, data)
	for _, b := range bodies {
		f.seq++
		partData := `{"type":"text","text":` + jsonString(b) + `}`
		f.exec(`INSERT INTO part (id, message_id, session_id, data) VALUES (?, ?, ?, ?)`,
			mkID("prt", f.seq), msgID, sessionID, partData)
	}
}

// reasoningTurn inserts a message whose only part is non-text (reasoning), so
// its rendered body is empty but it still counts as a turn.
func (f *fixture) reasoningTurn(sessionID, role string, createdMs int64) {
	f.seq++
	msgID := mkID("msg", f.seq)
	data := `{"role":"` + role + `","time":{"created":` + itoa(createdMs) + `}}`
	f.exec(`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
		msgID, sessionID, createdMs, data)
	f.seq++
	f.exec(`INSERT INTO part (id, message_id, session_id, data) VALUES (?, ?, ?, ?)`,
		mkID("prt", f.seq), msgID, sessionID, `{"type":"reasoning","text":"thinking"}`)
}

// projectionRow inserts a session_message row with the raw data JSON verbatim.
func (f *fixture) projectionRow(sessionID, typ string, seq int64, data string) {
	f.seq++
	f.exec(`INSERT INTO session_message (id, session_id, type, data, seq) VALUES (?, ?, ?, ?, ?)`,
		mkID("sm", f.seq), sessionID, typ, data, seq)
}

func (f *fixture) migrations(ids ...string) {
	for _, id := range ids {
		f.exec(`INSERT INTO migration (id) VALUES (?)`, id)
	}
}

func mkID(prefix string, n int) string { return prefix + "_" + itoa(int64(n)) }

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// jsonString quotes s as a JSON string literal (handles quotes/backslashes).
func jsonString(s string) string {
	var b []byte
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
		case '\n':
			b = append(b, '\\', 'n')
		default:
			b = append(b, string(r)...)
		}
	}
	b = append(b, '"')
	return string(b)
}
