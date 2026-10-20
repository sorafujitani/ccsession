package jsonscan

// Scalar versions of String and numbers before SWAR, the oracles for
// differential tests.

func referenceString(b []byte, i int) int {
	if i < 0 || i >= len(b) || b[i] != '"' {
		return -1
	}
	i++
	for {
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

func referenceNumbers(b []byte, i int, inArray bool) int {
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
		i++
	}
}
