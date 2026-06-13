package opencode

import (
	"slices"
	"sort"
	"strings"
	"testing"
	"unicode"
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

	if got := keysSorted(mustGrep(t, d, "needle", false)); !slices.Equal(got, []string{"ses_body"}) {
		t.Errorf("body match = %v, want [ses_body]", got)
	}
	if got := keysSorted(mustGrep(t, d, "title", false)); !slices.Equal(got, []string{"ses_title"}) {
		t.Errorf("title match = %v, want [ses_title]", got)
	}
}

func TestGrepKeys_CaseInsensitiveFixedString(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "Hello World", 100)
	if got := keysSorted(mustGrep(t, f.open(), "hello", false)); !slices.Equal(got, []string{"ses_a"}) {
		t.Errorf("case-insensitive match = %v, want [ses_a]", got)
	}
}

func TestGrepKeys_RegexMode(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_a", "/p", "opencode rules", 100)
	if got := keysSorted(mustGrep(t, f.open(), "o.*code", true)); !slices.Equal(got, []string{"ses_a"}) {
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
		{"hello", false, true},       // ASCII, no case-fold-target letter
		{"two words", false, true},   // spaces are fine
		{"50%", false, true},         // wildcard handled by escapeLike, no i/k
		{"regexy", true, false},      // regex mode
		{`has"quote`, false, false},  // quote
		{`back\slash`, false, false}, // backslash
		{"new\nline", false, false},  // newline spans the joined body
		{"café", false, false},       // non-ASCII case-folding
		{"kit", false, false},        // 'k' could be reached by U+212A in the haystack
		{"think", false, false},      // 'i' and 'k'
		{"INDEX", false, false},      // uppercase 'I' lower-folds to a target 'i'
	}
	for _, c := range cases {
		if got := prefilterUsable(c.query, c.regex); got != c.want {
			t.Errorf("prefilterUsable(%q, regex=%v) = %v, want %v", c.query, c.regex, got, c.want)
		}
	}
}

// TestAsciiFoldTargetsExact scans every rune and asserts asciiFoldTargets lists
// exactly the ASCII letters reachable by lower-casing a non-ASCII rune. If a
// future Unicode table adds another, this fails loudly so prefilterUsable's
// guard stays complete.
func TestAsciiFoldTargetsExact(t *testing.T) {
	seen := map[rune]bool{}
	for r := rune(0x80); r <= unicode.MaxRune; r++ {
		for _, c := range strings.ToLower(string(r)) {
			if c >= 'a' && c <= 'z' {
				seen[c] = true
			}
		}
	}
	var letters []rune
	for c := range seen {
		letters = append(letters, c)
	}
	slices.Sort(letters)
	if got := string(letters); got != asciiFoldTargets {
		t.Fatalf("ascii fold targets = %q, but asciiFoldTargets = %q; a Unicode update "+
			"changed the set — update the const so prefilterUsable still bypasses every one", got, asciiFoldTargets)
	}
}

// TestGrepKeys_FindsUnicodeCaseFoldMatch is the regression for the prefilter
// drop: a haystack holding U+212A/U+0130 matches an ASCII query under the
// Unicode-folding matcher, so GrepKeys must surface it even though the
// ASCII-only LIKE prefilter cannot.
func TestGrepKeys_FindsUnicodeCaseFoldMatch(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_kelvin", "/p", "temp \u212a units", 100) // U+212A folds to 'k'
	f.session("ses_dotted", "/p", "boring", 200)
	f.partsTurn("ses_dotted", "user", 10, "\u0130stanbul notes") // U+0130 folds to 'i'
	f.session("ses_plain", "/p", "zzz", 300)
	d := f.open()

	if got := mustGrep(t, d, "k", false); !contains(got, "ses_kelvin") {
		t.Errorf("query \"k\" = %v, want it to include ses_kelvin (U+212A folds to k)", keysSorted(got))
	}
	if got := mustGrep(t, d, "i", false); !contains(got, "ses_dotted") {
		t.Errorf("query \"i\" = %v, want it to include ses_dotted (U+0130 folds to i)", keysSorted(got))
	}
}

func contains(m map[string]struct{}, k string) bool {
	_, ok := m[k]
	return ok
}

// A query containing a LIKE wildcard must match literally, not as a wildcard.
func TestGrepKeys_LikeWildcardsAreLiteral(t *testing.T) {
	f := newFixture(t, fixtureOpts{})
	f.session("ses_pct", "/p", "100% sure", 100)
	f.session("ses_other", "/p", "1000 sure", 200) // would match if % were a wildcard
	d := f.open()

	got := keysSorted(mustGrep(t, d, "100%", false))
	if !slices.Equal(got, []string{"ses_pct"}) {
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
