// Package kiro reads sessions from Kiro CLI's classic, v2, and v3 stores.
package kiro

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/sorafujitani/ccsession/internal/filescan"
	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/timefmt"

	_ "github.com/ncruces/go-sqlite3/driver"
)

const (
	Binary        = "kiro-cli"
	classicPrefix = "classic:"
	jsonlLineCap  = 64 * 1024 * 1024
)

type Store struct {
	home   string
	dbPath string
}

type v2Metadata struct {
	ID        string `json:"session_id"`
	CWD       string `json:"cwd"`
	Title     string `json:"title"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type v2Entry struct {
	Kind string `json:"kind"`
	Data struct {
		Content []v2ContentBlock `json:"content"`
		Meta    struct {
			Timestamp int64 `json:"timestamp"`
		} `json:"meta"`
	} `json:"data"`
}

type v2ContentBlock struct {
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
}

type v3Metadata struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	WorkspacePaths []string `json:"workspacePaths"`
	RootPaths      []string `json:"rootPaths"`
	CreatedAt      string   `json:"createdAt"`
	LastModifiedAt string   `json:"lastModifiedAt"`
}

type v3Entry struct {
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	} `json:"payload"`
}

type classicConversation struct {
	History []classicTurn `json:"history"`
}

type classicTurn struct {
	User struct {
		Content struct {
			Prompt *struct {
				Prompt string `json:"prompt"`
			} `json:"Prompt"`
		} `json:"content"`
		Timestamp string `json:"timestamp"`
	} `json:"user"`
	Assistant struct {
		ToolUse  *classicText `json:"ToolUse"`
		Response *classicText `json:"Response"`
	} `json:"assistant"`
}

type classicText struct {
	Content string `json:"content"`
}

func Open() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Store{
		home:   filepath.Join(home, ".kiro"),
		dbPath: classicDBPath(home),
	}, nil
}

// OpenAt treats home as a self-contained Kiro fixture with data.sqlite3 at
// its root and sessions below it.
func OpenAt(home string) *Store {
	return &Store{home: home, dbPath: filepath.Join(home, "data.sqlite3")}
}

func (s *Store) Scan() ([]*session.Session, error) {
	return s.scanFiltered(nil)
}

func (s *Store) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	return s.scanFiltered(allow)
}

func (s *Store) FindByID(id string) (*session.Session, error) {
	sessions, err := s.scanFiltered(map[string]struct{}{id: {}})
	if err != nil {
		return nil, err
	}
	if len(sessions) == 0 {
		return nil, session.ErrSessionFileMissing
	}
	return sessions[0], nil
}

func (s *Store) FindByLocator(id, path string) (*session.Session, error) {
	if isClassicPath(path) {
		sess, err := s.classicSession(id, strings.TrimPrefix(path, classicPrefix))
		if err != nil {
			return nil, err
		}
		if sess == nil || sess.ID != id {
			return nil, session.ErrSessionFileMissing
		}
		return sess, nil
	}
	var (
		sess *session.Session
		err  error
	)
	if IsV3Path(path) {
		sess, err = s.readV3Metadata(filepath.Join(filepath.Dir(path), "session.json"))
	} else {
		sess, err = s.readV2Metadata(strings.TrimSuffix(path, ".jsonl") + ".json")
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, session.ErrSessionFileMissing
		}
		return nil, err
	}
	if sess == nil || sess.ID != id || sess.JSONLPath != path {
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
	keys := make(map[string]struct{})
	classic := make(map[string]string)
	for _, sess := range sessions {
		if match(sess.Label) {
			keys[sess.ID] = struct{}{}
			continue
		}
		if isClassicPath(sess.JSONLPath) {
			classic[sess.ID] = sess.CWD
			continue
		}
		ok, err := fileMessagesMatch(sess.JSONLPath, match)
		if err != nil {
			return nil, err
		}
		if ok {
			keys[sess.ID] = struct{}{}
		}
	}
	classicKeys, err := s.grepClassic(classic, query, regex, match)
	if err != nil {
		return nil, err
	}
	for id := range classicKeys {
		keys[id] = struct{}{}
	}
	return keys, nil
}

func (s *Store) Messages(sessionID string, limit int) ([]session.Message, time.Time, int, error) {
	sess, err := s.FindByID(sessionID)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	return s.MessagesForSession(sess, limit)
}

func (s *Store) MessagesForSession(sess *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	if sess == nil {
		return nil, time.Time{}, 0, session.ErrSessionFileMissing
	}
	var (
		messages  []session.Message
		startedAt time.Time
		err       error
	)
	if isClassicPath(sess.JSONLPath) {
		messages, startedAt, err = s.classicMessages(sess.ID, sess.CWD)
	} else {
		return readFileMessages(sess.JSONLPath, limit)
	}
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	total := len(messages)
	if limit > 0 && total > limit {
		messages = messages[total-limit:]
	}
	return messages, startedAt, total, nil
}

func (s *Store) scanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	sessions, err := s.representativeSessions()
	if err != nil {
		return nil, err
	}
	out := make([]*session.Session, 0, len(sessions))
	for _, sess := range sessions {
		if allowed(allow, sess.ID) {
			out = append(out, sess)
		}
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

func (s *Store) representativeSessions() ([]*session.Session, error) {
	classic, err := s.classicSessions()
	if err != nil {
		return nil, err
	}
	metadataPaths, err := s.metadataPaths()
	if err != nil {
		return nil, err
	}
	files := filescan.Parallel(metadataPaths, func(path string) (*session.Session, bool) {
		var (
			sess *session.Session
			err  error
		)
		if filepath.Base(path) == "session.json" {
			sess, err = s.readV3Metadata(path)
		} else {
			sess, err = s.readV2Metadata(path)
		}
		return sess, err == nil && sess != nil
	})
	byID := make(map[string]*session.Session, len(classic)+len(files))
	for _, sess := range append(classic, files...) {
		current := byID[sess.ID]
		if current == nil || sess.LastEpoch > current.LastEpoch ||
			(sess.LastEpoch == current.LastEpoch && isClassicPath(current.JSONLPath) && !isClassicPath(sess.JSONLPath)) {
			byID[sess.ID] = sess
		}
	}
	out := make([]*session.Session, 0, len(byID))
	for _, sess := range byID {
		out = append(out, sess)
	}
	return out, nil
}

func (s *Store) metadataPaths() ([]string, error) {
	var paths []string
	v2Root := filepath.Join(s.home, "sessions", "cli")
	entries, err := os.ReadDir(v2Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".json" {
			path := filepath.Join(v2Root, entry.Name())
			if fileExists(strings.TrimSuffix(path, ".json") + ".jsonl") {
				paths = append(paths, path)
			}
		}
	}
	v3Root := filepath.Join(s.home, "sessions", "_global")
	entries, err = os.ReadDir(v3Root)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(v3Root, entry.Name())
		if fileExists(filepath.Join(dir, "session.json")) && fileExists(filepath.Join(dir, "messages.jsonl")) {
			paths = append(paths, filepath.Join(dir, "session.json"))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (s *Store) readV2Metadata(path string) (*session.Session, error) {
	var meta v2Metadata
	if err := readJSON(path, &meta); err != nil {
		return nil, err
	}
	if meta.ID == "" {
		meta.ID = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	last := firstTime(meta.UpdatedAt, meta.CreatedAt)
	if last.IsZero() {
		last = fileModTime(path)
	}
	return newSession(meta.ID, meta.CWD, meta.Title, last, filepath.Dir(path),
		strings.TrimSuffix(path, ".json")+".jsonl"), nil
}

func (s *Store) readV3Metadata(path string) (*session.Session, error) {
	var meta v3Metadata
	if err := readJSON(path, &meta); err != nil {
		return nil, err
	}
	cwd := firstString(meta.WorkspacePaths, meta.RootPaths)
	last := firstTime(meta.LastModifiedAt, meta.CreatedAt)
	if last.IsZero() {
		last = fileModTime(path)
	}
	return newSession(meta.ID, cwd, meta.Title, last, filepath.Dir(path),
		filepath.Join(filepath.Dir(path), "messages.jsonl")), nil
}

func (s *Store) classicSessions() ([]*session.Session, error) {
	db, err := s.openClassic()
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	const q = `SELECT c.key, c.conversation_id,
	COALESCE((
		SELECT json_extract(h.value, '$.user.content.Prompt.prompt')
		FROM json_each(c.value, '$.history') h
		WHERE json_type(h.value, '$.user.content.Prompt.prompt') = 'text'
		ORDER BY CAST(h.key AS INTEGER) DESC
		LIMIT 1
	), ''), c.updated_at
FROM conversations_v2 c
ORDER BY c.key`
	rows, err := db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*session.Session
	for rows.Next() {
		var cwd, id, label string
		var updatedAt int64
		if err := rows.Scan(&cwd, &id, &label, &updatedAt); err != nil {
			return nil, err
		}
		sess := newSession(id, cwd, label, msToTime(updatedAt), filepath.Dir(s.dbPath), classicPrefix+cwd)
		if sess != nil {
			out = append(out, sess)
		}
	}
	return out, rows.Err()
}

func (s *Store) classicSession(id, cwd string) (*session.Session, error) {
	db, err := s.openClassic()
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, session.ErrSessionFileMissing
	}
	defer db.Close()
	const q = `SELECT COALESCE((
		SELECT json_extract(h.value, '$.user.content.Prompt.prompt')
		FROM json_each(c.value, '$.history') h
		WHERE json_type(h.value, '$.user.content.Prompt.prompt') = 'text'
		ORDER BY CAST(h.key AS INTEGER) DESC
		LIMIT 1
	), ''), c.updated_at
FROM conversations_v2 c
WHERE c.key = ? AND c.conversation_id = ?`
	var label string
	var updatedAt int64
	if err := db.QueryRow(q, cwd, id).Scan(&label, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, session.ErrSessionFileMissing
		}
		return nil, err
	}
	return newSession(id, cwd, label, msToTime(updatedAt), filepath.Dir(s.dbPath), classicPrefix+cwd), nil
}

func (s *Store) classicMessages(id, cwd string) ([]session.Message, time.Time, error) {
	db, err := s.openClassic()
	if err != nil {
		return nil, time.Time{}, err
	}
	if db == nil {
		return nil, time.Time{}, session.ErrSessionFileMissing
	}
	defer db.Close()
	const q = `SELECT value, created_at FROM conversations_v2 WHERE key = ? AND conversation_id = ?`
	var raw []byte
	var createdAt int64
	if err := db.QueryRow(q, cwd, id).Scan(&raw, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, time.Time{}, session.ErrSessionFileMissing
		}
		return nil, time.Time{}, err
	}
	messages, err := parseClassicMessages(raw)
	return messages, msToTime(createdAt), err
}

func (s *Store) grepClassic(wanted map[string]string, query string, regex bool, match func(string) bool) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	if len(wanted) == 0 {
		return out, nil
	}
	db, err := s.openClassic()
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()
	q := `SELECT key, conversation_id, value FROM conversations_v2`
	var rows *sql.Rows
	if !regex && canPrefilterClassic(query) {
		rows, err = db.Query(q+` WHERE value COLLATE NOCASE LIKE ? ESCAPE '\'`, "%"+escapeLike(query)+"%")
	} else {
		rows, err = db.Query(q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var cwd, id string
		var raw []byte
		if err := rows.Scan(&cwd, &id, &raw); err != nil {
			return nil, err
		}
		if wantCWD, ok := wanted[id]; !ok || wantCWD != cwd {
			continue
		}
		messages, err := parseClassicMessages(raw)
		if err != nil {
			continue
		}
		for _, message := range messages {
			if match(message.Body) {
				out[id] = struct{}{}
				break
			}
		}
	}
	return out, rows.Err()
}

