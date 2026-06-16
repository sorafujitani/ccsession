package grep

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func makeProjects(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Setenv("HOME", home)
	return filepath.Join(home, ".claude", "projects")
}

func writeFile(t testing.TB, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestFilter_EmptyQueryReturnsNil(t *testing.T) {
	got, err := Filter("   ", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestFilter_MatchesUserContent(t *testing.T) {
	projects := makeProjects(t)
	hit1 := filepath.Join(projects, "-p1", "11111111-1111-1111-1111-111111111111.jsonl")
	hit2 := filepath.Join(projects, "-p2", "22222222-2222-2222-2222-222222222222.jsonl")
	miss := filepath.Join(projects, "-p1", "33333333-3333-3333-3333-333333333333.jsonl")

	writeFile(t, hit1, `{"type":"user","message":{"role":"user","content":"please find NEEDLE here"}}`+"\n")
	writeFile(t, hit2, `{"type":"assistant","message":{"role":"assistant","content":"another needle hit"}}`+"\n")
	writeFile(t, miss, `{"type":"user","message":{"role":"user","content":"unrelated"}}`+"\n")

	set, err := Filter("needle", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[hit1]; !ok {
		t.Errorf("expected %s in result", hit1)
	}
	if _, ok := set[hit2]; !ok {
		t.Errorf("expected %s in result", hit2)
	}
	if _, ok := set[miss]; ok {
		t.Errorf("did not expect %s in result", miss)
	}
}

// B-7: a query that only appears in JSON keys (every line has `"type"`)
// must not match any session.
func TestFilter_DoesNotMatchJSONKeys(t *testing.T) {
	projects := makeProjects(t)
	p := filepath.Join(projects, "-p", "a.jsonl")
	writeFile(t, p,
		`{"type":"user","message":{"role":"user","content":"actual conversation text"}}`+"\n")

	set, err := Filter("type", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[p]; ok {
		t.Errorf("query 'type' should not match JSON keys, got %v", set)
	}
}

// B-7: a query that only appears in the cwd field must not match.
func TestFilter_DoesNotMatchCWDField(t *testing.T) {
	projects := makeProjects(t)
	p := filepath.Join(projects, "-tmp-proj-normal", "a.jsonl")
	writeFile(t, p,
		`{"type":"user","cwd":"/tmp/proj-normal","message":{"role":"user","content":"hello"}}`+"\n")

	set, err := Filter("proj-normal", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[p]; ok {
		t.Errorf("query 'proj-normal' should not match cwd field, got %v", set)
	}
}

func TestFilter_ExcludesAgentJSONL(t *testing.T) {
	projects := makeProjects(t)
	user := filepath.Join(projects, "-p", "11111111-1111-1111-1111-111111111111.jsonl")
	agent := filepath.Join(projects, "-p", "agent-deadbeef.jsonl")
	writeFile(t, user,
		`{"type":"user","message":{"role":"user","content":"shared TOKEN here"}}`+"\n")
	writeFile(t, agent,
		`{"type":"user","message":{"role":"user","content":"shared TOKEN here"}}`+"\n")

	set, err := Filter("TOKEN", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[user]; !ok {
		t.Errorf("expected user session %s", user)
	}
	if _, ok := set[agent]; ok {
		t.Errorf("agent-*.jsonl should be excluded but found %s", agent)
	}
}

func TestFilter_CaseInsensitive(t *testing.T) {
	projects := makeProjects(t)
	p := filepath.Join(projects, "-p", "a.jsonl")
	writeFile(t, p,
		`{"type":"user","message":{"role":"user","content":"Lowercase pattern here"}}`+"\n")

	set, err := Filter("LOWERCASE", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[p]; !ok {
		t.Errorf("expected case-insensitive hit on %s", p)
	}
}

// B-8: in fixed-string (default) mode, regex metacharacters in the query
// are treated as literals, so an invalid regex like "[" doesn't crash.
func TestFilter_FixedStringIgnoresRegexMetachars(t *testing.T) {
	projects := makeProjects(t)
	p := filepath.Join(projects, "-p", "a.jsonl")
	writeFile(t, p,
		`{"type":"user","message":{"role":"user","content":"normal content"}}`+"\n")

	set, err := Filter("[invalid", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if len(set) != 0 {
		t.Errorf("expected no match for literal '[invalid', got %v", set)
	}
}

func TestFilter_RegexMode(t *testing.T) {
	projects := makeProjects(t)
	p := filepath.Join(projects, "-p", "a.jsonl")
	writeFile(t, p,
		`{"type":"user","message":{"role":"user","content":"build 42 widgets"}}`+"\n")

	set, err := Filter(`build\s+\d+`, Options{Regex: true})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[p]; !ok {
		t.Errorf("expected regex match, got %v", set)
	}
}

// Regression: a phrase that only appears in the ai-title (= the label
// shown in the list view) must be matchable, otherwise users searching
// for what they can see on screen find nothing. See issue #8.
func TestFilter_MatchesAITitleLabel(t *testing.T) {
	projects := makeProjects(t)
	p := filepath.Join(projects, "-p", "a.jsonl")
	writeFile(t, p, strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"plain body without the keyword"}}`,
		`{"type":"ai-title","aiTitle":"toridoriのapplication監視の達成状態"}`,
	}, "\n")+"\n")

	set, err := Filter("application監視", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[p]; !ok {
		t.Errorf("expected ai-title keyword to hit, got %v", set)
	}
}

func TestFilter_MatchesLastPrompt(t *testing.T) {
	projects := makeProjects(t)
	p := filepath.Join(projects, "-p", "a.jsonl")
	writeFile(t, p, strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"plain"}}`,
		`{"type":"last-prompt","lastPrompt":"investigate xyz"}`,
	}, "\n")+"\n")

	set, err := Filter("investigate", Options{})
	if err != nil {
		t.Fatalf("Filter: %v", err)
	}
	if _, ok := set[p]; !ok {
		t.Errorf("expected last-prompt to hit, got %v", set)
	}
}

func TestFilter_RegexInvalidReturnsError(t *testing.T) {
	makeProjects(t)
	_, err := Filter("[invalid", Options{Regex: true})
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
}

func TestFilter_CacheInvalidatesOnFileUpdate(t *testing.T) {
	t.Setenv(EnvCacheDir, t.TempDir())
	projects := makeProjects(t)
	p := filepath.Join(projects, "-p", "a.jsonl")
	firstTime := time.Unix(1_800_000_000, 0)
	secondTime := firstTime.Add(time.Second)
	writeFile(t, p, `{"type":"user","message":{"role":"user","content":"needle"}}`+"\n")
	if err := os.Chtimes(p, firstTime, firstTime); err != nil {
		t.Fatalf("chtimes first: %v", err)
	}

	set, err := Filter("needle", Options{})
	if err != nil {
		t.Fatalf("first Filter: %v", err)
	}
	if _, ok := set[p]; !ok {
		t.Fatalf("first Filter missed %s", p)
	}

	writeFile(t, p, `{"type":"user","message":{"role":"user","content":"updated"}}`+"\n")
	if err := os.Chtimes(p, secondTime, secondTime); err != nil {
		t.Fatalf("chtimes second: %v", err)
	}
	set, err = Filter("needle", Options{})
	if err != nil {
		t.Fatalf("second Filter: %v", err)
	}
	if _, ok := set[p]; ok {
		t.Fatalf("second Filter used stale cache: %#v", set)
	}
}

func BenchmarkFilterRepeatedQuery(b *testing.B) {
	for _, tc := range []struct {
		name     string
		cacheDir string
		warm     bool
	}{
		{name: "direct-read"},
		{name: "warm-cache", cacheDir: b.TempDir(), warm: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			if tc.cacheDir == "" {
				blocker := filepath.Join(b.TempDir(), "not-a-dir")
				writeFile(b, blocker, "cache dir blocker")
				b.Setenv(EnvCacheDir, blocker)
			} else {
				b.Setenv(EnvCacheDir, tc.cacheDir)
			}
			writeBenchmarkProjects(b)
			if tc.warm {
				if _, err := Filter("needle", Options{}); err != nil {
					b.Fatalf("warm Filter: %v", err)
				}
			}

			b.ResetTimer()
			for range b.N {
				if _, err := Filter("needle", Options{}); err != nil {
					b.Fatalf("Filter: %v", err)
				}
			}
		})
	}
}

func writeBenchmarkProjects(b *testing.B) {
	b.Helper()
	projects := makeProjects(b)
	for i := range 64 {
		body := strings.Repeat("ordinary transcript text\n", 128)
		if i%8 == 0 {
			body += "needle\n"
		}
		p := filepath.Join(projects, "-p", fmt.Sprintf("session-%02d.jsonl", i))
		writeFile(b, p, `{"type":"user","message":{"role":"user","content":`+quoteJSON(body)+`}}`+"\n")
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
