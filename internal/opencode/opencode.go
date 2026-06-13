// Package opencode reads sessions from OpenCode's SQLite store, satisfying the
// same source.Source contract as the claude backend.
//
// The driver is ncruces/go-sqlite3 (pure Go) rather than a cgo driver, so the
// release pipeline keeps CGO_ENABLED=0 cross-compilation across all targets.
package opencode

import (
	_ "github.com/ncruces/go-sqlite3/driver"
)
