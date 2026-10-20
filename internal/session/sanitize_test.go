package session

import (
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// referenceSanitizeLabel is SanitizeLabel before it stopped early.
func referenceSanitizeLabel(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	return Truncate(s, LabelMaxLen)
}

func TestSanitizeLabelMatchesReference(t *testing.T) {
	pieces := []string{"a", "é", " ", "  ", "\t", "\n", "\x1b[31m", "\x7f", " ", "　", "\xff", "😀", "word"}
	rapid.Check(t, func(t *rapid.T) {
		var b strings.Builder
		for _, p := range rapid.SliceOfN(rapid.SampledFrom(pieces), 0, 3*LabelMaxLen).Draw(t, "pieces") {
			b.WriteString(p)
		}
		in := b.String()
		if rapid.Bool().Draw(t, "raw") {
			in = rapid.String().Draw(t, "s")
		}
		if got, want := SanitizeLabel(in), referenceSanitizeLabel(in); got != want {
			t.Fatalf("SanitizeLabel(%q) = %q, want %q", in, got, want)
		}
	})
}

func BenchmarkSanitizeLabelLongPrompt(b *testing.B) {
	prompt := strings.Repeat("long pasted prompt\n", 200_000)
	for range b.N {
		SanitizeLabel(prompt)
	}
}
