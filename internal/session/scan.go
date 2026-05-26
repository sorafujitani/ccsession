package session

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProjectsDir returns the directory where Claude Code stores transcripts.
// If CLAUDE_CONFIG_DIR is set, it uses that as the base directory;
// otherwise it falls back to ~/.claude.
func ProjectsDir() (string, error) {
	if base := os.Getenv("CLAUDE_CONFIG_DIR"); base != "" {
		return filepath.Join(base, "projects"), nil
	}
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
	paths, err := CollectJSONLPaths(base)
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
	paths, err := CollectJSONLPaths(base)
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

// CollectJSONLPaths walks the projects directory and returns every session
// JSONL path, excluding agent-*.jsonl. Returned nil when projects dir doesn't exist.
func CollectJSONLPaths(base string) ([]string, error) {
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
	nowEpoch := time.Now().Unix()
	sort.Slice(out, func(i, j int) bool {
		ki, kj := sortEpoch(out[i], nowEpoch), sortEpoch(out[j], nowEpoch)
		if ki != kj {
			return ki > kj
		}
		// Deterministic tiebreak so future-clamped entries don't shuffle.
		return out[i].ID < out[j].ID
	})
	return out
}

// sortEpoch returns the timestamp to use for ordering. Future timestamps
// are untrustworthy (typically a clock skew or pasted-in transcript) so
// they sink to the bottom rather than floating to the top.
func sortEpoch(s *Session, nowEpoch int64) int64 {
	if s.LastEpoch > nowEpoch {
		return 0
	}
	return s.LastEpoch
}

// FindByID scans every project directory looking for a session whose
// filename matches the given UUID. Returns ErrSessionFileMissing when no
// JSONL exists for the id, and ErrSessionEmpty when the file is present
// but has no usable content.
func FindByID(id string) (*Session, error) {
	base, err := ProjectsDir()
	if err != nil {
		return nil, err
	}
	projDirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrSessionFileMissing
		}
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
	return nil, ErrSessionFileMissing
}
