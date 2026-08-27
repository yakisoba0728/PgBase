// Package admin embeds and serves the production pgBase administrator UI.
package admin

import (
	"embed"
)

// Dist contains the tracked production Vite build so pgBase needs no runtime
// Node installation or external static directory.
//
//go:embed all:dist
var Dist embed.FS
