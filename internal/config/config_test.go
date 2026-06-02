package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	file := &Keybindings{Grep: "ctrl-r"}
	cases := []struct {
		name string
		src  Sources
		want Keybindings
	}{
		{
			name: "all defaults when nothing set",
			src:  Sources{},
			want: Keybindings{Grep: "ctrl-g", Dir: "ctrl-o", Fuzzy: "ctrl-f"},
		},
		{
			name: "env overrides default",
			src:  Sources{Env: Keybindings{Fuzzy: "alt-f"}},
			want: Keybindings{Grep: "ctrl-g", Dir: "ctrl-o", Fuzzy: "alt-f"},
		},
		{
			name: "flag beats env",
			src:  Sources{Flags: Keybindings{Grep: "alt-p"}, Env: Keybindings{Grep: "alt-g"}},
			want: Keybindings{Grep: "alt-p", Dir: "ctrl-o", Fuzzy: "ctrl-f"},
		},
		{
			name: "file beats flag and env",
			src:  Sources{Flags: Keybindings{Grep: "alt-p"}, Env: Keybindings{Grep: "alt-g"}, File: file},
			want: Keybindings{Grep: "ctrl-r", Dir: "ctrl-o", Fuzzy: "ctrl-f"},
		},
		{
			name: "per-field mix from different sources",
			src: Sources{
				Flags: Keybindings{Fuzzy: "alt-f"},
				Env:   Keybindings{Dir: "alt-d"},
				File:  &Keybindings{Grep: "ctrl-r"},
			},
			want: Keybindings{Grep: "ctrl-r", Dir: "alt-d", Fuzzy: "alt-f"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.src)
			if err != nil {
				t.Fatalf("Resolve(%+v) error: %v", tc.src, err)
			}
			if got != tc.want {
				t.Fatalf("Resolve(%+v) = %+v, want %+v", tc.src, got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		kb      Keybindings
		wantErr bool
	}{
		{"defaults ok", Defaults(), false},
		{"custom valid", Keybindings{"ctrl-r", "ctrl-o", "alt-f"}, false},
		{"function key ok", Keybindings{"f1", "f2", "f3"}, false},
		{"grep/dir collide", Keybindings{"ctrl-o", "ctrl-o", "ctrl-f"}, true},
		{"grep/fuzzy collide", Keybindings{"ctrl-f", "ctrl-o", "ctrl-f"}, true},
		{"dir/fuzzy collide", Keybindings{"ctrl-g", "ctrl-f", "ctrl-f"}, true},
		{"reserved enter", Keybindings{"enter", "ctrl-o", "ctrl-f"}, true},
		{"reserved change", Keybindings{"ctrl-g", "change", "ctrl-f"}, true},
		{"reserved start", Keybindings{"ctrl-g", "ctrl-o", "start"}, true},
		{"empty key", Keybindings{"", "ctrl-o", "ctrl-f"}, true},
		{"space injection", Keybindings{"ctrl x", "ctrl-o", "ctrl-f"}, true},
		{"shell metachar", Keybindings{"$(rm)", "ctrl-o", "ctrl-f"}, true},
		{"uppercase rejected", Keybindings{"Ctrl-G", "ctrl-o", "ctrl-f"}, true},
		{"trailing hyphen rejected", Keybindings{"ctrl-", "ctrl-o", "ctrl-f"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.kb)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate(%+v) error = %v, wantErr = %v", tc.kb, err, tc.wantErr)
			}
		})
	}
}

func TestResolveValidationError(t *testing.T) {
	// Resolve must surface a validation failure rather than returning a
	// colliding set silently.
	_, err := Resolve(Sources{Flags: Keybindings{Grep: "ctrl-o", Dir: "ctrl-o", Fuzzy: "ctrl-f"}})
	if err == nil {
		t.Fatal("Resolve with colliding flags: want error, got nil")
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()

	t.Run("missing file returns nil without error", func(t *testing.T) {
		kb, err := Load(filepath.Join(dir, "does-not-exist.toml"))
		if err != nil {
			t.Fatalf("Load missing file: unexpected error %v", err)
		}
		if kb != nil {
			t.Fatalf("Load missing file: want nil, got %+v", kb)
		}
	})

	t.Run("valid toml", func(t *testing.T) {
		path := filepath.Join(dir, "config.toml")
		writeFile(t, path, "[keybindings]\ngrep = \"ctrl-r\"\nfuzzy = \"alt-f\"\n")
		kb, err := Load(path)
		if err != nil {
			t.Fatalf("Load: unexpected error %v", err)
		}
		if kb == nil || kb.Grep != "ctrl-r" || kb.Fuzzy != "alt-f" {
			t.Fatalf("Load: got %+v, want grep=ctrl-r fuzzy=alt-f", kb)
		}
		// Unset keys stay empty so lower-precedence sources can fill them.
		if kb.Dir != "" {
			t.Fatalf("Load: dir = %q, want empty", kb.Dir)
		}
	})

	t.Run("unsupported extension errors", func(t *testing.T) {
		path := filepath.Join(dir, "config.yaml")
		writeFile(t, path, "keybindings:\n  grep: ctrl-r\n")
		if _, err := Load(path); err == nil {
			t.Fatal("Load .yaml: want error, got nil")
		}
	})

	t.Run("malformed toml errors", func(t *testing.T) {
		path := filepath.Join(dir, "bad.toml")
		writeFile(t, path, "[keybindings\ngrep = ")
		if _, err := Load(path); err == nil {
			t.Fatal("Load malformed toml: want error, got nil")
		}
	})
}

func TestDefaultPath(t *testing.T) {
	t.Run("honors XDG_CONFIG_HOME", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "/custom/xdg")
		want := filepath.Join("/custom/xdg", "ccsession", "config.toml")
		if got := DefaultPath(); got != want {
			t.Fatalf("DefaultPath() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to ~/.config", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("HOME", "/home/tester")
		want := filepath.Join("/home/tester", ".config", "ccsession", "config.toml")
		if got := DefaultPath(); got != want {
			t.Fatalf("DefaultPath() = %q, want %q", got, want)
		}
	})
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}
