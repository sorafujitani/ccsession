// Package pi reads sessions from the pi coding agent's on-disk JSONL store.
package pi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/filescan"
	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/timefmt"
)

// EnvSessionsDir is pi's own override for its sessions directory, honored so
// ccsession and pi always agree on where sessions live.
const EnvSessionsDir = "PI_CODING_AGENT_SESSION_DIR"

const jsonlLineCap = 64 * 1024 * 1024

var uuidInName = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

type Store struct {
	dir string
}

type entry struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	CWD       string          `json:"cwd"`
	Name      string          `json:"name"`
	Message   json.RawMessage `json:"message"`
}

type messagePayload struct {
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	Timestamp int64           `json:"timestamp"`
}

var parseSessionFile = parseFile

func Open() (*Store, error) {
	dir, err := ResolveSessionsDir()
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

func OpenAt(dir string) *Store {
	return &Store{dir: dir}
}

func ResolveSessionsDir() (string, error) {
	if dir := os.Getenv(EnvSessionsDir); dir != "" {
		return filepath.Abs(dir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent", "sessions"), nil
}

func (s *Store) Scan() ([]*session.Session, error) {
	return s.scanFiltered(nil)
}

func (s *Store) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	return s.scanFiltered(allow)
}

func (s *Store) FindByID(id string) (*session.Session, error) {
	sessions, err := s.representativeSessions()
	if err != nil {
		return nil, err
	}
	for _, sess := range sessions {
		if sess.ID == id {
			return sess, nil
		}
	}
	return nil, session.ErrSessionFileMissing
}

func (s *Store) FindByLocator(id, path string) (*session.Session, error) {
	sess, _, _, _, err := parseSessionFile(path, false, 0)
	if err != nil {
		return nil, err
	}
	if sess == nil || sess.ID != id {
		return nil, session.ErrSessionFileMissing
	}
	return sess, nil
}

func (s *Store) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	match, err := grep.BuildMatcher(query, grep.Options{Regex: regex})
	if err != nil {
		return nil, err
	}
	sessions, err := s.representativeSessions()
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, sess := range sessions {
		if match(sess.Label) {
			set[sess.ID] = struct{}{}
			continue
		}
		ok, err := fileMessagesMatch(sess.JSONLPath, match)
		if err != nil {
			return nil, err
		}
		if ok {
			set[sess.ID] = struct{}{}
		}
	}
	return set, nil
}

func (s *Store) Messages(sessionID string, limit int) ([]session.Message, time.Time, int, error) {
	sess, err := s.FindByID(sessionID)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	return s.MessagesForSession(sess, limit)
}

func (s *Store) MessagesForSession(sess *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	if sess == nil || sess.JSONLPath == "" {
		return nil, time.Time{}, 0, session.ErrSessionFileMissing
	}
	_, msgs, startedAt, total, err := parseFile(sess.JSONLPath, true, limit)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	return msgs, startedAt, total, nil
}

func (s *Store) scanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	sessions, err := s.representativeSessions()
	if err != nil {
		return nil, err
	}
	out := make([]*session.Session, 0, len(sessions))
	for _, sess := range sessions {
		if !allowed(allow, sess.ID) {
			continue
		}
		out = append(out, sess)
	}
	nowEpoch := time.Now().Unix()
	sort.SliceStable(out, func(i, j int) bool {
		ki, kj := sortEpoch(out[i].LastEpoch, nowEpoch), sortEpoch(out[j].LastEpoch, nowEpoch)
		if ki != kj {
			return ki > kj
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *Store) sessionPaths() ([]string, error) {
	var paths []string
	err := filepath.WalkDir(s.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(d.Name(), ".jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	sort.Strings(paths)
	return paths, err
}

func (s *Store) representativeSessions() ([]*session.Session, error) {
	paths, err := s.sessionPaths()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	out := make([]*session.Session, 0, len(paths))
	candidates := filescan.Parallel(paths, func(path string) (*session.Session, bool) {
		sess, _, _, _, err := parseSessionFile(path, false, 0)
		if err != nil || sess == nil {
			return nil, false
		}
		return sess, true
	})
	for _, sess := range candidates {
		if _, ok := seen[sess.ID]; ok {
			continue
		}
		seen[sess.ID] = struct{}{}
		out = append(out, sess)
	}
	return out, nil
}

func parseFile(path string, includeMessages bool, messageLimit int) (*session.Session, []session.Message, time.Time, int, error) {
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
		firstUser string
		infoName  string
		lastTS    time.Time
		startedAt time.Time
		msgs      []session.Message
		total     int
	)
	err = scanJSONLLines(f, func(line []byte) {
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		lineTS := timefmt.Parse(e.Timestamp)
		if !lineTS.IsZero() && lineTS.After(lastTS) {
			lastTS = lineTS
		}
		switch e.Type {
		case "session":
			if e.ID != "" {
				sess.ID = e.ID
			}
			if e.CWD != "" {
				sess.CWD = e.CWD
			}
			if !lineTS.IsZero() && startedAt.IsZero() {
				startedAt = lineTS
			}
		case "session_info":
			// The latest entry always wins: pi treats an empty name as an
			// explicit clear, reverting the label to the first user message.
			infoName = e.Name
		case "message":
			msg, ok := parseMessagePayload(e.Message, lineTS)
			if !ok {
				return
			}
			if startedAt.IsZero() && !msg.Timestamp.IsZero() {
				startedAt = msg.Timestamp
			}
			if msg.Role == "user" && firstUser == "" {
				firstUser = msg.Body
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
	label := session.SanitizeLabel(infoName)
	if label == "" {
		label = session.SanitizeLabel(firstUser)
	}
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

func parseMessagePayload(raw json.RawMessage, ts time.Time) (session.Message, bool) {
	var p messagePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return session.Message{}, false
	}
	if p.Role != "user" && p.Role != "assistant" {
		return session.Message{}, false
	}
	body := strings.TrimSpace(session.ExtractText(p.Content, "\n"))
	if body == "" {
		return session.Message{}, false
	}
	if p.Timestamp > 0 {
		ts = time.UnixMilli(p.Timestamp)
	}
	return session.Message{Role: p.Role, Timestamp: ts, Body: body}, true
}

func fileMessagesMatch(path string, match func(string) bool) (bool, error) {
	return grep.FileContains(path, match, fileMessageTexts)
}

func fileMessageTexts(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var texts []string
	err = scanJSONLLines(f, func(line []byte) {
		var e entry
		if err := json.Unmarshal(line, &e); err != nil || e.Type != "message" {
			return
		}
		msg, ok := parseMessagePayload(e.Message, time.Time{})
		if ok {
			texts = append(texts, msg.Body)
		}
	})
	return texts, err
}

func appendMessage(msgs []session.Message, msg session.Message, total, limit int) []session.Message {
	if limit <= 0 {
		return append(msgs, msg)
	}
	if len(msgs) < limit {
		return append(msgs, msg)
	}
	msgs[total%limit] = msg
	return msgs
}

func collectMessages(msgs []session.Message, total, limit int) []session.Message {
	if limit <= 0 || total <= limit || len(msgs) == 0 {
		return msgs
	}
	out := make([]session.Message, 0, len(msgs))
	start := total % limit
	for i := range len(msgs) {
		out = append(out, msgs[(start+i)%limit])
	}
	return out
}

func scanJSONLLines(r io.Reader, visit func([]byte)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := readJSONLLine(br, jsonlLineCap)
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

func readJSONLLine(r *bufio.Reader, max int) ([]byte, error) {
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

func idFromPath(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	matches := uuidInName.FindAllString(name, -1)
	if len(matches) == 0 {
		return name
	}
	return matches[len(matches)-1]
}

func sortEpoch(epoch, nowEpoch int64) int64 {
	if epoch > nowEpoch {
		return 0
	}
	return epoch
}

func allowed(allow map[string]struct{}, id string) bool {
	if allow == nil {
		return true
	}
	_, ok := allow[id]
	return ok
}

func idCorruptsRow(id string) bool {
	return strings.ContainsAny(id, "\t\n\r")
}

func pathIsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
