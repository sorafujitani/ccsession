package session

import (
	"encoding/json"
	"strings"
)

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
