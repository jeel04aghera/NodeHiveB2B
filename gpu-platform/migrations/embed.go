// Package migrations embeds the goose SQL migration files into the binary so the
// control plane can apply them on startup — no separate goose CLI or .sql files
// need to ship in the runtime image (important for Railway/containers).
package migrations

import "embed"

// FS holds every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
