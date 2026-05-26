package session

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrSessionFileMissing is returned by FindByID when no JSONL file matches
// the session id. Callers distinguish this from a file that exists but
// contains no usable content (see ErrSessionEmpty).
var ErrSessionFileMissing = errors.New("session file missing")

// ErrSessionEmpty is returned by ParseSessionTail / FindByID when the file
// exists but has no user message or no extractable label.
var ErrSessionEmpty = errors.New("session has no usable content")

// Session is a parsed view of one Claude Code session transcript.
type Session struct {
	ID          string
	ProjectDir  string
	JSONLPath   string
	CWD         string
	CWDBasename string
	Label       string
	LastTime    time.Time
	LastEpoch   int64
	CWDExists   bool
}

// entry is the union shape of every JSONL line in a Claude transcript.
type entry struct {
	Type       string          `json:"type"`
	AITitle    string          `json:"aiTitle"`
	LastPrompt string          `json:"lastPrompt"`
	Timestamp  string          `json:"timestamp"`
	CWD        string          `json:"cwd"`
	Message    *entryMessage   `json:"message"`
}

type entryMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
