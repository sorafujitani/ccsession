package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/sorafujitani/ccsession/internal/config"
)

// TestBuildScriptDefaults guards against tokenization drift: with default
// keys the script must keep the original header and binds, preserve the
// start/change wiring, and leave no __CCS_BIND_*__ token unsubstituted.
func TestBuildScriptDefaults(t *testing.T) {
	got := buildScript(config.Defaults())

	wants := []string{
		`--header='ctrl-g: grep / ctrl-o: dir / ctrl-f: fuzzy / enter: resume'`,
		`--bind 'start:unbind(change)'`,
		`--bind "change:reload(sleep 0.05;`,
		`--preview "$CCSESSION_BIN preview --color=always --query {q} {1}"`,
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
	got := buildScript(config.Keybindings{Grep: "ctrl-r", Dir: "alt-d", Fuzzy: "alt-f"})

	wants := []string{
		`--header='ctrl-r: grep / alt-d: dir / alt-f: fuzzy / enter: resume'`,
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
