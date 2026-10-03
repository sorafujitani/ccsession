package session

import (
	"encoding/json"
	"strings"
)

// FilterOutByDir removes sessions whose cwd (or fallback basename) contains
// needle, case-insensitively. It reuses the input slice's backing array.
func FilterOutByDir(sessions []*Session, needle string) []*Session {
	lneedle := strings.ToLower(needle)
	out := sessions[:0]
	for _, s := range sessions {
		target := s.CWD
		if target == "" {
			target = s.CWDBasename
		}
		if target != "" && strings.Contains(strings.ToLower(target), lneedle) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// ExtractText returns the concatenated text of a message Content payload.
// Content may be a bare JSON string or an array of typed blocks; only "text"
// blocks contribute, joined by sep.
func ExtractText(raw json.RawMessage, sep string) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var b strings.Builder
		for _, block := range blocks {
			if block.Type != "text" || block.Text == "" {
				continue
			}
			if b.Len() > 0 {
				b.WriteString(sep)
			}
			b.WriteString(block.Text)
		}
		return b.String()
	}
	return ""
}

// Truncate shortens s to at most n runes, appending an ellipsis when cut.
func Truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
