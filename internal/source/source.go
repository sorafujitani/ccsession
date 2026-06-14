// Package source abstracts where ccsession reads sessions from, so a second
// backend can be added without changing the list/preview/resume wiring.
package source

import (
	"fmt"
	"os"
	"strings"

	"github.com/sorafujitani/ccsession/internal/session"
)

// EnvVar selects the backend and is inherited by the fzf subprocesses via
// os.Environ() (see cmd/ccsession/main.go), so every re-invocation resolves
// the same source.
const EnvVar = "CCSESSION_SOURCE"

// nameClaude is the backend name for the Claude Code source.
const nameClaude = "claude"

const nameAll = "all"

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

// Names lists the valid backend names (excluding the empty default).
func Names() []string {
	return []string{nameAll, nameClaude, nameOpencode, nameGrok, nameCodex}
}

// ValidName reports whether name selects a known backend; the empty string is
// valid and means the claude default.
func ValidName(name string) bool {
	return name == "" || name == nameAll || name == nameClaude || name == nameOpencode || name == nameGrok || name == nameCodex
}

func forName(name string) (Source, error) {
	switch name {
	case "", nameClaude:
		return claudeSource{}, nil
	case nameAll:
		return newAllSource()
	case nameOpencode:
		return newOpencodeSource()
	case nameGrok:
		return newGrokSource()
	case nameCodex:
		return newCodexSource()
	default:
		return nil, fmt.Errorf("unknown source %q (valid: %s)", name, strings.Join(Names(), ", "))
	}
}
