// Package source abstracts where ccsession reads sessions from, so a second
// backend can be added without changing the list/preview/resume wiring.
package source

import (
	"fmt"
	"os"

	"github.com/sorafujitani/ccsession/internal/session"
)

// EnvVar selects the backend and is propagated to the fzf subprocesses.
const EnvVar = "CCSESSION_SOURCE"

type Source interface {
	Name() string
	Scan() ([]*session.Session, error)
	ScanFiltered(allow map[string]struct{}) ([]*session.Session, error)
	FindByID(id string) (*session.Session, error)
	GrepKeys(query string, regex bool) (map[string]struct{}, error)
	ResumeSpec(s *session.Session) (bin string, args []string, err error)
}

func FromEnv() (Source, error) {
	return forName(os.Getenv(EnvVar))
}

func forName(name string) (Source, error) {
	switch name {
	case "", "claude":
		return claudeSource{}, nil
	default:
		return nil, fmt.Errorf("unknown source %q (valid: claude)", name)
	}
}
