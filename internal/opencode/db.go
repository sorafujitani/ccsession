package opencode

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// EnvDBPath overrides database discovery with an explicit file path.
const EnvDBPath = "OPENCODE_DB"

var ErrDBNotFound = errors.New("opencode database not found")

// The channel form (opencode-prod.db, …) coexists with the default in the wild.
var dbGlobs = []string{"opencode.db", "opencode-*.db"}

type DB struct {
	sql  *sql.DB
	path string
}

func (d *DB) Path() string { return d.path }

func Open() (*DB, error) {
	path, _, err := ResolveDBPath()
	if err != nil {
		return nil, err
	}
	return openAt(path)
}

func openAt(path string) (*DB, error) {
	db, err := sql.Open("sqlite3", readOnlyDSN(path))
	if err != nil {
		return nil, err
	}
	return &DB{sql: db, path: path}, nil
}

func (d *DB) Close() error { return d.sql.Close() }

// query is the single read funnel, so the read-only contract can't be bypassed.
func (d *DB) query(q string, args ...any) (*sql.Rows, error) {
	return d.sql.Query(q, args...)
}

// readOnlyDSN deliberately avoids immutable=1: that flag makes SQLite ignore the
// -wal sidecar, silently dropping every session still in an un-checkpointed WAL
// (the common case while OpenCode is running).
func readOnlyDSN(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	u.RawQuery = "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	return u.String()
}

// ResolveDBPath returns the chosen database file, the locations it probed (for
// the not-found message), and ErrDBNotFound when nothing matched.
func ResolveDBPath() (path string, probed []string, err error) {
	return resolveDBPath(os.Getenv, runtime.GOOS)
}

func resolveDBPath(getenv func(string) string, goos string) (string, []string, error) {
	if explicit := getenv(EnvDBPath); explicit != "" {
		// Absolute so a relative path can't be read as a file: URI authority.
		if abs, err := filepath.Abs(explicit); err == nil {
			explicit = abs
		}
		if fileExists(explicit) {
			return explicit, []string{explicit}, nil
		}
		return "", []string{explicit}, ErrDBNotFound
	}

	dirs := candidateDirs(getenv, goos)
	newest, found := newestDB(dirs)
	if !found {
		return "", dirs, ErrDBNotFound
	}
	return newest, dirs, nil
}

func newestDB(dirs []string) (path string, found bool) {
	var bestMod int64
	for _, dir := range dirs {
		for _, g := range dbGlobs {
			matches, _ := filepath.Glob(filepath.Join(dir, g))
			for _, m := range matches {
				fi, err := os.Stat(m)
				if err != nil || fi.IsDir() {
					continue
				}
				if mod := fi.ModTime().UnixNano(); !found || mod > bestMod {
					path, bestMod, found = m, mod, true
				}
			}
		}
	}
	return path, found
}

// candidateDirs is the ordered, de-duplicated probe list. macOS has no dedicated
// branch in OpenCode's xdg-basedir, so ~/.local/share covers it; the Application
// Support entry is a defensive fallback for a future move.
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

// Preflight resolves and opens the database before the TUI launches, so a
// missing DB, a pre-SQLite (legacy JSON) install, or an unreadable file fails
// on the normal terminal with an actionable message instead of invisibly
// inside fzf.
func Preflight() error {
	path, probed, err := ResolveDBPath()
	if err == nil {
		db, oerr := openAt(path)
		if oerr != nil {
			return oerr
		}
		defer db.Close()
		rows, qerr := db.query("SELECT 1")
		if qerr != nil {
			return fmt.Errorf("opencode database at %s is unreadable: %w", path, qerr)
		}
		rows.Close()
		return nil
	}
	if !errors.Is(err, ErrDBNotFound) {
		return err
	}
	if legacy := legacyDirs(probed); len(legacy) > 0 {
		return fmt.Errorf("OpenCode v1.2.0+ (SQLite storage) is required, but only a legacy "+
			"JSON layout was found under %s; ccsession does not read it", strings.Join(legacy, ", "))
	}
	return fmt.Errorf("opencode database not found; probed: %s (legacy data may remain under storage/)",
		strings.Join(probed, ", "))
}

// legacyDirs reports which probed locations hold a pre-v1.2.0 storage/ tree.
func legacyDirs(probed []string) []string {
	var out []string
	for _, d := range probed {
		if fi, err := os.Stat(filepath.Join(d, "storage")); err == nil && fi.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// escapeLike neutralizes LIKE wildcards in a literal; pair with `LIKE ? ESCAPE '\'`.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
