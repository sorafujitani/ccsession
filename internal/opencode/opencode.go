// Package opencode reads sessions from OpenCode's SQLite store, mirroring the
// claude source so both backends satisfy the same source.Source contract.
//
// The driver is ncruces/go-sqlite3 (pure Go, wazero/wasm) rather than a cgo
// driver, so the release pipeline keeps CGO_ENABLED=0 cross-compilation across
// all four targets. The blank import registers the "sqlite3" database/sql
// driver used by db.go.
package opencode

import (
	_ "github.com/ncruces/go-sqlite3/driver"
)
