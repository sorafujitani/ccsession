package opencode

import (
	"encoding/json"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
)

// Messages returns up to limit of a session's most recent renderable turns in
// chronological order, plus the first turn's time and the total turn count for
// the preview header.
//
// v1 (message+part) is the live write path; v2 (session_message) currently only
// holds switch events and is read as forward-compat, falling through to v1 when
// it yields nothing renderable.
func (d *DB) Messages(sessionID string, limit int) (msgs []session.Message, startedAt time.Time, total int, err error) {
	all, err := d.messagesV2(sessionID)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	if len(all) == 0 {
		all, err = d.messagesV1(sessionID)
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

type v1MessageData struct {
	Role string `json:"role"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

type v1PartData struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// messagesV1 groups each message's text parts into one turn, keeping only
// user/assistant turns. The LEFT JOIN preserves a turn whose only parts are
// non-text (reasoning/step-start) as an empty body rather than dropping it.
func (d *DB) messagesV1(sessionID string) ([]session.Message, error) {
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
		var partData []byte // NULL for a message with no parts
		if err := rows.Scan(&msgID, &msgData, &partData); err != nil {
			return nil, err
		}
		if !curHasID || msgID != curID {
			flush()
			curID, curHasID = msgID, true
			cur = newV1Message(msgData)
		}
		if cur != nil {
			appendPartText(cur, partData)
		}
	}
	flush()
	return out, rows.Err()
}

func newV1Message(data []byte) *session.Message {
	var md v1MessageData
	_ = json.Unmarshal(data, &md) // tolerant: unknown fields ignored, role may stay ""
	return &session.Message{
		Role:      md.Role,
		Timestamp: msToTime(md.Time.Created),
	}
}

func appendPartText(m *session.Message, partData []byte) {
	if len(partData) == 0 {
		return
	}
	var p v1PartData
	if err := json.Unmarshal(partData, &p); err != nil || p.Type != "text" || p.Text == "" {
		return
	}
	if m.Body != "" {
		m.Body += "\n"
	}
	m.Body += p.Text
}

type v2Data struct {
	Text string `json:"text"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

// messagesV2 reads the newest turns by seq and reverses them to chronological
// order. type comes from the column, not the JSON. Switch events (no text) are
// dropped, so a table holding only those reads as empty and v1 takes over.
func (d *DB) messagesV2(sessionID string) ([]session.Message, error) {
	const q = `SELECT type, data
FROM session_message
WHERE session_id = ?
ORDER BY seq DESC, id DESC`
	rows, err := d.query(q, sessionID)
	if err != nil {
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
		var vd v2Data
		_ = json.Unmarshal(data, &vd)
		if vd.Text == "" {
			continue
		}
		rev = append(rev, session.Message{
			Role:      typ,
			Timestamp: msToTime(vd.Time.Created),
			Body:      vd.Text,
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
