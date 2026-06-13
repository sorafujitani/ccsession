package opencode

import (
	"encoding/json"
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
	all, err := d.messagesFromProjection(sessionID)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	if len(all) == 0 {
		all, err = d.messagesFromParts(sessionID)
		if err != nil {
			return nil, time.Time{}, 0, err
		}
	}
	if len(all) == 0 {
		return nil, time.Time{}, 0, nil
	}
	startedAt = all[0].Timestamp
	total = len(all)
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
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
func (d *DB) messagesFromParts(sessionID string) ([]session.Message, error) {
	const q = `SELECT m.id, m.data, p.data
FROM message m
LEFT JOIN part p ON p.message_id = m.id
WHERE m.session_id = ?
ORDER BY m.time_created, m.id, p.id`
	rows, err := d.query(q, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var (
		out      []session.Message
		curID    string
		cur      *session.Message
		curHasID bool
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
		if err := rows.Scan(&msgID, &msgData, &part); err != nil {
			return nil, err
		}
		if !curHasID || msgID != curID {
			flush()
			curID, curHasID = msgID, true
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
func (d *DB) messagesFromProjection(sessionID string) ([]session.Message, error) {
	const q = `SELECT type, data
FROM session_message
WHERE session_id = ?
ORDER BY seq DESC, id DESC`
	rows, err := d.query(q, sessionID)
	if err != nil {
		if isMissingTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	var rev []session.Message
	for rows.Next() {
		var typ string
		var data []byte
		if err := rows.Scan(&typ, &data); err != nil {
			return nil, err
		}
		if !isRenderableRole(typ) {
			continue
		}
		var pd projectionData
		_ = json.Unmarshal(data, &pd)
		if pd.Text == "" {
			continue
		}
		rev = append(rev, session.Message{
			Role:      typ,
			Timestamp: msToTime(pd.Time.Created),
			Body:      pd.Text,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	reverse(rev)
	return rev, nil
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

func reverse(m []session.Message) {
	for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
		m[i], m[j] = m[j], m[i]
	}
}
