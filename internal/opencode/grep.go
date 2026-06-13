package opencode

import (
	"strings"

	"github.com/sorafujitani/ccsession/internal/grep"
)

// GrepKeys returns the set of root session ids whose title or message text
// matches query. An empty query returns (nil, nil) to mean "no filtering",
// matching grep.Filter's contract.
//
// A LIKE prefilter narrows the candidate sessions before the authoritative
// Go-side match. It is only an optimization, so it must never drop a real
// match: prefilterUsable disables it whenever LIKE could diverge from the
// matcher (regex, JSON-escapable chars, or non-ASCII case-folding), in which
// case every root session is matched directly.
func (d *DB) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	match, err := grep.BuildMatcher(query, grep.Options{Regex: regex})
	if err != nil {
		return nil, err
	}

	roots, err := d.grepRoots(query, regex)
	if err != nil {
		return nil, err
	}

	set := make(map[string]struct{})
	for _, r := range roots {
		hit, err := d.sessionMatches(r, match)
		if err != nil {
			return nil, err
		}
		if hit {
			set[r.id] = struct{}{}
		}
	}
	return set, nil
}

type rootRow struct {
	id    string
	title string
}

func (d *DB) grepRoots(query string, regex bool) ([]rootRow, error) {
	var (
		q    string
		args []any
	)
	if prefilterUsable(query, regex) {
		like := "%" + escapeLike(query) + "%"
		q = `SELECT id, title FROM session s
WHERE s.parent_id IS NULL AND s.time_archived IS NULL AND (
	s.title LIKE ? ESCAPE '\'
	OR EXISTS (SELECT 1 FROM part p JOIN message m ON p.message_id = m.id
		WHERE m.session_id = s.id AND p.data LIKE ? ESCAPE '\')
	OR EXISTS (SELECT 1 FROM session_message sm
		WHERE sm.session_id = s.id AND sm.data LIKE ? ESCAPE '\'))`
		args = []any{like, like, like}
	} else {
		q = `SELECT id, title FROM session
WHERE parent_id IS NULL AND time_archived IS NULL`
	}

	rows, err := d.query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rootRow
	for rows.Next() {
		var r rootRow
		if err := rows.Scan(&r.id, &r.title); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (d *DB) sessionMatches(r rootRow, match func(string) bool) (bool, error) {
	if match(r.title) {
		return true, nil
	}
	msgs, _, _, err := d.Messages(r.id, 0)
	if err != nil {
		return false, err
	}
	for _, m := range msgs {
		if match(m.Body) {
			return true, nil
		}
	}
	return false, nil
}

func prefilterUsable(query string, regex bool) bool {
	if regex {
		return false
	}
	// `"` and `\` risk over-pruning JSON-escaped data; a newline can't be found
	// in any single part row but matches the \n-joined body, so the LIKE would
	// drop a real multi-part match.
	if strings.ContainsAny(query, "\"\\\n\r") {
		return false
	}
	return isASCII(query)
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7f {
			return false
		}
	}
	return true
}
