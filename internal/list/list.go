package list

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/timefmt"
)

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiCyan   = "\x1b[36m"
	ansiYellow = "\x1b[33m"
)

// Options controls list output.
type Options struct {
	Grep    string
	Regex   bool
	NoColor bool
	// Color overrides auto-detection: "always", "never", or "" (auto).
	// --no-color and NO_COLOR still force off when Color is empty/auto.
	Color string
	Out   io.Writer
}

// Run scans sessions, optionally filters them by grep query, and writes
// TSV rows to opts.Out.
func Run(opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	sessions, err := loadSessions(opts.Grep, opts.Regex)
	if err != nil {
		return err
	}
	color := colorEnabled(opts)
	now := time.Now()
	w := bufio.NewWriter(opts.Out)
	defer w.Flush()
	for _, s := range sessions {
		if _, err := fmt.Fprintln(w, formatLine(s, now, color)); err != nil {
			return err
		}
	}
	return nil
}

// colorEnabled decides whether to emit ANSI escapes.
//
// Precedence: an explicit --color=always|never wins. Then --no-color and
// the NO_COLOR env var (https://no-color.org/) force off. Otherwise color
// is on only when the output is a terminal — so `ccsession list | cat` no
// longer leaks ANSI codes, while the fzf integration (which explicitly
// passes --color=always) keeps its styling.
func colorEnabled(opts Options) bool {
	switch strings.ToLower(opts.Color) {
	case "always":
		return true
	case "never":
		return false
	}
	if opts.NoColor {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if f, ok := opts.Out.(*os.File); ok {
		return isTerminal(f)
	}
	return false
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func loadSessions(query string, regex bool) ([]*session.Session, error) {
	if query == "" {
		return session.Scan()
	}
	allow, err := grep.Filter(query, grep.Options{Regex: regex})
	if err != nil {
		return nil, err
	}
	if len(allow) == 0 {
		return nil, nil
	}
	return session.ScanFiltered(allow)
}

func formatLine(s *session.Session, now time.Time, color bool) string {
	rel := timefmt.Relative(s.LastTime, now)
	base := s.CWDBasename
	if base == "" {
		base = "(unknown)"
	}
	// Choose a marker. cwd-unknown sessions get [cwd?] so the user can tell
	// "the recorded cwd is gone" (B-9) apart from "we never knew the cwd
	// for this session" (B-10).
	marker := ""
	healthy := true
	switch {
	case s.CWDUnknown:
		marker = "[cwd?] "
		healthy = false
	case !s.CWDExists:
		marker = "[gone] "
		healthy = false
	}
	if color {
		rel = ansiDim + padRight(rel, 9) + ansiReset
		if healthy {
			base = ansiCyan + base + ansiReset
		} else {
			// Yellow on the basename so a glance picks it up even without
			// reading the marker prefix (which used to be the only signal).
			base = ansiYellow + base + ansiReset
			marker = ansiYellow + marker + ansiReset
		}
	} else {
		rel = padRight(rel, 9)
	}
	return fmt.Sprintf("%s\t%d\t%s\t%s\t%s%s",
		s.ID, s.LastEpoch, rel, base, marker, s.Label)
}

func padRight(s string, n int) string {
	for len([]rune(s)) < n {
		s += " "
	}
	return s
}
