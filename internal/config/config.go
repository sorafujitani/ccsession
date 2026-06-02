// Package config resolves the fzf picker's mode-switch keybindings from
// (in precedence order) a TOML config file, CLI flags, environment variables,
// and built-in defaults.
package config

import (
	"fmt"
	"regexp"
)

// Keybindings holds the three picker mode-switch keys. An empty field means
// "not set here" and lets a lower-precedence source supply it.
type Keybindings struct {
	Grep  string `toml:"grep"`
	Dir   string `toml:"dir"`
	Fuzzy string `toml:"fuzzy"`
}

// Config mirrors the TOML file layout: a single [keybindings] table.
type Config struct {
	Keybindings Keybindings `toml:"keybindings"`
}

// Defaults are the historical hardcoded keys. buildScript(Defaults()) must
// reproduce the original fzf script (see main_test.go).
func Defaults() Keybindings {
	return Keybindings{Grep: "ctrl-g", Dir: "ctrl-o", Fuzzy: "ctrl-f"}
}

// Sources carries the candidate keybindings from each origin. File is a
// pointer because the config file is optional (nil = no file present).
type Sources struct {
	Flags Keybindings
	Env   Keybindings
	File  *Keybindings
}

// Resolve picks each key from the first non-empty source (file > flag > env >
// default) and validates the result. It touches no OS state.
func Resolve(s Sources) (Keybindings, error) {
	var file Keybindings
	if s.File != nil {
		file = *s.File
	}
	def := Defaults()
	kb := Keybindings{
		Grep:  pick(file.Grep, s.Flags.Grep, s.Env.Grep, def.Grep),
		Dir:   pick(file.Dir, s.Flags.Dir, s.Env.Dir, def.Dir),
		Fuzzy: pick(file.Fuzzy, s.Flags.Fuzzy, s.Env.Fuzzy, def.Fuzzy),
	}
	if err := Validate(kb); err != nil {
		return Keybindings{}, err
	}
	return kb, nil
}

func pick(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}

// keyName is a deliberately strict, lower-case key syntax. Rejecting quotes,
// spaces, '$' and ':' guards against shell injection into the bash --bind
// strings; fzf has the final say on whether a key name is real.
var keyName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// reserved are fzf event names the picker script uses internally; binding a
// mode switch to one would break the script.
var reserved = map[string]bool{
	"start":  true,
	"change": true,
	"enter":  true,
	"load":   true,
	"result": true,
	"focus":  true,
}

// Validate enforces that the three keys are mutually distinct, are not fzf
// reserved event names, and match the safe key syntax.
func Validate(kb Keybindings) error {
	for _, f := range []struct{ label, key string }{
		{"grep", kb.Grep}, {"dir", kb.Dir}, {"fuzzy", kb.Fuzzy},
	} {
		if !keyName.MatchString(f.key) {
			return fmt.Errorf("invalid %s keybinding %q: must match %s", f.label, f.key, keyName.String())
		}
		if reserved[f.key] {
			return fmt.Errorf("%s keybinding %q is a reserved fzf event name", f.label, f.key)
		}
	}
	switch {
	case kb.Grep == kb.Dir:
		return fmt.Errorf("grep and dir keybindings collide on %q", kb.Grep)
	case kb.Grep == kb.Fuzzy:
		return fmt.Errorf("grep and fuzzy keybindings collide on %q", kb.Grep)
	case kb.Dir == kb.Fuzzy:
		return fmt.Errorf("dir and fuzzy keybindings collide on %q", kb.Dir)
	}
	return nil
}
