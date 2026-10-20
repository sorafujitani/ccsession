package kiro

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sorafujitani/ccsession/internal/jsonscan"
	"pgregory.net/rapid"
)

const sqliteClassicLabel = `SELECT COALESCE((
	SELECT json_extract(h.value, '$.user.content.Prompt.prompt')
	FROM jsonb_each(?, '$.history') h
	WHERE json_type(h.value, '$.user.content.Prompt.prompt') = 'text'
	ORDER BY CAST(h.key AS INTEGER) DESC LIMIT 1
), '')`

func openLabelDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

// checkClassicLabel requires native extraction to agree with SQLite whenever
// it does not decline, including SQLite errors that it must not hide.
func checkClassicLabel(t interface {
	Helper()
	Fatalf(string, ...any)
}, db *sql.DB, raw string) bool {
	t.Helper()
	got, ok := classicLastPrompt([]byte(raw))
	var want string
	err := db.QueryRow(sqliteClassicLabel, raw).Scan(&want)
	if ok && err != nil {
		t.Fatalf("native accepted what SQLite rejects (%v): %q", err, raw)
	}
	if ok && got != want {
		t.Fatalf("label mismatch: native=%q sqlite=%q for %q", got, want, raw)
	}
	return ok
}

func TestClassicLastPromptCases(t *testing.T) {
	db := openLabelDB(t)
	for _, tc := range []struct {
		raw    string
		accept bool
	}{
		{`{"history":[]}`, true},
		{`{}`, true},
		{` {"a":1,"history":[{"user":{"content":{"Prompt":{"prompt":"one"}}}},{"user":{"content":{"Prompt":{"prompt":"two"}}}}]} `, true},
		{`{"history":[{"user":{"content":{"Prompt":{"prompt":"one"}}}},{"user":{"content":{"ToolUseResults":{}}}}]}`, true},
		{`{"history":[{"user":{"content":{"Prompt":{"prompt":7}}}},1,null,[],{}]}`, true},
		{`{"history":[{"user":{"content":{"Prompt":{"prompt":"a"}}}},"x"]}`, false},
		{`{"history":[{"user":{"content":{"Prompt":{"prompt":"esc \"\\\/\b\f\n\r\t é 😀"}}}}]}`, true},
		{`{"history":[{"user":{"content":{"Prompt":{"prompt":"a"}}}}],"n":[-0,1.5e+3,2E-2,0.25,true,false,null]}`, true},
		{`{"history":[{"user":{"content":{"Prompt":{"prompt":"lone \ud83d"}}}}]}`, false},
		{`{"history":[{"user":{"content":{"Prompt":{"prompt":"nul \u0000"}}}}]}`, false},
		{`{"history":[],"history":[]}`, false},
		{`{"hist\u006fry":[{"user":{"content":{"Prompt":{"prompt":"escaped key"}}}}]}`, false},
		{`{"history":[{"user":{"content":{"Prompt":{"prompt":"a","prompt":"b"}}}}]}`, false},
		{`{"history":{"0":{"user":{"content":{"Prompt":{"prompt":"obj"}}}}}}`, false},
		{`{"history":null}`, false},
		{`[1,2]`, false},
		{`{"history":[]} x`, false},
		{`{"history":[],}`, false},
		{`{'history':[]}`, false},
		{`{"history":[01]}`, false},
		{`{"history":[1.]}`, false},
		{`{"history":[+1]}`, false},
		{`{"history":["tab	in string"]}`, false},
		{`{"history":[` + strings.Repeat("[", jsonscan.MaxDepth) + strings.Repeat("]", jsonscan.MaxDepth) + `]}`, false},
		{`invalid JSON`, false},
		{``, false},
	} {
		if got := checkClassicLabel(t, db, tc.raw); got != tc.accept {
			t.Errorf("accept=%v, want %v for %q", got, tc.accept, tc.raw)
		}
	}
}

func TestClassicLastPromptMatchesSQLite(t *testing.T) {
	db := openLabelDB(t)
	rapid.Check(t, func(t *rapid.T) {
		raw := rapid.OneOf(classicJSONGen(0), classicConversationGen()).Draw(t, "conversation")
		if rapid.Bool().Draw(t, "mutate") && len(raw) > 0 {
			pos := rapid.IntRange(0, len(raw)-1).Draw(t, "pos")
			b := rapid.SampledFrom([]string{"", ",", "]", "}", "\"", "\\", "\t", "0", " ", "\x01", "\xff", "'"}).Draw(t, "byte")
			raw = raw[:pos] + b + raw[pos+rapid.IntRange(0, 1).Draw(t, "drop"):]
		}
		checkClassicLabel(t, db, raw)
	})
}

