package grep

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/sorafujitani/ccsession/internal/session"
)

// Options controls Filter behavior.
type Options struct {
	// Regex turns the query into a case-insensitive regular expression.
	// When false (default), the query is matched as a case-insensitive
	// fixed string, which means regex metacharacters in the query are
	// treated as literals.
	Regex bool
}

// Filter scans every session JSONL under ~/.claude/projects and returns
// the set of paths whose user/assistant content matches the query.
// agent-*.jsonl files are excluded.
//
// Empty / whitespace-only query returns (nil, nil) to signal "no filtering".
func Filter(query string, opts Options) (map[string]struct{}, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}

	match, err := buildMatcher(query, opts)
	if err != nil {
		return nil, err
	}

	base, err := session.ProjectsDir()
	if err != nil {
		return nil, err
	}
	paths, err := session.CollectJSONLPaths(base)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return map[string]struct{}{}, nil
	}

	set := make(map[string]struct{})
	var mu sync.Mutex
	concurrency := runtime.NumCPU() * 2
	if concurrency < 4 {
		concurrency = 4
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, p := range paths {
		wg.Add(1)
		sem <- struct{}{}
		go func(p string) {
			defer wg.Done()
			defer func() { <-sem }()
			var hit bool
			_ = session.IterContent(p, func(text string) bool {
				if match(text) {
					hit = true
					return false
				}
				return true
			})
			if hit {
				mu.Lock()
				set[p] = struct{}{}
				mu.Unlock()
			}
		}(p)
	}
	wg.Wait()
	return set, nil
}

func buildMatcher(query string, opts Options) (func(string) bool, error) {
	if opts.Regex {
		re, err := regexp.Compile("(?i)" + query)
		if err != nil {
			return nil, fmt.Errorf("invalid regex: %w", err)
		}
		return re.MatchString, nil
	}
	needle := strings.ToLower(query)
	return func(text string) bool {
		return strings.Contains(strings.ToLower(text), needle)
	}, nil
}
