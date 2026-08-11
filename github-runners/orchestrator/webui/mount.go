// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"io/fs"
	"net/http"
)

// Handler returns the top-level HTTP handler for the web UI: the JSON API under
// BasePath plus the single-page app at the root. The root always resolves an
// index.html — the built SPA when present, otherwise the checked-in placeholder
// that links to the API (see embed.go).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(BasePath+"/", s.APIHandler())
	mux.Handle("/", spaHandler(s.assets))
	return mux
}

// spaHandler serves the embedded static build, falling back to index.html for
// any path that does not resolve to a file so client-side routing works.
func spaHandler(assets fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(assets, p[1:]); err != nil {
			// Unknown path: hand the SPA its entry point.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
