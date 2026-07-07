package source

import (
	"time"

	"github.com/sorafujitani/ccsession/internal/pi"
	"github.com/sorafujitani/ccsession/internal/session"
)

const namePi = "pi"

type piSource struct{ store *pi.Store }

func newPiSource() (Source, error) {
	store, err := pi.Open()
	if err != nil {
		return nil, err
	}
	return piSource{store: store}, nil
}

func (p piSource) Name() string { return namePi }

func (p piSource) Scan() ([]*session.Session, error) {
	ss, err := p.store.Scan()
	return stamp(ss, namePi), err
}

func (p piSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	ss, err := p.store.ScanFiltered(allow)
	return stamp(ss, namePi), err
}

func (p piSource) FindByID(id string) (*session.Session, error) {
	s, err := p.store.FindByID(id)
	if s != nil {
		s.Source = namePi
	}
	return s, err
}

func (p piSource) FindByLocator(id, locator string) (*session.Session, error) {
	path, ok := decodeLocator(locator)
	if !ok {
		return nil, session.ErrSessionFileMissing
	}
	s, err := p.store.FindByLocator(id, path)
	if s != nil {
		s.Source = namePi
	}
	return s, err
}

func (p piSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return p.store.GrepKeys(query, regex)
}

func (p piSource) ResumeSpec(s *session.Session) (string, []string, error) {
	// The JSONL path is the unambiguous resume target; the id also works but
	// only the path pins the exact file.
	target := s.JSONLPath
	if target == "" {
		target = s.ID
	}
	return namePi, []string{namePi, "--session", target}, nil
}

func (p piSource) Messages(s *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	return p.store.MessagesForSession(s, limit)
}
