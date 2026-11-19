package web

import "embed"

// Dist is the Vite production build. Generate it with `make frontend`
// before compiling the Go server.
//
//go:embed all:dist
var Dist embed.FS
