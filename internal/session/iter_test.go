package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIterContent_SkipsOversizeLineAndContinues(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "session.jsonl")
	body := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":"before"}}`,
		strings.Repeat("X", iterLineCap+1),
		`{"type":"assistant","message":{"role":"assistant","content":"searchable after oversize"}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	var got []string
	err := IterContent(p, func(text string) bool {
		got = append(got, text)
		return true
	})
	if err != nil {
		t.Fatalf("IterContent: %v", err)
	}

	want := []string{"before", "searchable after oversize"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %q, want %q", got, want)
	}
}
