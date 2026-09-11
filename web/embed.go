// Package web holds the dashboard's embedded static frontend.
package web

import "embed"

// Files is the embedded frontend (index.html, app.js, style.css).
//
//go:embed index.html app.js style.css
var Files embed.FS
