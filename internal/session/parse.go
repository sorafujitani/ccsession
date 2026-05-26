package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// TailReadBytes is the upper bound on how many bytes we read from the end of a JSONL file.
const TailReadBytes int64 = 64 * 1024

// LabelMaxLen is the max length of the label string after sanitization.
const LabelMaxLen = 200

// ParseSessionTail reads the tail of one JSONL transcript and returns a Session,
// or (nil, nil) when the session should be excluded (no user messages / no label).
func ParseSessionTail(path string, maxBytes int64) (*Session, error) {
	buf, err := readTail(path, maxBytes)
	if err != nil {
		return nil, err
	}

	sess := &Session{
		ID:         strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		JSONLPath:  path,
		ProjectDir: filepath.Dir(path),
	}

	var (
		hasUser      bool
		aiTitle      string
		lastPrompt   string
		lastUserText string
		cwd          string
		lastTS       time.Time
	)

	lines := bytes.Split(buf, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		switch e.Type {
		case "ai-title":
			if aiTitle == "" && e.AITitle != "" {
				aiTitle = e.AITitle
			}
		case "last-prompt":
			if lastPrompt == "" && e.LastPrompt != "" {
				lastPrompt = e.LastPrompt
			}
		case "user":
			hasUser = true
			if cwd == "" && e.CWD != "" {
				cwd = e.CWD
			}
			if lastUserText == "" && e.Message != nil {
				lastUserText = extractText(e.Message.Content)
			}
			if t := parseTimestamp(e.Timestamp); !t.IsZero() && t.After(lastTS) {
				lastTS = t
			}
		case "assistant":
			if t := parseTimestamp(e.Timestamp); !t.IsZero() && t.After(lastTS) {
				lastTS = t
			}
		}
	}

	if !hasUser {
		return nil, nil
	}

	label := firstNonEmpty(aiTitle, lastPrompt, lastUserText)
	if label == "" {
		return nil, nil
	}
	sess.Label = sanitizeLabel(label)

	sess.CWD = cwd
	if cwd == "" {
		sess.CWD = restoreCWDFromDir(sess.ProjectDir)
	}
	sess.CWDBasename = filepath.Base(sess.CWD)
	sess.CWDExists = pathIsDir(sess.CWD)

	if lastTS.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			lastTS = fi.ModTime()
		}
	}
	sess.LastTime = lastTS
	sess.LastEpoch = lastTS.Unix()
	return sess, nil
}

func readTail(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	if size == 0 {
		return nil, nil
	}

	var (
		offset int64
		length = size
	)
	if size > maxBytes {
		offset = size - maxBytes
		length = maxBytes
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(f, buf); err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, err
	}
	if offset > 0 {
		// Drop the first (likely truncated) line.
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		}
	}
	return buf, nil
}

func parseTimestamp(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type != "text" || block.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(block.Text)
		}
		return b.String()
	}
	return ""
}

func sanitizeLabel(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	s = strings.TrimSpace(s)
	return truncate(s, LabelMaxLen)
}

func truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// restoreCWDFromDir falls back to the project directory name when JSONL
// has no cwd field. The encoding replaces "/" with "-" so this is lossy.
func restoreCWDFromDir(dir string) string {
	base := filepath.Base(dir)
	base = strings.TrimPrefix(base, "-")
	return "/" + strings.ReplaceAll(base, "-", "/")
}

func pathIsDir(path string) bool {
	if path == "" {
		return false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return fi.IsDir()
}
