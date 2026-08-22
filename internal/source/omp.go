package source

import (
	"time"

	"github.com/sorafujitani/ccsession/internal/omp"
	"github.com/sorafujitani/ccsession/internal/session"
)

const nameOMP = "omp"

type ompSource struct{ store *omp.Store }

func newOMPSource() (Source, error) {
	store, err := omp.Open()
	if err != nil {
		return nil, err
	}
	return ompSource{store: store}, nil
}

func (o ompSource) Name() string { return nameOMP }

func (o ompSource) Scan() ([]*session.Session, error) {
	ss, err := o.store.Scan()
	return stamp(ss, nameOMP), err
}

func (o ompSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	ss, err := o.store.ScanFiltered(allow)
	return stamp(ss, nameOMP), err
}

func (o ompSource) FindByID(id string) (*session.Session, error) {
	s, err := o.store.FindByID(id)
	if s != nil {
		s.Source = nameOMP
	}
	return s, err
}

func (o ompSource) FindByLocator(id, locator string) (*session.Session, error) {
	path, ok := decodeLocator(locator)
	if !ok {
		return nil, session.ErrSessionFileMissing
	}
	s, err := o.store.FindByLocator(id, path)
	if s != nil {
		s.Source = nameOMP
	}
	return s, err
}

func (o ompSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return o.store.GrepKeys(query, regex)
}

func (o ompSource) ResumeSpec(s *session.Session) (string, []string, error) {
	target := s.JSONLPath
	if target == "" {
		target = s.ID
	}
	return nameOMP, []string{nameOMP, "--resume", target}, nil
}

func (o ompSource) Messages(s *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	return o.store.MessagesForSession(s, limit)
}