func (s *Store) openClassic() (*sql.DB, error) {
	if !fileExists(s.dbPath) {
		return nil, nil
	}
	u := url.URL{Scheme: "file", Path: s.dbPath}
	u.RawQuery = "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
	return sql.Open("sqlite3", u.String())
}

func parseClassicMessages(raw []byte) ([]session.Message, error) {
	var conversation classicConversation
	if err := json.Unmarshal(raw, &conversation); err != nil {
		return nil, err
	}
	var messages []session.Message
	var lastTimestamp time.Time
	for _, turn := range conversation.History {
		if ts := timefmt.Parse(turn.User.Timestamp); !ts.IsZero() {
			lastTimestamp = ts
		}
		if turn.User.Content.Prompt != nil {
			messages = appendTextMessage(messages, "user", turn.User.Content.Prompt.Prompt, lastTimestamp)
		}
		if turn.Assistant.ToolUse != nil {
			messages = appendTextMessage(messages, "assistant", turn.Assistant.ToolUse.Content, lastTimestamp)
		}
		if turn.Assistant.Response != nil {
			messages = appendTextMessage(messages, "assistant", turn.Assistant.Response.Content, lastTimestamp)
		}
	}
	return messages, nil
}

func readFileMessages(path string, limit int) ([]session.Message, time.Time, int, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, time.Time{}, 0, session.ErrSessionFileMissing
		}
		return nil, time.Time{}, 0, err
	}
	defer f.Close()
	var (
		messages      []session.Message
		lastTimestamp time.Time
		startedAt     time.Time
		total         int
	)
	v3 := IsV3Path(path)
	err = scanJSONLLines(f, func(line []byte) {
		var (
			message session.Message
			ok      bool
		)
		if v3 {
			var entry v3Entry
			if json.Unmarshal(line, &entry) != nil {
				return
			}
			lastTimestamp = timefmt.Parse(entry.Timestamp)
			message, ok = textMessage(entry.Payload.Type, entry.Payload.Content, lastTimestamp)
		} else {
			var entry v2Entry
			if json.Unmarshal(line, &entry) != nil {
				return
			}
			role := ""
			switch entry.Kind {
			case "Prompt":
				role = "user"
			case "AssistantMessage":
				role = "assistant"
			}
			if role == "" {
				return
			}
			if entry.Data.Meta.Timestamp != 0 {
				lastTimestamp = time.Unix(entry.Data.Meta.Timestamp, 0)
			}
			var parts []string
			for _, block := range entry.Data.Content {
				if block.Kind == "text" {
					if text := blockText(block.Data); text != "" {
						parts = append(parts, text)
					}
				}
			}
			message, ok = textMessage(role, strings.Join(parts, "\n"), lastTimestamp)
		}
		if !ok {
			return
		}
		if startedAt.IsZero() && !message.Timestamp.IsZero() {
			startedAt = message.Timestamp
		}
		messages = appendMessage(messages, message, total, limit)
		total++
	})
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	return collectMessages(messages, total, limit), startedAt, total, nil
}

