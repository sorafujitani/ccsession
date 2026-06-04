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
	"github.com/sorafujitani/ccsession/internal/termcolor"
	"github.com/sorafujitani/ccsession/internal/timefmt"
)

// Options controls preview rendering. Query, when non-empty, highlights its
// matches in the message bodies; Regex treats Query as a regular expression
// (mirroring grep.Options).
type Options struct {
	Query   string
	Regex   bool
	NoColor bool
	// Color overrides auto-detection: "always", "never", or ""/"auto".
	Color string
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

type messageItem struct {
	Role      string
	Timestamp time.Time
	Body      string
}

// Run writes the preview pane content for a given session id to stdout.
func Run(id string, opts Options) error {
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
	return render(s, os.Stdout, opts)
}

func render(s *session.Session, out io.Writer, opts Options) error {
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
	color := termcolor.Enabled(out, opts.Color, opts.NoColor)

	bold := colorCode(color, ansi.Bold)
	dim := colorCode(color, ansi.Dim)
	reset := colorCode(color, ansi.Reset)

	fmt.Fprintf(w, "%ssession%s : %s\n", bold, reset, s.ID)
	cwd := s.CWD
	if cwd == "" {
		cwd = "(unknown)"
	}
	if !s.CWDExists {
		cwd = colorize(color, ansi.Yellow, cwd+" [gone]")
	} else {
		cwd = colorize(color, ansi.Cyan, cwd)
	}
	fmt.Fprintf(w, "%sproject%s : %s\n", bold, reset, cwd)
	fmt.Fprintf(w, "%sstarted%s : %s  %s(%s)%s\n",
		bold, reset,
		startedAt.Local().Format("2006-01-02 15:04"),
		dim, relativeOrFuture(startedAt, now), reset,
	)
	fmt.Fprintf(w, "%slast%s    : %s  %s(%d msgs)%s\n",
		bold, reset,
		s.LastTime.Local().Format("2006-01-02 15:04"),
		dim, totalMsgs, reset,
	)
	fmt.Fprintln(w, colorize(color, ansi.Dim, strings.Repeat("─", 60)))

	tail := messages
	if len(tail) > maxMessages {
		tail = tail[len(tail)-maxMessages:]
	}
	for _, m := range tail {
		writeMessage(w, m, opts, color)
	}
	return nil
}

func writeMessage(w io.Writer, m messageItem, opts Options, colorEnabled bool) {
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
	body := highlightMatches(truncateBody(m.Body), opts, colorEnabled)
	fmt.Fprintf(w, "%s[%s %s]%s %s\n",
		colorCode(colorEnabled, color),
		role,
		stamp,
		colorCode(colorEnabled, ansi.Reset),
		body,
	)
}

// highlightMatches wraps every case-insensitive match of opts.Query in s with
// the highlight color. A fixed-string query is escaped so regex metacharacters
// are treated literally; an invalid regex (in Regex mode) leaves s untouched.
func highlightMatches(s string, opts Options, color bool) string {
	if !color {
		return s
	}
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

func colorCode(enabled bool, code string) string {
	if !enabled {
		return ""
	}
	return code
}

func colorize(enabled bool, code, s string) string {
	if !enabled {
		return s
	}
	return code + s + ansi.Reset
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

func loadMessages(path string) ([]messageItem, time.Time, int, error) {
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
		ring      = make([]messageItem, maxMessages)
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
func collectRing(ring []messageItem, total int) []messageItem {
	n := min(total, maxMessages)
	if n == 0 {
		return nil
	}
	out := make([]messageItem, 0, n)
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
	body := session.ExtractText(e.Message.Content, "\n")
	if body == "" {
		return messageItem{}, time.Time{}, false
	}
	ts := timefmt.Parse(e.Timestamp)
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
