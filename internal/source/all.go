package source

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/sorafujitani/ccsession/internal/opencode"
	"github.com/sorafujitani/ccsession/internal/session"
)

type allSource struct {
	sources []Source
}

func newAllSource() (Source, error) {
	sources := []Source{claudeSource{}}
	if src, ok, err := newOptionalOpencodeSource(); err != nil {
		return nil, err
	} else if ok {
		sources = append(sources, src)
	}
	for _, open := range []func() (Source, error){newGrokSource, newCodexSource} {
		src, err := open()
		if err != nil {
			return nil, err
		}
		sources = append(sources, src)
	}
	return allSource{sources: sources}, nil
}

func newOptionalOpencodeSource() (Source, bool, error) {
	_, probed, err := opencode.ResolveDBPath()
	if err != nil {
		if errors.Is(err, opencode.ErrDBNotFound) && os.Getenv(opencode.EnvDBPath) == "" {
			if opencode.HasLegacyStorage(probed) {
				return nil, false, opencode.Preflight()
			}
			return nil, false, nil
		}
		return nil, false, err
	}
	src, err := newOpencodeSource()
	if err != nil {
		return nil, false, err
	}
	return src, true, nil
}

func (a allSource) Name() string { return nameAll }

func (a allSource) Scan() ([]*session.Session, error) {
	var out []*session.Session
	for _, src := range a.sources {
		ss, err := src.Scan()
		if err != nil {
			return nil, fmt.Errorf("%s scan: %w", src.Name(), err)
		}
		out = append(out, keyedSessions(src.Name(), ss)...)
	}
	sortSessions(out)
	return out, nil
}

func (a allSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	if allow == nil {
		return a.Scan()
	}
	grouped := make(map[string]map[string]struct{})
	for key := range allow {
		name, local, ok := splitKey(key)
		if !ok {
			continue
		}
		if grouped[name] == nil {
			grouped[name] = make(map[string]struct{})
		}
		grouped[name][local] = struct{}{}
	}

	var out []*session.Session
	for _, src := range a.sources {
		keys := grouped[src.Name()]
		if len(keys) == 0 {
			continue
		}
		ss, err := src.ScanFiltered(keys)
		if err != nil {
			return nil, fmt.Errorf("%s scan filtered: %w", src.Name(), err)
		}
		out = append(out, keyedSessions(src.Name(), ss)...)
	}
	sortSessions(out)
	return out, nil
}

func (a allSource) FindByID(id string) (*session.Session, error) {
	if name, local, ok := splitKey(id); ok {
		src, ok := a.sourceByName(name)
		if !ok {
			return nil, session.ErrSessionFileMissing
		}
		return src.FindByID(local)
	}
	for _, src := range a.sources {
		s, err := src.FindByID(id)
		if err == nil || !errors.Is(err, session.ErrSessionFileMissing) {
			return s, err
		}
	}
	return nil, session.ErrSessionFileMissing
}

func (a allSource) FindByLocator(id, locator string) (*session.Session, error) {
	name, localLocator, ok := splitKey(locator)
	if !ok {
		return a.FindByID(id)
	}
	src, ok := a.sourceByName(name)
	if !ok {
		return nil, session.ErrSessionFileMissing
	}
	localID := id
	if idName, idLocal, ok := splitKey(id); ok {
		if idName != name {
			return nil, session.ErrSessionFileMissing
		}
		localID = idLocal
	}
	return ResolveSession(src, localID, localLocator)
}

func (a allSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	out := make(map[string]struct{})
	for _, src := range a.sources {
		keys, err := src.GrepKeys(query, regex)
		if err != nil {
			return nil, fmt.Errorf("%s grep: %w", src.Name(), err)
		}
		for key := range keys {
			out[joinKey(src.Name(), key)] = struct{}{}
		}
	}
	return out, nil
}

func (a allSource) ResumeSpec(s *session.Session) (string, []string, error) {
	src, ok := a.SourceForSession(s)
	if !ok {
		return "", nil, fmt.Errorf("unknown source %q for session %s", s.Source, s.ID)
	}
	return src.ResumeSpec(s)
}

func (a allSource) SourceForSession(s *session.Session) (Source, bool) {
	if s == nil {
		return nil, false
	}
	return a.sourceByName(s.Source)
}

func (a allSource) sourceByName(name string) (Source, bool) {
	for _, src := range a.sources {
		if src.Name() == name {
			return src, true
		}
	}
	return nil, false
}

func keyedSessions(name string, ss []*session.Session) []*session.Session {
	out := make([]*session.Session, 0, len(ss))
	for _, s := range ss {
		cp := *s
		if cp.Source == "" {
			cp.Source = name
		}
		cp.ID = joinKey(cp.Source, cp.ID)
		out = append(out, &cp)
	}
	return out
}

func joinKey(name, id string) string {
	return name + ":" + id
}

func splitKey(key string) (name, local string, ok bool) {
	name, local, ok = strings.Cut(key, ":")
	if !ok || name == "" || local == "" {
		return "", "", false
	}
	return name, local, true
}

func sortSessions(ss []*session.Session) {
	nowEpoch := time.Now().Unix()
	sort.SliceStable(ss, func(i, j int) bool {
		ki, kj := sortEpoch(ss[i].LastEpoch, nowEpoch), sortEpoch(ss[j].LastEpoch, nowEpoch)
		if ki != kj {
			return ki > kj
		}
		if ss[i].Source != ss[j].Source {
			return ss[i].Source < ss[j].Source
		}
		return ss[i].ID < ss[j].ID
	})
}

func sortEpoch(epoch, nowEpoch int64) int64 {
	if epoch > nowEpoch {
		return 0
	}
	return epoch
}
