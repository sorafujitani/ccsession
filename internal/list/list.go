package list

import (
	"bufio"
	"fmt"
	"io"
	"os"
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
	NoColor bool
	Out     io.Writer
}

// Run scans sessions, optionally filters them by grep query, and writes
// TSV rows to opts.Out.
func Run(opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	sessions, err := loadSessions(opts.Grep)
	if err != nil {
		return err
	}
	now := time.Now()
	w := bufio.NewWriter(opts.Out)
	defer w.Flush()
	for _, s := range sessions {
		if _, err := fmt.Fprintln(w, formatLine(s, now, !opts.NoColor)); err != nil {
			return err
		}
	}
	return nil
}

func loadSessions(query string) ([]*session.Session, error) {
	if query == "" {
		return session.Scan()
	}
	allow, err := grep.Filter(query)
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
	marker := ""
	if !s.CWDExists {
		marker = "[gone] "
	}
	if color {
		rel = ansiDim + padRight(rel, 9) + ansiReset
		base = ansiCyan + base + ansiReset
		if marker != "" {
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
