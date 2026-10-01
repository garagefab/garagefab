package garagefab

import (
	"embed"
	"io/fs"
)

//go:embed all:ui/dist
var distFS embed.FS

// Dist returns the embedded UI filesystem rooted at ui/dist.
func Dist() fs.FS {
	sub, err := fs.Sub(distFS, "ui/dist")
	if err != nil {
		panic(err)
	}
	return sub
}
