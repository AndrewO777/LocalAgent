package web

import (
	"embed"
	"io/fs"
)

//go:embed index.html
var assets embed.FS

// FS returns the embedded UI as an fs.FS rooted at the directory containing
// index.html.
func FS() fs.FS { return assets }
