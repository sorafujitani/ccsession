package preview

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/ansi"
	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/source"
	"github.com/sorafujitani/ccsession/internal/timefmt"
)

// Options controls preview rendering. Query, when non-empty, highlights its
// matches in the message bodies; Regex treats Query as a regular expression
// (mirroring grep.Options).
type Options struct {
	Query   string
	Regex   bool
	Locator string
	JSON    bool
	Out     io.Writer
}

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

type JSONPreview struct {
	Source        string        `json:"source"`
	ID            string        `json:"id"`
	Locator       string        `json:"locator"`
	CWD           string        `json:"cwd"`
	CWDExists     bool          `json:"cwd_exists"`
	CWDUnknown    bool          `json:"cwd_unknown"`
	Label         string        `json:"label"`
	StartedAt     string        `json:"started_at"`
	LastActivity  string        `json:"last_activity"`
	TotalMessages int           `json:"total_messages"`
	Messages      []JSONMessage `json:"messages"`
	LoadError     string        `json:"load_error,omitempty"`
}

type JSONMessage struct {
	Role      string `json:"role"`
	Timestamp string `json:"timestamp"`
	Body      string `json:"body"`
}

// Run writes the preview pane content for a given session id to stdout.
func Run(id string, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	src, err := source.FromEnv()
	if err != nil {
		return err
	}
	s, err := source.ResolveSession(src, id, opts.Locator)
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
	if rs, ok := src.(routeSource); ok {
		if routed, ok := rs.SourceForSession(s); ok {
			src = routed
		}
	}
	if opts.JSON {
		return renderJSONFrom(src, s, opts.Out, opts)
	}
	return renderFrom(src, s, opts.Out, opts)
}

// messageSource is the seam for a backend that has no JSONL transcript: it
// supplies the preview's turns directly. Satisfied structurally by the
// OpenCode source; the claude source omits it and falls back to JSONLPath.
type messageSource interface {
	Messages(s *session.Session, limit int) ([]session.Message, time.Time, int, error)
}

type routeSource interface {
	SourceForSession(s *session.Session) (source.Source, bool)
}

func renderFrom(src source.Source, s *session.Session, out io.Writer, opts Options) error {
	if ms, ok := src.(messageSource); ok {
		msgs, startedAt, total, err := ms.Messages(s, maxMessages)
		return renderWith(s, out, opts, msgs, startedAt, total, err)
	}
	return render(s, out, opts)
}

func renderJSONFrom(src source.Source, s *session.Session, out io.Writer, opts Options) error {
	msgs, startedAt, total, loadErr, err := previewData(src, s)
	if err != nil {
		return err
	}
	return writeJSONPreview(s, out, opts, msgs, startedAt, total, loadErr)
}

func previewData(src source.Source, s *session.Session) ([]session.Message, time.Time, int, error, error) {
	if ms, ok := src.(messageSource); ok {
		msgs, startedAt, total, loadErr := ms.Messages(s, maxMessages)
		return msgs, startedAt, total, loadErr, nil
	}
	msgs, startedAt, total, err := loadMessages(s.JSONLPath)
	return msgs, startedAt, total, nil, err
}

func writeJSONPreview(s *session.Session, out io.Writer, opts Options, messages []session.Message, startedAt time.Time, totalMsgs int, loadErr error) error {
	if startedAt.IsZero() && !s.LastTime.IsZero() {
		startedAt = s.LastTime
	}
	row := JSONPreview{
		Source:        s.Source,
		ID:            s.ID,
		Locator:       opts.Locator,
		CWD:           s.CWD,
		CWDExists:     s.CWDExists,
		CWDUnknown:    s.CWDUnknown,
		Label:         s.Label,
		StartedAt:     formatJSONTime(startedAt),
		LastActivity:  formatJSONTime(s.LastTime),
		TotalMessages: totalMsgs,
		Messages:      jsonMessages(messages),
	}
	if row.Locator == "" {
		row.Locator = source.LocatorFor(s)
	}
	if loadErr != nil {
		row.LoadError = loadErr.Error()
	}
	return json.NewEncoder(out).Encode(row)
}

func jsonMessages(messages []session.Message) []JSONMessage {
	rows := make([]JSONMessage, 0, len(messages))
	for _, m := range messages {
		rows = append(rows, JSONMessage{
			Role:      m.Role,
			Timestamp: formatJSONTime(m.Timestamp),
			Body:      truncateBody(m.Body),
		})
	}
	return rows
}

func formatJSONTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func render(s *session.Session, out io.Writer, opts Options) error {
	messages, startedAt, totalMsgs, err := loadMessages(s.JSONLPath)
	if err != nil {
		return err
	}
	return renderWith(s, out, opts, messages, startedAt, totalMsgs, nil)
}

