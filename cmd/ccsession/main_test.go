package main

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/sorafujitani/ccsession/internal/config"
	"github.com/sorafujitani/ccsession/internal/source"
)

// TestBuildScriptDefaults guards against tokenization drift: with default
// keys the script must keep the original header and binds, preserve the
// start/change wiring, and leave no __CCS_BIND_*__ token unsubstituted.
func TestBuildScriptDefaults(t *testing.T) {
	got := buildScript(config.Defaults(), "claude")

	wants := []string{
		`--header='[claude] ctrl-g: grep / ctrl-o: dir / ctrl-f: fuzzy / enter: resume'`,
		`--bind 'start:unbind(change)'`,
		`--bind "change:reload(sleep 0.05;`,
		`--bind "ctrl-g:transform:echo \"change-prompt(grep> )`,
		`--bind "ctrl-o:transform:echo \"change-prompt(dir> )`,
		`--bind "ctrl-f:transform:echo \"change-prompt(> )`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("buildScript(Defaults()) missing %q", w)
		}
	}
	if strings.Contains(got, "__CCS_BIND_") {
		t.Errorf("buildScript left an unsubstituted token:\n%s", got)
	}
}

// TestBuildScriptCustom verifies resolved keys reach the header and binds and
// that the defaults they replace are gone.
func TestBuildScriptCustom(t *testing.T) {
	got := buildScript(config.Keybindings{Grep: "ctrl-r", Dir: "alt-d", Fuzzy: "alt-f"}, "opencode")

	wants := []string{
		`--header='[opencode] ctrl-r: grep / alt-d: dir / alt-f: fuzzy / enter: resume'`,
		`--bind "ctrl-r:transform:echo \"change-prompt(grep> )`,
		`--bind "alt-d:transform:echo \"change-prompt(dir> )`,
		`--bind "alt-f:transform:echo \"change-prompt(> )`,
	}
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("buildScript(custom) missing %q", w)
		}
	}
	// The default keys must not survive as bind triggers.
	for _, gone := range []string{`--bind "ctrl-g:transform`, `--bind "ctrl-o:transform`, `--bind "ctrl-f:transform`} {
		if strings.Contains(got, gone) {
			t.Errorf("buildScript(custom) still contains default bind %q", gone)
		}
	}
}

func TestParseGlobalFlags(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantGF   globalFlags
		wantRest []string
	}{
		{
			name:     "no global flags",
			args:     []string{"list", "--grep", "foo"},
			wantRest: []string{"list", "--grep", "foo"},
		},
		{
			name:   "space form binds",
			args:   []string{"--bind-grep", "ctrl-r", "--bind-fuzzy", "alt-f"},
			wantGF: globalFlags{binds: config.Keybindings{Grep: "ctrl-r", Fuzzy: "alt-f"}},
		},
		{
			name:   "equals form binds and exclude-dir",
			args:   []string{"--exclude-dir=work", "--bind-dir=alt-d"},
			wantGF: globalFlags{excludeDir: "work", binds: config.Keybindings{Dir: "alt-d"}},
		},
		{
			name:     "stops at subcommand, passes rest through",
			args:     []string{"--bind-grep", "ctrl-r", "resume", "abc123"},
			wantGF:   globalFlags{binds: config.Keybindings{Grep: "ctrl-r"}},
			wantRest: []string{"resume", "abc123"},
		},
		{
			name:   "source equals form",
			args:   []string{"--source=opencode"},
			wantGF: globalFlags{source: "opencode"},
		},
		{
			name:   "source space form",
			args:   []string{"--source", "opencode"},
			wantGF: globalFlags{source: "opencode"},
		},
		{
			name:   "opencode sugar takes no value",
			args:   []string{"--opencode", "list"},
			wantGF: globalFlags{opencode: true},
			// "list" is the subcommand, not a value consumed by --opencode.
			wantRest: []string{"list"},
		},
		{
			name:     "grok sugar takes no value",
			args:     []string{"--grok", "list"},
			wantGF:   globalFlags{grok: true},
			wantRest: []string{"list"},
		},
		{
			name:     "codex sugar takes no value",
			args:     []string{"--codex", "list"},
			wantGF:   globalFlags{codex: true},
			wantRest: []string{"list"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gf, rest := parseGlobalFlags(tc.args)
			if gf != tc.wantGF {
				t.Errorf("parseGlobalFlags(%v) gf = %+v, want %+v", tc.args, gf, tc.wantGF)
			}
			if !reflect.DeepEqual(rest, tc.wantRest) {
				t.Errorf("parseGlobalFlags(%v) rest = %v, want %v", tc.args, rest, tc.wantRest)
			}
		})
	}
}

func TestApplySource(t *testing.T) {
	cases := []struct {
		name    string
		gf      globalFlags
		env     string // pre-set CCSESSION_SOURCE
		wantErr bool
		wantEnv string // CCSESSION_SOURCE after applySource
	}{
		{name: "default claude leaves env empty", wantEnv: ""},
		{name: "opencode sugar", gf: globalFlags{opencode: true}, wantEnv: "opencode"},
		{name: "grok sugar", gf: globalFlags{grok: true}, wantEnv: "grok"},
		{name: "codex sugar", gf: globalFlags{codex: true}, wantEnv: "codex"},
		{name: "source flag", gf: globalFlags{source: "opencode"}, wantEnv: "opencode"},
		{name: "source flag grok", gf: globalFlags{source: "grok"}, wantEnv: "grok"},
		{name: "source flag codex", gf: globalFlags{source: "codex"}, wantEnv: "codex"},
		{name: "sugar agrees with source", gf: globalFlags{opencode: true, source: "opencode"}, wantEnv: "opencode"},
		{name: "codex sugar agrees with source", gf: globalFlags{codex: true, source: "codex"}, wantEnv: "codex"},
		{name: "sugar contradicts source", gf: globalFlags{opencode: true, source: "claude"}, wantErr: true},
		{name: "grok sugar contradicts source", gf: globalFlags{grok: true, source: "opencode"}, wantErr: true},
		{name: "codex sugar contradicts source", gf: globalFlags{codex: true, source: "grok"}, wantErr: true},
		{name: "backend sugars conflict", gf: globalFlags{opencode: true, grok: true}, wantErr: true},
		{name: "codex backend sugar conflicts", gf: globalFlags{grok: true, codex: true}, wantErr: true},
		{name: "unknown source flag", gf: globalFlags{source: "bogus"}, wantErr: true},
		{name: "inherited env is validated", env: "bogus", wantErr: true},
		{name: "inherited valid env survives", env: "opencode", wantEnv: "opencode"},
		{name: "inherited grok env survives", env: "grok", wantEnv: "grok"},
		{name: "inherited codex env survives", env: "codex", wantEnv: "codex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(source.EnvVar, tc.env)
			err := applySource(tc.gf)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("applySource(%+v) = nil, want error", tc.gf)
				}
				return
			}
			if err != nil {
				t.Fatalf("applySource(%+v): %v", tc.gf, err)
			}
			if got := os.Getenv(source.EnvVar); got != tc.wantEnv {
				t.Errorf("CCSESSION_SOURCE = %q, want %q", got, tc.wantEnv)
			}
		})
	}
}
