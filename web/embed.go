// Package web embeds the built frontend into the server binary.
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

// dist always holds at least .gitkeep, so the Go code compiles before the
// frontend has been built. public/.gitkeep puts it back after each Vite build.
//
//go:embed all:dist
var dist embed.FS

// UI returns the built frontend, rooted at its index.html.
func UI() (fs.FS, error) {
	ui, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("open embedded frontend: %w", err)
	}
	return ui, nil
}
