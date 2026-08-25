// Copyright 2026 cloudeng llc. All rights reserved.
// Use of this source code is governed by the Apache-2.0
// license that can be found in the LICENSE file.

package main

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"howett.net/plist"
)

// TestExpandEnv covers the ${ENV} expansion applied to the bundle
// configuration, which is how signing identities and profile paths are kept out
// of the file.
func TestExpandEnv(t *testing.T) {
	t.Setenv("ORCH_TEST_ID", "Developer ID Application: Example")

	got := expandEnv(map[string]any{
		"identity":  "${ORCH_TEST_ID}",
		"nested":    map[string]any{"profile": "$ORCH_TEST_ID/p.provisionprofile"},
		"list":      []any{"${ORCH_TEST_ID}", 42},
		"untouched": 7,
	})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expandEnv returned %T, want a map", got)
	}
	if want := "Developer ID Application: Example"; m["identity"] != want {
		t.Errorf("identity: got %v, want %v", m["identity"], want)
	}
	nested := m["nested"].(map[string]any)
	if want := "Developer ID Application: Example/p.provisionprofile"; nested["profile"] != want {
		t.Errorf("nested: got %v, want %v", nested["profile"], want)
	}
	list := m["list"].([]any)
	if want := "Developer ID Application: Example"; list[0] != want {
		t.Errorf("list: got %v, want %v", list[0], want)
	}
	// Non-string values pass through untouched.
	if list[1] != 42 || m["untouched"] != 7 {
		t.Errorf("non-string values were altered: %v", m)
	}
	// An unset variable expands to empty, as os.ExpandEnv does.
	if got := expandEnv("${ORCH_TEST_UNSET}"); got != "" {
		t.Errorf("unset variable: got %q, want empty", got)
	}
}

// TestBuildInfoPlist covers the defaults every bundle needs and the caller's
// ability to override them, since a missing key makes an unlaunchable bundle.
func TestBuildInfoPlist(t *testing.T) {
	version := versionInfo{Short: "1.0.0", Build: "1.0.0+abcdef12", Commit: "abcdef12345", BuildTime: time.Now()}
	info, err := buildInfoPlist(nil, "my-exe", "io.cloudeng.example", version)
	if err != nil {
		t.Fatalf("buildInfoPlist: %v", err)
	}
	if got, want := info.CFBundleExecutable, "my-exe"; got != want {
		t.Errorf("CFBundleExecutable: got %q, want %q", got, want)
	}
	if got, want := info.CFBundleIdentifier, "io.cloudeng.example"; got != want {
		t.Errorf("CFBundleIdentifier: got %q, want %q", got, want)
	}
	if info.CFBundlePackageType == "" || info.LSMinimumSystemVersion == "" || info.CFBundleVersion == "" {
		t.Errorf("a required key was left unset: %+v", info)
	}
	// The version is stamped from the build rather than hard-coded.
	if got, want := info.CFBundleShortVersionString, version.Short; got != want {
		t.Errorf("CFBundleShortVersionString: got %q, want %q", got, want)
	}
	if got, want := info.CFBundleVersion, version.Build; got != want {
		t.Errorf("CFBundleVersion: got %q, want %q", got, want)
	}
	if got, want := info.Extra["CGCommit"], version.Commit; got != want {
		t.Errorf("CGCommit: got %v, want %v", got, want)
	}
	if _, ok := info.Extra["CGBuildTime"]; !ok {
		t.Errorf("CGBuildTime was not recorded: %+v", info.Extra)
	}

	// User keys override the defaults and unknown keys are preserved, so that
	// a bundle can carry keys this package knows nothing about.
	info, err = buildInfoPlist(map[string]any{
		"CFBundleVersion":         "1.2.3",
		"NSHighResolutionCapable": true,
	}, "my-exe", "io.cloudeng.example", version)
	if err != nil {
		t.Fatalf("buildInfoPlist: %v", err)
	}
	if got, want := info.CFBundleVersion, "1.2.3"; got != want {
		t.Errorf("CFBundleVersion: got %q, want %q", got, want)
	}
	data, err := plist.MarshalIndent(info, plist.XMLFormat, "\t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<key>NSHighResolutionCapable</key>") {
		t.Errorf("an unknown key was dropped:\n%s", data)
	}

	// Blanking a required key is rejected rather than producing a bundle that
	// will not launch.
	if _, err := buildInfoPlist(map[string]any{"CFBundleName": nil}, "my-exe", "id", version); err == nil {
		t.Error("a nil CFBundleName was accepted")
	}
}

