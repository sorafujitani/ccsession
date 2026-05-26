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

// ParseSessionTail reads the tail of one JSONL transcript and returns a Session.
// Returns ErrSessionEmpty when the file exists but has no user message or no
// extractable label. Returns ErrSessionFileMissing when the file is absent.
func ParseSessionTail(path string, maxBytes int64) (*Session, error) {
	buf, err := readTail(path, maxBytes)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSessionFileMissing
		}
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
		return nil, ErrSessionEmpty
	}

	label := firstNonEmpty(aiTitle, lastPrompt, lastUserText)
	if label == "" {
		return nil, ErrSessionEmpty
	}
	sess.Label = sanitizeLabel(label)
	// Defense in depth: if every label candidate sanitized to empty
	// (e.g., a label that was just control chars), treat as empty.
	if sess.Label == "" {
		return nil, ErrSessionEmpty
	}

	sess.CWD = cwd
	if cwd == "" {
		// The encoded directory name replaces "/" with "-", which is lossy
		// for any path that contains a "-". Only trust the recovered path
		// if it actually exists on disk; otherwise mark the cwd as unknown
		// rather than silently producing a wrong-but-plausible value.
		guess := restoreCWDFromDir(sess.ProjectDir)
		if pathIsDir(guess) {
			sess.CWD = guess
		} else {
			sess.CWDUnknown = true
		}
	}
	if sess.CWD != "" {
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
	// Replace every C0/C1-ish control char (ESC, BEL, DEL, …) with a space,
	// not just CR/LF/TAB. Pasted ANSI color codes otherwise leak through
	// and can hijack fzf rendering.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
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
		if strings.TrimSpace(v) != "" {
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
