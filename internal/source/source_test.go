package source

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/codex"
	"github.com/sorafujitani/ccsession/internal/grok"
	"github.com/sorafujitani/ccsession/internal/opencode"
	"github.com/sorafujitani/ccsession/internal/session"
)

func TestFromEnv_SelectsBackend(t *testing.T) {
	// opencode resolves its DB from OPENCODE_DB; point it at a real file so the
	// backend constructs (the file isn't opened until first query).
	db := filepath.Join(t.TempDir(), "opencode.db")
	if err := os.WriteFile(db, nil, 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	t.Setenv(opencode.EnvDBPath, db)
	t.Setenv(grok.EnvHome, t.TempDir())
	t.Setenv(codex.EnvHome, t.TempDir())

	cases := []struct {
		env      string
		wantName string
		wantErr  bool
	}{
		{"", "claude", false},
		{"all", "all", false},
		{"claude", "claude", false},
		{"opencode", "opencode", false},
		{"grok", "grok", false},
		{"codex", "codex", false},
		// An unknown value is an error, not a silent fall back to claude:
		// a typo must surface, not quietly show the wrong agent's sessions.
		{"clauded", "", true},
	}
	for _, c := range cases {
		t.Setenv(EnvVar, c.env)
		got, err := FromEnv()
		if c.wantErr {
			if err == nil {
				t.Errorf("FromEnv(%q) = %v, want error", c.env, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("FromEnv(%q): %v", c.env, err)
		}
		if got.Name() != c.wantName {
			t.Errorf("FromEnv(%q).Name() = %q, want %q", c.env, got.Name(), c.wantName)
		}
	}
}

func TestCodex_ResumeSpec(t *testing.T) {
	bin, args, err := codexSource{}.ResumeSpec(&session.Session{ID: "abc123"})
	if err != nil {
		t.Fatalf("ResumeSpec: %v", err)
	}
	if bin != "codex" {
		t.Errorf("bin = %q, want codex", bin)
	}
	want := []string{"codex", "resume", "abc123"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestGrok_ResumeSpec(t *testing.T) {
	bin, args, err := grokSource{}.ResumeSpec(&session.Session{ID: "abc123"})
	if err != nil {
		t.Fatalf("ResumeSpec: %v", err)
	}
	if bin != "grok" {
		t.Errorf("bin = %q, want grok", bin)
	}
	want := []string{"grok", "--resume", "abc123"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestClaude_ResumeSpec(t *testing.T) {
	bin, args, err := claudeSource{}.ResumeSpec(&session.Session{ID: "abc123"})
	if err != nil {
		t.Fatalf("ResumeSpec: %v", err)
	}
	if bin != "claude" {
		t.Errorf("bin = %q, want claude", bin)
	}
	want := []string{"claude", "--resume", "abc123"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

// fixtureHome writes one parseable session under a fake ~/.claude/projects and
// points HOME at it. Returns the session id.
func fixtureHome(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := "11111111-1111-1111-1111-111111111111"
	body := `{"type":"user","timestamp":"2026-05-26T10:00:00Z","cwd":"` + dir + `","message":{"role":"user","content":"` + content + `"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("HOME", home)
	return id
}

func TestClaude_ScanStampsSource(t *testing.T) {
	fixtureHome(t, "hello")
	ss, err := claudeSource{}.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) == 0 {
		t.Fatal("Scan returned no sessions")
	}
	for _, s := range ss {
		if s.Source != "claude" {
			t.Errorf("session %s Source = %q, want claude", s.ID, s.Source)
		}
	}
}

// GrepKeys keys are opaque tokens that feed straight back into ScanFiltered;
// this round trip is the only contract between the two methods.
func TestClaude_GrepKeysFeedScanFiltered(t *testing.T) {
	fixtureHome(t, "the unique NEEDLE token")
	src := claudeSource{}

	keys, err := src.GrepKeys("needle", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if len(keys) == 0 {
		t.Fatal("GrepKeys found nothing for a matching session")
	}

	ss, err := src.ScanFiltered(keys)
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("ScanFiltered returned %d sessions, want 1", len(ss))
	}
}

func TestClaude_FindByIDStampsSource(t *testing.T) {
	id := fixtureHome(t, "hello")
	s, err := claudeSource{}.FindByID(id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if s == nil || s.Source != "claude" {
		t.Errorf("FindByID Source = %v, want claude", s)
	}
}

func TestCodex_ScanStampsSource(t *testing.T) {
	home, _ := fixtureCodexHome(t, "hello codex")
	ss, err := (codexSource{store: codex.OpenAt(home)}).Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) == 0 {
		t.Fatal("Scan returned no sessions")
	}
	for _, s := range ss {
		if s.Source != "codex" {
			t.Errorf("session %s Source = %q, want codex", s.ID, s.Source)
		}
	}
}

func TestCodex_FindByIDStampsSource(t *testing.T) {
	home, id := fixtureCodexHome(t, "hello codex")
	s, err := (codexSource{store: codex.OpenAt(home)}).FindByID(id)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if s == nil || s.Source != "codex" {
		t.Errorf("FindByID Source = %v, want codex", s)
	}
}

func TestAllSource_ScanReturnsCompositeIDsSorted(t *testing.T) {
	src := allSource{sources: []Source{
		fakeSource{name: "claude", sessions: []*session.Session{
			{ID: "c1", Source: "claude", LastEpoch: 10},
		}},
		fakeSource{name: "codex", sessions: []*session.Session{
			{ID: "x1", Source: "codex", LastEpoch: 20},
		}},
	}}

	ss, err := src.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("Scan returned %d sessions, want 2", len(ss))
	}
	if ss[0].ID != "codex:x1" || ss[1].ID != "claude:c1" {
		t.Fatalf("IDs = %q, %q; want sorted composite IDs", ss[0].ID, ss[1].ID)
	}
	if ss[0].Source != "codex" || ss[1].Source != "claude" {
		t.Fatalf("Sources = %q, %q", ss[0].Source, ss[1].Source)
	}
}

func TestAllSource_ScanRunsBackendsConcurrently(t *testing.T) {
	codexStarted := make(chan struct{})
	src := allSource{sources: []Source{
		fakeSource{name: "claude", scanFunc: func() ([]*session.Session, error) {
			select {
			case <-codexStarted:
				return []*session.Session{{ID: "c1", Source: "claude", LastEpoch: 10}}, nil
			case <-time.After(time.Second):
				return nil, errors.New("codex scan did not start")
			}
		}},
		fakeSource{name: "codex", scanFunc: func() ([]*session.Session, error) {
			close(codexStarted)
			return []*session.Session{{ID: "x1", Source: "codex", LastEpoch: 20}}, nil
		}},
	}}

	ss, err := src.Scan()
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("Scan returned %d sessions, want 2", len(ss))
	}
}

func TestAllSource_GrepKeysFeedScanFiltered(t *testing.T) {
	src := allSource{sources: []Source{
		fakeSource{name: "claude", sessions: []*session.Session{
			{ID: "c1", Source: "claude"},
		}, grep: map[string]struct{}{"path:with:colon.jsonl": {}}},
		fakeSource{name: "codex", sessions: []*session.Session{
			{ID: "x1", Source: "codex"},
		}, grep: map[string]struct{}{"x1": {}}},
	}}

	keys, err := src.GrepKeys("needle", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	if _, ok := keys["claude:path:with:colon.jsonl"]; !ok {
		t.Fatalf("missing wrapped claude key in %v", keys)
	}
	if _, ok := keys["codex:x1"]; !ok {
		t.Fatalf("missing wrapped codex key in %v", keys)
	}

	ss, err := src.ScanFiltered(keys)
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	ids := make(map[string]struct{})
	for _, s := range ss {
		ids[s.ID] = struct{}{}
	}
	if _, ok := ids["claude:c1"]; !ok {
		t.Fatalf("missing claude session in %v", ids)
	}
	if _, ok := ids["codex:x1"]; !ok {
		t.Fatalf("missing codex session in %v", ids)
	}
}

func TestAllSource_ScanFilteredRunsBackendsConcurrently(t *testing.T) {
	codexStarted := make(chan struct{})
	src := allSource{sources: []Source{
		fakeSource{name: "claude", scanFilteredFunc: func(map[string]struct{}) ([]*session.Session, error) {
			select {
			case <-codexStarted:
				return []*session.Session{{ID: "c1", Source: "claude"}}, nil
			case <-time.After(time.Second):
				return nil, errors.New("codex filtered scan did not start")
			}
		}},
		fakeSource{name: "codex", scanFilteredFunc: func(map[string]struct{}) ([]*session.Session, error) {
			close(codexStarted)
			return []*session.Session{{ID: "x1", Source: "codex"}}, nil
		}},
	}}

	ss, err := src.ScanFiltered(map[string]struct{}{
		"claude:c1": {},
		"codex:x1":  {},
	})
	if err != nil {
		t.Fatalf("ScanFiltered: %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("ScanFiltered returned %d sessions, want 2", len(ss))
	}
}

func TestAllSource_GrepKeysRunsBackendsConcurrently(t *testing.T) {
	codexStarted := make(chan struct{})
	src := allSource{sources: []Source{
		fakeSource{name: "claude", grepFunc: func(string, bool) (map[string]struct{}, error) {
			select {
			case <-codexStarted:
				return map[string]struct{}{"c1": {}}, nil
			case <-time.After(time.Second):
				return nil, errors.New("codex grep did not start")
			}
		}},
		fakeSource{name: "codex", grepFunc: func(string, bool) (map[string]struct{}, error) {
			close(codexStarted)
			return map[string]struct{}{"x1": {}}, nil
		}},
	}}

	keys, err := src.GrepKeys("needle", false)
	if err != nil {
		t.Fatalf("GrepKeys: %v", err)
	}
	for _, key := range []string{"claude:c1", "codex:x1"} {
		if _, ok := keys[key]; !ok {
			t.Fatalf("missing key %q in %v", key, keys)
		}
	}
}

func TestAllSource_ErrorsIncludeBackendName(t *testing.T) {
	boom := errors.New("boom")
	cases := []struct {
		name string
		run  func(allSource) error
		src  fakeSource
		want string
	}{
		{
			name: "scan",
			run: func(src allSource) error {
				_, err := src.Scan()
				return err
			},
			src:  fakeSource{name: "codex", scanErr: boom},
			want: "codex scan: boom",
		},
		{
			name: "scan filtered",
			run: func(src allSource) error {
				_, err := src.ScanFiltered(map[string]struct{}{"codex:x1": {}})
				return err
			},
			src:  fakeSource{name: "codex", scanFilteredErr: boom},
			want: "codex scan filtered: boom",
		},
		{
			name: "grep",
			run: func(src allSource) error {
				_, err := src.GrepKeys("needle", false)
				return err
			},
			src:  fakeSource{name: "codex", grepErr: boom},
			want: "codex grep: boom",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.run(allSource{sources: []Source{c.src}})
			if err == nil {
				t.Fatal("got nil error")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want to contain %q", err, c.want)
			}
		})
	}
}

func TestAllSource_ScanFilteredNilScansAll(t *testing.T) {
	src := allSource{sources: []Source{
		fakeSource{name: "claude", sessions: []*session.Session{
			{ID: "c1", Source: "claude"},
		}},
		fakeSource{name: "codex", sessions: []*session.Session{
			{ID: "x1", Source: "codex"},
		}},
	}}

	ss, err := src.ScanFiltered(nil)
	if err != nil {
		t.Fatalf("ScanFiltered(nil): %v", err)
	}
	if len(ss) != 2 {
		t.Fatalf("ScanFiltered(nil) returned %d sessions, want 2", len(ss))
	}
}

func TestAllSource_FindByIDAndResumeRouteByCompositeKey(t *testing.T) {
	src := allSource{sources: []Source{
		fakeSource{name: "claude", sessions: []*session.Session{
			{ID: "same", Source: "claude"},
		}, resumeBin: "claude"},
		fakeSource{name: "codex", sessions: []*session.Session{
			{ID: "same", Source: "codex"},
		}, resumeBin: "codex"},
	}}

	s, err := src.FindByID("codex:same")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if s.ID != "same" || s.Source != "codex" {
		t.Fatalf("FindByID returned %+v, want codex real ID", s)
	}
	bin, args, err := src.ResumeSpec(s)
	if err != nil {
		t.Fatalf("ResumeSpec: %v", err)
	}
	if bin != "codex" || len(args) != 2 || args[1] != "same" {
		t.Fatalf("ResumeSpec = %q %v, want codex resume same", bin, args)
	}
}

func TestLocatorForEncodesPathAndPrefixesCompositeID(t *testing.T) {
	s := &session.Session{
		ID:        "codex:abc",
		Source:    "codex",
		JSONLPath: "/tmp/path\twith-tab/session.jsonl",
	}
	locator := LocatorFor(s)
	name, encoded, ok := splitKey(locator)
	if !ok {
		t.Fatalf("LocatorFor = %q, want source-prefixed locator", locator)
	}
	if name != "codex" {
		t.Fatalf("locator source = %q, want codex", name)
	}
	got, ok := decodeLocator(encoded)
	if !ok {
		t.Fatalf("locator payload did not decode: %q", encoded)
	}
	if got != s.JSONLPath {
		t.Fatalf("decoded locator = %q, want %q", got, s.JSONLPath)
	}
	if strings.ContainsAny(locator, "\t\n\r") {
		t.Fatalf("locator contains row-breaking control char: %q", locator)
	}
}

func TestAllSource_FindByLocatorRoutesCompositeLocator(t *testing.T) {
	src := allSource{sources: []Source{
		fakeSource{name: "claude", sessions: []*session.Session{
			{ID: "same", Source: "claude", JSONLPath: "/claude/path.jsonl"},
		}},
		fakeSource{name: "codex", sessions: []*session.Session{
			{ID: "same", Source: "codex", JSONLPath: "/codex/path.jsonl"},
		}},
	}}

	s, err := src.FindByLocator("codex:same", joinKey("codex", encodeLocator("/codex/path.jsonl")))
	if err != nil {
		t.Fatalf("FindByLocator: %v", err)
	}
	if s.ID != "same" || s.Source != "codex" {
		t.Fatalf("FindByLocator returned %+v, want codex real ID", s)
	}
}

type fakeSource struct {
	name             string
	sessions         []*session.Session
	grep             map[string]struct{}
	resumeBin        string
	scanErr          error
	scanFilteredErr  error
	grepErr          error
	scanFunc         func() ([]*session.Session, error)
	scanFilteredFunc func(map[string]struct{}) ([]*session.Session, error)
	grepFunc         func(string, bool) (map[string]struct{}, error)
}

func (f fakeSource) Name() string { return f.name }

func (f fakeSource) Scan() ([]*session.Session, error) {
	if f.scanFunc != nil {
		return f.scanFunc()
	}
	if f.scanErr != nil {
		return nil, f.scanErr
	}
	return f.sessions, nil
}

func (f fakeSource) ScanFiltered(allow map[string]struct{}) ([]*session.Session, error) {
	if f.scanFilteredFunc != nil {
		return f.scanFilteredFunc(allow)
	}
	if f.scanFilteredErr != nil {
		return nil, f.scanFilteredErr
	}
	var out []*session.Session
	for _, s := range f.sessions {
		if _, ok := allow[s.ID]; ok {
			out = append(out, s)
			continue
		}
		if f.name == "claude" {
			if _, ok := allow["path:with:colon.jsonl"]; ok {
				out = append(out, s)
			}
		}
	}
	return out, nil
}

func (f fakeSource) FindByID(id string) (*session.Session, error) {
	for _, s := range f.sessions {
		if s.ID == id {
			cp := *s
			return &cp, nil
		}
	}
	return nil, session.ErrSessionFileMissing
}

func (f fakeSource) FindByLocator(id, locator string) (*session.Session, error) {
	raw, ok := decodeLocator(locator)
	if !ok {
		return nil, session.ErrSessionFileMissing
	}
	for _, s := range f.sessions {
		if s.ID == id && (s.JSONLPath == raw || s.ID == raw) {
			cp := *s
			return &cp, nil
		}
	}
	return nil, session.ErrSessionFileMissing
}

func (f fakeSource) GrepKeys(query string, regex bool) (map[string]struct{}, error) {
	if f.grepFunc != nil {
		return f.grepFunc(query, regex)
	}
	if f.grepErr != nil {
		return nil, f.grepErr
	}
	return f.grep, nil
}

func (f fakeSource) ResumeSpec(s *session.Session) (string, []string, error) {
	return f.resumeBin, []string{f.resumeBin, s.ID}, nil
}

func fixtureCodexHome(t *testing.T, content string) (home, id string) {
	t.Helper()
	home = t.TempDir()
	cwd := t.TempDir()
	dir := filepath.Join(home, "sessions", "2026", "06", "14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id = "019ec14c-b49c-7a40-a386-0a1699dbb01c"
	body := `{"timestamp":"2026-06-14T00:00:00Z","type":"session_meta","payload":{"id":"` + id + `","timestamp":"2026-06-14T00:00:00Z","cwd":"` + cwd + `"}}` + "\n" +
		`{"timestamp":"2026-06-14T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + content + `"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "rollout-2026-06-14T00-00-00-"+id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return home, id
}

func BenchmarkAllSourceScan(b *testing.B) {
	src := allSource{sources: []Source{
		benchmarkFakeSource("claude", 500, 10_000),
		benchmarkFakeSource("opencode", 500, 20_000),
		benchmarkFakeSource("grok", 500, 30_000),
		benchmarkFakeSource("codex", 500, 40_000),
	}}
	b.ResetTimer()
	for range b.N {
		ss, err := src.Scan()
		if err != nil {
			b.Fatalf("Scan: %v", err)
		}
		if len(ss) != 2000 {
			b.Fatalf("Scan returned %d sessions, want 2000", len(ss))
		}
	}
}

func BenchmarkAllSourceGrepKeys(b *testing.B) {
	src := allSource{sources: []Source{
		benchmarkFakeGrepSource("claude", 500),
		benchmarkFakeGrepSource("opencode", 500),
		benchmarkFakeGrepSource("grok", 500),
		benchmarkFakeGrepSource("codex", 500),
	}}
	b.ResetTimer()
	for range b.N {
		keys, err := src.GrepKeys("needle", false)
		if err != nil {
			b.Fatalf("GrepKeys: %v", err)
		}
		if len(keys) != 2000 {
			b.Fatalf("GrepKeys returned %d keys, want 2000", len(keys))
		}
	}
}

func benchmarkFakeSource(name string, n int, baseEpoch int64) fakeSource {
	sessions := make([]*session.Session, 0, n)
	for i := range n {
		sessions = append(sessions, &session.Session{
			ID:        name + "-" + itoaBench(i),
			Source:    name,
			LastEpoch: baseEpoch + int64(i),
		})
	}
	return fakeSource{name: name, sessions: sessions}
}

func benchmarkFakeGrepSource(name string, n int) fakeSource {
	keys := make(map[string]struct{}, n)
	for i := range n {
		keys[name+"-"+itoaBench(i)] = struct{}{}
	}
	return fakeSource{name: name, grep: keys}
}

func itoaBench(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
