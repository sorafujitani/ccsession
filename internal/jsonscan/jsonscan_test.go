package jsonscan

import (
	"encoding/json"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

func depth(b []byte) int {
	d, maxD, inString, escaped := 0, 0, false, false
	for _, c := range b {
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && (c == '{' || c == '['):
			d++
			maxD = max(maxD, d)
		case !inString && (c == '}' || c == ']'):
			d--
		}
	}
	return maxD
}

// checkValid requires Value to agree with encoding/json on whole inputs,
// except that Value rejects nesting deeper than MaxDepth.
func checkValid(t interface{ Fatalf(string, ...any) }, b []byte) {
	end := Value(b, 0, 0)
	got := end >= 0 && Space(b, end) == len(b)
	want := json.Valid(b)
	if got && !want {
		t.Fatalf("accepted invalid JSON %q", b)
	}
	if want && !got && depth(b) <= MaxDepth {
		t.Fatalf("rejected valid JSON %q", b)
	}
}

func TestValueCases(t *testing.T) {
	for _, s := range []string{
		``, ` `, `0`, `-0`, `01`, `1.`, `.5`, `1e`, `1e+`, `1E-7`, `-`, `+1`, `[1,2,,3]`, `[1,]`, `[,1]`,
		`[1 2]`, `{"a":1,}`, `{"a" 1}`, `{1:2}`, `{"a":1}x`, `"\u12"`, `"\x"`, "\"\t\"", `"\ud800"`,
		"\"\xff\"", `tru`, `nulll`, `[true,false,null]`, `{"a":{"b":[{"c":"d"}]}}`, ` [ 1 , [ ] , { } ] `,
		`[01]`, `[-01]`, `[1,-2,3.5,4e2]`, `[1,02]`, `{"a":[1,2,3],"b":"\"\\\/\b\f\n\r\t"}`,
		strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth),
	} {
		checkValid(t, []byte(s))
	}
	if end := Value([]byte(strings.Repeat("[", MaxDepth+1)+strings.Repeat("]", MaxDepth+1)), 0, 0); end >= 0 {
		t.Fatal("MaxDepth was not enforced")
	}
}

func TestValueMatchesEncodingJSON(t *testing.T) {
	alphabet := []string{"{", "}", "[", "]", ",", ":", `"`, `\`, "0", "1", "9", "-", "+", ".", "e", "E",
		" ", "\n", "t", "true", "false", "null", `"a"`, `"é"`, `\u`, "\x01", "\xff", "a"}
	rapid.Check(t, func(t *rapid.T) {
		parts := rapid.SliceOfN(rapid.SampledFrom(alphabet), 0, 24).Draw(t, "parts")
		checkValid(t, []byte(strings.Join(parts, "")))
	})
}

func TestValueAcceptsEncodedValues(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		v := rapid.OneOf(
			rapid.Map(rapid.String(), func(s string) any { return s }),
			rapid.Map(rapid.SliceOf(rapid.Int()), func(s []int) any { return s }),
			rapid.Map(rapid.MapOf(rapid.String(), rapid.Float64()), func(m map[string]float64) any { return m }),
		).Draw(t, "value")
		b, err := json.MarshalIndent(map[string]any{"v": v, "list": []any{v, nil, true}}, "", rapid.SampledFrom([]string{"", " ", "\t"}).Draw(t, "indent"))
		if err != nil {
			t.Skip()
		}
		if end := Value(b, 0, 0); end != len(b) {
			t.Fatalf("rejected encoder output %s", b)
		}
		checkValid(t, b)
	})
}

func FuzzValue(f *testing.F) {
	for _, s := range []string{`{"a":[1,2,{"b":"c"}]}`, `[1,-0.5e3,"é"]`, `{"a":1,}`, `"\ud800"`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		checkValid(t, b)
	})
}

func TestObjectAndArray(t *testing.T) {
	b := []byte(` {"a" : [1, {"x":2}], "b\n":"s", "c":{}} `)
	var keys []string
	end := Object(b, 0, 0, func(key []byte, escaped bool, i int) int {
		keys = append(keys, string(key))
		if string(key) == "a" {
			n := 0
			end := Array(b, i, 1, func(i int) int { n++; return Value(b, i, 2) })
			if n != 2 {
				return -1
			}
			return end
		}
		if escaped != (string(key) == `b\n`) {
			return -1
		}
		return Value(b, i, 1)
	})
	if end != len(b)-1 || strings.Join(keys, ",") != `a,b\n,c` {
		t.Fatalf("end=%d keys=%v", end, keys)
	}
	for _, bad := range []string{`{"a":1`, `{"a":1,}`, `{"a"}`, `[1,]`, `[`, `{`, `x`} {
		if Object([]byte(bad), 0, 0, func(_ []byte, _ bool, i int) int { return Value([]byte(bad), i, 1) }) >= 0 &&
			Array([]byte(bad), 0, 0, func(i int) int { return Value([]byte(bad), i, 1) }) >= 0 {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSWARMatchesScalar(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		alphabet := rapid.SampledFrom([]string{"0123456789,", "0123456789,,0-.e] \x80", "ab\"\\\x01\x1f \x7f\xff\xc3\xa9"}).Draw(t, "alphabet")
		raw := rapid.SliceOfN(rapid.SampledFrom([]byte(alphabet)), 0, 64).Draw(t, "bytes")
		for _, in := range []bool{true, false} {
			if got, want := numbers(raw, 0, in), referenceNumbers(raw, 0, in); got != want {
				t.Fatalf("numbers(%q, %v) = %d, want %d", raw, in, got, want)
			}
		}
		quoted := append(append([]byte{'"'}, raw...), '"')
		if got, want := String(quoted, 0), referenceString(quoted, 0); got != want {
			t.Fatalf("String(%q) = %d, want %d", quoted, got, want)
		}
		checkValid(t, append(append([]byte{'['}, raw...), ']'))
	})
}
