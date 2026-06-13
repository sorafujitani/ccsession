package opencode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// envMap builds a getenv func from a fixed map for resolveDBPath's seam.
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolveDBPath_ExplicitEnvWinsAndSkipsDiscovery(t *testing.T) {
	home := t.TempDir()
	mkDB(t, filepath.Join(home, ".local", "share", "opencode", "opencode.db")) // discoverable, ignored
	explicit := mkDB(t, filepath.Join(t.TempDir(), "custom.db"))

	got, probed, err := resolveDBPath(envMap(map[string]string{"HOME": home, EnvDBPath: explicit}), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if got != explicit {
		t.Errorf("path = %q, want %q", got, explicit)
	}
	if len(probed) != 1 || probed[0] != explicit {
		t.Errorf("probed = %v, want only the explicit path (discovery skipped)", probed)
	}
}

func TestResolveDBPath_ExplicitMissingIsError(t *testing.T) {
	_, _, err := resolveDBPath(envMap(map[string]string{EnvDBPath: "/nope/missing.db"}), "linux")
	if !errors.Is(err, ErrDBNotFound) {
		t.Errorf("err = %v, want ErrDBNotFound", err)
	}
}

func TestResolveDBPath_XDGBeatsLocalShare(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	mkDB(t, filepath.Join(home, ".local", "share", "opencode", "opencode.db"))
	xdgDB := mkDB(t, filepath.Join(xdg, "opencode", "opencode.db"))

	got, _, err := resolveDBPath(envMap(map[string]string{"HOME": home, "XDG_DATA_HOME": xdg}), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if got != xdgDB {
		t.Errorf("path = %q, want XDG db %q", got, xdgDB)
	}
}

func TestResolveDBPath_NewestByMtimeAcrossGlob(t *testing.T) {
	home := t.TempDir()
	base := filepath.Join(home, ".local", "share", "opencode")
	old := mkDB(t, filepath.Join(base, "opencode.db"))
	prod := mkDB(t, filepath.Join(base, "opencode-prod.db"))
	// Make prod newer than the default.
	older := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, older, older); err != nil {
		t.Fatal(err)
	}

	got, _, err := resolveDBPath(envMap(map[string]string{"HOME": home}), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if got != prod {
		t.Errorf("path = %q, want newest %q", got, prod)
	}
}

func TestResolveDBPath_DarwinAppSupportCandidate(t *testing.T) {
	home := t.TempDir()
	appSupport := mkDB(t, filepath.Join(home, "Library", "Application Support", "opencode", "opencode.db"))

	got, probed, err := resolveDBPath(envMap(map[string]string{"HOME": home}), "darwin")
	if err != nil {
		t.Fatalf("err = %v, probed = %v", err, probed)
	}
	if got != appSupport {
		t.Errorf("path = %q, want %q", got, appSupport)
	}
}

func TestResolveDBPath_NotFoundListsProbed(t *testing.T) {
	home := t.TempDir()
	_, probed, err := resolveDBPath(envMap(map[string]string{"HOME": home}), "linux")
	if !errors.Is(err, ErrDBNotFound) {
		t.Fatalf("err = %v, want ErrDBNotFound", err)
	}
	if len(probed) == 0 || !strings.Contains(probed[0], "opencode") {
		t.Errorf("probed = %v, want the opencode data dir", probed)
	}
}

func TestReadOnlyDSN_EncodesSpacesAndStaysReadOnly(t *testing.T) {
	dsn := readOnlyDSN("/a b/opencode.db")
	if !strings.Contains(dsn, "%20") {
		t.Errorf("dsn %q should percent-encode the space", dsn)
	}
	for _, want := range []string{"mode=ro", "query_only(1)", "busy_timeout(5000)"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn %q missing %q", dsn, want)
		}
	}
	if strings.Contains(dsn, "immutable") {
		t.Errorf("dsn %q must not set immutable", dsn)
	}
}

func TestReadOnlyConnection_RejectsWrites(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	d := f.open()
	if _, err := d.sql.Exec("CREATE TABLE x (a)"); err == nil {
		t.Error("read-only connection should reject writes")
	}
}

func TestWALDatabaseReadableReadOnly(t *testing.T) {
	// WAL on, seed connection kept open so rows live in the un-checkpointed
	// -wal sidecar — the case immutable=1 would have hidden.
	f := newFixture(t, fixtureOpts{wal: true})
	f.session("ses_a", "/p", "t", 100)
	f.partsTurn("ses_a", "user", 10, "in the wal")

	ss, err := f.open().Scan()
	if err != nil {
		t.Fatal(err)
	}
	if len(ss) != 1 {
		t.Errorf("WAL read: got %d sessions, want 1", len(ss))
	}
}

func TestPreflight_LegacyStorageDetected(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".local", "share", "opencode", "storage"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv(EnvDBPath, "")

	err := Preflight()
	if err == nil || !strings.Contains(err.Error(), "SQLite storage") {
		t.Errorf("err = %v, want a legacy-storage message", err)
	}
}

func TestPreflight_OKForRealDB(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	t.Setenv(EnvDBPath, f.path)
	if err := Preflight(); err != nil {
		t.Errorf("Preflight: %v", err)
	}
}

func mkDB(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
