package filescan

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStamp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	stat := func() os.FileInfo {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		return info
	}
	before := stat()
	if Stamp(before) == "" {
		t.Skip("no file stamps on this platform")
	}
	if Settled(before, time.Now()) {
		t.Fatal("a file changed just now is settled")
	}
	if !Settled(before, time.Now().Add(racyWindow+time.Second)) {
		t.Fatal("an old change is not settled")
	}
	if _, err := os.ReadFile(path); err != nil || Stamp(stat()) != Stamp(before) {
		t.Fatal("reading changed the stamp")
	}
	// Same size and restored mtime: only ctime tells the versions apart.
	if err := os.WriteFile(path, []byte("xyz"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if Stamp(stat()) == Stamp(before) {
		t.Skip("ctime did not advance within this filesystem's resolution")
	}
}
