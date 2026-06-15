package source

import (
	"time"

	"github.com/sorafujitani/ccsession/internal/codex"
	"github.com/sorafujitani/ccsession/internal/session"
)

const nameCodex = "codex"

type codexSource struct{ store *codex.Store }

func newCodexSource() (Source, error) {
	store, err := codex.Open()
	if err != nil {
		return nil, err
	}
	return codexSource{store: store}, nil
}

func (c codexSource) Name() string { return nameCodex }

func (c codexSource) Scan() ([]*session.Session, error) {
	ss, err := c.store.Scan()
	return stamp(ss, nameCodex), err
}

func (c codexSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	ss, err := c.store.ScanFiltered(allow)
	return stamp(ss, nameCodex), err
}

func (c codexSource) FindByID(id string) (*session.Session, error) {
	s, err := c.store.FindByID(id)
	if s != nil {
		s.Source = nameCodex
	}
	return s, err
}

func (c codexSource) FindByLocator(id, locator string) (*session.Session, error) {
	path, ok := decodeLocator(locator)
	if !ok {
		return nil, session.ErrSessionFileMissing
	}
	s, err := c.store.FindByLocator(id, path)
	if s != nil {
		s.Source = nameCodex
	}
	return s, err
}

func (c codexSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return c.store.GrepKeys(query, regex)
}

func (c codexSource) ResumeSpec(s *session.Session) (string, []string, error) {
	return nameCodex, []string{nameCodex, "resume", s.ID}, nil
}

func (c codexSource) Messages(s *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	return c.store.MessagesForSession(s, limit)
}
