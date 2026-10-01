// Package web embeds the site's templates and static assets into the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed templates static
var FS embed.FS

//go:embed static/fonts
var fontsFS embed.FS

// Fonts is the house type (docs/design-pov.md, section 8): self-hosted OFL
// WOFF2 files and their licenses, rooted at the fonts directory. The main
// site serves it at /static/fonts/ and the user-content service at /fonts/,
// so front ends (visitor-made ones too) can use it under font-src 'self'.
// Files are cached for a year: never change one in place, add a new name.
var Fonts fs.FS = mustSub(fontsFS, "static/fonts")

func mustSub(f fs.FS, dir string) fs.FS {
	s, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return s
}
