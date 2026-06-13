package opencode

import (
	"sort"
	"testing"
)

func keysSorted(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestGrepKeys_EmptyQueryMeansNoFilter(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	keys, err := f.open().GrepKeys("", false)
	if err != nil {
		t.Fatal(err)
	}
	if keys != nil {
		t.Errorf("empty query keys = %v, want nil (no filtering)", keys)
	}
}

func TestGrepKeys_NoMatchIsEmptyNotNil(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "title", 100)
	keys, err := f.open().GrepKeys("zzz_nomatch", false)
	if err != nil {
		t.Fatal(err)
	}
	if keys == nil || len(keys) != 0 {
		t.Errorf("no-match keys = %v, want empty non-nil set", keys)
	}
}

func TestGrepKeys_MatchesTitleAndBody(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_title", "/p", "find me in the title", 100)
	f.session("ses_body", "/p", "boring", 200)
	f.partsTurn("ses_body", "user", 10, "the needle is in the body")
	f.session("ses_none", "/p", "nothing", 300)
	d := f.open()

	if got := keysSorted(mustGrep(t, d, "needle", false)); !equal(got, []string{"ses_body"}) {
		t.Errorf("body match = %v, want [ses_body]", got)
	}
	if got := keysSorted(mustGrep(t, d, "title", false)); !equal(got, []string{"ses_title"}) {
		t.Errorf("title match = %v, want [ses_title]", got)
	}
}

func TestGrepKeys_CaseInsensitiveFixedString(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "Hello World", 100)
	if got := keysSorted(mustGrep(t, f.open(), "hello", false)); !equal(got, []string{"ses_a"}) {
		t.Errorf("case-insensitive match = %v, want [ses_a]", got)
	}
}

func TestGrepKeys_RegexMode(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "opencode rules", 100)
	if got := keysSorted(mustGrep(t, f.open(), "o.*code", true)); !equal(got, []string{"ses_a"}) {
		t.Errorf("regex match = %v, want [ses_a]", got)
	}
}

func TestGrepKeys_InvalidRegexErrors(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "t", 100)
	if _, err := f.open().GrepKeys("(unclosed", true); err == nil {
		t.Error("invalid regex should error")
	}
}

// Queries that the prefilter must not handle (else it could drop a real match).
func TestPrefilterUsable_SkipConditions(t *testing.T) {
	cases := []struct {
		query string
		regex bool
		want  bool
	}{
		{"plain", false, true},
		{"with space", false, true},
		{"regexy", true, false},      // regex mode
		{`has"quote`, false, false},  // quote
		{`back\slash`, false, false}, // backslash
		{"new\nline", false, false},  // newline spans the joined body
		{"café", false, false},       // non-ASCII case-folding
	}
	for _, c := range cases {
		if got := prefilterUsable(c.query, c.regex); got != c.want {
			t.Errorf("prefilterUsable(%q, regex=%v) = %v, want %v", c.query, c.regex, got, c.want)
		}
	}
}

// A query containing a LIKE wildcard must match literally, not as a wildcard.
func TestGrepKeys_LikeWildcardsAreLiteral(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_pct", "/p", "100% sure", 100)
	f.session("ses_other", "/p", "1000 sure", 200) // would match if % were a wildcard
	d := f.open()

	got := keysSorted(mustGrep(t, d, "100%", false))
	if !equal(got, []string{"ses_pct"}) {
		t.Errorf("literal %% match = %v, want only ses_pct", got)
	}
}

func TestEscapeLike(t *testing.T) {
	cases := map[string]string{
		"plain": "plain",
		"50%":   `50\%`,
		"a_b":   `a\_b`,
		`a\b`:   `a\\b`,
		`%_\`:   `\%\_\\`,
	}
	for in, want := range cases {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}

func mustGrep(t *testing.T, d *DB, q string, regex bool) map[string]struct{} {
	t.Helper()
	keys, err := d.GrepKeys(q, regex)
	if err != nil {
		t.Fatalf("GrepKeys(%q): %v", q, err)
	}
	return keys
}
