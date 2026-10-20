package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/timefmt"
)

// The scanner before list caching, kept as the oracle that list scans,
// cached resumes, and parseMessages must match.

func referenceParseFile(path string, includeMessages bool, messageLimit int) (*session.Session, []session.Message, time.Time, int, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, time.Time{}, 0, session.ErrSessionFileMissing
		}
		return nil, nil, time.Time{}, 0, err
	}
	defer f.Close()

	sess := &session.Session{
		ID:         idFromPath(path),
		ProjectDir: filepath.Dir(path),
		JSONLPath:  path,
	}
	var (
		label     string
		lastTS    time.Time
		startedAt time.Time
		msgs      []session.Message
		total     int
	)
	err = referenceScanLines(f, func(line []byte) {
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		lineTS := timefmt.Parse(e.Timestamp)
		if !lineTS.IsZero() && lineTS.After(lastTS) {
			lastTS = lineTS
		}
		switch e.Type {
		case "session_meta":
			var meta sessionMetaPayload
			if err := json.Unmarshal(e.Payload, &meta); err != nil {
				return
			}
			if meta.ID != "" {
				sess.ID = meta.ID
			}
			if meta.CWD != "" {
				sess.CWD = meta.CWD
			}
			metaTS := timefmt.Parse(meta.Timestamp)
			if !metaTS.IsZero() && startedAt.IsZero() {
				startedAt = metaTS
			}
			if !metaTS.IsZero() && metaTS.After(lastTS) {
				lastTS = metaTS
			}
		case "response_item":
			msg, ok := parseMessagePayload(e.Payload, lineTS)
			if !ok {
				return
			}
			if startedAt.IsZero() && !msg.Timestamp.IsZero() {
				startedAt = msg.Timestamp
			}
			if msg.Role == "user" {
				label = msg.Body
			}
			if includeMessages {
				msgs = appendMessage(msgs, msg, total, messageLimit)
			}
			total++
		}
	})
	if err != nil {
		return nil, nil, time.Time{}, 0, err
	}
	if sess.ID == "" || idCorruptsRow(sess.ID) {
		return nil, nil, time.Time{}, 0, nil
	}
	label = session.SanitizeLabel(label)
	if label == "" {
		return nil, nil, time.Time{}, 0, session.ErrSessionEmpty
	}
	sess.Label = label
	if sess.CWD == "" {
		sess.CWDUnknown = true
	} else {
		sess.CWDBasename = filepath.Base(sess.CWD)
		sess.CWDExists = pathIsDir(sess.CWD)
	}
	if lastTS.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			lastTS = fi.ModTime()
		}
	}
	sess.LastTime = lastTS
	sess.LastEpoch = lastTS.Unix()
	return sess, collectMessages(msgs, total, messageLimit), startedAt, total, nil
}

func referenceScanLines(r io.Reader, visit func([]byte)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := referenceReadLine(br, jsonlLineCap)
		if len(line) > 0 {
			visit(line)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func referenceReadLine(r *bufio.Reader, max int) ([]byte, error) {
	var (
		buf       bytes.Buffer
		truncated bool
	)
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 && !truncated {
			if buf.Len()+len(chunk) > max {
				truncated = true
			} else {
				buf.Write(chunk)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if truncated {
			return nil, err
		}
		return bytes.TrimSpace(buf.Bytes()), err
	}
}
