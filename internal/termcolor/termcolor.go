// Package termcolor centralizes ANSI color enablement for command output.
package termcolor

import (
	"io"
	"os"
	"strings"
)

// Enabled decides whether ANSI escapes should be emitted for out.
//
// Precedence matches the CLI flags: explicit "always" or "never" wins.
// Otherwise --no-color and NO_COLOR force color off, and auto mode emits color
// only when writing to a terminal.
func Enabled(out io.Writer, mode string, noColor bool) bool {
	switch strings.ToLower(mode) {
	case "always":
		return true
	case "never":
		return false
	}
	if noColor {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if f, ok := out.(*os.File); ok {
		return isTerminal(f)
	}
	return false
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}
