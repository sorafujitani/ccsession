package codex

import (
	"os"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/grep"
	"github.com/sorafujitani/ccsession/internal/session"
)

// Keep scan and grep caches out of the user's cache directory, and treat
// fixtures scanned right after they are written as settled.
func TestMain(m *testing.M) {
	now = func() time.Time { return time.Now().Add(time.Hour) }
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
