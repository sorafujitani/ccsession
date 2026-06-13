package opencode

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
)

// scanQuery lists resumable root sessions newest-first. parent_id IS NULL drops
// sub-sessions (tool/child turns); time_archived IS NULL drops archived ones.
// The id tiebreaker keeps the order stable when two sessions share a timestamp.
const scanQuery = `SELECT id, title, directory, time_updated
FROM session
WHERE parent_id IS NULL AND time_archived IS NULL
ORDER BY time_updated DESC, id DESC`

// Scan returns every resumable root session, newest-first.
func (d *DB) Scan() ([]*session.Session, error) {
	return d.scanFiltered(nil, time.Now())
}

// ScanFiltered returns only the sessions whose id is in allow (the id set grep
// produced). A nil/empty allow returns everything, matching Scan.
func (d *DB) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	return d.scanFiltered(allow, time.Now())
}

// scanFiltered is the env-injected core (now is a seam for the future-sinking
// test). allow == nil means "no filter".
func (d *DB) scanFiltered(allow map[string]struct{}, now time.Time) ([]*session.Session, error) {
	rows, err := d.query(scanQuery)
	if err != nil {
		return nil, d.schemaError(err)
	}
	defer rows.Close()

	out, err := buildSessions(rows, allow, now)
	if err != nil {
		return nil, err
	}
	return out, rows.Err()
}

// buildSessions maps query rows to sessions, applying the optional id filter.
// Exported-for-test via the package-internal call sites.
func buildSessions(rows *sql.Rows, allow map[string]struct{}, now time.Time) ([]*session.Session, error) {
	nowEpoch := now.Unix()
	var out []*session.Session
	for rows.Next() {
		var (
			id, title, dir string
			timeUpdatedMs  int64
		)
		if err := rows.Scan(&id, &title, &dir, &timeUpdatedMs); err != nil {
			return nil, err
		}
		// id feeds the tab-separated fzf row and the resume exec; a control
		// char in it would corrupt the column contract, and it can't be
		// sanitized (resume needs the exact id), so drop the row entirely.
		if strings.ContainsAny(id, "\t\n\r") {
			continue
		}
		if allow != nil {
			if _, ok := allow[id]; !ok {
				continue
			}
		}
		out = append(out, newSession(id, title, dir, timeUpdatedMs))
	}

	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := sortEpoch(out[i].LastEpoch, nowEpoch), sortEpoch(out[j].LastEpoch, nowEpoch)
		if ki != kj {
			return ki > kj
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

func newSession(id, title, dir string, timeUpdatedMs int64) *session.Session {
	last := time.UnixMilli(timeUpdatedMs)
	s := &session.Session{
		ID:        id,
		Label:     session.SanitizeLabel(title),
		CWD:       dir,
		LastTime:  last,
		LastEpoch: last.Unix(),
	}
	// An empty directory is a legacy/incomplete session: we know nothing about
	// where it ran, which the list marks [cwd?] and resume refuses to act on.
	if dir == "" {
		s.CWDUnknown = true
	} else {
		s.CWDBasename = filepath.Base(dir)
		s.CWDExists = pathIsDir(dir)
	}
	return s
}

// sortEpoch clamps a future timestamp to now for ordering, so a clock-skewed or
// bogus future time_updated can't pin a session to the top of the list. Mirrors
// the claude scan's identically-named helper.
func sortEpoch(epoch, nowEpoch int64) int64 {
	if epoch > nowEpoch {
		return nowEpoch
	}
	return epoch
}

func pathIsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// schemaError wraps a failed scan query with the recovery path: which DB, and
// the migration state it carries, so a column drift in a future OpenCode
// release produces an actionable message instead of a bare "no such column".
// Only the OpenCode feature degrades; the claude source is untouched.
func (d *DB) schemaError(cause error) error {
	newest, count := d.migrationState()
	if count == 0 {
		return fmt.Errorf("opencode: reading sessions from %s: %w", d.path, cause)
	}
	return fmt.Errorf("opencode: reading sessions from %s failed (%v); "+
		"db has %d migrations applied, newest %q — this ccsession may predate a schema change",
		d.path, cause, count, newest)
}

// migrationState reports the newest applied migration id and the total count.
// Best-effort: a DB without a migration table returns ("", 0).
func (d *DB) migrationState() (newest string, count int) {
	rows, err := d.query("SELECT id FROM migration ORDER BY id")
	if err != nil {
		return "", 0
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return newest, count
		}
		newest = id
		count++
	}
	return newest, count
}
