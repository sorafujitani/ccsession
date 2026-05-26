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
		if e.Type != "user" && e.Type != "assistant" {
			continue
		}
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
	}
	return sc.Err()
}
