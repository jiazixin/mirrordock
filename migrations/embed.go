package migrations

import _ "embed"

// Initial is the sole source for the initial database schema.
//
//go:embed 001-initial.sql
var Initial string
