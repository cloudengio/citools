// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"embed"
	"io/fs"

	"cloudeng.io/webapp/webassets"
)

// frontendFS holds the compiled single-page app. A placeholder
// frontend/dist/index.html is checked in (the build artifacts are git-ignored)
// so this package always compiles; `npm --prefix frontend run build` (or
// `go run . webapp-build`) overwrites it, plus an assets/ directory, with the
// real SPA. Until then the placeholder index is served instead.
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
