package session

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	_ = os.Unsetenv("CLAUDE_CONFIG_DIR")
	os.Exit(m.Run())
}

func TestFilterOutByDir(t *testing.T) {
	mk := func(id, cwd, base string) *Session {
		return &Session{ID: id, CWD: cwd, CWDBasename: base}
	}
	all := []*Session{
		mk("a", "/Users/x/work/myproj", "myproj"),
		mk("b", "/Users/x/scratch/test-thing", "test-thing"),
		mk("c", "/Users/x/work/Test", "Test"),
		mk("d", "", ""),
		mk("e", "", "test-fallback"),
	}
	in := append([]*Session(nil), all...)
	got := FilterOutByDir(in, "test")

	ids := make([]string, len(got))
	for i, s := range got {
		ids[i] = s.ID
	}
	want := []string{"a", "d"}
	if len(ids) != len(want) {
		t.Fatalf("got %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"helloX", 5, "hell…"},
		{"あいうえお", 5, "あいうえお"},
		{"あいうえおX", 5, "あいうえ…"},
		{"x", 0, "x"},
		{"x", -1, "x"},
	}
	for _, c := range cases {
		got := Truncate(c.s, c.n)
		if got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestExtractText(t *testing.T) {
	cases := []struct {
		name string
		raw  json.RawMessage
		sep  string
		want string
	}{
		{
			name: "bare string",
			raw:  json.RawMessage(`"hello"`),
			sep:  " ",
			want: "hello",
		},
		{
			name: "text block array",
			raw: json.RawMessage(`[
				{"type":"text","text":"first"},
				{"type":"text","text":"second"}
			]`),
			sep:  " ",
			want: "first second",
		},
		{
			name: "empty raw",
			raw:  nil,
			sep:  " ",
			want: "",
		},
		{
			name: "skips non text and empty blocks",
			raw: json.RawMessage(`[
				{"type":"image","text":"ignored"},
				{"type":"text","text":""},
				{"type":"text","text":"kept"}
			]`),
			sep:  " ",
			want: "kept",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractText(c.raw, c.sep)
			if got != c.want {
				t.Errorf("ExtractText(%s, %q) = %q, want %q", string(c.raw), c.sep, got, c.want)
			}
		})
	}
}
