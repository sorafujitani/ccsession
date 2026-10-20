package kiro

import (
	"strconv"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/sorafujitani/ccsession/internal/jsonscan"
)

// extractClassicLabel is replaced by tests that count label extractions.
var extractClassicLabel = classicLastPrompt

var classicPromptPath = []string{"user", "content", "Prompt", "prompt"}

// classicLastPrompt returns what classicCachedLabelQuery's SQL fallback
// returns for strict JSON: the prompt of the last $.history element whose
// user.content.Prompt.prompt is a string, or "" without one.
//
// It reports ok=false whenever SQLite might read the value differently: JSON5
// or malformed text, a non-array history, string history elements, escaped
// top-level keys or keys on the label path, duplicate keys on that path, and
// NUL or unpaired surrogate escapes in the chosen prompt. Those rows keep using SQLite, which also keeps its errors.
func classicLastPrompt(raw []byte) (string, bool) {
	var prompt []byte
	seenHistory := false
	end := jsonscan.Object(raw, 0, 0, func(key []byte, escaped bool, i int) int {
		if escaped {
			return -1
		}
		if string(key) != "history" {
			return jsonscan.Value(raw, i, 1)
		}
		if seenHistory {
			return -1
		}
		seenHistory = true
		return jsonscan.Array(raw, i, 1, func(i int) int {
			// jsonb_each yields a string element as SQL text, which json_type
			// then parses as JSON; leave that reinterpretation to SQLite.
			if i = jsonscan.Space(raw, i); i < len(raw) && raw[i] == '"' {
				return -1
			}
			token, end := classicPath(raw, i, 2, classicPromptPath)
			if token != nil {
				prompt = token
			}
			return end
		})
	})
	if end < 0 || jsonscan.Space(raw, end) != len(raw) {
		return "", false
	}
	if prompt == nil {
		return "", true
	}
	return unquoteClassicString(prompt)
}

// classicPath consumes the value at i, nested in depth containers, and
// returns the string token found at keys, if any. Values that are not objects
// simply do not match, as with json_type.
func classicPath(b []byte, i, depth int, keys []string) ([]byte, int) {
	i = jsonscan.Space(b, i)
	if len(keys) == 0 {
		if i < len(b) && b[i] == '"' {
			end := jsonscan.String(b, i)
			if end < 0 {
				return nil, -1
			}
			return b[i:end], end
		}
		return nil, jsonscan.Value(b, i, depth)
	}
	if i >= len(b) || b[i] != '{' {
		return nil, jsonscan.Value(b, i, depth)
	}
	var token []byte
	seen := false
	end := jsonscan.Object(b, i, depth, func(key []byte, escaped bool, v int) int {
		if escaped || (seen && string(key) == keys[0]) {
			return -1
		}
		if string(key) != keys[0] {
			return jsonscan.Value(b, v, depth+1)
		}
		seen = true
		var end int
		token, end = classicPath(b, v, depth+1, keys[1:])
		return end
	})
	return token, end
}

// unquoteClassicString decodes a validated string token the way SQLite's
// json_extract does, copying non-escape bytes unchanged.
func unquoteClassicString(token []byte) (string, bool) {
	s := token[1 : len(token)-1]
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		if c != '\\' {
			out = append(out, c)
			i++
			continue
		}
		switch s[i+1] {
		case 'b':
			out = append(out, '\b')
		case 'f':
			out = append(out, '\f')
		case 'n':
			out = append(out, '\n')
		case 'r':
			out = append(out, '\r')
		case 't':
			out = append(out, '\t')
		case 'u':
			r := hex4(s[i+2 : i+6])
			i += 6
			if utf16.IsSurrogate(r) {
				if r >= 0xdc00 || i+6 > len(s) || s[i] != '\\' || s[i+1] != 'u' {
					return "", false
				}
				low := hex4(s[i+2 : i+6])
				r = utf16.DecodeRune(r, low)
				if r == utf8.RuneError {
					return "", false
				}
				i += 6
			}
			if r == 0 {
				return "", false
			}
			out = utf8.AppendRune(out, r)
			continue
		default: // '"', '\\', '/'
			out = append(out, s[i+1])
		}
		i += 2
	}
	return string(out), true
}

// hex4 decodes the four hex digits of a validated \u escape.
func hex4(b []byte) rune {
	r, _ := strconv.ParseUint(string(b), 16, 32)
	return rune(r)
}
