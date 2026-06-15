package resume

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/source"
)

type Options struct {
	Locator string
}

// Run resolves the original cwd for the given session id, chdirs into it,
// and execs the source's resume command to fully replace the current process.
func Run(id string, opts Options) error {
	src, err := source.FromEnv()
	if err != nil {
		return err
	}
	s, err := source.ResolveSession(src, id, opts.Locator)
	if err != nil {
		if errors.Is(err, session.ErrSessionFileMissing) {
			if strings.HasPrefix(id, "ses_") && src.Name() != "opencode" {
				return fmt.Errorf("session not found: %s "+
					"(this looks like an OpenCode id — did you mean --source=opencode?)", id)
			}
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
	if s.CWDUnknown || s.CWD == "" {
		return fmt.Errorf("cwd unknown for session %s; refusing to resume "+
			"(the JSONL had no cwd and the directory-name fallback could "+
			"not be reconciled with the filesystem)", id)
	}
	if !s.CWDExists {
		return fmt.Errorf("original cwd is gone: %s", s.CWD)
	}
	bin, args, err := src.ResumeSpec(s)
	if err != nil {
		return err
	}
	binPath, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("%s not found in PATH: %w", bin, err)
	}
	if err := os.Chdir(s.CWD); err != nil {
		return fmt.Errorf("chdir to %s: %w", s.CWD, err)
	}
	return syscall.Exec(binPath, args, os.Environ())
}
