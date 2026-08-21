package webdist

import "embed"

// All embeds the built web SPA. Build output is placed in this directory
// by `npm run build` (vite.config.ts outDir: ../webdist).
// ponytail: all:* keeps clean Go builds working before Vite assets exist;
// move generated output under a tracked subdir if exposing embed.go matters.
//
//go:embed all:*
var All embed.FS
