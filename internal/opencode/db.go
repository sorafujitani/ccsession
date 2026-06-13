package opencode

import (
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// EnvDBPath points ccsession at an explicit OpenCode database file, bypassing
// discovery. It is the highest-priority source and the primary test seam.
const EnvDBPath = "OPENCODE_DB"

// ErrDBNotFound is returned by ResolveDBPath when no database is found in any
// probed location. The caller turns this into the legacy-detection / required
// -version message (it owns the probed-path list to print).
var ErrDBNotFound = errors.New("opencode database not found")

// dbGlobs are the file names a single OpenCode data directory may hold. The
// channel form (opencode-prod.db, opencode-dev.db, …) genuinely coexists with
// the default in the wild, so we glob both and pick the newest by mtime.
var dbGlobs = []string{"opencode.db", "opencode-*.db"}

// DB is a read-only handle to an OpenCode SQLite database. Every read goes
// through query() so the connection's read-only contract lives in one place.
type DB struct {
	sql  *sql.DB
	path string
}

// Path reports the database file this handle was opened against.
func (d *DB) Path() string { return d.path }

// Open resolves the database path from the environment and opens it read-only.
func Open() (*DB, error) {
	path, _, err := ResolveDBPath()
	if err != nil {
		return nil, err
	}
	return openAt(path)
}

// openAt opens a specific database file read-only.
func openAt(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", readOnlyDSN(path))
	if err != nil {
		return nil, err
	}
	return &DB{sql: db, path: path}, nil
}

// Close releases the underlying connection pool.
func (d *DB) Close() error { return d.sql.Close() }

// query is the single funnel for every read. Keeping it in one method means
// the read-only DSN and any future instrumentation can't be bypassed by a
// caller that opens its own statement.
func (d *DB) query(q string, args ...any) (*sql.Rows, error) {
	return d.sql.Query(q, args...)
}

// readOnlyDSN builds the ncruces file: URI.
//
//	mode=ro      open existing file read-only; never create.
//	query_only   reject writes at the SQL layer as a second guard.
//	busy_timeout wait out a concurrent writer's checkpoint instead of erroring.
//
// immutable=1 is deliberately NOT set: it tells SQLite the file can't change
// and to ignore the -wal sidecar, which would silently drop every message and
// session still living in an un-checkpointed WAL (the common case for a DB an
// OpenCode process is actively writing).
func readOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	return u.String()
}

// ResolveDBPath finds the OpenCode database. It returns the chosen file, the
// ordered list of locations it probed (for the not-found error message), and
// ErrDBNotFound when nothing matched.
func ResolveDBPath() (path string, probed []string, err error) {
	return resolveDBPath(os.Getenv, runtime.GOOS)
}

// resolveDBPath is the env-injected core of ResolveDBPath. Tests drive it with
// a fake getenv and goos; production passes os.Getenv / runtime.GOOS.
func resolveDBPath(getenv func(string) string, goos string) (string, []string, error) {
	// An explicit path wins outright; a set-but-missing value is still an
	// error so a stale OPENCODE_DB surfaces instead of silently falling back.
	if explicit := getenv(EnvDBPath); explicit != "" {
		if fileExists(explicit) {
			return explicit, []string{explicit}, nil
		}
		return "", []string{explicit}, ErrDBNotFound
	}

	dirs := candidateDirs(getenv, goos)

	var (
		best    string
		bestMod int64
		hasBest bool
	)
	for _, dir := range dirs {
		for _, g := range dbGlobs {
			matches, _ := filepath.Glob(filepath.Join(dir, g))
			for _, m := range matches {
				fi, statErr := os.Stat(m)
				if statErr != nil || fi.IsDir() {
					continue
				}
				if mod := fi.ModTime().UnixNano(); !hasBest || mod > bestMod {
					best, bestMod, hasBest = m, mod, true
				}
			}
		}
	}
	if !hasBest {
		return "", dirs, ErrDBNotFound
	}
	return best, dirs, nil
}

// candidateDirs is the ordered, de-duplicated set of directories that may hold
// an OpenCode database: $XDG_DATA_HOME/opencode, then ~/.local/share/opencode
// (the XDG default, which is also where macOS keeps it — OpenCode's xdg-basedir
// has no darwin branch), and defensively ~/Library/Application Support/opencode
// on darwin in case a future version does switch.
func candidateDirs(getenv func(string) string, goos string) []string {
	home := getenv("HOME")
	var dirs []string
	add := func(d string) {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}

	if xdg := getenv("XDG_DATA_HOME"); xdg != "" {
		add(filepath.Join(xdg, "opencode"))
	}
	if home != "" {
		add(filepath.Join(home, ".local", "share", "opencode"))
		if goos == "darwin" {
			add(filepath.Join(home, "Library", "Application Support", "opencode"))
		}
	}
	return dirs
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// escapeLike makes s safe as a literal inside a LIKE pattern, escaping the
// wildcards (%, _) and the escape char itself with a backslash. Callers pair
// it with `LIKE ? ESCAPE '\'`. Pure function; unit-tested in isolation.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