// TestLoadBundleConfig covers the defaults applied to an installer
// configuration, which is what makes a minimal installer.yaml usable.
func TestLoadBundleConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installer.yaml")
	if err := os.WriteFile(path, []byte("info_plist:\n  CFBundleVersion: \"9.9\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadBundleConfig(path)
	if err != nil {
		t.Fatalf("loadBundleConfig: %v", err)
	}
	if got, want := cfg.Bundle, defaultExecutable+".app"; got != want {
		t.Errorf("bundle: got %q, want %q", got, want)
	}
	if got, want := cfg.OrchestratorConfig, "minimal_config.yml"; got != want {
		t.Errorf("orchestrator config: got %q, want %q", got, want)
	}
	if got, want := cfg.LaunchAgentConfig, "launch_agent.yml"; got != want {
		t.Errorf("launch agent config: got %q, want %q", got, want)
	}

	if _, err := loadBundleConfig(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Error("a missing installer configuration was accepted")
	}
}

// TestShippedInstallerConfig verifies that the installer.yaml in the tree
// still loads and yields a valid Info.plist, so that a bundle build fails at
// review rather than at release time.
func TestShippedInstallerConfig(t *testing.T) {
	if _, err := os.Stat("installer.yaml"); err != nil {
		t.Skipf("no installer.yaml in the tree: %v", err)
	}
	cfg, err := loadBundleConfig("installer.yaml")
	if err != nil {
		t.Fatalf("loadBundleConfig: %v", err)
	}
	version := versionInfo{Short: cfg.Version, Build: cfg.Version + "+abcdef12", Commit: "abcdef12", BuildTime: time.Now()}
	if _, err := buildInfoPlist(cfg.Info, launcherExecutable, outerBundleID, version); err != nil {
		t.Errorf("the shipped installer.yaml yields an invalid outer Info.plist: %v", err)
	}
	if _, err := buildInfoPlist(nil, defaultExecutable, orchestratorBundleID, version); err != nil {
		t.Errorf("the nested Info.plist is invalid: %v", err)
	}
}

// TestDetailErr covers the error decoration applied to GitHub API failures,
// which is what turns a bare 404 into something actionable.
func TestDetailErr(t *testing.T) {
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "api.github.com", Path: "/repos/o/r"}}

	if got := detailErr(nil, req, nil); got != nil {
		t.Errorf("a nil error was decorated: %v", got)
	}

	base := os.ErrNotExist
	got := detailErr([]byte("not found\nsecond line\n"), req, base)
	if got == nil {
		t.Fatal("got nil, want an error")
	}
	msg := got.Error()
	for _, want := range []string{"api.github.com/repos/o/r", "not found"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
	// The cause is preserved for errors.Is.
	if !strings.Contains(msg, base.Error()) {
		t.Errorf("error %q does not wrap the cause", msg)
	}

	// A nil request, or one with no URL, is tolerated.
	if got := detailErr([]byte("body"), nil, base); got == nil || !strings.Contains(got.Error(), "body") {
		t.Errorf("nil request: got %v, want the body included", got)
	}
	// An empty body yields the location only.
	got = detailErr(nil, req, base)
	if got == nil || !strings.Contains(got.Error(), "api.github.com") {
		t.Errorf("empty body: got %v, want the location included", got)
	}
	// Neither body nor request leaves the error unchanged.
	if got := detailErr(nil, nil, base); got != base {
		t.Errorf("got %v, want the original error unchanged", got)
	}
}
