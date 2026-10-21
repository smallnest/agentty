// Package web embeds the browser terminal assets.
package web

import "embed"

// Static holds index.html and app.js served at the root path.
//
//go:embed static
var Static embed.FS
