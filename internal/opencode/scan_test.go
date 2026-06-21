package opencode

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
)

func ids(ss []*session.Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.ID
	}
	return out
}

func TestScan_OrdersByUpdatedDescThenID(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_b", "/p", "b", 200)
	f.session("ses_a", "/p", "a", 300)
	f.session("ses_c", "/p", "c", 100)

	ss, err := f.open().scanFiltered(nil, time.UnixMilli(1_000_000))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(ss), []string{"ses_a", "ses_b", "ses_c"}; !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestScan_TiebreakByIDAscending(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_z", "/p", "z", 500)
	f.session("ses_a", "/p", "a", 500)

	ss, _ := f.open().scanFiltered(nil, time.UnixMilli(1_000_000))
	if got, want := ids(ss), []string{"ses_a", "ses_z"}; !slices.Equal(got, want) {
		t.Errorf("tiebreak order = %v, want %v", got, want)
	}
}

func TestScan_ExcludesChildAndArchived(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_root", "/p", "root", 100)
	f.childSession("ses_child", "ses_root", "/p", "child", 200)
	f.archivedSession("ses_arch", "/p", "arch", 300)

	ss, _ := f.open().scanFiltered(nil, time.UnixMilli(1_000_000))
	if got, want := ids(ss), []string{"ses_root"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want only the root session", got)
	}
}

func TestScan_EmptyDirectoryIsCWDUnknown(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_nodir", "", "no dir", 100)

	ss, _ := f.open().scanFiltered(nil, time.UnixMilli(1_000_000))
	if len(ss) != 1 {
		t.Fatalf("want 1 session, got %d", len(ss))
	}
	if !ss[0].CWDUnknown || ss[0].CWDBasename != "" {
		t.Errorf("empty dir: CWDUnknown=%v basename=%q, want true/empty", ss[0].CWDUnknown, ss[0].CWDBasename)
	}
}

func TestScan_ControlCharIDDroppedButTitleSanitized(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_good\ttab", "/p", "ok", 100) // tab in id -> dropped
	f.session("ses_clean", "/p", "line1\nline2", 200)

	ss, _ := f.open().scanFiltered(nil, time.UnixMilli(1_000_000))
	if got, want := ids(ss), []string{"ses_clean"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want the corrupt-id row dropped", got)
	}
	if ss[0].Label != "line1 line2" {
		t.Errorf("label = %q, want control chars collapsed to a space", ss[0].Label)
	}
}

func TestScan_MsToSecondsAndFutureSink(t *testing.T) {
	now := time.UnixMilli(1_000_000) // 1000s
	f := newFixture(t, fixtureOpts{})
	f.session("ses_past", "/p", "past", 500_000)       // 500s
	f.session("ses_future", "/p", "future", 9_000_000) // 9000s, in the future vs now

	ss, _ := f.open().scanFiltered(nil, now)
	// ms->s: 500_000ms -> 500s epoch.
	if got := find(t, ss, "ses_past").LastEpoch; got != 500 {
		t.Errorf("LastEpoch = %d, want 500 (ms->s)", got)
	}
	// Future session sinks to the bottom despite the larger timestamp.
	if got, want := ids(ss), []string{"ses_past", "ses_future"}; !slices.Equal(got, want) {
		t.Errorf("future-sink order = %v, want %v", got, want)
	}
}

func TestScanFiltered_KeepsOnlyAllowedIDs(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "a", 100)
	f.session("ses_b", "/p", "b", 200)

	allow := map[string]struct{}{"ses_b": {}}
	ss, _ := f.open().scanFiltered(allow, time.UnixMilli(1_000_000))
	if got, want := ids(ss), []string{"ses_b"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFindByID(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "hello", 100)
	d := f.open()

	s, err := d.FindByID("ses_a")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if s.Label != "hello" {
		t.Errorf("label = %q, want hello", s.Label)
	}

	_, err = d.FindByID("ses_missing")
	if !errors.Is(err, session.ErrSessionFileMissing) {
		t.Errorf("missing id err = %v, want ErrSessionFileMissing", err)
	}
}

func TestScan_SchemaMismatchReportsMigrations(t *testing.T) {
	f := newFixture(t, fixtureOpts{dropTable: "session"})
	f.migrations("0001_init", "0002_add_thing", "0003_latest")

	_, err := f.open().Scan()
	if err == nil {
		t.Fatal("want error when session table is missing")
	}
	msg := err.Error()
	if !strings.Contains(msg, "0003_latest") || !strings.Contains(msg, "3 migrations") {
		t.Errorf("error %q should name the newest migration and the count", msg)
	}
}

func find(t *testing.T, ss []*session.Session, id string) *session.Session {
	t.Helper()
	for _, s := range ss {
		if s.ID == id {
			return s
		}
	}
	t.Fatalf("session %q not found in %v", id, ids(ss))
	return nil
}

func BenchmarkScanManySessions(b *testing.B) {
	f := newFixture(b, fixtureOpts{})
	for i := range 1000 {
		id := "ses_scan_" + itoa(int64(i))
		f.session(id, "/tmp/"+id, "title "+id, int64(i+1)*1000)
	}
	d := f.open()

	b.ResetTimer()
	for range b.N {
		ss, err := d.Scan()
		if err != nil {
			b.Fatalf("Scan: %v", err)
		}
		if len(ss) != 1000 {
			b.Fatalf("Scan returned %d sessions, want 1000", len(ss))
		}
	}
}
