package uiembed

import (
	"embed"
	"io/fs"
)

// Dist contains the built admin UI assets under ui/dist.
//
//go:embed dist
var Dist embed.FS

func DistFS() fs.FS {
	subFS, err := fs.Sub(Dist, "dist")
	if err != nil {
		panic(err)
	}

	return subFS
}
