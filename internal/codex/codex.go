// Package codex reads sessions from Codex CLI's on-disk JSONL store.
package codex

import (
	"bufio"
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
	sess, err := listFile(path)
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
	return parseMessages(sess.JSONLPath, limit)
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

func (s *Store) representativeSessions() ([]*session.Session, error) {
	paths, err := s.sessionPaths()
	if err != nil {
		return nil, err
	}
	cachePath, home := codexCachePath(s.home)
	cache := readCodexCache(cachePath, home)
	type scanned struct {
		path      string
		sess      *session.Session
		rec       codexCacheRecord
		cacheable bool // rec describes the file.
	}
	results := filescan.Parallel(paths, func(path string) (scanned, bool) {
		var prev *codexCacheRecord
		if rec, ok := cache.Files[path]; ok {
			prev = &rec
		}
		sess, rec, err := scanSessionFile(path, prev)
		// Empty and unusable transcripts are cached too, so they are not
		// rescanned; I/O errors are not.
		cacheable := err == nil || errors.Is(err, session.ErrSessionEmpty)
		return scanned{path: path, sess: sess, rec: rec, cacheable: cacheable}, true
	})
	files := make(map[string]codexCacheRecord, len(results))
	seen := make(map[string]struct{})
	out := make([]*session.Session, 0, len(paths))
	for _, r := range results {
		if r.cacheable {
			files[r.path] = r.rec
		}
		if r.sess == nil {
			continue
		}
		if _, ok := seen[r.sess.ID]; ok {
			continue
		}
		seen[r.sess.ID] = struct{}{}
		out = append(out, r.sess)
	}
	changed := len(files) != len(cache.Files)
	for path, rec := range files {
		if prev, ok := cache.Files[path]; !ok || !prev.equal(rec) {
			changed = true
			break
		}
	}
	if changed && cachePath != "" {
		cache.Files = files
		_ = writeCodexCache(cachePath, cache)
	}
	return out, nil
}

// parseMessages reads every message for preview. It fails like the list
// scan for transcripts the list would not show.
func parseMessages(path string, limit int) ([]session.Message, time.Time, int, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, time.Time{}, 0, session.ErrSessionFileMissing
		}
		return nil, time.Time{}, 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	var (
		st        listState
		startedAt time.Time
		msgs      []session.Message
		total     int
	)
	err = scanJSONLLines(f, func(line []byte) {
		st.visit(line)
		var e entry
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		switch e.Type {
		case "session_meta":
			var meta sessionMetaPayload
			if err := json.Unmarshal(e.Payload, &meta); err != nil {
				return
			}
			if metaTS := timefmt.Parse(meta.Timestamp); !metaTS.IsZero() && startedAt.IsZero() {
				startedAt = metaTS
			}
		case "response_item":
			msg, ok := parseMessagePayload(e.Payload, timefmt.Parse(e.Timestamp))
			if !ok {
				return
			}
			if startedAt.IsZero() && !msg.Timestamp.IsZero() {
				startedAt = msg.Timestamp
			}
			msgs = appendMessage(msgs, msg, total, limit)
			total++
		}
	})
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	if sess, err := st.finish(path, info); sess == nil || err != nil {
		return nil, time.Time{}, 0, err
	}
	return collectMessages(msgs, total, limit), startedAt, total, nil
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
		if err := json.Unmarshal(line, &e); err != nil || e.Type != "response_item" {
			return
		}
		msg, ok := parseMessagePayload(e.Payload, time.Time{})
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
	br := bufio.NewReaderSize(r, listReadSize)
	for {
		line, _, _, err := readListLine(br, nil, jsonlLineCap)
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
