// Package ansi holds the ANSI escape sequences shared by the list and
// preview renderers.
package ansi

const (
	Reset     = "\x1b[0m"
	Bold      = "\x1b[1m"
	Dim       = "\x1b[2m"
	Cyan      = "\x1b[36m"
	Green     = "\x1b[32m"
	Yellow    = "\x1b[33m"
	Highlight = "\x1b[7m" // reverse video; marks grep-query matches
)
