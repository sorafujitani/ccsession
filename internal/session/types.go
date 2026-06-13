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
	// CWDUnknown is true when the JSONL had no cwd field and the
	// directory-name fallback could not be reconciled with the filesystem.
	// Callers (resume) refuse to act on these sessions.
	CWDUnknown bool
	// Source is the backend that produced this session ("claude"), stamped by
	// the source package; the session package itself leaves it empty.
	Source string
}

// Message is one rendered transcript turn, the unit the preview pane displays.
// Sources that don't store a JSONL transcript (OpenCode) produce these directly.
type Message struct {
	Role      string
	Timestamp time.Time
	Body      string
}

// entry is the union shape of every JSONL line in a Claude transcript.
type entry struct {
	Type       string        `json:"type"`
	AITitle    string        `json:"aiTitle"`
	LastPrompt string        `json:"lastPrompt"`
	Timestamp  string        `json:"timestamp"`
	CWD        string        `json:"cwd"`
	Message    *entryMessage `json:"message"`
}

type entryMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
