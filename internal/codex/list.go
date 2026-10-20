package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/sorafujitani/ccsession/internal/jsonscan"
	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/timefmt"
)

// listState is what a list-only scan keeps between lines. Cached together
// with the byte offset it covers, it lets an appended transcript resume.
type listState struct {
	ID     string    `json:"id,omitempty"`
	CWD    string    `json:"cwd,omitempty"`
	Label  string    `json:"label,omitempty"` // Already sanitized; sanitizing is idempotent.
	LastTS time.Time `json:"last_ts"`
}

// visit applies one JSONL record as the list has always read it:
// every timestamp counts, session_meta sets identity, and the last visible
// user message becomes the label. Only those payloads are decoded.
func (st *listState) visit(line []byte) {
	f, ok := scanEntry(line)
	if !ok {
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		f = entryFields{timestamp: e.Timestamp, typ: e.Type, payload: e.Payload}
	}
	lineTS := timefmt.Parse(f.timestamp)
	if !lineTS.IsZero() && lineTS.After(st.LastTS) {
		st.LastTS = lineTS
	}
	switch f.typ {
	case "session_meta":
		var meta sessionMetaPayload
		if err := json.Unmarshal(f.payload, &meta); err != nil {
			return
		}
		if meta.ID != "" {
			st.ID = meta.ID
		}
		if meta.CWD != "" {
			st.CWD = meta.CWD
		}
		if metaTS := timefmt.Parse(meta.Timestamp); !metaTS.IsZero() && metaTS.After(st.LastTS) {
			st.LastTS = metaTS
		}
	case "response_item":
		// Assistant messages only feed message counts, which lists ignore.
		if f.known && (f.payloadType != "message" || f.payloadRole != "user") {
			return
		}
		if msg, ok := parseMessagePayload(f.payload, lineTS); ok && msg.Role == "user" {
			st.Label = session.SanitizeLabel(msg.Body)
		}
	}
}

func (st listState) equal(o listState) bool {
	return st.ID == o.ID && st.CWD == o.CWD && st.Label == o.Label && st.LastTS.Equal(o.LastTS)
}

// finish turns a scan state into the listed Session, or nil for a
// transcript without a usable ID.
func (st listState) finish(path string, info os.FileInfo) (*session.Session, error) {
	sess := &session.Session{
		ID:         idFromPath(path),
		ProjectDir: filepath.Dir(path),
		JSONLPath:  path,
		CWD:        st.CWD,
	}
	if st.ID != "" {
		sess.ID = st.ID
	}
	if sess.ID == "" || idCorruptsRow(sess.ID) {
		return nil, nil
	}
	sess.Label = st.Label
	if sess.Label == "" {
		return nil, session.ErrSessionEmpty
	}
	if sess.CWD == "" {
		sess.CWDUnknown = true
	} else {
		sess.CWDBasename = filepath.Base(sess.CWD)
		sess.CWDExists = pathIsDir(sess.CWD)
	}
	lastTS := st.LastTS
	if lastTS.IsZero() {
		lastTS = info.ModTime()
	}
	sess.LastTime = lastTS
	sess.LastEpoch = lastTS.Unix()
	return sess, nil
}

type entryFields struct {
	timestamp, typ string
	payload        []byte // nil when absent, as with json.RawMessage.
	// known reports that payloadType and payloadRole are what
	// encoding/json would decode into messagePayload.
	known                    bool
	payloadType, payloadRole string
}

// scanEntry validates a record and extracts its top-level fields in one pass.
// It reports ok=false whenever encoding/json might decode them differently:
// invalid JSON, a non-object, escaped, duplicate, or non-ASCII keys (which
// encoding/json matches case-insensitively), or a field that is not a plain
// string. The caller then decodes the record with encoding/json.
func scanEntry(b []byte) (entryFields, bool) {
	var f entryFields
	var seen [3]bool
	end := jsonscan.Object(b, 0, 0, func(key []byte, escaped bool, i int) int {
		field := topLevelField(key, escaped)
		switch {
		case field == fieldOther:
			return jsonscan.Value(b, i, 1)
		case field == fieldAmbiguous || seen[field]:
			return -1
		}
		seen[field] = true
		switch field {
		case fieldPayload:
			start := jsonscan.Space(b, i)
			end := scanPayload(b, start, &f)
			if end >= 0 {
				f.payload = b[start:end]
			}
			return end
		default:
			s, end := plainString(b, i)
			if field == fieldTimestamp {
				f.timestamp = s
			} else {
				f.typ = s
			}
			return end
		}
	})
	if end < 0 || jsonscan.Space(b, end) != len(b) {
		return entryFields{}, false
	}
	if !seen[fieldPayload] {
		f.known = true // An absent payload never decodes as a message.
	}
	return f, true
}

