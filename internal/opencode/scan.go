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

// parent_id IS NULL drops sub-sessions; time_archived IS NULL drops archived ones.
const scanQuery = `SELECT id, title, directory, time_updated
FROM session
WHERE parent_id IS NULL AND time_archived IS NULL
ORDER BY time_updated DESC, id DESC`

func (d *DB) Scan() ([]*session.Session, error) {
	return d.scanFiltered(nil, time.Now())
}

// ScanFiltered keeps only sessions whose id is in allow; nil allow keeps all.
func (d *DB) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	return d.scanFiltered(allow, time.Now())
}

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
		if idCorruptsRow(id) || !allowed(allow, id) {
			continue
		}
		out = append(out, newSession(id, title, dir, timeUpdatedMs))
	}

	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := sortEpoch(out[i].LastEpoch, nowEpoch), sortEpoch(out[j].LastEpoch, nowEpoch)
		if ki != kj {
			return ki > kj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// idCorruptsRow reports an id that can't be sanitized away: it feeds both the
// tab-separated fzf row and the resume exec, so a control char drops the row.
func idCorruptsRow(id string) bool {
	return strings.ContainsAny(id, "\t\n\r")
}

func allowed(allow map[string]struct{}, id string) bool {
	if allow == nil {
		return true
	}
	_, ok := allow[id]
	return ok
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
	if dir == "" {
		s.CWDUnknown = true
	} else {
		s.CWDBasename = filepath.Base(dir)
		s.CWDExists = pathIsDir(dir)
	}
	return s
}

// sortEpoch sinks future timestamps to the bottom (returns 0), matching the
// claude scan so a clock-skewed time_updated can't pin a session to the top.
func sortEpoch(epoch, nowEpoch int64) int64 {
	if epoch > nowEpoch {
		return 0
	}
	return epoch
}

func pathIsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// schemaError turns a column drift in a future OpenCode release into an
// actionable message carrying the migration state, instead of "no such column".
func (d *DB) schemaError(cause error) error {
	newest, count := d.migrationState()
	if count == 0 {
		return fmt.Errorf("opencode: reading sessions from %s: %w", d.path, cause)
	}
	return fmt.Errorf("opencode: reading sessions from %s failed (%v); "+
		"db has %d migrations applied, newest %q — this ccsession may predate a schema change",
		d.path, cause, count, newest)
}

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
