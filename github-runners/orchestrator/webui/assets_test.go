// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cloudeng.io/webapp/webassets"
)

// TestEmbeddedSPAServed verifies the default (embedded) asset filesystem serves
// the built SPA entry point at the root.
func TestEmbeddedSPAServed(t *testing.T) {
	if !hasIndex(FrontendAssets()) {
		t.Skip("no embedded SPA build present (run `npm run build`)")
	}
	ts := httptest.NewServer(NewServer(newFakeBackend()).Handler())
	defer ts.Close()
	_, body := get(t, ts.URL+"/")
	if !strings.Contains(body, `<div id="root">`) {
		t.Errorf("root did not serve the SPA index: %.120s", body)
	}
}

// TestReloadableAssetsOverrideEmbedded verifies that with reloading enabled, a
// newer index.html on the local filesystem is served in preference to whatever
// is embedded — the dev workflow of rebuilding the SPA without recompiling Go.
func TestReloadableAssetsOverrideEmbedded(t *testing.T) {
	root := t.TempDir()
	dist := filepath.Join(root, "frontend", "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatal(err)
	}
	const marker = "<!-- reloaded-from-disk -->"
	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	assets := FrontendAssets(webassets.WithReloading(root, time.Time{}, true))
	ts := httptest.NewServer(NewServer(newFakeBackend(), WithAssets(assets)).Handler())
	defer ts.Close()

	_, body := get(t, ts.URL+"/")
	if !strings.Contains(body, marker) {
		t.Errorf("expected the on-disk index.html to be served, got: %.120s", body)
	}
	// The API must still work alongside reloadable assets.
	if code, _ := get(t, ts.URL+BasePath+"/pools"); code != http.StatusOK {
		t.Errorf("api under reloadable assets: status %d", code)
	}
}
