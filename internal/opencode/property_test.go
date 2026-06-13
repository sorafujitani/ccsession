package opencode

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/sorafujitani/ccsession/internal/session"
	"pgregory.net/rapid"
)

// fatalf is the slice of testing.TB / rapid.T shared by both the standard and
// property tests, so the SQLite-oracle helpers attribute failures to whichever
// is driving (rapid.T enables shrinking; *testing.T for the explicit cases).
type fatalf interface {
	Helper()
	Fatalf(format string, args ...any)
}

func runeRange(lo, hi rune) []rune {
	out := make([]rune, 0, hi-lo+1)
	for r := lo; r <= hi; r++ {
		out = append(out, r)
	}
	return out
}

// printableASCIIRunes spans the printable-ASCII range — including the LIKE
// metacharacters (% _ \) and JSON-risky chars (" ') — so escapeLike is
// exercised on exactly the bytes it exists to neutralize.
var printableASCIIRunes = runeRange(0x20, 0x7e)

// idRunes are the bytes of a well-formed session id (the corruption case adds a
// tab separately).
var idRunes = append(runeRange('a', 'z'), runeRange('0', '9')...)

// printableASCII draws strings over the printable-ASCII range, which is the
// domain prefilterUsable admits for the LIKE prefilter.
func printableASCII(t *rapid.T, label string) string {
	return rapid.StringOfN(rapid.RuneFrom(printableASCIIRunes), 0, 12, -1).Draw(t, label)
}

