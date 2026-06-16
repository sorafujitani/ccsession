package resume

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/sorafujitani/ccsession/internal/session"
	"github.com/sorafujitani/ccsession/internal/source"
)

type Options struct {
	Locator string
	Out     io.Writer
}

type Spec struct {
	Source     string   `json:"source"`
	ID         string   `json:"id"`
	Locator    string   `json:"locator"`
	CWD        string   `json:"cwd"`
	CWDExists  bool     `json:"cwd_exists"`
	CWDUnknown bool     `json:"cwd_unknown"`
	Bin        string   `json:"bin"`
	Args       []string `json:"args"`
}

// Run resolves the original cwd for the given session id, chdirs into it,
// and execs the source's resume command to fully replace the current process.
func Run(id string, opts Options) error {
	src, s, err := resolve(id, opts)
	if err != nil {
		return err
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

// RunSpec prints the resume target without checking PATH, chdiring, or execing.
func RunSpec(id string, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	src, s, err := resolve(id, opts)
	if err != nil {
		return err
	}
	bin, args, err := src.ResumeSpec(s)
	if err != nil {
		return err
	}
	locator := opts.Locator
	if locator == "" {
		locator = source.LocatorFor(s)
	}
	return json.NewEncoder(opts.Out).Encode(Spec{
		Source:     s.Source,
		ID:         id,
		Locator:    locator,
		CWD:        s.CWD,
		CWDExists:  s.CWDExists,
		CWDUnknown: s.CWDUnknown,
		Bin:        bin,
		Args:       args,
	})
}

func resolve(id string, opts Options) (source.Source, *session.Session, error) {
	src, err := source.FromEnv()
	if err != nil {
		return nil, nil, err
	}
	s, err := source.ResolveSession(src, id, opts.Locator)
	if err != nil {
		if errors.Is(err, session.ErrSessionFileMissing) {
			if strings.HasPrefix(id, "ses_") && src.Name() != "opencode" {
				return nil, nil, fmt.Errorf("session not found: %s "+
					"(this looks like an OpenCode id — did you mean --source=opencode?)", id)
			}
			return nil, nil, fmt.Errorf("session not found: %s", id)
		}
		if errors.Is(err, session.ErrSessionEmpty) {
			return nil, nil, fmt.Errorf("session has no usable content: %s", id)
		}
		return nil, nil, err
	}
	if s == nil {
		return nil, nil, fmt.Errorf("session not found: %s", id)
	}
	if s.CWDUnknown || s.CWD == "" {
		return nil, nil, fmt.Errorf("cwd unknown for session %s; refusing to resume "+
			"(the JSONL had no cwd and the directory-name fallback could "+
			"not be reconciled with the filesystem)", id)
	}
	if !s.CWDExists {
		return nil, nil, fmt.Errorf("original cwd is gone: %s", s.CWD)
	}
	return src, s, nil
}
