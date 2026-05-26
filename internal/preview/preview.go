package preview

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/timefmt"
)

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
)

const (
	maxMessages = 30
	maxBodyLen  = 200
)

type previewEntry struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Message   *previewMessage `json:"message"`
}

type previewMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type messageItem struct {
	Role      string
	Timestamp time.Time
	Body      string
}

// Run writes the preview pane content for a given session id to stdout.
func Run(id string) error {
	s, err := session.FindByID(id)
	if err != nil {
		if errors.Is(err, session.ErrSessionFileMissing) {
			return fmt.Errorf("session not found: %s", id)
		}
		if errors.Is(err, session.ErrSessionEmpty) {
			return fmt.Errorf("session has no usable content: %s", id)
		}
		return err
	}
	if s == nil {
		return fmt.Errorf("session not found: %s", id)
	}
	return render(s, os.Stdout)
}

func render(s *session.Session, out io.Writer) error {
	messages, startedAt, totalMsgs, err := loadMessages(s.JSONLPath)
	if err != nil {
		return err
	}
	if startedAt.IsZero() && !s.LastTime.IsZero() {
		startedAt = s.LastTime
	}

	now := time.Now()
	w := bufio.NewWriter(out)
	defer w.Flush()

	fmt.Fprintf(w, "%ssession%s : %s\n", ansiBold, ansiReset, s.ID)
	cwd := s.CWD
	if cwd == "" {
		cwd = "(unknown)"
	}
	if !s.CWDExists {
		cwd = ansiYellow + cwd + " [gone]" + ansiReset
	} else {
		cwd = ansiCyan + cwd + ansiReset
	}
	fmt.Fprintf(w, "%sproject%s : %s\n", ansiBold, ansiReset, cwd)
	fmt.Fprintf(w, "%sstarted%s : %s  %s(%s)%s\n",
		ansiBold, ansiReset,
		startedAt.Local().Format("2006-01-02 15:04"),
		ansiDim, relativeOrFuture(startedAt, now), ansiReset,
	)
	fmt.Fprintf(w, "%slast%s    : %s  %s(%d msgs)%s\n",
		ansiBold, ansiReset,
		s.LastTime.Local().Format("2006-01-02 15:04"),
		ansiDim, totalMsgs, ansiReset,
	)
	fmt.Fprintln(w, ansiDim+strings.Repeat("─", 60)+ansiReset)

	tail := messages
	if len(tail) > maxMessages {
		tail = tail[len(tail)-maxMessages:]
	}
	for _, m := range tail {
		writeMessage(w, m)
	}
	return nil
}

func writeMessage(w io.Writer, m messageItem) {
	role := m.Role
	color := ansiGreen
	if role == "assistant" {
		role = "asst"
		color = ansiCyan
	}
	// A zero time renders as "00:00" by default, which is indistinguishable
	// from an actual midnight-UTC message; render it as "--:--" instead.
	stamp := "--:--"
	if !m.Timestamp.IsZero() {
		stamp = m.Timestamp.Local().Format("15:04")
	}
	body := truncateBody(m.Body)
	fmt.Fprintf(w, "%s[%s %s]%s %s\n", color, role, stamp, ansiReset, body)
}

func truncateBody(s string) string {
	s = strings.ReplaceAll(s, "\r", "")
	// Keep at most the first two non-empty lines, joined with " | ".
	lines := strings.Split(s, "\n")
	var kept []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		kept = append(kept, l)
		if len(kept) >= 2 {
			break
		}
	}
	joined := strings.Join(kept, " | ")
	runes := []rune(joined)
	if len(runes) > maxBodyLen {
		joined = string(runes[:maxBodyLen-1]) + "…"
	}
	return joined
}

// previewLineCap bounds the number of bytes we'll read for a single JSONL
// line before giving up on it. bufio.Scanner used to cap at 4 MiB and then
// fail the entire load when a single tool-output line went over; switching
// to bufio.Reader + ReadString lets us skip the oversize line and continue.
const previewLineCap = 16 * 1024 * 1024

func loadMessages(path string) ([]messageItem, time.Time, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)

	var (
		items     []messageItem
		startedAt time.Time
		total     int
	)
	for {
		line, err := readJSONLLine(r, previewLineCap)
		if line != "" {
			if item, ts, ok := parseMessageLine(line); ok {
				if startedAt.IsZero() && !ts.IsZero() {
					startedAt = ts
				}
				items = append(items, item)
				total++
			}
		}
		if err == io.EOF {
			return items, startedAt, total, nil
		}
		if err != nil {
			return items, startedAt, total, err
		}
	}
}

// readJSONLLine returns one logical line from r. A line longer than max
// bytes is discarded entirely (the rest of the line is consumed silently)
// rather than aborting the whole scan.
func readJSONLLine(r *bufio.Reader, max int) (string, error) {
	var (
		buf       strings.Builder
		truncated bool
	)
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 {
			if !truncated {
				if buf.Len()+len(chunk) > max {
					truncated = true
				} else {
					buf.Write(chunk)
				}
			}
		}
		if err == bufio.ErrBufferFull {
			// More of the same line remains in the reader.
			truncated = true
			continue
		}
		if truncated {
			return "", err
		}
		return strings.TrimSpace(buf.String()), err
	}
}

func parseMessageLine(line string) (messageItem, time.Time, bool) {
	var e previewEntry
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return messageItem{}, time.Time{}, false
	}
	if e.Type != "user" && e.Type != "assistant" {
		return messageItem{}, time.Time{}, false
	}
	if e.Message == nil {
		return messageItem{}, time.Time{}, false
	}
	body := extractText(e.Message.Content)
	if body == "" {
		return messageItem{}, time.Time{}, false
	}
	ts := parseTime(e.Timestamp)
	return messageItem{Role: e.Type, Timestamp: ts, Body: body}, ts, true
}

// relativeOrFuture wraps timefmt.Relative but renders future timestamps
// honestly. Relative clamps the future to "just now", which is fine in the
// list view (one column, glance-time) but produces a contradictory header
// in the preview ("started 2099-01-01 00:00 (just now)").
func relativeOrFuture(t, now time.Time) string {
	if !t.IsZero() && t.After(now) {
		return "in the future"
	}
	return timefmt.Relative(t, now)
}

func parseTime(raw string) time.Time {
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type != "text" || block.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(block.Text)
		}
		return b.String()
	}
	return ""
}
