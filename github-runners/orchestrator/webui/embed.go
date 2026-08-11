// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"embed"
	"io/fs"

	"cloudeng.io/webapp/webassets"
)

// frontendFS holds the compiled single-page app. Build it with
// `npm --prefix frontend run build`, which regenerates frontend/dist, before
// `go build`. Only frontend/dist/.gitkeep is checked in (the build artifacts are
// git-ignored), so this package always compiles; when no real build is present
// the server falls back to a placeholder page (see hasIndex and mount.go).
//
//go:embed all:frontend/dist
var frontendFS embed.FS

// FrontendAssets returns the SPA asset filesystem rooted at frontend/dist. With
// no options it serves the files embedded in the binary. Pass reload options
// (webassets.WithReloading, or webassets.Config.Options / OptionsFromFlags) to
// prefer newer files from the local filesystem, so the UI can be rebuilt with
// `npm run build` and picked up without recompiling the binary.
func FrontendAssets(opts ...webassets.AssetsOption) fs.FS {
	return webassets.NewAssets("frontend/dist", frontendFS, opts...)
}

// hasIndex reports whether the asset filesystem currently resolves a built SPA
// entry point (index.html). With reloading enabled this consults the local
// filesystem, so a build produced after startup is detected.
func hasIndex(assets fs.FS) bool {
	_, err := fs.Stat(assets, "index.html")
	return err == nil
}
