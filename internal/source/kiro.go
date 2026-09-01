package source

import (
	"time"

	"github.com/sorafujitani/ccsession/internal/kiro"
	"github.com/sorafujitani/ccsession/internal/session"
)

const nameKiro = "kiro"

type kiroSource struct{ store *kiro.Store }

func newKiroSource() (Source, error) {
	store, err := kiro.Open()
	if err != nil {
		return nil, err
	}
	return kiroSource{store: store}, nil
}

func (k kiroSource) Name() string { return nameKiro }

func (k kiroSource) Scan() ([]*session.Session, error) {
	ss, err := k.store.Scan()
	return stamp(ss, nameKiro), err
}

func (k kiroSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	ss, err := k.store.ScanFiltered(allow)
	return stamp(ss, nameKiro), err
}

func (k kiroSource) FindByID(id string) (*session.Session, error) {
	s, err := k.store.FindByID(id)
	if s != nil {
		s.Source = nameKiro
	}
	return s, err
}

func (k kiroSource) FindByLocator(id, locator string) (*session.Session, error) {
	path, ok := decodeLocator(locator)
	if !ok {
		return nil, session.ErrSessionFileMissing
	}
	s, err := k.store.FindByLocator(id, path)
	if s != nil {
		s.Source = nameKiro
	}
	return s, err
}

func (k kiroSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return k.store.GrepKeys(query, regex)
}

func (k kiroSource) ResumeSpec(s *session.Session) (string, []string, error) {
	args := []string{kiro.Binary, "chat"}
	if kiro.IsV3Path(s.JSONLPath) {
		args = append(args, "--v3")
	}
	return kiro.Binary, append(args, "--resume-id", s.ID), nil
}

func (k kiroSource) Messages(s *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	return k.store.MessagesForSession(s, limit)
}
