// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"io/fs"
	"net/http"
)

// Handler returns the top-level HTTP handler for the web UI: the JSON API under
// BasePath plus the embedded single-page app at the root. When no compiled
// frontend is embedded, the root serves a small placeholder that links to the
// API instead.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle(BasePath+"/", s.APIHandler())
	if hasIndex(s.assets) {
		mux.Handle("/", spaHandler(s.assets))
	} else {
		mux.HandleFunc("/", placeholderHandler)
	}
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

func placeholderHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(placeholderIndex))
}

const placeholderIndex = `<!doctype html>
<html><head><meta charset="utf-8"><title>Runner Orchestrator</title></head>
<body>
<h1>GitHub Runner Orchestrator</h1>
<p>The compiled web UI is not embedded in this build. Build it with
<code>npm --prefix webui/frontend run build</code> and rebuild the binary.</p>
<p>The management API is served under <code>` + BasePath + `</code>:</p>
<ul>
<li><a href="` + BasePath + `/config">config</a></li>
<li><a href="` + BasePath + `/pools">pools</a></li>
<li><a href="` + BasePath + `/workflows">workflows</a></li>
<li><a href="` + BasePath + `/events">events (SSE)</a></li>
</ul>
</body></html>`
