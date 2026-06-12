package source

import (
	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

type claudeSource struct{}

func (claudeSource) Name() string { return "claude" }

func (claudeSource) Scan() ([]*session.Session, error) {
	ss, err := session.Scan()
	return stamp(ss), err
}

func (claudeSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	ss, err := session.ScanFiltered(allow)
	return stamp(ss), err
}

func (claudeSource) FindByID(id string) (*session.Session, error) {
	s, err := session.FindByID(id)
	if s != nil {
		s.Source = "claude"
	}
	return s, err
}

func (claudeSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return grep.Filter(query, grep.Options{Regex: regex})
}

func (claudeSource) ResumeSpec(s *session.Session) (string, []string, error) {
	return "claude", []string{"claude", "--resume", s.ID}, nil
}

func stamp(ss []*session.Session) []*session.Session {
	for _, s := range ss {
		s.Source = "claude"
	}
	return ss
}
