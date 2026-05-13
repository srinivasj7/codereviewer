// Package db exposes the embedded SQL migrations as an io/fs.FS so the
// migrate binary doesn't need to know about the host filesystem layout
// and can run from inside a distroless container.
package db

import "embed"

// Migrations holds the goose-formatted SQL files under migrations/.
//
//go:embed migrations/*.sql
var Migrations embed.FS
