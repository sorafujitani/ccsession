package pi

import (
	"os"
	"testing"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

// Keep scan and grep caches out of the user's cache directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ccsession-test-cache-")
	if err != nil {
		panic(err)
	}
	os.Setenv(session.EnvCacheDir, dir)
	os.Setenv(grep.EnvCacheDir, dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
