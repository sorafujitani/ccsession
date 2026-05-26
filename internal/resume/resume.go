package resume

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/sorafujitani/ccsession/internal/session"
)

// Run resolves the original cwd for the given session id, chdirs into it,
// and execs `claude --resume <id>` to fully replace the current process.
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
	if s.CWDUnknown || s.CWD == "" {
		return fmt.Errorf("cwd unknown for session %s; refusing to resume "+
			"(the JSONL had no cwd and the directory-name fallback could "+
			"not be reconciled with the filesystem)", id)
	}
	if !s.CWDExists {
		return fmt.Errorf("original cwd is gone: %s", s.CWD)
	}
	claudePath, err := exec.LookPath("claude")
	if err != nil {
		return fmt.Errorf("claude not found in PATH: %w", err)
	}
	if err := os.Chdir(s.CWD); err != nil {
		return fmt.Errorf("chdir to %s: %w", s.CWD, err)
	}
	return syscall.Exec(claudePath, []string{"claude", "--resume", id}, os.Environ())
}
