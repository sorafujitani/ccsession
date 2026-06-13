package source

import (
	"os"
	"time"

	"github.com/sorafujitani/ccsession/internal/opencode"
	"github.com/sorafujitani/ccsession/internal/session"
)

const nameOpencode = "opencode"

type opencodeSource struct{ db *opencode.DB }

func newOpencodeSource() (Source, error) {
	db, err := opencode.Open()
	if err != nil {
		return nil, err
	}
	return opencodeSource{db: db}, nil
}

// Preflight validates the selected backend before the TUI launches; errors
// raised inside fzf are invisible. Only opencode has a failure mode worth
// catching early (missing/legacy/unreadable DB); claude is always ready.
func Preflight() error {
	if os.Getenv(EnvVar) == nameOpencode {
		return opencode.Preflight()
	}
	return nil
}

func (opencodeSource) Name() string { return nameOpencode }

func (o opencodeSource) Scan() ([]*session.Session, error) {
	ss, err := o.db.Scan()
	return stamp(ss, nameOpencode), err
}

func (o opencodeSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	ss, err := o.db.ScanFiltered(allow)
	return stamp(ss, nameOpencode), err
}

func (o opencodeSource) FindByID(id string) (*session.Session, error) {
	s, err := o.db.FindByID(id)
	if s != nil {
		s.Source = nameOpencode
	}
	return s, err
}

func (o opencodeSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return o.db.GrepKeys(query, regex)
}

func (o opencodeSource) ResumeSpec(s *session.Session) (string, []string, error) {
	return nameOpencode, []string{nameOpencode, "--session", s.ID}, nil
}

// Messages satisfies preview's message-source seam: OpenCode has no JSONL
// transcript, so the preview reads turns from the DB instead of a file.
func (o opencodeSource) Messages(s *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	return o.db.Messages(s.ID, limit)
}
