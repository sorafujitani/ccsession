// Package codex reads sessions from Codex CLI's on-disk JSONL store.
package codex

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

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/timefmt"
)

const EnvHome = "CODEX_HOME"

const jsonlLineCap = 64 * 1024 * 1024

var uuidInName = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

type Store struct {
	home string
}

type entry struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMetaPayload struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	CWD       string `json:"cwd"`
}

type messagePayload struct {
	Type    string          `json:"type"`
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func Open() (*Store, error) {
	home, err := ResolveHome()
	if err != nil {
		return nil, err
	}
	return &Store{home: home}, nil
}

func OpenAt(home string) *Store {
	return &Store{home: home}
}

func ResolveHome() (string, error) {
	if home := os.Getenv(EnvHome); home != "" {
		return filepath.Abs(home)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func (s *Store) Scan() ([]*session.Session, error) {
	return s.scanFiltered(nil)
}

func (s *Store) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	return s.scanFiltered(allow)
}

func (s *Store) FindByID(id string) (*session.Session, error) {
	paths, err := s.representativePaths()
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		sess, _, _, _, err := parseFile(path, false, 0)
		if err != nil || sess == nil {
			continue
		}
		if sess.ID == id {
			return sess, nil
		}
	}
	return nil, session.ErrSessionFileMissing
}

func (s *Store) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	match, err := grep.BuildMatcher(query, grep.Options{Regex: regex})
	if err != nil {
		return nil, err
	}
	paths, err := s.representativePaths()
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, path := range paths {
		sess, _, _, _, err := parseFile(path, false, 0)
		if err != nil || sess == nil {
			continue
		}
		if match(sess.Label) {
			set[sess.ID] = struct{}{}
			continue
		}
		ok, err := fileMessagesMatch(path, match)
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
	_, msgs, startedAt, total, err := parseFile(sess.JSONLPath, true, limit)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	return msgs, startedAt, total, nil
}

func (s *Store) scanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	paths, err := s.representativePaths()
	if err != nil {
		return nil, err
	}
	out := make([]*session.Session, 0, len(paths))
	for _, path := range paths {
		sess, _, _, _, err := parseFile(path, false, 0)
		if err != nil || sess == nil || !allowed(allow, sess.ID) {
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
	root := filepath.Join(s.home, "sessions")
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
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

func (s *Store) representativePaths() ([]string, error) {
	paths, err := s.sessionPaths()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		sess, _, _, _, err := parseFile(path, false, 0)
		if err != nil || sess == nil {
			continue
		}
		if _, ok := seen[sess.ID]; ok {
			continue
		}
		seen[sess.ID] = struct{}{}
		out = append(out, path)
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
		label     string
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

func parseMessagePayload(raw json.RawMessage, ts time.Time) (session.Message, bool) {
	var p messagePayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return session.Message{}, false
	}
	if p.Type != "message" || (p.Role != "user" && p.Role != "assistant") {
		return session.Message{}, false
	}
	body := extractContent(p.Content)
	if p.Role == "user" {
		body = userVisibleBody(body)
	}
	if strings.TrimSpace(body) == "" {
		return session.Message{}, false
	}
	return session.Message{Role: p.Role, Timestamp: ts, Body: body}, true
}

func fileMessagesMatch(path string, match func(string) bool) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	hit := false
	err = scanJSONLLines(f, func(line []byte) {
		if hit {
			return
		}
		var e entry
		if err := json.Unmarshal(line, &e); err != nil || e.Type != "response_item" {
			return
		}
		msg, ok := parseMessagePayload(e.Payload, time.Time{})
		if ok && match(msg.Body) {
			hit = true
		}
	})
	return hit, err
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

func userVisibleBody(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	if strings.HasPrefix(body, "# AGENTS.md instructions for ") ||
		strings.Contains(body, "\n<INSTRUCTIONS>") ||
		strings.HasPrefix(body, "<environment_context>") ||
		strings.HasPrefix(body, "<system_reminder>") {
		return ""
	}
	if _, after, ok := strings.Cut(body, "## My request for Codex:\n"); ok {
		return strings.TrimSpace(after)
	}
	return body
}

func extractContent(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
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
