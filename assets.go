// Package slidekick exposes the files that are compiled into the binary:
// the deck schema and catalogue, the themes, and the built web runtime.
package slidekick

import (
	"embed"
	"io/fs"
)

//go:embed schema/deck.schema.json schema/catalog.json
var Schema embed.FS

//go:embed themes
var Themes embed.FS

//go:embed all:web/dist
var webDist embed.FS

// Web returns the built browser app (web/dist) rooted at its top level.
func Web() fs.FS {
	sub, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		panic(err)
	}
	return sub
}
