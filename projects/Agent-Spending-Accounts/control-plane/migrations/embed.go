// Package migrations embeds the SQL schema files applied by the store.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