// Canonical encoder output must stay on the native path; otherwise the
// optimization would silently disappear.
func TestClassicLastPromptAcceptsEncodedConversations(t *testing.T) {
	db := openLabelDB(t)
	rapid.Check(t, func(t *rapid.T) {
		history := []any{}
		for _, prompt := range rapid.SliceOf(rapid.String()).Draw(t, "prompts") {
			history = append(history, map[string]any{
				"user":      map[string]any{"content": map[string]any{"Prompt": map[string]any{"prompt": prompt}}},
				"assistant": map[string]any{"Response": map[string]any{"content": prompt, "n": []int{1, -2, 30}}},
			})
		}
		raw, err := json.Marshal(map[string]any{"conversation_id": "id", "history": history})
		if err != nil {
			t.Fatal(err)
		}
		if !checkClassicLabel(t, db, string(raw)) {
			// Go escapes invalid UTF-8 as U+FFFD, so declines need a NUL.
			if !strings.Contains(string(raw), `\u0000`) {
				t.Fatalf("declined canonical JSON: %s", raw)
			}
		}
	})
}

var classicStrings = []string{
	`""`, `"prompt"`, `"Prompt"`, `"history"`, `"user"`, `"content"`, `"x\ny"`,
	`"é😀"`, `"\ud800"`, `"\u0000"`, `"\""`, `"\\"`, `"\/"`, `"é"`, "\"\xff\"",
}

func classicJSONGen(depth int) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		space := func() string {
			return rapid.SampledFrom([]string{"", "", " ", "\n\t", "\r "}).Draw(t, "space")
		}
		kind := rapid.IntRange(0, 9).Draw(t, "kind")
		if depth >= 4 {
			kind = rapid.IntRange(0, 3).Draw(t, "leaf")
		}
		switch kind {
		case 0:
			return rapid.SampledFrom(classicStrings).Draw(t, "string")
		case 1:
			return rapid.SampledFrom([]string{"0", "-1", "12.5", "1e9", "-0.0E-3", "true", "false", "null"}).Draw(t, "scalar")
		case 2, 3:
			return fmt.Sprintf("%q", rapid.String().Draw(t, "text"))
		case 4, 5:
			n := rapid.IntRange(0, 3).Draw(t, "len")
			parts := make([]string, n)
			for i := range parts {
				parts[i] = space() + classicJSONGen(depth+1).Draw(t, "elem") + space()
			}
			return "[" + strings.Join(parts, ",") + "]"
		default:
			keys := []string{`"history"`, `"user"`, `"content"`, `"Prompt"`, `"prompt"`, `"other"`, `"history"`}
			n := rapid.IntRange(0, 4).Draw(t, "members")
			parts := make([]string, n)
			for i := range parts {
				key := rapid.SampledFrom(keys).Draw(t, "key")
				var value string
				switch key {
				case `"history"`:
					m := rapid.IntRange(0, 3).Draw(t, "turns")
					turns := make([]string, m)
					for j := range turns {
						turns[j] = classicTurnGen(depth+1).Draw(t, "turn")
					}
					value = "[" + strings.Join(turns, ",") + "]"
				default:
					value = classicJSONGen(depth+1).Draw(t, "value")
				}
				parts[i] = space() + key + space() + ":" + space() + value + space()
			}
			return "{" + strings.Join(parts, ",") + "}"
		}
	})
}

// classicConversationGen keeps the Kiro shape, so most draws reach the
// label comparison instead of declining early.
func classicConversationGen() *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		turns := make([]string, rapid.IntRange(0, 4).Draw(t, "turns"))
		for i := range turns {
			turns[i] = classicTurnGen(1).Draw(t, "turn")
		}
		members := []string{`"history":[` + strings.Join(turns, ",") + `]`}
		for range rapid.IntRange(0, 2).Draw(t, "extra") {
			members = append(members, `"other":`+classicJSONGen(1).Draw(t, "other"))
		}
		order := rapid.Permutation(members).Draw(t, "order")
		return "{" + strings.Join(order, ",") + "}"
	})
}

func classicTurnGen(depth int) *rapid.Generator[string] {
	return rapid.Custom(func(t *rapid.T) string {
		prompt := rapid.OneOf(
			rapid.SampledFrom(classicStrings),
			rapid.Map(rapid.String(), func(s string) string { return fmt.Sprintf("%q", s) }),
			rapid.Map(rapid.String(), func(s string) string { return fmt.Sprintf("%q", s) }),
			classicJSONGen(depth+1),
		).Draw(t, "prompt")
		shape := rapid.SampledFrom([]string{
			`{"user":{"content":{"Prompt":{"prompt":%s}}}}`,
			`{"assistant":{},"user":{"timestamp":"t","content":{"Prompt":{"prompt":%s}}}}`,
			`{"user":{"content":{"ToolUseResults":%s}}}`,
			`{"user":{"content":{"Prompt":{"prompt":%s},"Prompt":{}}}}`,
			`[%s]`,
		}).Draw(t, "shape")
		return fmt.Sprintf(shape, prompt)
	})
}
