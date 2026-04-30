package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var staticFiles embed.FS

// FS returns the embedded static file system rooted at "static/".
func FS() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
