package session

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

// IterContent reads a session JSONL transcript and invokes fn with the
// extractable text of each user/assistant message. fn can return false to
// stop early. Non-JSON and non-message lines are skipped silently.
// Lines longer than 4 MiB are skipped.
func IterContent(path string, fn func(text string) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		// Yield only the *values* of explicitly-listed user-visible fields.
		// This still keeps JSON keys (e.g. "type") and the cwd field out of
		// the search corpus — matching only the things the user actually
		// reads in the list view: conversation body, the ai-title that
		// shows up as the label, and the last-prompt summary.
		switch e.Type {
		case "user", "assistant":
			if e.Message == nil {
				continue
			}
			text := extractText(e.Message.Content)
			if text == "" {
				continue
			}
			if !fn(text) {
				return nil
			}
		case "ai-title":
			if e.AITitle != "" && !fn(e.AITitle) {
				return nil
			}
		case "last-prompt":
			if e.LastPrompt != "" && !fn(e.LastPrompt) {
				return nil
			}
		}
	}
	return sc.Err()
}
