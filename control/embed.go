// Package control embeds the built control plane UI (control/web/dist).
//
// The dist directory is committed so a Seed can build its kernel without
// Node.js. Rebuild it with `make control`.
package control

import (
	"embed"
	"io/fs"
)

//go:embed all:web/dist
var dist embed.FS

// FS returns the built UI rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "web/dist")
	if err != nil {
		panic(err)
	}
	return sub
}