// openMemLike returns a single in-memory connection used purely to evaluate
// `text LIKE pattern ESCAPE '\'` — SQLite is the oracle for escapeLike, since
// the prefilter's correctness is defined by SQLite's own LIKE semantics.
func openMemLike(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func sqliteLike(t fatalf, db *sql.DB, text, pattern string) bool {
	t.Helper()
	var b int
	if err := db.QueryRow(`SELECT (? LIKE ? ESCAPE '\')`, text, pattern).Scan(&b); err != nil {
		t.Fatalf("LIKE eval (text=%q pattern=%q): %v", text, pattern, err)
	}
	return b == 1
}

// P1 (oracle, escapeLike): on the ASCII domain prefilterUsable admits, the LIKE
// prefilter agrees exactly with the Go matcher. The forward direction is the
// critical one — grep.go promises the prefilter "must never drop a real match".
func TestProp_EscapeLike_MatchesGoMatcherOnASCII(t *testing.T) {
	db := openMemLike(t)
	rapid.Check(t, func(rt *rapid.T) {
		lit := printableASCII(rt, "needle")
		hay := printableASCII(rt, "haystack")
		pattern := "%" + escapeLike(lit) + "%"

		like := sqliteLike(rt, db, hay, pattern)
		goMatch := strings.Contains(strings.ToLower(hay), strings.ToLower(lit))

		if goMatch && !like {
			rt.Fatalf("prefilter DROPPED a real match: lit=%q hay=%q escaped=%q", lit, hay, escapeLike(lit))
		}
		if like != goMatch {
			rt.Fatalf("LIKE/matcher divergence: lit=%q hay=%q escaped=%q LIKE=%v go=%v",
				lit, hay, escapeLike(lit), like, goMatch)
		}
	})
}

// P1b (invariant, escapeLike): an escaped literal is a pure literal — it always
// matches itself under LIKE, for arbitrary input including metacharacters and
// non-ASCII, because escaping leaves a byte-identical pattern.
func TestProp_EscapeLike_LiteralMatchesItself(t *testing.T) {
	db := openMemLike(t)
	rapid.Check(t, func(rt *rapid.T) {
		lit := rapid.String().Draw(rt, "literal")
		if !sqliteLike(rt, db, lit, escapeLike(lit)) {
			rt.Fatalf("escaped literal must match itself: lit=%q escaped=%q", lit, escapeLike(lit))
		}
	})
}

// P1c (invariant, escapeLike): every metacharacter is neutralized by exactly one
// added backslash, so the escaped length equals the input length plus one per
// special byte — no special survives as a wildcard, none is over-escaped.
func TestProp_EscapeLike_LengthAccountsForEverySpecial(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		lit := printableASCII(rt, "literal")
		specials := strings.Count(lit, `\`) + strings.Count(lit, "%") + strings.Count(lit, "_")
		if got, want := len(escapeLike(lit)), len(lit)+specials; got != want {
			rt.Fatalf("escapeLike(%q)=%q: len %d, want %d (specials=%d)", lit, escapeLike(lit), got, want, specials)
		}
	})
}

// P2 (invariant, prefilterUsable): the gate is exactly "non-regex AND ASCII AND
// free of the over-pruning bytes". This pins the boundary that decides whether
// the (only-an-optimization) LIKE path runs at all.
func TestProp_PrefilterUsable_Definition(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		q := rapid.String().Draw(rt, "query")
		regex := rapid.Bool().Draw(rt, "regex")

		want := !regex && isASCII(q) && !strings.ContainsAny(q, "\"\\\n\r")
		if prefilterUsable(q, regex) != want {
			rt.Fatalf("prefilterUsable(%q, %v)=%v, want %v", q, regex, prefilterUsable(q, regex), want)
		}
		if regex && prefilterUsable(q, true) {
			rt.Fatalf("regex query must never use the prefilter: %q", q)
		}
	})
}

// P3 (invariant, sortEpoch): a future timestamp is sunk to 0 and can never
// outrank a present one; a present-or-past timestamp is returned unchanged.
func TestProp_SortEpoch_FutureSinksBelowPresent(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		now := rapid.Int64Range(0, 1<<40).Draw(rt, "now")
		epoch := rapid.Int64Range(-(1<<40), 1<<41).Draw(rt, "epoch")

		got := sortEpoch(epoch, now)
		if epoch > now {
			if got != 0 {
				rt.Fatalf("future epoch %d (now %d) must sink to 0, got %d", epoch, now, got)
			}
			if now > 0 && got >= sortEpoch(now, now) {
				rt.Fatalf("future must not outrank present: future=%d present=%d", got, sortEpoch(now, now))
			}
		} else if got != epoch {
			rt.Fatalf("present/past epoch %d (now %d) must pass through, got %d", epoch, now, got)
		}
		if got > now {
			rt.Fatalf("sortEpoch result %d exceeds now %d", got, now)
		}
	})
}

// P4 (roundtrip, msToTime): nonzero milliseconds round-trip through time.Time;
// zero is the sentinel for "no timestamp" and maps to the zero Time.
func TestProp_MsToTime_RoundTrips(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		ms := rapid.Int64Range(1, 9_999_999_999_999).Draw(rt, "ms")
		if got := msToTime(ms).UnixMilli(); got != ms {
			rt.Fatalf("msToTime(%d).UnixMilli()=%d, want round-trip", ms, got)
		}
	})
	if !msToTime(0).IsZero() {
		t.Fatalf("msToTime(0) must be the zero Time")
	}
}

// P5 (metamorphic, idCorruptsRow): equals the tab/newline/CR membership test and
// distributes over concatenation, so no split of the id hides a corrupting byte.
func TestProp_IdCorruptsRow_DistributesOverConcat(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapid.String().Draw(rt, "a")
		b := rapid.String().Draw(rt, "b")

		if idCorruptsRow(a) != strings.ContainsAny(a, "\t\n\r") {
			rt.Fatalf("idCorruptsRow(%q) disagrees with membership test", a)
		}
		if idCorruptsRow(a+b) != (idCorruptsRow(a) || idCorruptsRow(b)) {
			rt.Fatalf("idCorruptsRow not distributive over concat: a=%q b=%q", a, b)
		}
	})
}

// P6 (metamorphic, isASCII): equals the all-runes-≤0x7f test and is conjunctive
// over concatenation.
func TestProp_IsASCII_ConjunctiveOverConcat(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapid.String().Draw(rt, "a")
		b := rapid.String().Draw(rt, "b")

		want := true
		for _, r := range a {
			if r > 0x7f {
				want = false
			}
		}
		if isASCII(a) != want {
			rt.Fatalf("isASCII(%q)=%v, want %v", a, isASCII(a), want)
		}
		if isASCII(a+b) != (isASCII(a) && isASCII(b)) {
			rt.Fatalf("isASCII not conjunctive over concat: a=%q b=%q", a, b)
		}
	})
}

type sessionSeed struct {
	id        string
	updatedMs int64
}

// buildFromSeeds materializes the seeds into a throwaway SQLite db, runs the
// real scanQuery, and feeds the rows through buildSessions with an injected
// clock. Every handle is closed before return so the 100-iteration property
// run doesn't leak connections.
func buildFromSeeds(t *testing.T, seeds []sessionSeed, allow map[string]struct{}, now time.Time) []*session.Session {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	u := url.URL{Scheme: "file", Path: path}
	w, err := sql.Open("sqlite3", u.String())
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if _, err := w.Exec(schemaSQL); err != nil {
		t.Fatalf("schema: %v", err)
	}
	for _, s := range seeds {
		if _, err := w.Exec(`INSERT INTO session
			(id, parent_id, directory, title, time_created, time_updated, time_archived)
			VALUES (?, NULL, ?, ?, ?, ?, NULL)`,
			s.id, "/p/x", "title", s.updatedMs, s.updatedMs); err != nil {
			t.Fatalf("insert %q: %v", s.id, err)
		}
	}
	w.Close()

	d, err := openAt(path)
	if err != nil {
		t.Fatalf("openAt: %v", err)
	}
	defer d.Close()
	rows, err := d.query(scanQuery)
	if err != nil {
		t.Fatalf("scanQuery: %v", err)
	}
	defer rows.Close()
	got, err := buildSessions(rows, allow, now)
	if err != nil {
		t.Fatalf("buildSessions: %v", err)
	}
	return got
}

// P7 (stateful/invariant, buildSessions): over a randomly seeded session table,
// the scan projection drops exactly the corrupt and disallowed ids, keeps every
// other root, and emits them sorted by (sortEpoch desc, id asc) against a fixed
// clock — the global ordering sortEpoch only justifies per-pair.
func TestProp_BuildSessions_FiltersAndOrders(t *testing.T) {
	const nowEpoch int64 = 3_000_000_000 // seconds; ~year 2065
	now := time.Unix(nowEpoch, 0)

	rapid.Check(t, func(rt *rapid.T) {
		// Distinct ids (the PK); ~1/8 carry a tab so the corruption filter is exercised.
		idGen := rapid.Custom(func(rt *rapid.T) string {
			base := rapid.StringOfN(rapid.RuneFrom(idRunes), 1, 8, -1).Draw(rt, "base")
			if rapid.IntRange(0, 7).Draw(rt, "corrupt") == 0 {
				return base + "\t" + base
			}
			return base
		})
		seeds := rapid.SliceOfNDistinct(
			rapid.Custom(func(rt *rapid.T) sessionSeed {
				return sessionSeed{
					id: idGen.Draw(rt, "id"),
					// Straddle now (in ms) so sortEpoch's future-sink fires.
					updatedMs: rapid.Int64Range(0, 2*nowEpoch*1000).Draw(rt, "updatedMs"),
				}
			}),
			0, 20,
			func(s sessionSeed) string { return s.id },
		).Draw(rt, "seeds")

		// allow: nil (keep all) ~1/3 of the time, else a random subset.
		var allow map[string]struct{}
		if rapid.IntRange(0, 2).Draw(rt, "useAllow") != 0 {
			allow = map[string]struct{}{}
			for _, s := range seeds {
				if rapid.Bool().Draw(rt, "pick:"+s.id) {
					allow[s.id] = struct{}{}
				}
			}
		}

		got := buildFromSeeds(t, seeds, allow, now)

		// Expected membership: roots that are non-corrupt and allowed.
		want := map[string]struct{}{}
		for _, s := range seeds {
			if idCorruptsRow(s.id) {
				continue
			}
			if allow != nil {
				if _, ok := allow[s.id]; !ok {
					continue
				}
			}
			want[s.id] = struct{}{}
		}
		if len(got) != len(want) {
			rt.Fatalf("got %d sessions, want %d (seeds=%v allow=%v)", len(got), len(want), seeds, allow)
		}
		for _, g := range got {
			if _, ok := want[g.ID]; !ok {
				rt.Fatalf("unexpected session %q in output", g.ID)
			}
			if idCorruptsRow(g.ID) {
				rt.Fatalf("corrupt id leaked into output: %q", g.ID)
			}
		}

		// Ordering: sortEpoch(epoch, now) desc, then id asc — recomputed independently.
		type key struct {
			epoch int64
			id    string
		}
		keyed := make([]key, len(got))
		for i, g := range got {
			keyed[i] = key{sortEpoch(g.LastEpoch, nowEpoch), g.ID}
		}
		expected := append([]key(nil), keyed...)
		sort.SliceStable(expected, func(i, j int) bool {
			if expected[i].epoch != expected[j].epoch {
				return expected[i].epoch > expected[j].epoch
			}
			return expected[i].id < expected[j].id
		})
		for i := range keyed {
			if keyed[i] != expected[i] {
				rt.Fatalf("ordering violated at %d: got %+v, want %+v (full=%+v)", i, keyed[i], expected[i], keyed)
			}
		}
	})
}

// P8 (metamorphic safety): the documented invariant end-to-end. Whenever
// prefilterUsable admits the query, the LIKE prefilter must never drop a match
// the Go matcher would make — even against haystacks seeded with the Unicode
// code points that lower-case onto ASCII letters (U+0130, U+212A KELVIN SIGN).
// Without prefilterUsable's case-fold guard this fails on, e.g., q="k" against a
// KELVIN-bearing haystack; with the guard those queries bypass the prefilter and
// the antecedent is simply false.
func TestProp_Prefilter_NeverDropsRealMatch(t *testing.T) {
	db := openMemLike(t)
	// Haystack alphabet: printable ASCII plus the case-fold troublemakers
	// (U+0130, U+212A) and a couple of ordinary non-ASCII letters.
	hayRunes := append(append([]rune{}, printableASCIIRunes...), '\u0130', '\u212a', '\u00e9', '\u03a9')
	rapid.Check(t, func(rt *rapid.T) {
		q := printableASCII(rt, "query")
		hay := rapid.StringOfN(rapid.RuneFrom(hayRunes), 0, 16, -1).Draw(rt, "haystack")
		if !prefilterUsable(q, false) {
			return // prefilter bypassed; the full Go scan is authoritative there
		}
		goMatch := strings.Contains(strings.ToLower(hay), strings.ToLower(q))
		if goMatch && !sqliteLike(rt, db, hay, "%"+escapeLike(q)+"%") {
			rt.Fatalf("prefilter dropped a real match: q=%q hay=%q", q, hay)
		}
	})
}
