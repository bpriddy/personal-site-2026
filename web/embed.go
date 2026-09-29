// Package web embeds the site's templates and static assets into the binary.
package web

import "embed"

//go:embed templates static
var FS embed.FS