// renderWith writes the header and message tail. loadErr (non-nil only for a
// message-source backend) degrades the body to a notice while still printing
// the header, since the session itself resolved fine.
func renderWith(s *session.Session, out io.Writer, opts Options, messages []session.Message, startedAt time.Time, totalMsgs int, loadErr error) error {
	if startedAt.IsZero() && !s.LastTime.IsZero() {
		startedAt = s.LastTime
	}

	now := time.Now()
	w := bufio.NewWriter(out)
	defer w.Flush()

	fmt.Fprintf(w, "%ssession%s : %s\n", ansi.Bold, ansi.Reset, s.ID)
	cwd := s.CWD
	if cwd == "" {
		cwd = "(unknown)"
	}
	if !s.CWDExists {
		cwd = ansi.Yellow + cwd + " [gone]" + ansi.Reset
	} else {
		cwd = ansi.Cyan + cwd + ansi.Reset
	}
	fmt.Fprintf(w, "%sproject%s : %s\n", ansi.Bold, ansi.Reset, cwd)
	fmt.Fprintf(w, "%sstarted%s : %s  %s(%s)%s\n",
		ansi.Bold, ansi.Reset,
		startedAt.Local().Format("2006-01-02 15:04"),
		ansi.Dim, relativeOrFuture(startedAt, now), ansi.Reset,
	)
	fmt.Fprintf(w, "%slast%s    : %s  %s(%d msgs)%s\n",
		ansi.Bold, ansi.Reset,
		s.LastTime.Local().Format("2006-01-02 15:04"),
		ansi.Dim, totalMsgs, ansi.Reset,
	)
	fmt.Fprintln(w, ansi.Dim+strings.Repeat("─", 60)+ansi.Reset)

	if loadErr != nil {
		fmt.Fprintf(w, "%s(messages unavailable: %v)%s\n", ansi.Dim, loadErr, ansi.Reset)
		return nil
	}

	tail := messages
	if len(tail) > maxMessages {
		tail = tail[len(tail)-maxMessages:]
	}
	for _, m := range tail {
		writeMessage(w, m, opts)
	}
	return nil
}

func writeMessage(w io.Writer, m session.Message, opts Options) {
	role := m.Role
	color := ansi.Green
	if role == "assistant" {
		role = "asst"
		color = ansi.Cyan
	}
	// A zero time renders as "00:00" by default, which is indistinguishable
	// from an actual midnight-UTC message; render it as "--:--" instead.
	stamp := "--:--"
	if !m.Timestamp.IsZero() {
		stamp = m.Timestamp.Local().Format("15:04")
	}
	body := highlightMatches(truncateBody(m.Body), opts)
	fmt.Fprintf(w, "%s[%s %s]%s %s\n", color, role, stamp, ansi.Reset, body)
}

// highlightMatches wraps every case-insensitive match of opts.Query in s with
// the highlight color. A fixed-string query is escaped so regex metacharacters
// are treated literally; an invalid regex (in Regex mode) leaves s untouched.
func highlightMatches(s string, opts Options) string {
	if strings.TrimSpace(opts.Query) == "" {
		return s
	}
	pattern := opts.Query
	if !opts.Regex {
		pattern = regexp.QuoteMeta(opts.Query)
	}
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return s
	}
	locs := re.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		if loc[0] == loc[1] {
			// Skip zero-width matches (e.g. a query like "a*").
			continue
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(ansi.Highlight)
		b.WriteString(s[loc[0]:loc[1]])
		b.WriteString(ansi.Reset)
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
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
	return session.Truncate(joined, maxBodyLen)
}

// previewLineCap bounds the number of bytes we'll read for a single JSONL
// line before giving up on it. bufio.Scanner used to cap at 4 MiB and then
// fail the entire load when a single tool-output line went over; switching
// to bufio.Reader + ReadString lets us skip the oversize line and continue.
const previewLineCap = 16 * 1024 * 1024

func loadMessages(path string) ([]session.Message, time.Time, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, time.Time{}, 0, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64*1024)

	// render only displays the last maxMessages items, so we keep a ring
	// buffer of that size rather than accumulating every message. startedAt
	// (first message time) and total (overall count) are still derived from
	// the full scan so the header stays accurate.
	var (
		ring      = make([]session.Message, maxMessages)
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
				ring[total%maxMessages] = item
				total++
			}
		}
		if err == io.EOF {
			return collectRing(ring, total), startedAt, total, nil
		}
		if err != nil {
			return collectRing(ring, total), startedAt, total, err
		}
	}
}

// collectRing returns the buffered messages in chronological (ascending)
// order, at most maxMessages of them. total is the overall number of items
// written into the ring; when it exceeds maxMessages the oldest entries have
// been overwritten and the live window starts at total%maxMessages.
func collectRing(ring []session.Message, total int) []session.Message {
	n := min(total, maxMessages)
	if n == 0 {
		return nil
	}
	out := make([]session.Message, 0, n)
	start := 0
	if total > maxMessages {
		start = total % maxMessages
	}
	for i := range n {
		out = append(out, ring[(start+i)%maxMessages])
	}
	return out
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
			continue
		}
		if truncated {
			return "", err
		}
		return strings.TrimSpace(buf.String()), err
	}
}

func parseMessageLine(line string) (session.Message, time.Time, bool) {
	var e previewEntry
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return session.Message{}, time.Time{}, false
	}
	if e.Type != "user" && e.Type != "assistant" {
		return session.Message{}, time.Time{}, false
	}
	if e.Message == nil {
		return session.Message{}, time.Time{}, false
	}
	body := session.ExtractText(e.Message.Content, "\n")
	if body == "" {
		return session.Message{}, time.Time{}, false
	}
	ts := timefmt.Parse(e.Timestamp)
	return session.Message{Role: e.Type, Timestamp: ts, Body: body}, ts, true
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
