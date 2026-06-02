package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Loader decodes a config file's bytes into c. Formats are registered by file
// extension in loaders, so adding e.g. ".pkl" is a one-line change here.
type Loader func(data []byte, c *Config) error

var loaders = map[string]Loader{
	".toml": tomlLoad,
}

func tomlLoad(data []byte, c *Config) error {
	return toml.Unmarshal(data, c)
}

// Load decodes the config file at path. A missing file is not an error (the
// file is optional): it returns (nil, nil) so the caller falls back to
// env/defaults. An unknown extension is an error so typos surface.
func Load(path string) (*Keybindings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	load, ok := loaders[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported config format %q (%s)", ext, path)
	}
	var c Config
	if err := load(data, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &c.Keybindings, nil
}

// DefaultPath is the config file location, honoring XDG_CONFIG_HOME. It never
// creates the file; ccsession only reads config, never writes it.
func DefaultPath() string {
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return filepath.Join(base, "ccsession", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "ccsession", "config.toml")
}