func messageTexts(path string) ([]string, error) {
	messages, _, _, err := readFileMessages(path, 0)
	if err != nil {
		return nil, err
	}
	texts := make([]string, 0, len(messages))
	for _, message := range messages {
		texts = append(texts, message.Body)
	}
	return texts, nil
}

func appendTextMessage(messages []session.Message, role, body string, timestamp time.Time) []session.Message {
	message, ok := textMessage(role, body, timestamp)
	if !ok {
		return messages
	}
	return append(messages, message)
}

func textMessage(role, body string, timestamp time.Time) (session.Message, bool) {
	if (role != "user" && role != "assistant") || strings.TrimSpace(body) == "" {
		return session.Message{}, false
	}
	return session.Message{Role: role, Timestamp: timestamp, Body: body}, true
}

func fileMessagesMatch(path string, match func(string) bool) (bool, error) {
	return grep.FileContains(path, match, messageTexts)
}

func appendMessage(messages []session.Message, message session.Message, total, limit int) []session.Message {
	if limit <= 0 {
		return append(messages, message)
	}
	if len(messages) < limit {
		return append(messages, message)
	}
	messages[total%limit] = message
	return messages
}

func collectMessages(messages []session.Message, total, limit int) []session.Message {
	if limit <= 0 || total <= limit || len(messages) == 0 {
		return messages
	}
	out := make([]session.Message, 0, len(messages))
	start := total % limit
	for i := range len(messages) {
		out = append(out, messages[(start+i)%limit])
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

func blockText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var signed struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(raw, &signed)
	return signed.Text
}

func newSession(id, cwd, label string, last time.Time, projectDir, messagesPath string) *session.Session {
	if id == "" || idCorruptsRow(id) {
		return nil
	}
	label = session.SanitizeLabel(label)
	if label == "" {
		label = "(no summary)"
	}
	sess := &session.Session{
		ID:         id,
		ProjectDir: projectDir,
		JSONLPath:  messagesPath,
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
	return sess
}

func readJSON(path string, dst any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(f).Decode(dst)
}

func classicDBPath(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "kiro-cli", "data.sqlite3")
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return filepath.Join(local, "kiro-cli", "data.sqlite3")
		}
		return filepath.Join(home, "AppData", "Local", "kiro-cli", "data.sqlite3")
	default:
		if data := os.Getenv("XDG_DATA_HOME"); data != "" {
			return filepath.Join(data, "kiro-cli", "data.sqlite3")
		}
		return filepath.Join(home, ".local", "share", "kiro-cli", "data.sqlite3")
	}
}

func firstTime(values ...string) time.Time {
	for _, value := range values {
		if t := timefmt.Parse(value); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

func firstString(groups ...[]string) string {
	for _, group := range groups {
		for _, value := range group {
			if value != "" {
				return value
			}
		}
	}
	return ""
}

func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
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

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func pathIsDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

func isClassicPath(path string) bool {
	return strings.HasPrefix(path, classicPrefix)
}

// IsV3Path reports whether path belongs to Kiro's v3 session store.
func IsV3Path(path string) bool {
	return filepath.Base(path) == "messages.jsonl" &&
		filepath.Base(filepath.Dir(filepath.Dir(path))) == "_global"
}

func fileModTime(path string) time.Time {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

func canPrefilterClassic(query string) bool {
	for _, r := range query {
		if r < 0x20 || r == '"' || r == '\\' ||
			(r > unicode.MaxASCII && unicode.ToLower(r) != unicode.ToUpper(r)) {
			return false
		}
	}
	return true
}

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
