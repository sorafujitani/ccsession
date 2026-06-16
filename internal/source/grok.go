package source

import (
	"time"

	"github.com/sorafujitani/ccsession/internal/grok"
	"github.com/sorafujitani/ccsession/internal/session"
)

const nameGrok = "grok"

type grokSource struct{ store *grok.Store }

func newGrokSource() (Source, error) {
	store, err := grok.Open()
	if err != nil {
		return nil, err
	}
	return grokSource{store: store}, nil
}

func (g grokSource) Name() string { return nameGrok }

func (g grokSource) Scan() ([]*session.Session, error) {
	ss, err := g.store.Scan()
	return stamp(ss, nameGrok), err
}

func (g grokSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	ss, err := g.store.ScanFiltered(allow)
	return stamp(ss, nameGrok), err
}

func (g grokSource) FindByID(id string) (*session.Session, error) {
	s, err := g.store.FindByID(id)
	if s != nil {
		s.Source = nameGrok
	}
	return s, err
}

func (g grokSource) FindByLocator(id, locator string) (*session.Session, error) {
	path, ok := decodeLocator(locator)
	if !ok {
		return nil, session.ErrSessionFileMissing
	}
	s, err := g.store.FindByLocator(id, path)
	if s != nil {
		s.Source = nameGrok
	}
	return s, err
}

func (g grokSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return g.store.GrepKeys(query, regex)
}

func (g grokSource) ResumeSpec(s *session.Session) (string, []string, error) {
	return nameGrok, []string{nameGrok, "--resume", s.ID}, nil
}

func (g grokSource) Messages(s *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	return g.store.MessagesForSession(s, limit)
}
