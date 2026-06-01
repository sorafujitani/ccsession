package session

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
)

const iterLineCap = 4 * 1024 * 1024

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

	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := readIterLine(r, iterLineCap)
		if line != "" {
			var e entry
			if err := json.Unmarshal([]byte(line), &e); err == nil {
				// Yield only the *values* of explicitly-listed user-visible fields.
				// This still keeps JSON keys (e.g. "type") and the cwd field out of
				// the search corpus — matching only the things the user actually
				// reads in the list view: conversation body, the ai-title that
				// shows up as the label, and the last-prompt summary.
				switch e.Type {
				case "user", "assistant":
					if e.Message == nil {
						break
					}
					text := ExtractText(e.Message.Content, " ")
					if text == "" {
						break
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
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func readIterLine(r *bufio.Reader, max int) (string, error) {
	var (
		buf       strings.Builder
		truncated bool
	)
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 {
			if !truncated {
				if buf.Len()+len(chunk) > max {
					truncated = true
				} else {
					buf.Write(chunk)
				}
			}
		}
		if err == bufio.ErrBufferFull {
			// More of the same logical line remains in the reader.
			continue
		}
		if truncated {
			return "", err
		}
		return strings.TrimSpace(buf.String()), err
	}
}
