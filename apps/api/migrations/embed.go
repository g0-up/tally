// Package migrations embeds the versioned PostgreSQL schema migrations (golang-migrate format,
// NNNNNN_<domain>_<subject>.up.sql / .down.sql) so the tally binary and the tests apply exactly
// the same files.
package migrations

import "embed"

// FS holds every *.up.sql and *.down.sql file at its root.
//
//go:embed *.sql
var FS embed.FS
