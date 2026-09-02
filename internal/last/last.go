package last

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/source"
)

type Options struct {
	Here       bool
	ExcludeDir string
}

func filterOutByDir(sessions []*session.Session, needle string) []*session.Session {
	lneedle := strings.ToLower(needle)
	// Deliberately reuse the argument slice's backing array for the filtered
	// result; safe because the caller (list.Run) immediately reassigns the
	// return value over the slice it passed in.
	out := sessions[:0]
	for _, s := range sessions {
		target := s.CWD
		if target == "" {
			target = s.CWDBasename
		}
		if target != "" && strings.Contains(strings.ToLower(target), lneedle) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func Run(opts Options) (string, string, error) {
	src, err := source.FromEnv()
	if err != nil {
		return "", "", err
	}
	sessions, err := src.ScanFiltered(nil)
	if err != nil {
		return "", "", err
	}
	if len(sessions) == 0 {
		return "", "", fmt.Errorf("no sessions found")
	}

	in := sessions
	out := in[:0]
	for _, s := range in {
		if s.CWD == "" || !s.CWDExists {
			continue
		}
		out = append(out, s)
	}
	sessions = out

	if opts.Here {
		currentDir, err := os.Getwd()
		if err != nil {
			return "", "", err
		}
		for i := len(sessions) - 1; i >= 0; i-- {
			if !isUnderOrEqual(currentDir, sessions[i].CWD) {
				sessions = append(sessions[:i], sessions[i+1:]...)
			}
		}
		if len(sessions) == 0 {
			return "", "", fmt.Errorf("no sessions found")
		}
	}
	if needle := strings.TrimSpace(opts.ExcludeDir); needle != "" {
		sessions = filterOutByDir(sessions, needle)
	}
	if len(sessions) == 0 {
		return "", "", fmt.Errorf("no sessions found")
	}

	latest := sessions[0]

	locator := source.LocatorFor(latest)
	return latest.ID, locator, nil
}

func isUnderOrEqual(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	prefix := ".." + string(os.PathSeparator)
	return rel != ".." && !strings.HasPrefix(rel, prefix)
}
