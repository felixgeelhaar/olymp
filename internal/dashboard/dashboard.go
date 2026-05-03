// Package dashboard serves a small live-ops view of the Olymp loop.
//
// The dashboard is a single static HTML page (with inline CSS + a
// vanilla-JS module) that connects to `/v1/runs/stream` over SSE
// and animates each run's path through the four cognitive layers.
// No build step. No framework. No CORS to worry about — the page
// is served from the same origin as the SSE feed.
//
// Mount with `dashboard.Handler()`; wire under any prefix the host
// prefers (default in cmd/olymp: `/dashboard/`).
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// Handler returns an http.Handler that serves the embedded
// dashboard. Requests to the prefix root return index.html.
func Handler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Embedding always succeeds at compile time; this branch
		// only fires if the package is hand-instantiated wrongly.
		panic("dashboard: embed lookup failed: " + err.Error())
	}
	return http.FileServer(http.FS(sub))
}
