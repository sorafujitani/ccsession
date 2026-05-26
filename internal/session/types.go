package session

import (
	"encoding/json"
	"time"
)

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
