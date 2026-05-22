package web

import (
	"embed"
	"io/fs"
)

// The Vite build writes its output into web/dist (see web/vite.config.ts).
// `all:dist` includes files even when prefixed with `_` or `.`, which is
// necessary because Vite chunks land in subdirectories like dist/assets/.
//
// A committed placeholder dist/index.html keeps `go build .` working even
// without Node.js installed; running `make build` (or `npm run build`)
// overwrites it with the real UI.
//
//go:embed all:dist
var assets embed.FS

// FS returns the embedded UI rooted at dist/ — so the HTTP file server
// serves dist/index.html as "/" and dist/assets/* as "/assets/*".
func FS() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		// fs.Sub only fails for invalid paths; "dist" is always valid here.
		panic(err)
	}
	return sub
}
