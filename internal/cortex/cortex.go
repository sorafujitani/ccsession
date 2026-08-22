// Package cortex reads sessions from the Snowflake Cortex Code CLI's on-disk store.
package cortex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/filescan"
	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

const EnvHome = "CORTEX_CODE_HOME"

const jsonlLineCap = 64 * 1024 * 1024

type Store struct {
	home string
}

// メタデータ JSON の構造
type metadata struct {
	SessionID        string `json:"session_id"`
	Title            string `json:"title"`
	WorkingDirectory string `json:"working_directory"`
	CreatedAt        string `json:"created_at"`
	LastUpdated      string `json:"last_updated"`
	SessionType      string `json:"session_type"`
	ConnectionName   string `json:"connection_name"`
}

// JSONL 各行の構造
type entry struct {
	Role    string         `json:"role"`
	ID      string         `json:"id"`
	Content []contentBlock `json:"content"`
}

type contentBlock struct {
	Type         string `json:"type"`
	Text         string `json:"text"`
	IsUserPrompt bool   `json:"is_user_prompt"`
	InternalOnly bool   `json:"internalOnly"`
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
	return filepath.Join(home, ".snowflake", "cortex"), nil
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
	sess, _, _, _, err := parseFile(path, false, 0)
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
			continue
		}
		if ok {
			set[sess.ID] = struct{}{}
		}
	}
	return set, nil
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

// sessionPaths は conversations/ 直下の .history.jsonl のみ返す。
// サブディレクトリ内のファイルは Desktop(panel) セッションのため除外する。
func (s *Store) sessionPaths() ([]string, error) {
	root := filepath.Join(s.home, "conversations")
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".history.jsonl") {
			paths = append(paths, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *Store) representativeSessions() ([]*session.Session, error) {
	paths, err := s.sessionPaths()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	out := make([]*session.Session, 0, len(paths))
	candidates := filescan.Parallel(paths, func(path string) (*session.Session, bool) {
		sess, _, _, _, err := parseFile(path, false, 0)
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

// metadataPathFor は history.jsonl パスから対応するメタデータ JSON パスを導出する。
func metadataPathFor(historyPath string) string {
	base := filepath.Base(historyPath)
	name := strings.TrimSuffix(base, ".history.jsonl")
	return filepath.Join(filepath.Dir(historyPath), name+".json")
}

func parseFile(path string, includeMessages bool, messageLimit int) (*session.Session, []session.Message, time.Time, int, error) {
	metaPath := metadataPathFor(path)
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, time.Time{}, 0, session.ErrSessionFileMissing
		}
		return nil, nil, time.Time{}, 0, err
	}

	var meta metadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, nil, time.Time{}, 0, err
	}

	// main セッションのみ表示 (サブエージェントセッション除外)
	if meta.SessionType != "" && meta.SessionType != "main" {
		return nil, nil, time.Time{}, 0, nil
	}

	sess := &session.Session{
		ID:             meta.SessionID,
		ProjectDir:     filepath.Dir(path),
		JSONLPath:      path,
		ConnectionName: meta.ConnectionName,
	}

	// メタデータからラベル・CWD・タイムスタンプを取得
	label := meta.Title
	if strings.HasPrefix(label, "Chat for session:") {
		label = ""
	}

	if meta.WorkingDirectory != "" {
		sess.CWD = meta.WorkingDirectory
		sess.CWDBasename = filepath.Base(meta.WorkingDirectory)
		sess.CWDExists = pathIsDir(meta.WorkingDirectory)
	} else {
		sess.CWDUnknown = true
	}

	lastTS := parseISO(meta.LastUpdated)
	startedAt := parseISO(meta.CreatedAt)

	var (
		msgs      []session.Message
		total     int
		firstUser string
	)

	if includeMessages || label == "" {
		f, err := os.Open(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, nil, time.Time{}, 0, session.ErrSessionFileMissing
			}
			return nil, nil, time.Time{}, 0, err
		}
		defer f.Close()

		err = scanJSONLLines(f, func(line []byte) {
			var e entry
			if err := json.Unmarshal(line, &e); err != nil {
				return
			}
			if e.Role != "user" && e.Role != "assistant" {
				return
			}
			body := extractVisibleText(e.Content, e.Role)
			if body == "" {
				return
			}
			if e.Role == "user" && firstUser == "" {
				firstUser = body
			}
			if includeMessages {
				msg := session.Message{Role: e.Role, Body: body}
				msgs = appendMessage(msgs, msg, total, messageLimit)
			}
			total++
		})
		if err != nil {
			return nil, nil, time.Time{}, 0, err
		}
	}

	if label == "" {
		label = firstUser
	}
	label = session.SanitizeLabel(label)
	if label == "" {
		return nil, nil, time.Time{}, 0, session.ErrSessionEmpty
	}
	sess.Label = label

	if lastTS.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			lastTS = fi.ModTime()
		}
	}
	sess.LastTime = lastTS
	sess.LastEpoch = lastTS.Unix()

	return sess, collectMessages(msgs, total, messageLimit), startedAt, total, nil
}

func extractVisibleText(blocks []contentBlock, role string) string {
	var parts []string
	for _, b := range blocks {
		if b.Type != "text" {
			continue
		}
		if b.InternalOnly {
			continue
		}
		text := strings.TrimSpace(b.Text)
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "<system-reminder>") {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n")
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
		if err := json.Unmarshal(line, &e); err != nil {
			return
		}
		if e.Role != "user" && e.Role != "assistant" {
			return
		}
		body := extractVisibleText(e.Content, e.Role)
		if body != "" {
			texts = append(texts, body)
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

func parseISO(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.000Z", s)
		if err != nil {
			return time.Time{}
		}
	}
	return t
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

func pathIsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}
