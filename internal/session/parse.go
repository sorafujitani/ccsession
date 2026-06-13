package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/timefmt"
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
		earliestCWD  string // chronologically first cwd seen (reverse loop ⇒ last write wins)
		matchingCWD  string // cwd whose `/`→`-` encoding matches the project dir name
		lastTS       time.Time
	)

	// `claude --resume` resolves the JSONL by encoding the current cwd as
	// `/`→`-` and reading `~/.claude/projects/<encoded>/<id>.jsonl`. If the
	// user `cd`-ed mid-session, the JSONL records multiple distinct cwds
	// and only the one whose encoding matches the project dir is safe to
	// chdir to before exec — picking any other will make claude look in
	// the wrong folder and fail with "No conversation found".
	projectsBase := filepath.Base(sess.ProjectDir)

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
		// Collect cwd from any entry that carries one (user, assistant,
		// system, …). The JSONL can record several distinct cwds within
		// a single session.
		if e.CWD != "" {
			earliestCWD = e.CWD
			if matchingCWD == "" && encodeCWD(e.CWD) == projectsBase {
				matchingCWD = e.CWD
			}
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
			if lastUserText == "" && e.Message != nil {
				lastUserText = ExtractText(e.Message.Content, " ")
			}
			if t := timefmt.Parse(e.Timestamp); !t.IsZero() && t.After(lastTS) {
				lastTS = t
			}
		case "assistant":
			if t := timefmt.Parse(e.Timestamp); !t.IsZero() && t.After(lastTS) {
				lastTS = t
			}
		}
	}

	// cwd selection precedence (highest to lowest):
	//   1. a cwd from the tail whose `/`→`-` encoding matches the project
	//      dir name — that's exactly what `claude --resume` will look for.
	//   2. the cwd of the *first* entry in the file (session-start cwd):
	//      `claude` names the project dir after this, so it's the safest
	//      candidate. Required for large sessions where the entire tail
	//      records a post-`cd` cwd.
	//   3. the project dir name decoded back to a path, if that path exists
	//      (lossy fallback for transcripts with no cwd fields at all).
	//   4. the chronologically earliest cwd recorded in the tail — last
	//      resort, may point at the wrong directory.
	cwd := matchingCWD
	if cwd == "" {
		if start, _ := readStartCWD(path); start != "" {
			cwd = start
		}
	}
	if cwd == "" {
		if guess := restoreCWDFromDir(sess.ProjectDir); pathIsDir(guess) {
			cwd = guess
		}
	}
	if cwd == "" {
		cwd = earliestCWD
	}

	if !hasUser {
		return nil, ErrSessionEmpty
	}

	label := firstNonEmpty(aiTitle, lastPrompt, lastUserText)
	if label == "" {
		return nil, ErrSessionEmpty
	}
	sess.Label = SanitizeLabel(label)
	// Defense in depth: if every label candidate sanitized to empty
	// (e.g., a label that was just control chars), treat as empty.
	if sess.Label == "" {
		return nil, ErrSessionEmpty
	}

	sess.CWD = cwd
	if cwd == "" {
		// We exhausted every candidate (no recorded cwd, no encode-match,
		// dir-name decode was lossy AND the decoded path did not exist).
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

// SanitizeLabel collapses whitespace and neutralizes control characters in a
// session label so it is safe to emit into the tab-separated fzf row. Exported
// for reuse by other sources (OpenCode titles come from the DB, not a JSONL).
func SanitizeLabel(s string) string {
	// Replace every C0/C1-ish control char (ESC, BEL, DEL, …) with a space,
	// not just CR/LF/TAB. Pasted ANSI color codes otherwise leak through
	// and can hijack fzf rendering.
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	return Truncate(s, LabelMaxLen)
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

// encodeCWD mirrors Claude Code's projects-directory naming: every "/" in
// the absolute path becomes "-". This is the (lossy) inverse of
// restoreCWDFromDir; we use it to pick the cwd that `claude --resume` will
// actually look at, not the latest cwd recorded in the transcript.
func encodeCWD(cwd string) string {
	return strings.ReplaceAll(cwd, "/", "-")
}

// readStartCWD scans from the head of the file and returns the cwd of the
// first entry that records one. The project dir under ~/.claude/projects/
// is named after the session-start cwd, so this value reliably matches
// what `claude --resume` will look for — even when the tail only contains
// post-`cd` cwds.
//
// Stops scanning early once a cwd is found. Reads at most 64 KiB on entry
// boundaries.
func readStartCWD(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	sc := bufio.NewScanner(io.LimitReader(f, 64*1024))
	sc.Buffer(make([]byte, 16*1024), 64*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		if e.CWD != "" {
			return e.CWD, nil
		}
	}
	return "", sc.Err()
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
