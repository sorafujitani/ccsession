package grep

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/sorafujitani/ccsession/internal/session"
)

// Filter runs ripgrep across all session JSONL files and returns the set
// of file paths whose content matches the (case-insensitive) query.
// The agent-*.jsonl files are excluded via glob.
func Filter(query string) (map[string]struct{}, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	rgPath, err := exec.LookPath("rg")
	if err != nil {
		return nil, fmt.Errorf("ripgrep (rg) is required for --grep but was not found in PATH")
	}
	base, err := session.ProjectsDir()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(rgPath,
		"--files-with-matches",
		"--no-messages",
		"-i",
		"--glob", "*.jsonl",
		"--glob", "!agent-*.jsonl",
		"--", query, base,
	)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// rg exit code 1 = no matches; that is not a real error.
			if exitErr.ExitCode() == 1 {
				return map[string]struct{}{}, nil
			}
			return nil, fmt.Errorf("ripgrep failed (exit %d): %s",
				exitErr.ExitCode(), strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("ripgrep: %w", err)
	}
	set := make(map[string]struct{})
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		set[line] = struct{}{}
	}
	return set, nil
}
