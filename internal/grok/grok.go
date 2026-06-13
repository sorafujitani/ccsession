// Package grok reads sessions from Grok's on-disk JSON/JSONL store.
package grok

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

const EnvHome = "GROK_HOME"

const jsonlLineCap = 16 * 1024 * 1024

type Store struct {
	home string
}

type summaryFile struct {
	Info struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"info"`
	SessionSummary string `json:"session_summary"`
	GeneratedTitle string `json:"generated_title"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
	LastActiveAt   string `json:"last_active_at"`
}

type chatEntry struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
}

type updateEntry struct {
	Timestamp int64 `json:"timestamp"`
	Params    struct {
		Update struct {
			SessionUpdate string    `json:"sessionUpdate"`
			Content       textBlock `json:"content"`
		} `json:"update"`
	} `json:"params"`
}

type textBlock struct {
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
	return filepath.Join(home, ".grok"), nil
}

func (s *Store) Scan() ([]*session.Session, error) {
	return s.scanFiltered(nil)
}

func (s *Store) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	return s.scanFiltered(allow)
}

func (s *Store) FindByID(id string) (*session.Session, error) {
	paths, err := s.summaryPaths()
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		sess, err := s.readSummary(path)
		if err != nil {
			return nil, err
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
	sessions, err := s.Scan()
	if err != nil {
		return nil, err
	}
	set := make(map[string]struct{})
	for _, sess := range sessions {
		if match(sess.Label) {
			set[sess.ID] = struct{}{}
			continue
		}
		msgs, err := messagesForSession(sess, 0)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			if match(m.Body) {
				set[sess.ID] = struct{}{}
				break
			}
		}
	}
	return set, nil
}

func (s *Store) Messages(sessionID string, limit int) ([]session.Message, time.Time, int, error) {
	sess, err := s.FindByID(sessionID)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	msgs, err := messagesForSession(sess, 0)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	total := len(msgs)
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, summaryCreatedAt(filepath.Join(filepath.Dir(sess.JSONLPath), "summary.json")), total, nil
}

func (s *Store) scanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	paths, err := s.summaryPaths()
	if err != nil {
		return nil, err
	}
	out := make([]*session.Session, 0, len(paths))
	for _, path := range paths {
		sess, err := s.readSummary(path)
		if err != nil {
			return nil, err
		}
		if sess == nil || !allowed(allow, sess.ID) {
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

func (s *Store) summaryPaths() ([]string, error) {
	root := filepath.Join(s.home, "sessions")
	var paths []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == "summary.json" {
			paths = append(paths, path)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return paths, err
}

func (s *Store) readSummary(path string) (*session.Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var sf summaryFile
	if err := json.NewDecoder(f).Decode(&sf); err != nil {
		return nil, err
	}
	sessionDir := filepath.Dir(path)
	id := sf.Info.ID
	if id == "" {
		id = filepath.Base(sessionDir)
	}
	if idCorruptsRow(id) {
		return nil, nil
	}
	cwd := sf.Info.CWD
	if cwd == "" {
		cwd = cwdFromGroup(filepath.Dir(sessionDir))
	}
	last := firstTime(sf.UpdatedAt, sf.LastActiveAt, sf.CreatedAt)
	label := session.SanitizeLabel(firstNonEmpty(sf.GeneratedTitle, sf.SessionSummary, "(no summary)"))
	sess := &session.Session{
		ID:         id,
		ProjectDir: filepath.Dir(sessionDir),
		JSONLPath:  filepath.Join(sessionDir, "chat_history.jsonl"),
		CWD:        cwd,
		Label:      label,
		LastTime:   last,
		LastEpoch:  last.Unix(),
	}
	if cwd == "" {
		sess.CWDUnknown = true
	} else {
		sess.CWDBasename = filepath.Base(cwd)
		sess.CWDExists = pathIsDir(cwd)
	}
	return sess, nil
}

func messagesForSession(sess *session.Session, limit int) ([]session.Message, error) {
	msgs, err := readMessages(filepath.Dir(sess.JSONLPath))
	if err != nil {
		return nil, err
	}
	if limit > 0 && len(msgs) > limit {
		msgs = msgs[len(msgs)-limit:]
	}
	return msgs, nil
}

func readMessages(sessionDir string) ([]session.Message, error) {
	msgs, err := readUpdateMessages(filepath.Join(sessionDir, "updates.jsonl"))
	if err != nil {
		return nil, err
	}
	if len(msgs) > 0 {
		return msgs, nil
	}
	return readChatMessages(filepath.Join(sessionDir, "chat_history.jsonl"))
}

func readUpdateMessages(path string) ([]session.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []session.Message
	err = scanJSONLLines(f, func(line []byte) {
		var e updateEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		role := updateRole(e.Params.Update.SessionUpdate)
		body := e.Params.Update.Content.Text
		if role == "" || body == "" {
			return
		}
		if role == "assistant" && len(out) > 0 && out[len(out)-1].Role == role {
			out[len(out)-1].Body += body
			return
		}
		out = append(out, session.Message{
			Role:      role,
			Timestamp: epochTime(e.Timestamp),
			Body:      body,
		})
	})
	return out, err
}

func readChatMessages(path string) ([]session.Message, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []session.Message
	err = scanJSONLLines(f, func(line []byte) {
		var e chatEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		if e.Type != "user" && e.Type != "assistant" {
			return
		}
		body := extractContent(e.Content)
		if strings.TrimSpace(body) == "" {
			return
		}
		out = append(out, session.Message{Role: e.Type, Body: body})
	})
	return out, err
}

func updateRole(update string) string {
	switch update {
	case "user_message_chunk":
		return "user"
	case "agent_message_chunk":
		return "assistant"
	default:
		return ""
	}
}

func extractContent(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []textBlock
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

func cwdFromGroup(groupDir string) string {
	if b, err := os.ReadFile(filepath.Join(groupDir, ".cwd")); err == nil {
		return strings.TrimSpace(string(b))
	}
	cwd, err := url.PathUnescape(filepath.Base(groupDir))
	if err != nil {
		return ""
	}
	return cwd
}

func summaryCreatedAt(path string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	var sf summaryFile
	if err := json.NewDecoder(f).Decode(&sf); err != nil {
		return time.Time{}
	}
	return firstTime(sf.CreatedAt)
}

func firstTime(values ...string) time.Time {
	for _, v := range values {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
	}
	return time.Time{}
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

func sortEpoch(epoch, nowEpoch int64) int64 {
	if epoch > nowEpoch {
		return 0
	}
	return epoch
}

func epochTime(ts int64) time.Time {
	if ts == 0 {
		return time.Time{}
	}
	if ts > 1_000_000_000_000 {
		return time.UnixMilli(ts)
	}
	return time.Unix(ts, 0)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
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
