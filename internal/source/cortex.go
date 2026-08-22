package source

import (
	"time"

	"github.com/sorafujitani/ccsession/internal/cortex"
	"github.com/sorafujitani/ccsession/internal/session"
)

const nameCortex = "cortex"

type cortexSource struct{ store *cortex.Store }

func newCortexSource() (Source, error) {
	store, err := cortex.Open()
	if err != nil {
		return nil, err
	}
	return cortexSource{store: store}, nil
}

func (c cortexSource) Name() string { return nameCortex }

func (c cortexSource) Scan() ([]*session.Session, error) {
	ss, err := c.store.Scan()
	return stamp(ss, nameCortex), err
}

func (c cortexSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	ss, err := c.store.ScanFiltered(allow)
	return stamp(ss, nameCortex), err
}

func (c cortexSource) FindByID(id string) (*session.Session, error) {
	s, err := c.store.FindByID(id)
	if s != nil {
		s.Source = nameCortex
	}
	return s, err
}

func (c cortexSource) FindByLocator(id, locator string) (*session.Session, error) {
	path, ok := decodeLocator(locator)
	if !ok {
		return nil, session.ErrSessionFileMissing
	}
	s, err := c.store.FindByLocator(id, path)
	if s != nil {
		s.Source = nameCortex
	}
	return s, err
}

func (c cortexSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return c.store.GrepKeys(query, regex)
}

func (c cortexSource) ResumeSpec(s *session.Session) (string, []string, error) {
	args := []string{"cortex", "resume", s.ID}
	if s.ConnectionName != "" {
		args = append(args, "-c", s.ConnectionName)
	}
	return "cortex", args, nil
}

func (c cortexSource) Messages(s *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	return c.store.MessagesForSession(s, limit)
}
