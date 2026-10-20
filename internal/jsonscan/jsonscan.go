// Package jsonscan validates and skips strict RFC 8259 JSON without
// decoding it. Session stores are dominated by bodies that list scans never
// need, so callers locate a few fields and leave everything else unparsed.
//
// Every function takes an index into b and returns the index just past what
// it consumed, or -1 when the input is not strict JSON. Callers that need
// another parser's exact semantics should treat -1 as "ask that parser".
package jsonscan

import (
	"encoding/binary"
	"math/bits"
)

// MaxDepth is the deepest nesting accepted. It stays below the limits of
// SQLite (1000) and encoding/json (10000), so nothing accepted here is
// rejected by either for depth.
const MaxDepth = 900

// Space skips JSON whitespace.
func Space(b []byte, i int) int {
	for i < len(b) && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	return i
}

// safe marks bytes that need no attention inside a JSON string. Bytes from
// 0x80 are passed through, as encoding/json and SQLite both do.
var safe = func() (safe [256]bool) {
	for c := 0x20; c < 256; c++ {
		safe[c] = c != '"' && c != '\\'
	}
	return safe
}()

// String consumes a string starting at its opening quote. Most session bytes
// are string contents, so the safe-byte loop is the scanner's hot path.
func String(b []byte, i int) int {
	if i < 0 || i >= len(b) || b[i] != '"' {
		return -1
	}
	i++
	for {
		// Skip eight safe bytes at a time, then finish with the table.
		for i+8 <= len(b) {
			if m := special(binary.LittleEndian.Uint64(b[i:])); m != 0 {
				i += bits.TrailingZeros64(m) / 8
				break
			}
			i += 8
		}
		for i < len(b) && safe[b[i]] {
			i++
		}
		if i >= len(b) {
			return -1
		}
		switch b[i] {
		case '"':
			return i + 1
		case '\\':
			if i+1 >= len(b) {
				return -1
			}
			switch b[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if i+6 > len(b) {
					return -1
				}
				for _, h := range b[i+2 : i+6] {
					if unhex(h) < 0 {
						return -1
					}
				}
				i += 6
			default:
				return -1
			}
		default:
			return -1 // Unescaped control character.
		}
	}
}

// Value consumes any value after optional whitespace. depth is the nesting
// level of the value's container. Bodies are mostly skipped, so containers
// share one loop instead of a call per element.
func Value(b []byte, i, depth int) int {
	var stack []byte // Open containers: '{' or '['.
	for {
		// A value is expected at i.
		i = Space(b, i)
		if i >= len(b) {
			return -1
		}
		switch c := b[i]; c {
		case '{', '[':
			if depth+len(stack) >= MaxDepth {
				return -1
			}
			stack = append(stack, c)
			i = Space(b, i+1)
			if i < len(b) && b[i] == c+2 { // '}' or ']'
				stack = stack[:len(stack)-1]
				i++
				break
			}
			if c == '{' {
				if i = key(b, i); i < 0 {
					return -1
				}
			}
			continue
		case '"':
			i = String(b, i)
		case 't':
			i = literal(b, i, "true")
		case 'f':
			i = literal(b, i, "false")
		case 'n':
			i = literal(b, i, "null")
		default:
			i = numbers(b, i, len(stack) > 0 && stack[len(stack)-1] == '[')
		}
		if i < 0 {
			return -1
		}
		// Close finished containers until another value is expected.
		for {
			if len(stack) == 0 {
				return i
			}
			i = Space(b, i)
			if i >= len(b) {
				return -1
			}
			top := stack[len(stack)-1]
			if b[i] == top+2 {
				stack = stack[:len(stack)-1]
				i++
				continue
			}
			if b[i] != ',' {
				return -1
			}
			i++
			if top == '{' {
				if i = key(b, Space(b, i)); i < 0 {
					return -1
				}
			}
			break
		}
	}
}

// Object walks the object at b[i] (after optional whitespace). member gets the
// raw key, whether it holds an escape, and the index of the member value; it
// returns the index past the value it consumed, or -1 to stop.
func Object(b []byte, i, depth int, member func(key []byte, escaped bool, value int) int) int {
	i = Space(b, i)
	if i >= len(b) || b[i] != '{' || depth >= MaxDepth {
		return -1
	}
	if i = Space(b, i+1); i < len(b) && b[i] == '}' {
		return i + 1
	}
	for {
		start := Space(b, i)
		end := String(b, start)
		if end < 0 {
			return -1
		}
		k := b[start+1 : end-1]
		escaped := false
		for _, c := range k {
			if c == '\\' {
				escaped = true
				break
			}
		}
		if i = Space(b, end); i >= len(b) || b[i] != ':' {
			return -1
		}
		if i = member(k, escaped, i+1); i < 0 {
			return -1
		}
		if i = Space(b, i); i >= len(b) {
			return -1
		}
		switch b[i] {
		case '}':
			return i + 1
		case ',':
			i++
		default:
			return -1
		}
	}
}

