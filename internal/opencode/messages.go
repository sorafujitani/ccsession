package opencode

import (
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
)

// Messages returns up to limit of a session's most recent renderable turns in
// chronological order, plus the first turn's time and the total turn count for
// the preview header.
//
// The session_message projection is read first; it falls back to the message
// +part store when the projection is empty of renderable turns or absent.
func (d *DB) Messages(sessionID string, limit int) (msgs []session.Message, startedAt time.Time, total int, err error) {
	all, startedAt, total, err := d.messagesFromProjection(sessionID, limit)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	if total == 0 {
		all, startedAt, total, err = d.messagesFromParts(sessionID, limit)
		if err != nil {
			return nil, time.Time{}, 0, err
		}
	}
	return all, startedAt, total, nil
}

type messageData struct {
	Role string `json:"role"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

type textPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// messagesFromParts groups each message's text parts into one turn, keeping
// only user/assistant turns. The LEFT JOIN preserves a turn whose only parts
// are non-text (reasoning/step-start) as an empty body rather than dropping it.
func (d *DB) messagesFromParts(sessionID string, limit int) ([]session.Message, time.Time, int, error) {
	if limit > 0 {
		return d.limitedMessagesFromParts(sessionID, limit)
	}
	const q = `SELECT m.id, m.data, p.data
FROM message m
LEFT JOIN part p ON p.message_id = m.id
WHERE m.session_id = ?
ORDER BY m.time_created, m.id, p.id`
	rows, err := d.query(q, sessionID)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	defer rows.Close()

	out, err := scanPartMessages(rows, nil)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	if len(out) == 0 {
		return nil, time.Time{}, 0, nil
	}
	return out, out[0].Timestamp, len(out), nil
}

func (d *DB) limitedMessagesFromParts(sessionID string, limit int) ([]session.Message, time.Time, int, error) {
	total, startedAt, err := d.partsCountAndStartedAt(sessionID)
	if err != nil || total == 0 {
		return nil, time.Time{}, total, err
	}
	const q = `SELECT m.id, m.data, p.data
FROM (
	SELECT id, data, time_created
	FROM message
	WHERE session_id = ? AND (data LIKE '%"role":"user"%' OR data LIKE '%"role":"assistant"%')
	ORDER BY time_created DESC, id DESC
	LIMIT ?
) m
LEFT JOIN part p ON p.message_id = m.id
ORDER BY m.time_created, m.id, p.id`
	rows, err := d.query(q, sessionID, limit)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	defer rows.Close()
	out, err := scanPartMessages(rows, nil)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	return out, startedAt, total, nil
}

func (d *DB) partsCountAndStartedAt(sessionID string) (int, time.Time, error) {
	const q = `SELECT COUNT(*), COALESCE(MIN(time_created), 0)
FROM message
WHERE session_id = ? AND (data LIKE '%"role":"user"%' OR data LIKE '%"role":"assistant"%')`
	rows, err := d.query(q, sessionID)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer rows.Close()
	var total int
	var startedMs int64
	if rows.Next() {
		if err := rows.Scan(&total, &startedMs); err != nil {
			return 0, time.Time{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return 0, time.Time{}, err
	}
	return total, msToTime(startedMs), nil
}

func scanPartMessages(rows *sql.Rows, sessionIDs map[string]struct{}) ([]session.Message, error) {
	var (
		out   []session.Message
		curID string
		cur   *session.Message
	)
	flush := func() {
		if cur != nil && isRenderableRole(cur.Role) {
			out = append(out, *cur)
		}
	}
	for rows.Next() {
		var msgID string
		var msgData []byte
		var part []byte // NULL for a message with no parts
		if sessionIDs == nil {
			if err := rows.Scan(&msgID, &msgData, &part); err != nil {
				return nil, err
			}
		} else {
			var sessionID string
			if err := rows.Scan(&sessionID, &msgID, &msgData, &part); err != nil {
				return nil, err
			}
			sessionIDs[sessionID] = struct{}{}
		}
		if cur == nil || msgID != curID {
			flush()
			curID = msgID
			cur = newTurn(msgData)
		}
		appendText(cur, part)
	}
	flush()
	return out, rows.Err()
}

func newTurn(data []byte) *session.Message {
	var md messageData
	_ = json.Unmarshal(data, &md) // tolerant: unknown fields ignored, role may stay ""
	return &session.Message{Role: md.Role, Timestamp: msToTime(md.Time.Created)}
}

func appendText(m *session.Message, partData []byte) {
	if len(partData) == 0 {
		return
	}
	var p textPart
	if err := json.Unmarshal(partData, &p); err != nil || p.Type != "text" || p.Text == "" {
		return
	}
	if m.Body != "" {
		m.Body += "\n"
	}
	m.Body += p.Text
}

type projectionData struct {
	Text string `json:"text"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

// messagesFromProjection reads the newest turns by seq and reverses them to
// chronological order; type comes from the column, not the JSON. A projection
// empty of renderable turns, or absent (a DB predating it), reads as nil so the
// parts store takes over.
func (d *DB) messagesFromProjection(sessionID string, limit int) ([]session.Message, time.Time, int, error) {
	if limit > 0 {
		return d.limitedMessagesFromProjection(sessionID, limit)
	}
	const q = `SELECT type, data
FROM session_message
WHERE session_id = ?
ORDER BY seq DESC, id DESC`
	rows, err := d.query(q, sessionID)
	if err != nil {
		if isMissingTable(err) {
			return nil, time.Time{}, 0, nil
		}
		return nil, time.Time{}, 0, err
	}
	defer rows.Close()

	var rev []session.Message
	for rows.Next() {
		var typ string
		var data []byte
		if err := rows.Scan(&typ, &data); err != nil {
			return nil, time.Time{}, 0, err
		}
		if !isRenderableRole(typ) {
			continue
		}
		msg := projectionMessage(typ, data)
		if msg.Body == "" {
			continue
		}
		rev = append(rev, msg)
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, 0, err
	}
	slices.Reverse(rev)
	if len(rev) == 0 {
		return nil, time.Time{}, 0, nil
	}
	return rev, rev[0].Timestamp, len(rev), nil
}

func (d *DB) limitedMessagesFromProjection(sessionID string, limit int) ([]session.Message, time.Time, int, error) {
	total, startedAt, err := d.projectionCountAndStartedAt(sessionID)
	if err != nil || total == 0 {
		return nil, time.Time{}, total, err
	}
	const q = `SELECT type, data
FROM session_message
WHERE session_id = ?
	AND type IN ('user', 'assistant')
	AND COALESCE(json_extract(data, '$.text'), '') != ''
ORDER BY seq DESC, id DESC
LIMIT ?`
	rows, err := d.query(q, sessionID, limit)
	if err != nil {
		if isMissingTable(err) {
			return nil, time.Time{}, 0, nil
		}
		return nil, time.Time{}, 0, err
	}
	defer rows.Close()
	var rev []session.Message
	for rows.Next() {
		var typ string
		var data []byte
		if err := rows.Scan(&typ, &data); err != nil {
			return nil, time.Time{}, 0, err
		}
		rev = append(rev, projectionMessage(typ, data))
	}
	if err := rows.Err(); err != nil {
		return nil, time.Time{}, 0, err
	}
	slices.Reverse(rev)
	return rev, startedAt, total, nil
}

func (d *DB) projectionCountAndStartedAt(sessionID string) (int, time.Time, error) {
	const q = `SELECT COUNT(*), COALESCE(MIN(json_extract(data, '$.time.created')), 0)
FROM session_message
WHERE session_id = ?
	AND type IN ('user', 'assistant')
	AND COALESCE(json_extract(data, '$.text'), '') != ''`
	rows, err := d.query(q, sessionID)
	if err != nil {
		if isMissingTable(err) {
			return 0, time.Time{}, nil
		}
		return 0, time.Time{}, err
	}
	defer rows.Close()
	var total int
	var startedMs int64
	if rows.Next() {
		if err := rows.Scan(&total, &startedMs); err != nil {
			return 0, time.Time{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return 0, time.Time{}, err
	}
	return total, msToTime(startedMs), nil
}

func projectionMessage(typ string, data []byte) session.Message {
	var pd projectionData
	_ = json.Unmarshal(data, &pd)
	return session.Message{
		Role:      typ,
		Timestamp: msToTime(pd.Time.Created),
		Body:      pd.Text,
	}
}

func isRenderableRole(role string) bool {
	return role == "user" || role == "assistant"
}

func isMissingTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

func msToTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
