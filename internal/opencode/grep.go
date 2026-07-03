package opencode

import (
	"fmt"
	"strings"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

// GrepKeys returns the set of root session ids whose title or message text
// matches query. An empty query returns (nil, nil) to mean "no filtering",
// matching grep.Filter's contract.
//
// A LIKE prefilter narrows the candidate sessions before the authoritative
// Go-side match. It is only an optimization, so it must never drop a real
// match: prefilterUsable disables it whenever LIKE could diverge from the
// matcher (regex, JSON-escapable chars, or a case fold the ASCII-only LIKE
// can't reproduce), in which case every root session is matched directly.
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
	var bodyRoots []rootRow
	for _, r := range roots {
		if match(r.title) {
			set[r.id] = struct{}{}
			continue
		}
		bodyRoots = append(bodyRoots, r)
	}
	if err := d.matchBodies(bodyRoots, match, set); err != nil {
		return nil, err
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

func (d *DB) matchBodies(roots []rootRow, match func(string) bool, set map[string]struct{}) error {
	if len(roots) == 0 {
		return nil
	}
	for _, chunk := range chunkRoots(roots, 500) {
		hasProjection, err := d.matchProjectionBodies(chunk, match, set)
		if err != nil {
			return err
		}
		var fallback []rootRow
		for _, r := range chunk {
			if _, ok := hasProjection[r.id]; !ok {
				fallback = append(fallback, r)
			}
		}
		if err := d.matchPartBodies(fallback, match, set); err != nil {
			return err
		}
	}
	return nil
}

func (d *DB) matchProjectionBodies(roots []rootRow, match func(string) bool, set map[string]struct{}) (map[string]struct{}, error) {
	hasProjection := make(map[string]struct{})
	if len(roots) == 0 {
		return hasProjection, nil
	}
	q, args := inQuery(`SELECT session_id, type, data
FROM session_message
WHERE session_id IN (%s) AND type IN ('user', 'assistant')
ORDER BY session_id, seq, id`, rootIDs(roots))
	rows, err := d.query(q, args...)
	if err != nil {
		if isMissingTable(err) {
			return hasProjection, nil
		}
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sessionID string
		var typ string
		var data []byte
		if err := rows.Scan(&sessionID, &typ, &data); err != nil {
			return nil, err
		}
		msg := projectionMessage(typ, data)
		if msg.Body == "" {
			continue
		}
		hasProjection[sessionID] = struct{}{}
		if _, matched := set[sessionID]; matched {
			continue
		}
		if match(msg.Body) {
			set[sessionID] = struct{}{}
		}
	}
	return hasProjection, rows.Err()
}

func (d *DB) matchPartBodies(roots []rootRow, match func(string) bool, set map[string]struct{}) error {
	if len(roots) == 0 {
		return nil
	}
	q, args := inQuery(`SELECT m.session_id, m.id, m.data, p.data
FROM message m
LEFT JOIN part p ON p.message_id = m.id
WHERE m.session_id IN (%s)
ORDER BY m.session_id, m.time_created, m.id, p.id`, rootIDs(roots))
	rows, err := d.query(q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		curSession string
		curMsgID   string
		cur        *sessionBody
	)
	flush := func() {
		if cur == nil || !isRenderableRole(cur.role) {
			return
		}
		if _, matched := set[curSession]; matched {
			return
		}
		if match(cur.body) {
			set[curSession] = struct{}{}
		}
	}
	for rows.Next() {
		var sessionID string
		var msgID string
		var msgData []byte
		var part []byte
		if err := rows.Scan(&sessionID, &msgID, &msgData, &part); err != nil {
			return err
		}
		if _, matched := set[sessionID]; matched {
			continue
		}
		if cur == nil || sessionID != curSession || msgID != curMsgID {
			flush()
			curSession = sessionID
			curMsgID = msgID
			msg := newTurn(msgData)
			cur = &sessionBody{role: msg.Role}
		}
		appendBodyText(&cur.body, part)
	}
	flush()
	return rows.Err()
}

type sessionBody struct {
	role string
	body string
}

func appendBodyText(body *string, partData []byte) {
	msg := &session.Message{Body: *body}
	appendText(msg, partData)
	*body = msg.Body
}

func chunkRoots(roots []rootRow, size int) [][]rootRow {
	var chunks [][]rootRow
	for len(roots) > 0 {
		n := min(len(roots), size)
		chunks = append(chunks, roots[:n])
		roots = roots[n:]
	}
	return chunks
}

func rootIDs(roots []rootRow) []string {
	ids := make([]string, len(roots))
	for i, r := range roots {
		ids[i] = r.id
	}
	return ids
}

func inQuery(format string, ids []string) (string, []any) {
	var b strings.Builder
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
		args[i] = id
	}
	return fmt.Sprintf(format, b.String()), args
}

// asciiFoldTargets are the ASCII letters a non-ASCII rune lower-cases onto
// (U+0130 İ → 'i', U+212A KELVIN SIGN → 'k'). The matcher folds case with
// Unicode rules, but SQLite's LIKE folds only ASCII A–Z, so a query holding one
// of these letters can match a haystack through its non-ASCII source while the
// prefilter, seeing only raw bytes, misses it — dropping a real match.
// TestAsciiFoldTargetsExact proves this set is neither short nor long.
const asciiFoldTargets = "ik"

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
	if !isASCII(query) {
		return false
	}
	// A non-ASCII haystack rune can lower-case onto an ASCII letter the query
	// holds, which the ASCII-only LIKE fold can't see; bypass the prefilter so
	// such a match isn't dropped.
	return !strings.ContainsAny(strings.ToLower(query), asciiFoldTargets)
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7f {
			return false
		}
	}
	return true
}
