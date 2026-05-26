package session

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// ProjectsDir returns the directory where Claude Code stores transcripts.
func ProjectsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// Scan walks every project directory under ~/.claude/projects and parses
// each session JSONL in parallel. Sessions returned are sorted by last
// activity (newest first).
func Scan() ([]*Session, error) {
	base, err := ProjectsDir()
	if err != nil {
		return nil, err
	}
	paths, err := collectJSONLPaths(base)
	if err != nil {
		return nil, err
	}
	return scanPaths(paths), nil
}

// ScanFiltered behaves like Scan but only parses files whose path is in
// the allow-set. Empty allow returns nothing.
func ScanFiltered(allow map[string]struct{}) ([]*Session, error) {
	base, err := ProjectsDir()
	if err != nil {
		return nil, err
	}
	paths, err := collectJSONLPaths(base)
	if err != nil {
		return nil, err
	}
	if allow == nil {
		return scanPaths(paths), nil
	}
	filtered := paths[:0]
	for _, p := range paths {
		if _, ok := allow[p]; ok {
			filtered = append(filtered, p)
		}
	}
	return scanPaths(filtered), nil
}

func collectJSONLPaths(base string) ([]string, error) {
	projDirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var paths []string
	for _, pd := range projDirs {
		if !pd.IsDir() {
			continue
		}
		dir := filepath.Join(base, pd.Name())
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			name := f.Name()
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			if strings.HasPrefix(name, "agent-") {
				continue
			}
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	return paths, nil
}

func scanPaths(paths []string) []*Session {
	if len(paths) == 0 {
		return nil
	}
	sessions := make([]*Session, len(paths))
	concurrency := max(runtime.NumCPU()*2, 4)
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p string) {
			defer wg.Done()
			defer func() { <-sem }()
			s, err := ParseSessionTail(p, TailReadBytes)
			if err != nil || s == nil {
				return
			}
			sessions[i] = s
		}(i, p)
	}
	wg.Wait()

	out := make([]*Session, 0, len(sessions))
	for _, s := range sessions {
		if s != nil {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastEpoch > out[j].LastEpoch
	})
	return out
}

// FindByID scans every project directory looking for a session whose
// filename matches the given UUID. Returns the parsed session or nil.
func FindByID(id string) (*Session, error) {
	base, err := ProjectsDir()
	if err != nil {
		return nil, err
	}
	projDirs, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	want := id + ".jsonl"
	for _, pd := range projDirs {
		if !pd.IsDir() {
			continue
		}
		candidate := filepath.Join(base, pd.Name(), want)
		if _, err := os.Stat(candidate); err == nil {
			return ParseSessionTail(candidate, TailReadBytes*4)
		}
	}
	return nil, nil
}
