// Package omp reads Oh My Pi sessions through the Pi-compatible JSONL store.
package omp

import (
	"os"
	"path/filepath"
	"time"

	"github.com/sorafujitani/ccsession/internal/pi"
	"github.com/sorafujitani/ccsession/internal/session"
)

// EnvAgentDir is Oh My Pi's override for its agent root.
const EnvAgentDir = "PI_CODING_AGENT_DIR"

// EnvConfigDir changes the config root directory under the user's home. Oh My
// Pi derives its agent root from this value when EnvAgentDir is unset.
const EnvConfigDir = "PI_CONFIG_DIR"

type Store struct {
	store *pi.Store
}

func Open() (*Store, error) {
	dir, err := ResolveSessionsDir()
	if err != nil {
		return nil, err
	}
	return OpenAt(dir), nil
}

func OpenAt(sessionsDir string) *Store {
	return &Store{store: pi.OpenAt(sessionsDir)}
}

func ResolveSessionsDir() (string, error) {
	if dir := os.Getenv(EnvAgentDir); dir != "" {
		root, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configDir := os.Getenv(EnvConfigDir)
	if configDir == "" {
		configDir = ".omp"
	}
	return filepath.Join(home, configDir, "agent", "sessions"), nil
}

func (s *Store) Scan() ([]*session.Session, error) {
	return s.store.Scan()
}

func (s *Store) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	return s.store.ScanFiltered(allow)
}

func (s *Store) FindByID(id string) (*session.Session, error) {
	return s.store.FindByID(id)
}

func (s *Store) FindByLocator(id, path string) (*session.Session, error) {
	return s.store.FindByLocator(id, path)
}

func (s *Store) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	return s.store.GrepKeys(query, regex)
}

func (s *Store) MessagesForSession(sess *session.Session, limit int) ([]session.Message, time.Time, int, error) {
	return s.store.MessagesForSession(sess, limit)
}
