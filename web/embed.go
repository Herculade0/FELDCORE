// Package web embeds the compiled front-end (web/dist) into the binary so that
// FELDCORE ships as a single self-contained executable.
//
// `dist` is produced by `npm run build`. A committed placeholder keeps the
// embed directive valid before the first front-end build.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist returns the built front-end as a filesystem rooted at the SPA index.
func Dist() (fs.FS, error) {
	return fs.Sub(dist, "dist")
}