const (
	fieldTimestamp = iota
	fieldType
	fieldPayload
	fieldOther
	fieldAmbiguous
)

func topLevelField(key []byte, escaped bool) int {
	switch string(key) {
	case "timestamp":
		return fieldTimestamp
	case "type":
		return fieldType
	case "payload":
		return fieldPayload
	}
	if escaped || !foldSafeKey(key) {
		return fieldAmbiguous
	}
	return fieldOther
}

// scanPayload consumes a payload and records its type and role when they
// are certain. Unlike top-level ambiguity, an unclear payload only means the
// caller must decode it to decide.
func scanPayload(b []byte, i int, f *entryFields) int {
	if i >= len(b) || b[i] != '{' {
		return jsonscan.Value(b, i, 1)
	}
	known := true
	var seenType, seenRole bool
	end := jsonscan.Object(b, i, 1, func(key []byte, escaped bool, v int) int {
		var dst *string
		var seen *bool
		switch {
		case !escaped && string(key) == "type":
			dst, seen = &f.payloadType, &seenType
		case !escaped && string(key) == "role":
			dst, seen = &f.payloadRole, &seenRole
		case escaped || !foldSafeKey(key):
			known = false
			return jsonscan.Value(b, v, 2)
		default:
			return jsonscan.Value(b, v, 2)
		}
		s, end := plainString(b, v)
		if end < 0 || *seen {
			known = false
			return jsonscan.Value(b, v, 2)
		}
		*seen, *dst = true, s
		return end
	})
	f.known = known && end >= 0
	return end
}

// plainString returns a string value that needs no decoding, or "" for null,
// as encoding/json leaves a string field unset for null.
func plainString(b []byte, i int) (string, int) {
	i = jsonscan.Space(b, i)
	if i+4 <= len(b) && string(b[i:i+4]) == "null" {
		return "", i + 4
	}
	end := jsonscan.String(b, i)
	if end < 0 {
		return "", -1
	}
	s := b[i+1 : end-1]
	if bytes.IndexByte(s, '\\') >= 0 || !utf8.Valid(s) {
		return "", -1
	}
	return string(s), end
}

// foldSafeKey reports keys that encoding/json cannot match case-insensitively
// to a different field name: ASCII without uppercase letters. Non-ASCII keys
// can fold onto ASCII ones (U+017F folds to 's').
func foldSafeKey(key []byte) bool {
	for _, c := range key {
		if c >= 0x80 || (c >= 'A' && c <= 'Z') {
			return false
		}
	}
	return true
}

// listReadSize lets most records arrive in one ReadSlice.
const listReadSize = 256 << 10

// scanList visits records from r, starting at offset with state st, and
// writes the raw bytes of every newline-terminated line to a non-nil h. It
// returns the state and offset after the last complete line, which are safe
// to resume from, and the final state, which also includes an unterminated
// last line.
func scanList(r io.Reader, h *prefixHasher, st listState, offset int64) (complete listState, completeOffset int64, final listState, err error) {
	br := bufio.NewReaderSize(r, listReadSize)
	complete, completeOffset = st, offset
	for {
		line, n, terminated, err := readListLine(br, h, jsonlLineCap)
		if err != nil && err != io.EOF {
			return listState{}, 0, listState{}, err
		}
		if len(line) > 0 {
			st.visit(line)
		}
		if terminated {
			offset += n
			complete, completeOffset = st, offset
		}
		if err == io.EOF {
			return complete, completeOffset, st, nil
		}
	}
}

// readListLine returns the next line, trimmed, or nil for a line over max
// bytes. Lines that fit in the reader's buffer are not copied; the result is
// valid until the next read. size counts the bytes consumed, and a non-nil h
// receives them unless the line is an unterminated last line.
func readListLine(r *bufio.Reader, h *prefixHasher, max int) (line []byte, size int64, terminated bool, err error) {
	var (
		buf       bytes.Buffer
		truncated bool
		long      bool
	)
	for {
		chunk, err := r.ReadSlice('\n')
		size += int64(len(chunk))
		if err == bufio.ErrBufferFull {
			if h != nil {
				if !long {
					h.save() // Undone if the line turns out unterminated.
				}
				h.Write(chunk)
			}
			long = true
			if !truncated {
				if buf.Len()+len(chunk) > max {
					truncated = true
				} else {
					buf.Write(chunk)
				}
			}
			continue
		}
		terminated = len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
		if h != nil && terminated {
			h.Write(chunk)
		} else if h != nil && long {
			h.restore()
		}
		if !truncated && buf.Len()+len(chunk) > max {
			truncated = true
		}
		if truncated {
			return nil, size, terminated, err
		}
		if !long {
			return bytes.TrimSpace(chunk), size, terminated, err
		}
		buf.Write(chunk)
		return bytes.TrimSpace(buf.Bytes()), size, terminated, err
	}
}
