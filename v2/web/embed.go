// Package web embeds the built frontend (npm run build → dist/) into the Go binary.
// dist/.gitkeep is tracked (and copied from public/ on every build) so a fresh checkout compiles.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist is the built frontend; it has no index.html until the frontend has been built.
func Dist() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	return sub
}
