package webui

import (
	"embed"
	"io/fs"
)

// Files embeds the single Vite output. Build web before compiling this package.
//
//go:embed dist
var built embed.FS

func Files() fs.FS {
	sub, err := fs.Sub(built, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
