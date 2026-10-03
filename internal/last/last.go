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
		sessions = session.FilterOutByDir(sessions, needle)
		if len(sessions) == 0 {
			return "", "", fmt.Errorf("no sessions found")
		}
	}

	head := sessions[0]

	in := sessions
	out := in[:0]
	for _, s := range in {
		if s.CWD == "" || !s.CWDExists {
			continue
		}
		out = append(out, s)
	}
	sessions = out
	if len(sessions) == 0 {
		return "", "", fmt.Errorf("no sessions found")
	}
	if head.CWD == "" || !head.CWDExists {
		fmt.Fprintf(os.Stderr, "skipping most recent session %s: cwd %q missing\n", head.ID, head.CWD)
	}

	latest := sessions[0]
	locator := source.LocatorFor(latest)
	return latest.ID, locator, nil
}

func isUnderOrEqual(base, target string) bool {
	if base == "" || target == "" {
		return false
	}
	// Resolve the original paths before filepath.Abs cleans link/.. components.
	if bEval, err := filepath.EvalSymlinks(base); err == nil {
		base = bEval
	}
	if tEval, err := filepath.EvalSymlinks(target); err == nil {
		target = tEval
	}
	bAbs, err := filepath.Abs(base)
	if err != nil {
		return false
	}
	tAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(bAbs, tAbs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	prefix := ".." + string(os.PathSeparator)
	return rel != ".." && !strings.HasPrefix(rel, prefix)
}