// Array walks the array at b[i] (after optional whitespace). element gets the
// index of each element and returns the index past it, or -1 to stop.
func Array(b []byte, i, depth int, element func(value int) int) int {
	i = Space(b, i)
	if i >= len(b) || b[i] != '[' || depth >= MaxDepth {
		return -1
	}
	if i = Space(b, i+1); i < len(b) && b[i] == ']' {
		return i + 1
	}
	for {
		if i = element(i); i < 0 {
			return -1
		}
		if i = Space(b, i); i >= len(b) {
			return -1
		}
		switch b[i] {
		case ']':
			return i + 1
		case ',':
			i++
		default:
			return -1
		}
	}
}

// key consumes a member name and its colon.
func key(b []byte, i int) int {
	if i = String(b, i); i < 0 {
		return -1
	}
	if i = Space(b, i); i >= len(b) || b[i] != ':' {
		return -1
	}
	return i + 1
}

func literal(b []byte, i int, word string) int {
	if len(b)-i < len(word) || string(b[i:i+len(word)]) != word {
		return -1
	}
	return i + len(word)
}

// numbers consumes a number and, inside an array, the ",number" elements
// that directly follow it. Sessions can embed images as byte arrays, so
// plain integers take a short path without a loop iteration per element.
func numbers(b []byte, i int, inArray bool) int {
	for {
		if start := i; i < len(b) && b[i] >= '1' && b[i] <= '9' {
			i++
			for i < len(b) && b[i] >= '0' && b[i] <= '9' {
				i++
			}
			if i < len(b) && (b[i] == '.' || b[i] == 'e' || b[i] == 'E') {
				i = number(b, start)
			}
		} else {
			i = number(b, start)
		}
		if i < 0 || !inArray || i+1 >= len(b) || b[i] != ',' {
			return i
		}
		if c := b[i+1]; c != '-' && (c < '0' || c > '9') {
			return i
		}
		i = intRun(b, i+1)
	}
}

// Byte-parallel ("SWAR") helpers over eight bytes in a uint64. Each mask has
// the high bit set in the bytes it reports; no addition carries across bytes.
const (
	low7 = 0x7f7f7f7f7f7f7f7f
	high = 0x8080808080808080
)

// equal marks the bytes of x equal to the byte repeated in c.
func equal(x, c uint64) uint64 {
	y := x ^ c
	return ^((y&low7 + low7) | y) & high
}

// special marks bytes a string scan must stop at: '"', '\\', and controls.
func special(x uint64) uint64 {
	control := (x&low7+0x6060606060606060|x)&high ^ high // Below 0x20.
	return control | equal(x, 0x2222222222222222) | equal(x, 0x5c5c5c5c5c5c5c5c)
}

// intRun skips whole elements of an integer array eight bytes at a time,
// starting at the element at i. It returns the start of the first element it
// has not proven to be a valid integer followed by a comma and a digit, and
// the scalar loop decides everything from there.
func intRun(b []byte, i int) int {
	proven := i
	startsNext := uint64(0x80) // Byte 0 of the next word starts an element.
	zeroLast := uint64(0)      // Byte 7 was an element's leading '0'.
	for j := i; j+8 <= len(b); j += 8 {
		x := binary.LittleEndian.Uint64(b[j:])
		digit := (x + 0x5050505050505050) &^ (x + 0x4646464646464646) & high // '0' to '9'.
		comma := equal(x, 0x2c2c2c2c2c2c2c2c)
		starts := comma<<8 | startsNext
		zero := equal(x, 0x3030303030303030) & starts
		if x&high != 0 || digit|comma != high || // Only digits and commas.
			comma&starts != 0 || // No empty element.
			zero&^(comma>>8)&0x0080808080808080 != 0 || // No leading zero.
			zeroLast&^comma != 0 {
			break
		}
		if startsNext != 0 {
			proven = j
		}
		// A comma before byte 7 is followed by a digit in this word.
		if m := comma & 0x0080808080808080; m != 0 {
			proven = j + (63-bits.LeadingZeros64(m))/8 + 1
		}
		startsNext = comma >> 56
		zeroLast = zero >> 56
	}
	return proven
}

func number(b []byte, i int) int {
	if i < len(b) && b[i] == '-' {
		i++
	}
	switch {
	case i < len(b) && b[i] == '0':
		i++
	case i < len(b) && b[i] >= '1' && b[i] <= '9':
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
	default:
		return -1
	}
	if i < len(b) && b[i] == '.' {
		i++
		start := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == start {
			return -1
		}
	}
	if i < len(b) && (b[i] == 'e' || b[i] == 'E') {
		i++
		if i < len(b) && (b[i] == '+' || b[i] == '-') {
			i++
		}
		start := i
		for i < len(b) && b[i] >= '0' && b[i] <= '9' {
			i++
		}
		if i == start {
			return -1
		}
	}
	return i
}

// unhex returns the value of a hexadecimal digit, or -1.
func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}
